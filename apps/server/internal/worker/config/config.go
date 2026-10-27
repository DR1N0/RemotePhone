package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr          string
	ListenBaseURL       string
	WorkerID            string
	HomeDir             string
	AndroidSDKRoot      string
	AndroidEmulatorPath string
	FFmpegPath          string
	StunURL             string
	InstanceDataRoot    string
	BaseImageRoot       string
	StreamBitRate       int
	WebRTCHostIP        string
	AudioDebug          bool
	Headless            bool
	EmulatorAudio       string
	GPUMode             string
}

func Load() Config {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		if resolved, err := os.UserHomeDir(); err == nil {
			homeDir = resolved
		}
	}

	return Config{
		ListenAddr:          getenv("MAKO_WORKER_LISTEN_ADDR", "127.0.0.1:7780"),
		ListenBaseURL:       getenv("MAKO_WORKER_BASE_URL", "http://127.0.0.1:7780"),
		WorkerID:            getenv("MAKO_WORKER_ID", "local"),
		HomeDir:             homeDir,
		AndroidSDKRoot:      firstNonEmpty(os.Getenv("MAKO_ANDROID_SDK_ROOT"), os.Getenv("ANDROID_SDK_ROOT"), os.Getenv("ANDROID_HOME")),
		AndroidEmulatorPath: os.Getenv("MAKO_ANDROID_EMULATOR_PATH"),
		FFmpegPath:          os.Getenv("MAKO_FFMPEG_PATH"),
		StunURL:             stunURLFromEnv(),
		InstanceDataRoot:    getenv("MAKO_INSTANCE_DATA_ROOT", filepath.Join(homeDir, ".mako", "instance-data")),
		BaseImageRoot:       getenv("MAKO_BASE_IMAGE_ROOT", filepath.Join(homeDir, ".mako", "base-images")),
		StreamBitRate:       getenvInt("MAKO_STREAM_BITRATE", 2_000_000),
		WebRTCHostIP:        os.Getenv("MAKO_WEBRTC_HOST_IP"),
		AudioDebug:          os.Getenv("MAKO_AUDIO_DEBUG") == "1",
		// One worker owns one instance, so headless is the deployment default;
		// MAKO_HEADLESS=0 is only for local debugging with a visible emulator.
		Headless: getenvBool("MAKO_HEADLESS", true),
		// Default to the emulator's "none" audio backend: the guest's audio
		// graph still feeds gRPC streamAudio (verified 440Hz), but nothing is
		// played on the host speakers. Use MAKO_EMULATOR_AUDIO=auto for a
		// local debugging session where host sound is wanted.
		EmulatorAudio: getenv("MAKO_EMULATOR_AUDIO", "none"),
		// On Windows the emulator's persisted hardware-qemu.ini may select
		// SwiftShader even when a capable GPU is present. Explicitly request
		// the host GPU; MAKO_GPU_MODE=auto/swiftshader is the fallback for
		// machines without usable host acceleration.
		GPUMode: getenv("MAKO_GPU_MODE", defaultGPUMode()),
	}
}

func defaultGPUMode() string {
	if runtime.GOOS == "windows" {
		return "host"
	}
	return "auto"
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// getenvInt parses a positive integer override, falling back on an unset or
// invalid value (stream bitrate/fps are tuning knobs, not startup-critical
// configuration — unlike the control plane's duration env vars, a typo here
// should not prevent the worker from starting).
func getenvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func getenvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// stunURLFromEnv resolves STUN_URL. The default (a public Google STUN
// server) is useful for NAT-traversal experiments but is both unreachable in
// some networks and unnecessary on a trusted LAN, where host candidates are
// enough. Setting STUN_URL to "none" or "-" disables it explicitly; an empty
// unset value keeps the historical default for backwards compatibility.
func stunURLFromEnv() string {
	switch value := os.Getenv("STUN_URL"); value {
	case "none", "-":
		return ""
	case "":
		return "stun:stun.l.google.com:19302"
	default:
		return value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
