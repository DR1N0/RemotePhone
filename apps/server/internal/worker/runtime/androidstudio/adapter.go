package androidstudio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mako/internal/worker/config"
	"mako/internal/worker/host"
	"mako/internal/worker/runtime"
	"mako/internal/worker/transport"
)

type Adapter struct {
	workerID       string
	discovery      DiscoveryOptions
	store          runtime.HandleStore
	transportStore *transport.Store
	stunURL        string
	ffmpegPath     string
	streamBitRate  int
	webRTCHostIP   string
	dataRoot       string
	baseImageRoot  string
	launchFn       func(string, LaunchRequest) (ProcessHandle, error)
	stopFn         func(int) error
	// stopEmulatorFn owns the graceful shutdown path (adb console "emu kill",
	// then a process-kill fallback). Swappable in tests so they never shell
	// out to a real adb/emulator.
	stopEmulatorFn func(runtime.InstanceHandle) error
	// commitOverlayFn merges a running (or just-stopped) instance's
	// quickboot qcow2 overlay into its backing userdata-qemu.img — see
	// CaptureBaseImage's doc comment for why this is necessary. Swappable
	// in tests so they never shell out to a real qemu-img binary.
	commitOverlayFn func(qemuImgPath, overlayPath string) error
}

type ProcessHandle interface {
	Pid() int
}

type execProcess struct {
	pid int
}

func (p execProcess) Pid() int { return p.pid }

