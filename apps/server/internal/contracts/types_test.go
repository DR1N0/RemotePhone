package contracts

import (
	"encoding/json"
	"testing"
)

func TestResourceProfileJSONRoundTrip(t *testing.T) {
	rp := ResourceProfile{
		CPUMillicores:    1000,
		MemoryMB:         2048,
		DiskMB:           8192,
		AccelerationMode: AccelerationAuto,
		StreamProfile:    StreamPrototype,
	}
	b, err := json.Marshal(rp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ResourceProfile
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != rp {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, rp)
	}
}

func TestPlacementPolicySpecificWorkerJSON(t *testing.T) {
	p := PlacementPolicy{Kind: PlacementSpecificWorker, WorkerID: "w-1"}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"kind":"specific-worker","workerId":"w-1"}`
	if string(b) != want {
		t.Errorf("Marshal() = %s, want %s", b, want)
	}
}
