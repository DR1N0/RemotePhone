package androidstudio

import (
	"os"
	"path/filepath"
	"testing"

	"mako/internal/worker/config"
	"mako/internal/worker/runtime"
)

// setupFakeAVD creates a minimal AVD directory structure under a temp home
// so DiscoverTemplates (the real function, not a fake) can find it — more
// realistic than mocking template discovery, and exercises the same code
// path Start actually uses. Returns the temp home and the AVD's content
// directory.
func setupFakeAVD(t *testing.T, avdName string, withPristineUserdata bool) (homeDir, avdPath string) {
	t.Helper()
	homeDir = t.TempDir()
	avdDir := filepath.Join(homeDir, ".android", "avd")
	if err := os.MkdirAll(avdDir, 0755); err != nil {
		t.Fatalf("mkdir avd dir: %v", err)
	}
	avdPath = filepath.Join(homeDir, avdName+".avd")
	if err := os.MkdirAll(avdPath, 0755); err != nil {
		t.Fatalf("mkdir avd content dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(avdDir, avdName+".ini"), []byte("path="+avdPath+"\n"), 0644); err != nil {
		t.Fatalf("write avd ini: %v", err)
	}
	if err := os.WriteFile(filepath.Join(avdPath, "config.ini"), []byte("tag.id=google_apis_playstore\n"), 0644); err != nil {
		t.Fatalf("write config.ini: %v", err)
	}
	if err := os.WriteFile(filepath.Join(avdPath, "userdata-qemu.img"), []byte("live-state"), 0644); err != nil {
		t.Fatalf("write userdata-qemu.img: %v", err)
	}
	if err := os.WriteFile(filepath.Join(avdPath, "encryptionkey.img"), []byte("key-state"), 0644); err != nil {
		t.Fatalf("write encryptionkey.img: %v", err)
	}
	if withPristineUserdata {
		if err := os.WriteFile(filepath.Join(avdPath, "userdata.img"), []byte("pristine"), 0644); err != nil {
			t.Fatalf("write userdata.img: %v", err)
		}
	}
	// ResolveEmulatorBinary just needs a stat-able file at the configured
	// explicit path — Start's fake launchFn below never actually execs it.
	emulatorPath := filepath.Join(homeDir, "fake-emulator")
	if err := os.WriteFile(emulatorPath, []byte(""), 0755); err != nil {
		t.Fatalf("write fake emulator: %v", err)
	}
	// resolveQemuImgBinary looks for a "qemu-img" sibling of the emulator
	// binary — never actually executed (createOverlayFn/convertOverlayFn are
	// swapped out in tests), just needs to exist for the stat check to pass.
	if err := os.WriteFile(filepath.Join(homeDir, "qemu-img"), []byte(""), 0755); err != nil {
		t.Fatalf("write fake qemu-img: %v", err)
	}
	return homeDir, avdPath
}

func newTestAdapter(homeDir, dataRoot string) *Adapter {
	adapter := NewAdapter(config.Config{
		HomeDir:             homeDir,
		AndroidEmulatorPath: filepath.Join(homeDir, "fake-emulator"),
		InstanceDataRoot:    dataRoot,
	}, runtime.NewMemoryHandleStore(), nil)
	adapter.launchFn = func(_ string, _ LaunchRequest) (ProcessHandle, error) {
		return execProcess{pid: 4242}, nil
	}
	adapter.createOverlayFn = func(_, basePath, overlayPath string) error {
		return os.WriteFile(overlayPath, []byte("overlay-backed-by:"+basePath), 0644)
	}
	adapter.convertOverlayFn = func(_, sourcePath, destPath string) error {
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	}
	adapter.convertKeyFn = func(_, sourcePath, destPath string) error {
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	}
	return adapter
}

func TestStartClonesTemplateIntoANewIndependentlyRegisteredInstanceAVD(t *testing.T) {
	avdName := "test-avd"
	homeDir, avdPath := setupFakeAVD(t, avdName, true)
	dataRoot := t.TempDir()
	adapter := newTestAdapter(homeDir, dataRoot)

	var capturedReq LaunchRequest
	adapter.launchFn = func(_ string, req LaunchRequest) (ProcessHandle, error) {
		capturedReq = req
		return execProcess{pid: 4242}, nil
	}

	handle, err := adapter.Start(runtime.StartRequest{
		InstanceID:   "instance-1",
		RuntimeClass: RuntimeClass,
		TemplateID:   "android-studio:" + avdName,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	wantAVDName := "mako_instance_instance_1"
	if capturedReq.AVDName != wantAVDName {
		t.Errorf("LaunchRequest.AVDName = %q, want %q (per-instance AVD, not the shared template's)", capturedReq.AVDName, wantAVDName)
	}

	// The new AVD must actually be registered (a .ini pointing at its
	// content dir) — this is what makes `-avd <name>` resolvable at all.
	iniPath := filepath.Join(homeDir, ".android", "avd", wantAVDName+".ini")
	registered, err := readIniFile(iniPath)
	if err != nil {
		t.Fatalf("instance AVD was not registered: %v", err)
	}
	wantContentDir := filepath.Join(dataRoot, wantAVDName+".avd")
	if registered["path"] != wantContentDir {
		t.Errorf("registered path = %q, want %q", registered["path"], wantContentDir)
	}
	if handle.DataDir != wantContentDir {
		t.Errorf("InstanceHandle.DataDir = %q, want %q", handle.DataDir, wantContentDir)
	}

	// The clone must actually carry the template's config + seed userdata,
	// not an empty directory the emulator would fail to boot from.
	if got, err := os.ReadFile(filepath.Join(wantContentDir, "config.ini")); err != nil || string(got) != "tag.id=google_apis_playstore\n" {
		t.Errorf("cloned config.ini = %q, err %v, want copied from template", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(wantContentDir, "userdata-qemu.img")); err != nil || string(got) != "pristine" {
		t.Errorf("seeded userdata-qemu.img = %q, err %v, want pristine userdata.img contents", got, err)
	}

	// The template's own AVD must never be touched.
	if got, err := os.ReadFile(filepath.Join(avdPath, "userdata-qemu.img")); err != nil || string(got) != "live-state" {
		t.Errorf("template AVD's own userdata-qemu.img changed: %q, err %v", got, err)
	}
}

func TestStartUsesDistinctAVDsForDifferentInstancesOfSameTemplate(t *testing.T) {
	avdName := "shared-template"
	homeDir, _ := setupFakeAVD(t, avdName, true)
	dataRoot := t.TempDir()
	adapter := newTestAdapter(homeDir, dataRoot)

	templateID := "android-studio:" + avdName
	handleA, err := adapter.Start(runtime.StartRequest{InstanceID: "instance-a", RuntimeClass: RuntimeClass, TemplateID: templateID})
	if err != nil {
		t.Fatalf("Start instance-a: %v", err)
	}
	handleB, err := adapter.Start(runtime.StartRequest{InstanceID: "instance-b", RuntimeClass: RuntimeClass, TemplateID: templateID})
	if err != nil {
		t.Fatalf("Start instance-b: %v", err)
	}

	if handleA.DataDir == handleB.DataDir {
		t.Fatalf("two instances of the same template got the same AVD content dir: %q", handleA.DataDir)
	}

	// Diverge instance-a's data and confirm instance-b (and the template)
	// are unaffected — the real-world proof this whole feature exists for.
	if err := os.WriteFile(filepath.Join(handleA.DataDir, "userdata-qemu.img"), []byte("diverged-a"), 0644); err != nil {
		t.Fatalf("simulate divergence: %v", err)
	}
	bData, err := os.ReadFile(filepath.Join(handleB.DataDir, "userdata-qemu.img"))
	if err != nil || string(bData) != "pristine" {
		t.Errorf("instance-b userdata-qemu.img = %q, err %v, want untouched seed contents", bData, err)
	}
}

func TestStartReusesExistingInstanceAVDWithoutReseedingOnRespawn(t *testing.T) {
	avdName := "test-avd"
	homeDir, _ := setupFakeAVD(t, avdName, true)
	dataRoot := t.TempDir()
	adapter := newTestAdapter(homeDir, dataRoot)

	req := runtime.StartRequest{InstanceID: "instance-1", RuntimeClass: RuntimeClass, TemplateID: "android-studio:" + avdName}
	first, err := adapter.Start(req)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Simulate the instance having diverged from its seed while running.
	userdataPath := filepath.Join(first.DataDir, "userdata-qemu.img")
	if err := os.WriteFile(userdataPath, []byte("diverged-after-boot"), 0644); err != nil {
		t.Fatalf("simulate divergence: %v", err)
	}

	// A worker respawn/adopt cycle calls Start again for the same instance.
	second, err := adapter.Start(req)
	if err != nil {
		t.Fatalf("second Start (respawn): %v", err)
	}
	if second.DataDir != first.DataDir {
		t.Fatalf("respawn got a different AVD dir: %q vs %q", second.DataDir, first.DataDir)
	}
	got, err := os.ReadFile(userdataPath)
	if err != nil || string(got) != "diverged-after-boot" {
		t.Errorf("respawn re-seeded/clobbered diverged data: %q, err %v", got, err)
	}
}

func TestResolveSeedUserdataPathPrefersPristineUserdataImg(t *testing.T) {
	_, avdPath := setupFakeAVD(t, "with-pristine", true)
	got := resolveSeedUserdataPath(avdPath)
	want := filepath.Join(avdPath, "userdata.img")
	if got != want {
		t.Errorf("resolveSeedUserdataPath = %q, want %q", got, want)
	}
}

func TestResolveSeedUserdataPathFallsBackToLiveUserdataQemuImg(t *testing.T) {
	_, avdPath := setupFakeAVD(t, "no-pristine", false)
	got := resolveSeedUserdataPath(avdPath)
	want := filepath.Join(avdPath, "userdata-qemu.img")
	if got != want {
		t.Errorf("resolveSeedUserdataPath = %q, want %q", got, want)
	}
}

func TestCleanupInstanceDataRemovesClonedAVDAndRegistration(t *testing.T) {
	avdName := "test-avd"
	homeDir, _ := setupFakeAVD(t, avdName, true)
	dataRoot := t.TempDir()
	adapter := newTestAdapter(homeDir, dataRoot)

	handle, err := adapter.Start(runtime.StartRequest{
		InstanceID:   "instance-1",
		RuntimeClass: RuntimeClass,
		TemplateID:   "android-studio:" + avdName,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	iniPath := filepath.Join(homeDir, ".android", "avd", instanceAVDName("instance-1")+".ini")
	if _, err := os.Stat(iniPath); err != nil {
		t.Fatalf("expected instance AVD registered before cleanup: %v", err)
	}

	if err := adapter.CleanupInstanceData("instance-1"); err != nil {
		t.Fatalf("CleanupInstanceData: %v", err)
	}

	if _, err := os.Stat(iniPath); !os.IsNotExist(err) {
		t.Errorf("expected .ini removed, stat err: %v", err)
	}
	if _, err := os.Stat(handle.DataDir); !os.IsNotExist(err) {
		t.Errorf("expected content dir removed, stat err: %v", err)
	}
}

func TestCleanupInstanceDataIsNoOpWhenInstanceWasNeverStarted(t *testing.T) {
	homeDir, _ := setupFakeAVD(t, "test-avd", true)
	adapter := newTestAdapter(homeDir, t.TempDir())

	if err := adapter.CleanupInstanceData("never-started-instance"); err != nil {
		t.Errorf("CleanupInstanceData on never-started instance = %v, want nil (nothing to clean up)", err)
	}
}

func TestCleanupInstanceDataIsIdempotent(t *testing.T) {
	avdName := "test-avd"
	homeDir, _ := setupFakeAVD(t, avdName, true)
	adapter := newTestAdapter(homeDir, t.TempDir())

	req := runtime.StartRequest{InstanceID: "instance-1", RuntimeClass: RuntimeClass, TemplateID: "android-studio:" + avdName}
	if _, err := adapter.Start(req); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := adapter.CleanupInstanceData("instance-1"); err != nil {
		t.Fatalf("first CleanupInstanceData: %v", err)
	}
	if err := adapter.CleanupInstanceData("instance-1"); err != nil {
		t.Errorf("second CleanupInstanceData (already cleaned up) = %v, want nil", err)
	}
}

func TestCaptureBaseImageCopiesConfigAndFlattensUserdata(t *testing.T) {
	homeDir, avdPath := setupFakeAVD(t, "test-avd", true)
	baseImageRoot := t.TempDir()
	adapter := newTestAdapter(homeDir, t.TempDir())
	adapter.baseImageRoot = baseImageRoot

	artifactPath, err := adapter.CaptureBaseImage("image-1", avdPath)
	if err != nil {
		t.Fatalf("CaptureBaseImage: %v", err)
	}
	wantArtifactDir := filepath.Join(baseImageRoot, "image-1")
	if artifactPath != wantArtifactDir {
		t.Errorf("artifactPath = %q, want %q", artifactPath, wantArtifactDir)
	}
	if got, err := os.ReadFile(filepath.Join(artifactPath, "config.ini")); err != nil || string(got) != "tag.id=google_apis_playstore\n" {
		t.Errorf("captured config.ini = %q, err %v", got, err)
	}
	// CaptureBaseImage flattens the current userdata file into a standalone
	// artifact rather than copying a possibly-backed overlay verbatim.
	if got, err := os.ReadFile(filepath.Join(artifactPath, "userdata-qemu.img")); err != nil || string(got) != "live-state" {
		t.Errorf("captured userdata-qemu.img = %q, err %v, want live-state", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(artifactPath, "encryptionkey.img")); err != nil || string(got) != "key-state" {
		t.Errorf("captured encryptionkey.img = %q, err %v, want key-state", got, err)
	}
}

func TestCaptureBaseImageFlattensEncryptionKeyOverlay(t *testing.T) {
	homeDir, avdPath := setupFakeAVD(t, "test-avd", true)
	if err := os.WriteFile(filepath.Join(avdPath, "encryptionkey.img.qcow2"), []byte("key-overlay"), 0644); err != nil {
		t.Fatalf("write key overlay: %v", err)
	}
	adapter := newTestAdapter(homeDir, t.TempDir())
	adapter.baseImageRoot = t.TempDir()

	var keySource, keyDest string
	adapter.convertKeyFn = func(_, sourcePath, destPath string) error {
		keySource = sourcePath
		keyDest = destPath
		return os.WriteFile(destPath, []byte("flattened-key"), 0644)
	}

	artifactPath, err := adapter.CaptureBaseImage("image-1", avdPath)
	if err != nil {
		t.Fatalf("CaptureBaseImage: %v", err)
	}
	if keySource != filepath.Join(avdPath, "encryptionkey.img.qcow2") {
		t.Errorf("key convert source = %q, want the qcow2 overlay", keySource)
	}
	if keyDest != filepath.Join(artifactPath, "encryptionkey.img") {
		t.Errorf("key convert dest = %q, want artifact encryptionkey.img", keyDest)
	}
	if got, err := os.ReadFile(keyDest); err != nil || string(got) != "flattened-key" {
		t.Errorf("captured key = %q, err %v, want flattened-key", got, err)
	}
}

// TestCaptureBaseImageFlattensQuickbootOverlay covers the writable-layer bug
// this conversion exists to fix: once an instance has booted, the emulator
// writes new guest state into a sibling userdata-qemu.img.qcow2 overlay, not
// directly into userdata-qemu.img. qemu-img convert must follow that chain
// into a standalone artifact.
func TestCaptureBaseImageFlattensQuickbootOverlay(t *testing.T) {
	homeDir, avdPath := setupFakeAVD(t, "test-avd", true)
	if err := os.WriteFile(filepath.Join(avdPath, "userdata-qemu.img.qcow2"), []byte("overlay-marker"), 0644); err != nil {
		t.Fatalf("write fake overlay: %v", err)
	}
	adapter := newTestAdapter(homeDir, t.TempDir())
	adapter.baseImageRoot = t.TempDir()

	var convertedQemuImgPath, convertedSourcePath, convertedDestPath string
	adapter.convertOverlayFn = func(qemuImgPath, sourcePath, destPath string) error {
		convertedQemuImgPath = qemuImgPath
		convertedSourcePath = sourcePath
		convertedDestPath = destPath
		return os.WriteFile(destPath, []byte("flattened-from-overlay"), 0644)
	}

	artifactPath, err := adapter.CaptureBaseImage("image-1", avdPath)
	if err != nil {
		t.Fatalf("CaptureBaseImage: %v", err)
	}

	wantSourcePath := filepath.Join(avdPath, "userdata-qemu.img.qcow2")
	if convertedSourcePath != wantSourcePath {
		t.Errorf("convert source = %q, want %q", convertedSourcePath, wantSourcePath)
	}
	wantQemuImgPath := filepath.Join(homeDir, "qemu-img")
	if convertedQemuImgPath != wantQemuImgPath {
		t.Errorf("convert qemuImgPath = %q, want %q", convertedQemuImgPath, wantQemuImgPath)
	}
	wantDestPath := filepath.Join(artifactPath, "userdata-qemu.img")
	if convertedDestPath != wantDestPath {
		t.Errorf("convert dest = %q, want %q", convertedDestPath, wantDestPath)
	}
	if got, err := os.ReadFile(wantDestPath); err != nil || string(got) != "flattened-from-overlay" {
		t.Errorf("captured userdata-qemu.img = %q, err %v, want flattened-from-overlay", got, err)
	}
}

func TestCaptureBaseImageUsesUserdataWhenNoOverlayExists(t *testing.T) {
	homeDir, avdPath := setupFakeAVD(t, "test-avd", true)
	adapter := newTestAdapter(homeDir, t.TempDir())
	adapter.baseImageRoot = t.TempDir()

	var convertedSourcePath string
	adapter.convertOverlayFn = func(_, sourcePath, destPath string) error {
		convertedSourcePath = sourcePath
		return os.WriteFile(destPath, []byte("flat-without-overlay"), 0644)
	}

	if _, err := adapter.CaptureBaseImage("image-1", avdPath); err != nil {
		t.Fatalf("CaptureBaseImage: %v", err)
	}
	wantSourcePath := filepath.Join(avdPath, "userdata-qemu.img")
	if convertedSourcePath != wantSourcePath {
		t.Errorf("convert source = %q, want %q when no quickboot overlay exists", convertedSourcePath, wantSourcePath)
	}
}

func TestStartSeedsFromBaseImageArtifactWhenProvided(t *testing.T) {
	avdName := "test-avd"
	homeDir, _ := setupFakeAVD(t, avdName, true)
	adapter := newTestAdapter(homeDir, t.TempDir())

	// A captured base image artifact with DIFFERENT contents than the
	// template's own live AVD — proves Start actually prefers this over
	// the live AVD when BaseImageArtifactPath is set, and creates a thin
	// overlay instead of copying the base.
	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "config.ini"), []byte("from-base-image\n"), 0644); err != nil {
		t.Fatalf("write artifact config.ini: %v", err)
	}
	baseUserdata := filepath.Join(artifactDir, "userdata-qemu.img")
	if err := os.WriteFile(baseUserdata, []byte("base-userdata"), 0644); err != nil {
		t.Fatalf("write artifact userdata-qemu.img: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "encryptionkey.img"), []byte("base-key"), 0644); err != nil {
		t.Fatalf("write artifact encryptionkey.img: %v", err)
	}

	var overlayQemuImgPath, overlayBasePath, overlayDestPath string
	adapter.createOverlayFn = func(qemuImgPath, basePath, overlayPath string) error {
		overlayQemuImgPath = qemuImgPath
		overlayBasePath = basePath
		overlayDestPath = overlayPath
		return os.WriteFile(overlayPath, []byte("thin-overlay"), 0644)
	}

	handle, err := adapter.Start(runtime.StartRequest{
		InstanceID:            "instance-1",
		RuntimeClass:          RuntimeClass,
		TemplateID:            "android-studio:" + avdName,
		BaseImageArtifactPath: artifactDir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(handle.DataDir, "config.ini")); err != nil || string(got) != "from-base-image\n" {
		t.Errorf("instance config.ini = %q, err %v, want seeded from the base image artifact, not the live AVD", got, err)
	}
	if overlayBasePath != baseUserdata {
		t.Errorf("overlay base = %q, want %q", overlayBasePath, baseUserdata)
	}
	if overlayQemuImgPath != filepath.Join(homeDir, "qemu-img") {
		t.Errorf("overlay qemuImgPath = %q, want %q", overlayQemuImgPath, filepath.Join(homeDir, "qemu-img"))
	}
	if overlayDestPath != filepath.Join(handle.DataDir, "userdata-qemu.img") {
		t.Errorf("overlay dest = %q, want %q", overlayDestPath, filepath.Join(handle.DataDir, "userdata-qemu.img"))
	}
	if got, err := os.ReadFile(filepath.Join(handle.DataDir, "userdata-qemu.img")); err != nil || string(got) != "thin-overlay" {
		t.Errorf("instance userdata-qemu.img = %q, err %v, want thin overlay", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(handle.DataDir, "encryptionkey.img")); err != nil || string(got) != "base-key" {
		t.Errorf("instance encryptionkey.img = %q, err %v, want base-key", got, err)
	}
	if got, err := os.ReadFile(baseUserdata); err != nil || string(got) != "base-userdata" {
		t.Errorf("base userdata changed: %q, err %v", got, err)
	}
}

func TestInstanceAVDNameSanitizesHyphens(t *testing.T) {
	got := instanceAVDName("557ea68b-323e-4157-bc7d-502e0a24b6bb")
	want := "mako_instance_557ea68b_323e_4157_bc7d_502e0a24b6bb"
	if got != want {
		t.Errorf("instanceAVDName = %q, want %q", got, want)
	}
}
