package transport_test

import (
	"remotephone/internal/worker/transport"
	"testing"
)

func TestStoreGetPutDelete(t *testing.T) {
	s := transport.NewStore()
	key := "inst-1:att-1"

	s.Put(key, transport.NoopAdapter{})

	got, ok := s.Get(key)
	if !ok {
		t.Fatal("expected to find adapter")
	}
	if got == nil {
		t.Fatal("expected non-nil adapter")
	}

	s.Delete(key)
	_, ok = s.Get(key)
	if ok {
		t.Fatal("expected adapter to be gone after delete")
	}
}
