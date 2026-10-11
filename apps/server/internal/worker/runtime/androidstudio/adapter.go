package androidstudio

import (
	"fmt"
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

	launchReq := BuildLaunchRequestFromStart(req, ports, template.Metadata["avdName"])
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
