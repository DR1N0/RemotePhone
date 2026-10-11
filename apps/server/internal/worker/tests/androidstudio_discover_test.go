package tests

import (
	"os"
	"path/filepath"
	"testing"

	"mako/internal/worker/runtime/androidstudio"
)

func TestResolveEmulatorBinaryPrefersExplicitPath(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "sdk", "emulator", "emulator")
	if err := os.MkdirAll(filepath.Dir(explicit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := androidstudio.ResolveEmulatorBinary(androidstudio.DiscoveryOptions{
		ExplicitEmulatorPath: explicit,
		HomeDir:              root,
	})
	if err != nil {
		t.Fatal(err)
	}

	if path != explicit {
		t.Fatalf("expected %q, got %q", explicit, path)
	}
}

func TestDiscoverTemplatesFromAvdDirectory(t *testing.T) {
	root := t.TempDir()
	avdDir := filepath.Join(root, ".android", "avd")
	avdName := "Medium_Phone"
	avdConfigDir := filepath.Join(avdDir, avdName+".avd")
	emulatorPath := filepath.Join(root, "Library", "Android", "sdk", "emulator", "emulator")

	if err := os.MkdirAll(avdConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(emulatorPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(emulatorPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(avdDir, avdName+".ini"), []byte("path="+avdConfigDir+"\npath.rel=avd/"+avdName+".avd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(avdConfigDir, "config.ini"), []byte("hw.lcd.width=1080\nhw.lcd.height=2400\ndisk.dataPartition.size=6442450944\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := androidstudio.DiscoverTemplates(androidstudio.DiscoveryOptions{
		HomeDir:              root,
		ExplicitEmulatorPath: emulatorPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}

	template := templates[0]
	if template.TemplateID != "macos-android-studio:Medium_Phone" {
		t.Fatalf("unexpected template id %q", template.TemplateID)
	}
	if template.RuntimeClass != "android-studio-emulator" {
		t.Fatalf("unexpected runtime class %q", template.RuntimeClass)
	}
	if template.DisplayName != "Medium Phone" {
		t.Fatalf("unexpected display name %q", template.DisplayName)
	}
	if template.Metadata["avdName"] != avdName {
		t.Fatalf("expected avd metadata %q, got %q", avdName, template.Metadata["avdName"])
	}
	if template.Metadata["emulatorPath"] != emulatorPath {
		t.Fatalf("expected emulator path %q, got %q", emulatorPath, template.Metadata["emulatorPath"])
	}
	if template.Metadata["hw.lcd.width"] != "1080" {
		t.Fatalf("expected width metadata 1080, got %q", template.Metadata["hw.lcd.width"])
	}
}
