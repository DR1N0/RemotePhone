package config

import "testing"

func TestLoadDefaultsToLoopbackListener(t *testing.T) {
	t.Setenv("REMOTEPHONE_WORKER_LISTEN_ADDR", "")

	cfg := Load()

	if cfg.ListenAddr != "127.0.0.1:7780" {
		t.Fatalf("expected loopback listener by default, got %q", cfg.ListenAddr)
	}
}

func TestLoadUsesConfiguredListenerAddress(t *testing.T) {
	t.Setenv("REMOTEPHONE_WORKER_LISTEN_ADDR", "192.168.1.10:7780")

	cfg := Load()

	if cfg.ListenAddr != "192.168.1.10:7780" {
		t.Fatalf("expected configured listener, got %q", cfg.ListenAddr)
	}
}

func TestLoadStreamBitRate(t *testing.T) {
	t.Setenv("REMOTEPHONE_STREAM_BITRATE", "800000")
	if cfg := Load(); cfg.StreamBitRate != 800000 {
		t.Errorf("StreamBitRate = %d, want 800000", cfg.StreamBitRate)
	}
	t.Setenv("REMOTEPHONE_STREAM_BITRATE", "not-a-number")
	if cfg := Load(); cfg.StreamBitRate != 2_000_000 {
		t.Errorf("invalid REMOTEPHONE_STREAM_BITRATE fell back to %d, want 2000000", cfg.StreamBitRate)
	}
}

func TestLoadWebRTCHostIPOverride(t *testing.T) {
	t.Setenv("REMOTEPHONE_WEBRTC_HOST_IP", "192.168.1.10")
	if cfg := Load(); cfg.WebRTCHostIP != "192.168.1.10" {
		t.Errorf("WebRTCHostIP = %q, want explicit override", cfg.WebRTCHostIP)
	}
	t.Setenv("REMOTEPHONE_WEBRTC_HOST_IP", "")
	if cfg := Load(); cfg.WebRTCHostIP != "" {
		t.Errorf("WebRTCHostIP = %q, want empty (advertise all routable IPv4)", cfg.WebRTCHostIP)
	}
}

func TestLoadAudioDebug(t *testing.T) {
	t.Setenv("REMOTEPHONE_AUDIO_DEBUG", "1")
	if cfg := Load(); !cfg.AudioDebug {
		t.Error("AudioDebug = false, want true for REMOTEPHONE_AUDIO_DEBUG=1")
	}
	t.Setenv("REMOTEPHONE_AUDIO_DEBUG", "0")
	if cfg := Load(); cfg.AudioDebug {
		t.Error("AudioDebug = true, want false otherwise")
	}
}

func TestLoadHeadless(t *testing.T) {
	t.Setenv("REMOTEPHONE_HEADLESS", "")
	if cfg := Load(); !cfg.Headless {
		t.Error("Headless = false, want true by default")
	}
	t.Setenv("REMOTEPHONE_HEADLESS", "0")
	if cfg := Load(); cfg.Headless {
		t.Error("Headless = true, want false for REMOTEPHONE_HEADLESS=0")
	}
	t.Setenv("REMOTEPHONE_HEADLESS", "nonsense")
	if cfg := Load(); !cfg.Headless {
		t.Error("invalid REMOTEPHONE_HEADLESS should fall back to true")
	}
}

func TestLoadEmulatorAudio(t *testing.T) {
	t.Setenv("REMOTEPHONE_EMULATOR_AUDIO", "")
	if cfg := Load(); cfg.EmulatorAudio != "none" {
		t.Errorf("EmulatorAudio = %q, want none by default", cfg.EmulatorAudio)
	}
	t.Setenv("REMOTEPHONE_EMULATOR_AUDIO", "auto")
	if cfg := Load(); cfg.EmulatorAudio != "auto" {
		t.Errorf("EmulatorAudio = %q, want auto override", cfg.EmulatorAudio)
	}
}

func TestLoadGPUMode(t *testing.T) {
	t.Setenv("REMOTEPHONE_GPU_MODE", "")
	if cfg := Load(); cfg.GPUMode != defaultGPUMode() {
		t.Errorf("GPUMode = %q, want default %q", cfg.GPUMode, defaultGPUMode())
	}
	t.Setenv("REMOTEPHONE_GPU_MODE", "swiftshader")
	if cfg := Load(); cfg.GPUMode != "swiftshader" {
		t.Errorf("GPUMode = %q, want swiftshader", cfg.GPUMode)
	}
}

func TestLoadStunNoneDisablesSTUN(t *testing.T) {
	t.Setenv("STUN_URL", "none")
	if cfg := Load(); cfg.StunURL != "" {
		t.Errorf("StunURL = %q, want empty for STUN_URL=none", cfg.StunURL)
	}
}
