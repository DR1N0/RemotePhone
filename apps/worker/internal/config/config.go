package config

import "os"

type Config struct {
	ListenAddr          string
	ListenBaseURL       string
	WorkerID            string
	HomeDir             string
	AndroidSDKRoot      string
	AndroidEmulatorPath string
}

func Load() Config {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		if resolved, err := os.UserHomeDir(); err == nil {
			homeDir = resolved
		}
	}

	return Config{
		ListenAddr:          getenv("MAKO_WORKER_LISTEN_ADDR", ":7780"),
		ListenBaseURL:       getenv("MAKO_WORKER_BASE_URL", "http://127.0.0.1:7780"),
		WorkerID:            getenv("MAKO_WORKER_ID", "local"),
		HomeDir:             homeDir,
		AndroidSDKRoot:      firstNonEmpty(os.Getenv("MAKO_ANDROID_SDK_ROOT"), os.Getenv("ANDROID_SDK_ROOT"), os.Getenv("ANDROID_HOME")),
		AndroidEmulatorPath: os.Getenv("MAKO_ANDROID_EMULATOR_PATH"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
