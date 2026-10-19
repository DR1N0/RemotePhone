package androidstudio

import (
	"fmt"
	"io"
	"os"
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
	dataRoot       string
	launchFn       func(string, LaunchRequest) (ProcessHandle, error)
	stopFn         func(int) error
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
		dataRoot:       cfg.InstanceDataRoot,
		stopFn:         StopProcess,
	}
	adapter.launchFn = func(emulatorPath string, req LaunchRequest) (ProcessHandle, error) {
		cmd, err := Launch(emulatorPath, req)
		if err != nil {
			return nil, err
		}
		return execProcess{pid: cmd.Process.Pid}, nil
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

	instanceAVDName, instanceAVDDir, err := a.ensureInstanceAVD(req.InstanceID, template)
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
	if err := a.stopFn(handle.PID); err != nil {
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
func (a *Adapter) ensureInstanceAVD(instanceID string, template runtime.Template) (avdName, avdContentDir string, err error) {
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

	templateAVDPath := template.Metadata["avdPath"]
	if err := copyFile(filepath.Join(templateAVDPath, "config.ini"), filepath.Join(avdContentDir, "config.ini")); err != nil {
		return "", "", fmt.Errorf("copy config.ini: %w", err)
	}
	if err := copyFile(resolveSeedUserdataPath(templateAVDPath), filepath.Join(avdContentDir, "userdata-qemu.img")); err != nil {
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
	return "mako_instance_" + strings.ReplaceAll(instanceID, "-", "_")
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
