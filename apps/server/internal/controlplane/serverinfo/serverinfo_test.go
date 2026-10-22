package serverinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-id")
	first, err := LoadOrCreate(path, "Living Room")
	if err != nil {
		t.Fatal(err)
	}
	if first.ServerID == "" {
		t.Fatal("ServerID is empty")
	}
	if first.Name != "Living Room" {
		t.Fatalf("Name = %q, want Living Room", first.Name)
	}

	second, err := LoadOrCreate(path, "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if second.ServerID != first.ServerID {
		t.Fatalf("ServerID changed: %q != %q", second.ServerID, first.ServerID)
	}
	if second.Name != "Renamed" {
		t.Fatalf("Name = %q, want Renamed", second.Name)
	}
}

func TestLoadOrCreateRejectsEmptyIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-id")
	if err := os.WriteFile(path, []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path, "x"); err == nil {
		t.Fatal("accepted an empty server ID file")
	}
}