func NewAdapter(cfg config.Config, store runtime.HandleStore, transportStore *transport.Store) *Adapter {
	if store == nil {
		store = runtime.NewMemoryHandleStore()
	}

	adapter := &Adapter{
		workerID: cfg.WorkerID,
		discovery: DiscoveryOptions{
			HomeDir:              cfg.HomeDir,
			AndroidSDKRoot:       cfg.AndroidSDKRoot,
			ExplicitEmulatorPath: cfg.AndroidEmulatorPath,
		},
		store:          store,
		transportStore: transportStore,
		stunURL:        cfg.StunURL,
		ffmpegPath:     cfg.FFmpegPath,
		streamBitRate:  cfg.StreamBitRate,
		webRTCHostIP:   cfg.WebRTCHostIP,
		dataRoot:       cfg.InstanceDataRoot,
		baseImageRoot:  cfg.BaseImageRoot,
		stopFn:         StopProcess,
		stopEmulatorFn: stopEmulator,
	}
	adapter.launchFn = func(emulatorPath string, req LaunchRequest) (ProcessHandle, error) {
		cmd, err := Launch(emulatorPath, req)
		if err != nil {
			return nil, err
		}
		return execProcess{pid: cmd.Process.Pid}, nil
	}
	adapter.commitOverlayFn = func(qemuImgPath, overlayPath string) error {
		output, err := exec.Command(qemuImgPath, "commit", overlayPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("qemu-img commit: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	return adapter
}

func (a *Adapter) RuntimeClass() string {
	return RuntimeClass
}

func (a *Adapter) DisplayName() string {
	return "Android Studio Emulator"
}

func (a *Adapter) IsAvailable() error {
	_, err := ResolveEmulatorBinary(a.discovery)
	return err
}

func (a *Adapter) DiscoverTemplates() ([]runtime.Template, error) {
	return DiscoverTemplates(a.discovery)
}

func (a *Adapter) Start(req runtime.StartRequest) (runtime.InstanceHandle, error) {
	templates, err := a.DiscoverTemplates()
	if err != nil {
		return runtime.InstanceHandle{}, err
	}

	template, err := findTemplate(templates, req.TemplateID)
	if err != nil {
		return runtime.InstanceHandle{}, err
	}

	ports, err := host.AllocateEmulatorPorts()
	if err != nil {
		return runtime.InstanceHandle{}, err
	}

	instanceAVDName, instanceAVDDir, err := a.ensureInstanceAVD(req.InstanceID, template, req.BaseImageArtifactPath)
	if err != nil {
		return runtime.InstanceHandle{}, fmt.Errorf("prepare instance AVD: %w", err)
	}

	launchReq := BuildLaunchRequestFromStart(req, ports, instanceAVDName)
	process, err := a.launchFn(template.Metadata["emulatorPath"], launchReq)
	if err != nil {
		return runtime.InstanceHandle{}, err
	}

	handle := runtime.InstanceHandle{
		InstanceID:   req.InstanceID,
		TemplateID:   req.TemplateID,
		RuntimeClass: a.RuntimeClass(),
		WorkerID:     a.workerID,
		PID:          process.Pid(),
		ConsolePort:  ports.Console,
		AdbPort:      ports.Adb,
		GRPCPort:     ports.GRPC,
		DataDir:      instanceAVDDir,
	}
	if a.store != nil {
		a.store.Put(handle)
	}

	return handle, nil
}

func (a *Adapter) Inspect(instanceID string) (runtime.InstanceStatus, error) {
	return InspectHandle(a.store, a.RuntimeClass(), instanceID)
}

func (a *Adapter) Stop(instanceID string) (runtime.InstanceStatus, error) {
	handle, ok := a.store.Get(instanceID)
	if !ok || handle.RuntimeClass != a.RuntimeClass() {
		return runtime.InstanceStatus{}, runtime.ErrInstanceNotFound
	}

	lastError := ""
	stop := a.stopEmulatorFn
	if stop == nil {
		stop = func(h runtime.InstanceHandle) error { return a.stopFn(h.PID) }
	}
	if err := stop(handle); err != nil {
		lastError = err.Error()
	}
	if a.store != nil {
		a.store.Delete(instanceID)
	}

	// Clean up any active transport adapter for this instance.
	// We don't know the attachmentID here, so scan for any key prefixed with instanceID.
	if a.transportStore != nil {
		prefix := instanceID + ":"
		a.transportStore.StopAndDeleteByPrefix(prefix)
	}

	return runtime.InstanceStatus{
		State:     "stopped",
		Handle:    handle,
		LastError: lastError,
	}, nil
}

func (a *Adapter) Reconcile() ([]runtime.InstanceStatus, error) {
	return ReconcileHandles(a.store)
}

// CleanupInstanceData removes instanceID's cloned AVD (both its
// registration .ini and content directory created by ensureInstanceAVD) —
// called once the instance itself is deleted, not on every stop. Idempotent
// and best-effort: os.Remove/os.RemoveAll on a path that's already gone is
// not an error here, since "nothing to clean up" is success, not failure.
func (a *Adapter) CleanupInstanceData(instanceID string) error {
	avdName := instanceAVDName(instanceID)
	iniPath := filepath.Join(a.discovery.HomeDir, ".android", "avd", avdName+".ini")

	registered, err := readIniFile(iniPath)
	avdContentDir := registered["path"]
	if err != nil || avdContentDir == "" {
		// Never actually created (instance was never started) — nothing to
		// do, not an error.
		return nil
	}

	if err := os.RemoveAll(avdContentDir); err != nil {
		return fmt.Errorf("remove instance avd content dir: %w", err)
	}
	if err := os.Remove(iniPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove instance avd registration: %w", err)
	}
	return nil
}

// CaptureBaseImageArtifact snapshots a (stopped) instance's current AVD
// state — sourceAvdDir, its state_data_ref content directory — into a new,
// immutable artifact directory under baseImageRoot, keyed by baseImageID
// (the control plane's freshly-generated base_images.id). Only config.ini
// and userdata-qemu.img are copied: the same two files ensureInstanceAVD
// seeds a new instance from, nothing else (cache/sdcard/snapshots are
// ephemeral, regenerated fresh by the emulator either way). Returns the
// artifact directory path for the control plane to persist as
// base_images.artifact_path.
// CaptureBaseImage's trickiest step is mergeQuickbootOverlay: the emulator
// never writes new guest data directly into userdata-qemu.img once it's
// booted at least once. Instead it creates a sibling userdata-qemu.img.qcow2
// — a qcow2 overlay backed by userdata-qemu.img — and all live writes go
// there (confirmed by direct testing: userdata-qemu.img's own mtime/hash
// never change across a boot, while the .qcow2 sibling grows). Copying only
// userdata-qemu.img therefore silently captures stale, pre-boot state. The
// fix is `qemu-img commit`, which merges the overlay's deltas back into its
// backing file in place — verified by booting a fresh instance from a
// committed capture and confirming a file written and sync'd just before
// stop is actually present (raw byte-grepping the disk image is NOT a
// reliable way to check this — the qcow2/filesystem layout doesn't
// guarantee short strings stay contiguous — an actual boot is the only
// trustworthy verification).
func (a *Adapter) CaptureBaseImage(baseImageID, sourceDataDir string) (artifactPath string, err error) {
	if err := a.mergeQuickbootOverlay(sourceDataDir); err != nil {
		return "", fmt.Errorf("merge quickboot overlay: %w", err)
	}

	artifactDir := filepath.Join(a.baseImageRoot, baseImageID)
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		return "", fmt.Errorf("create base image artifact dir: %w", err)
	}
	if err := copyFile(filepath.Join(sourceDataDir, "config.ini"), filepath.Join(artifactDir, "config.ini")); err != nil {
		return "", fmt.Errorf("capture config.ini: %w", err)
	}
	if err := copyFile(filepath.Join(sourceDataDir, "userdata-qemu.img"), filepath.Join(artifactDir, "userdata-qemu.img")); err != nil {
		return "", fmt.Errorf("capture userdata-qemu.img: %w", err)
	}
	return artifactDir, nil
}

// mergeQuickbootOverlay is a no-op if sourceDataDir has no
// userdata-qemu.img.qcow2 (e.g. an instance that's never actually booted
// far enough to create one — capture already requires state_data_ref to be
// set, but that alone doesn't guarantee a boot completed).
func (a *Adapter) mergeQuickbootOverlay(sourceDataDir string) error {
	overlayPath := filepath.Join(sourceDataDir, "userdata-qemu.img.qcow2")
	if _, err := os.Stat(overlayPath); err != nil {
		return nil
	}
	qemuImgPath, err := a.resolveQemuImgBinary()
	if err != nil {
		return err
	}
	return a.commitOverlayFn(qemuImgPath, overlayPath)
}

// resolveQemuImgBinary finds qemu-img alongside the resolved emulator
// binary — the Android SDK ships its own copy there (<sdk>/emulator/qemu-img
// on Unix, <sdk>/emulator/qemu-img.exe on Windows), so this has no extra
// install requirement beyond the SDK this whole package already depends on.
func (a *Adapter) resolveQemuImgBinary() (string, error) {
	emulatorPath, err := ResolveEmulatorBinary(a.discovery)
	if err != nil {
		return "", fmt.Errorf("resolve emulator binary (needed to locate sibling qemu-img): %w", err)
	}
	dir := filepath.Dir(emulatorPath)
	candidates := []string{filepath.Join(dir, qemuImgBinaryName()), filepath.Join(dir, "qemu-img")}
	for _, qemuImgPath := range candidates {
		if info, err := os.Stat(qemuImgPath); err == nil && !info.IsDir() {
			return qemuImgPath, nil
		}
	}
	return "", fmt.Errorf("qemu-img not found alongside emulator binary in %s (tried %s)", dir, strings.Join(candidates, ", "))
}

// ensureInstanceAVD gives instanceID its own independently-registered AVD,
// isolated from every other instance of the same template (and from the
// template AVD itself) — confirmed by direct testing that the emulator's
// -datadir/-init-data flags do NOT achieve this when -avd is used (see
// BuildLaunchArgs); two separately-registered AVDs is the mechanism that
// actually works, including when their userdata-qemu.img files start out
// byte-identical copies (the emulator's own per-AVD-directory qcow2
// overlay/snapshot handling keeps them from ever cross-contaminating once
// booted).
//
// Idempotent across respawns: if this instance's AVD .ini is already
// registered, it's reused as-is (not re-cloned/overwritten) so an
// already-diverged instance's data is never clobbered by a worker
// crash-and-respawn cycle.
//
// seedOverrideDir, if non-empty, is a captured base image's artifact
// directory (see CaptureBaseImageArtifact) — both config.ini and the seed
// userdata image are copied from there instead of the template's live,
// on-disk AVD. Empty means "no base image has ever been promoted for this
// template," today's original behavior.
func (a *Adapter) ensureInstanceAVD(instanceID string, template runtime.Template, seedOverrideDir string) (avdName, avdContentDir string, err error) {
	avdName = instanceAVDName(instanceID)
	avdHomeDir := filepath.Join(a.discovery.HomeDir, ".android", "avd")
	iniPath := filepath.Join(avdHomeDir, avdName+".ini")

	if existing, statErr := readIniFile(iniPath); statErr == nil {
		if path := existing["path"]; path != "" {
			return avdName, path, nil
		}
	}

	avdContentDir = filepath.Join(a.dataRoot, avdName+".avd")
	if err := os.MkdirAll(avdContentDir, 0700); err != nil {
		return "", "", fmt.Errorf("create instance avd dir: %w", err)
	}

	sourceDir := template.Metadata["avdPath"]
	if seedOverrideDir != "" {
		sourceDir = seedOverrideDir
	}
	if err := copyFile(filepath.Join(sourceDir, "config.ini"), filepath.Join(avdContentDir, "config.ini")); err != nil {
		return "", "", fmt.Errorf("copy config.ini: %w", err)
	}
	if err := copyFile(resolveSeedUserdataPath(sourceDir), filepath.Join(avdContentDir, "userdata-qemu.img")); err != nil {
		return "", "", fmt.Errorf("seed userdata-qemu.img: %w", err)
	}

	if err := os.MkdirAll(avdHomeDir, 0700); err != nil {
		return "", "", fmt.Errorf("create avd home dir: %w", err)
	}
	iniContents := "avd.ini.encoding=UTF-8\npath=" + avdContentDir + "\n"
	if err := os.WriteFile(iniPath, []byte(iniContents), 0600); err != nil {
		return "", "", fmt.Errorf("register instance avd: %w", err)
	}

	return avdName, avdContentDir, nil
}

// instanceAVDName derives a valid, collision-free AVD identifier from an
// instance ID (a UUID) — AVD names/.ini filenames don't accept hyphens.
func instanceAVDName(instanceID string) string {
	return instanceAVDPrefix + strings.ReplaceAll(instanceID, "-", "_")
}

// resolveSeedUserdataPath picks the seed userdata image a new instance's
// own userdata-qemu.img is copied from: the AVD's pristine initial copy if
// it still has one (userdata.img — present on AVDs that haven't diverged
// far from creation), else its current live state (userdata-qemu.img) as
// the best available base. A template's whole point is that it already
// carries baked-in app/data state, so falling back to whatever it
// currently has (rather than a vendor-blank image) is deliberate, not a
// compromise.
func resolveSeedUserdataPath(avdPath string) string {
	pristine := filepath.Join(avdPath, "userdata.img")
	if info, err := os.Stat(pristine); err == nil && !info.IsDir() {
		return pristine
	}
	return filepath.Join(avdPath, "userdata-qemu.img")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func findTemplate(templates []runtime.Template, templateID string) (runtime.Template, error) {
	for _, template := range templates {
		if template.TemplateID == templateID {
			return template, nil
		}
	}
	return runtime.Template{}, fmt.Errorf("%w: %s", runtime.ErrTemplateNotFound, templateID)
}

func TemplateIDToAVDName(templateID string) string {
	parts := strings.SplitN(templateID, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return templateID
}
