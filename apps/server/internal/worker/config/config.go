package config

import (
	"os"
	"path/filepath"
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
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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
