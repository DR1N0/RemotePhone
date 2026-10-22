package mdns

import (
	"strings"
	"testing"

	"mako/internal/controlplane/serverinfo"
)

func TestInstanceNameIsStableAndCollisionResistant(t *testing.T) {
	info := &serverinfo.Info{ServerID: "7839a61b-2719-4196-8f1b-ce2c43ae177b", Name: "Living Room"}
	got := instanceName(info)
	if !strings.Contains(got, "Living Room") || !strings.Contains(got, "7839a61b") {
		t.Fatalf("instanceName = %q, want name and short ID", got)
	}
}

func TestTXTRecords(t *testing.T) {
	info := &serverinfo.Info{ServerID: "id-1", Name: "Desk"}
	records := txtRecords(info)
	want := map[string]bool{"id=id-1": true, "name=Desk": true, "v=1": true, "path=/api/v1": true}
	for _, record := range records {
		delete(want, record)
	}
	if len(want) != 0 {
		t.Fatalf("missing TXT records: %v (got %v)", want, records)
	}
}
