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
	audioDebug     bool
	headless       bool
	emulatorAudio  string
	gpuMode        string
	dataRoot       string
	baseImageRoot  string
	launchFn       func(string, LaunchRequest) (ProcessHandle, error)
	stopFn         func(int) error
	// stopEmulatorFn owns the graceful shutdown path (adb console "emu kill",
	// then a process-kill fallback). Swappable in tests so they never shell
	// out to a real adb/emulator.
	stopEmulatorFn func(runtime.InstanceHandle) error
	// createOverlayFn makes a per-instance writable qcow2 overlay backed by a
	// promoted base image. Swappable in tests so they never shell out to a
	// real qemu-img binary.
	createOverlayFn func(qemuImgPath, basePath, overlayPath string) error
	// convertOverlayFn flattens an instance's current userdata chain into a
	// standalone qcow2 base image. Swappable in tests for the same reason.
	convertOverlayFn func(qemuImgPath, sourcePath, destPath string) error
	// convertKeyFn flattens the emulator's encryption-key overlay into a
	// standalone raw key image. The key image must accompany a base image or
	// a cloned instance cannot decrypt the captured userdata.
	convertKeyFn func(qemuImgPath, sourcePath, destPath string) error
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
		audioDebug:     cfg.AudioDebug,
		headless:       cfg.Headless,
		emulatorAudio:  cfg.EmulatorAudio,
		gpuMode:        cfg.GPUMode,
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
	adapter.createOverlayFn = func(qemuImgPath, basePath, overlayPath string) error {
		output, err := exec.Command(
			qemuImgPath, "create", "-f", "qcow2",
			"-b", basePath, "-F", "qcow2", overlayPath,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("qemu-img create overlay: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	adapter.convertOverlayFn = func(qemuImgPath, sourcePath, destPath string) error {
		output, err := exec.Command(
			qemuImgPath, "convert", "-O", "qcow2", sourcePath, destPath,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("qemu-img convert: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	adapter.convertKeyFn = func(qemuImgPath, sourcePath, destPath string) error {
		output, err := exec.Command(
			qemuImgPath, "convert", "-O", "raw", sourcePath, destPath,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("qemu-img convert encryption key: %w (%s)", err, strings.TrimSpace(string(output)))
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
	// One worker owns one instance; headless is a worker deployment policy,
	// not something each client gets to choose. MAKO_HEADLESS=0 keeps a
	// visible window for local debugging.
	launchReq.Headless = a.headless
	launchReq.GPUMode = a.gpuMode
	if a.emulatorAudio != "" {
		launchReq.ExtraArgs = append(launchReq.ExtraArgs, "-audio", a.emulatorAudio)
	}
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
// (the control plane's freshly-generated base_images.id). It captures
// config.ini and flattens the complete userdata chain into one standalone
// qcow2 image. On current emulators the writable data is not necessarily in
// userdata-qemu.img at all: the emulator creates
// userdata-qemu.img.qcow2 on top of it, and a layered instance may itself be
// a qcow2 overlay on a promoted base. `qemu-img convert` follows the whole
// backing chain, so the resulting artifact has no external dependency and
// can safely become the backing file for future instances.
//
// /sdcard is part of the same /data filesystem on current AVDs (FUSE over
// /data/media), so external-storage game resources are included in this
// userdata capture rather than requiring a separate sdcard image. The AVD's
// encryption key is flattened into the artifact too; without it a cloned
// instance generates a different key and hangs before Android can mount the
// captured userdata.
func (a *Adapter) CaptureBaseImage(baseImageID, sourceDataDir string) (artifactPath string, err error) {
	artifactDir := filepath.Join(a.baseImageRoot, baseImageID)
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		return "", fmt.Errorf("create base image artifact dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(artifactDir) }

	if err := copyFile(filepath.Join(sourceDataDir, "config.ini"), filepath.Join(artifactDir, "config.ini")); err != nil {
		cleanup()
		return "", fmt.Errorf("capture config.ini: %w", err)
	}

	// Prefer the emulator's writable top layer; if the instance never
	// booted far enough to create one, flatten the base file directly.
	sourcePath := filepath.Join(sourceDataDir, "userdata-qemu.img.qcow2")
	if info, statErr := os.Stat(sourcePath); statErr != nil || info.IsDir() {
		sourcePath = filepath.Join(sourceDataDir, "userdata-qemu.img")
	}

	qemuImgPath, err := a.resolveQemuImgBinary()
	if err != nil {
		cleanup()
		return "", err
	}
	if err := a.convertOverlayFn(qemuImgPath, sourcePath, filepath.Join(artifactDir, "userdata-qemu.img")); err != nil {
		cleanup()
		return "", fmt.Errorf("flatten userdata for base image: %w", err)
	}
	if err := a.captureEncryptionKey(qemuImgPath, sourceDataDir, artifactDir); err != nil {
		cleanup()
		return "", err
	}
	return artifactDir, nil
}

// captureEncryptionKey includes the emulator's encryption-key state in the
// base artifact. Without it, a cloned instance gets a fresh key and cannot
// decrypt the captured userdata, which manifests as a boot hang. The key's
// qcow2 overlay is flattened to a standalone raw image so the base does not
// depend on the source instance directory.
func (a *Adapter) captureEncryptionKey(qemuImgPath, sourceDir, artifactDir string) error {
	keyTop := filepath.Join(sourceDir, "encryptionkey.img.qcow2")
	keyRaw := filepath.Join(sourceDir, "encryptionkey.img")
	dest := filepath.Join(artifactDir, "encryptionkey.img")

	if info, err := os.Stat(keyTop); err == nil && !info.IsDir() {
		if err := a.convertKeyFn(qemuImgPath, keyTop, dest); err != nil {
			return fmt.Errorf("flatten encryption key: %w", err)
		}
		return nil
	}
	if info, err := os.Stat(keyRaw); err == nil && !info.IsDir() {
		if err := copyFile(keyRaw, dest); err != nil {
			return fmt.Errorf("copy encryption key: %w", err)
		}
	}
	return nil
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
// directory (see CaptureBaseImage) — config.ini is copied from there, the
// instance's userdata-qemu.img becomes a small qcow2 overlay backed by the
// shared base, and the captured encryption key is copied so the clone can
// decrypt that userdata. Empty means "no base image has ever been promoted
// for this template," today's original copy behavior.
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

	if seedOverrideDir != "" {
		basePath, err := filepath.Abs(filepath.Join(seedOverrideDir, "userdata-qemu.img"))
		if err != nil {
			return "", "", fmt.Errorf("resolve base image path: %w", err)
		}
		qemuImgPath, err := a.resolveQemuImgBinary()
		if err != nil {
			return "", "", err
		}
		if err := a.createOverlayFn(qemuImgPath, basePath, filepath.Join(avdContentDir, "userdata-qemu.img")); err != nil {
			return "", "", fmt.Errorf("create instance userdata overlay: %w", err)
		}
		if err := copyOptionalFile(filepath.Join(seedOverrideDir, "encryptionkey.img"), filepath.Join(avdContentDir, "encryptionkey.img")); err != nil {
			return "", "", fmt.Errorf("copy base image encryption key: %w", err)
		}
	} else {
		if err := copyFile(resolveSeedUserdataPath(sourceDir), filepath.Join(avdContentDir, "userdata-qemu.img")); err != nil {
			return "", "", fmt.Errorf("seed userdata-qemu.img: %w", err)
		}
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

// copyOptionalFile copies src when it exists, and does nothing when the
// source AVD predates the key-in-base artifact format. Missing encryption-key
// files are not an error for legacy artifacts; current captures always
// include one.
func copyOptionalFile(src, dst string) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	return copyFile(src, dst)
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
