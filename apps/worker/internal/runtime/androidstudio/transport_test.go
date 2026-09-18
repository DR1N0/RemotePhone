package androidstudio

import (
	"testing"

	"mako-worker/internal/config"
	"mako-worker/internal/runtime"
	"mako-worker/internal/transport"
)

func TestCreateTransportSessionReusesExistingAdapterAndUpdatesRole(t *testing.T) {
	store := transport.NewStore()
	adapter := NewAdapter(config.Config{}, runtime.NewMemoryHandleStore(), store)
	request := runtime.TransportSessionRequest{
		SessionID:    "session-1",
		AttachmentID: "attachment-1",
		InstanceID:   "instance-1",
		Role:         "observer",
	}

	first, err := adapter.CreateTransportSession(request)
	if err != nil {
		t.Fatalf("create first transport session: %v", err)
	}
	firstAdapter, ok := store.Get("instance-1:attachment-1")
	if !ok {
		t.Fatal("expected first adapter in store")
	}

	request.Role = "participant"
	second, err := adapter.CreateTransportSession(request)
	if err != nil {
		t.Fatalf("create second transport session: %v", err)
	}
	secondAdapter, ok := store.Get("instance-1:attachment-1")
	if !ok {
		t.Fatal("expected reused adapter in store")
	}

	if first.SessionToken != second.SessionToken {
		t.Fatal("expected one reused transport session token")
	}
	if firstAdapter != secondAdapter {
		t.Fatal("expected existing adapter to be reused")
	}
	if got := secondAdapter.Role(); got != "participant" {
		t.Fatalf("got role %q, want participant", got)
	}
}
