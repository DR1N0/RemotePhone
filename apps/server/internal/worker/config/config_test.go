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

func TestLoadStreamBitRate(t *testing.T) {
	t.Setenv("MAKO_STREAM_BITRATE", "800000")
	if cfg := Load(); cfg.StreamBitRate != 800000 {
		t.Errorf("StreamBitRate = %d, want 800000", cfg.StreamBitRate)
	}
	t.Setenv("MAKO_STREAM_BITRATE", "not-a-number")
	if cfg := Load(); cfg.StreamBitRate != 2_000_000 {
		t.Errorf("invalid MAKO_STREAM_BITRATE fell back to %d, want 2000000", cfg.StreamBitRate)
	}
}

func TestLoadWebRTCHostIPOverride(t *testing.T) {
	t.Setenv("MAKO_WEBRTC_HOST_IP", "192.168.1.7")
	if cfg := Load(); cfg.WebRTCHostIP != "192.168.1.7" {
		t.Errorf("WebRTCHostIP = %q, want explicit override", cfg.WebRTCHostIP)
	}
}

func TestLoadStunNoneDisablesSTUN(t *testing.T) {
	t.Setenv("STUN_URL", "none")
	if cfg := Load(); cfg.StunURL != "" {
		t.Errorf("StunURL = %q, want empty for STUN_URL=none", cfg.StunURL)
	}
}
