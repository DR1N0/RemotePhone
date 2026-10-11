package config

import "testing"

func TestLoadDefaultsToLoopbackListener(t *testing.T) {
	t.Setenv("MAKO_WORKER_LISTEN_ADDR", "")

	cfg := Load()

	if cfg.ListenAddr != "127.0.0.1:7780" {
		t.Fatalf("expected loopback listener by default, got %q", cfg.ListenAddr)
	}
}

func TestLoadUsesConfiguredListenerAddress(t *testing.T) {
	t.Setenv("MAKO_WORKER_LISTEN_ADDR", "192.168.1.10:7780")

	cfg := Load()

	if cfg.ListenAddr != "192.168.1.10:7780" {
		t.Fatalf("expected configured listener, got %q", cfg.ListenAddr)
	}
}
