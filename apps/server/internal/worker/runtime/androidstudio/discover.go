package androidstudio

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"mako/internal/worker/runtime"
)

const (
	RuntimeClass = "android-studio-emulator"

	// TemplateIDPrefix is deliberately host-OS neutral: the same AVD
	// discovered on macOS, Linux, or Windows must produce the same template
	// id, otherwise instance rows (and clients) would be tied to whichever
	// OS happened to discover them first. This used to be
	// "macos-android-studio:", a leftover from the project's macOS-only
	// origins.
	TemplateIDPrefix = "android-studio:"

	// instanceAVDPrefix marks mako's own per-instance cloned AVDs. They live
	// in the same ~/.android/avd directory as real templates, so discovery
	// must skip them — otherwise a running instance's clone shows up as a
	// selectable template (and can be cloned again, producing chains).
	instanceAVDPrefix = "mako_instance_"
)

// emulatorBinaryName returns the platform's emulator executable filename.
func emulatorBinaryName() string {
	if goruntime.GOOS == "windows" {
		return "emulator.exe"
	}
	return "emulator"
}

// qemuImgBinaryName returns the platform's qemu-img executable filename.
func qemuImgBinaryName() string {
	if goruntime.GOOS == "windows" {
		return "qemu-img.exe"
	}
	return "qemu-img"
}

type DiscoveryOptions struct {
	HomeDir              string
	AndroidSDKRoot       string
	ExplicitEmulatorPath string
}

func ResolveEmulatorBinary(opts DiscoveryOptions) (string, error) {
	binaryName := emulatorBinaryName()
	candidates := make([]string, 0, 8)
	if opts.ExplicitEmulatorPath != "" {
		candidates = append(candidates, opts.ExplicitEmulatorPath)
	}
	if opts.AndroidSDKRoot != "" {
		candidates = append(candidates, filepath.Join(opts.AndroidSDKRoot, "emulator", binaryName))
	}
	if opts.HomeDir != "" {
		candidates = append(candidates,
			// macOS Android Studio's SDK location.
			filepath.Join(opts.HomeDir, "Library", "Android", "sdk", "emulator", binaryName),
			filepath.Join(opts.HomeDir, "Library", "Android", "sdk", "tools", "emulator", binaryName),
			// Linux user-scoped SDK location.
			filepath.Join(opts.HomeDir, "Android", "Sdk", "emulator", binaryName),
			filepath.Join(opts.HomeDir, "Android", "sdk", "emulator", binaryName),
			// Windows' default per-user SDK location. Derived from the home
			// directory rather than $LOCALAPPDATA so discovery stays
			// deterministic and testable with a fake home.
			filepath.Join(opts.HomeDir, "AppData", "Local", "Android", "Sdk", "emulator", binaryName),
		)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", errors.New("android studio emulator binary not found (set MAKO_ANDROID_EMULATOR_PATH or MAKO_ANDROID_SDK_ROOT)")
}

func DiscoverTemplates(opts DiscoveryOptions) ([]runtime.Template, error) {
	emulatorPath, err := ResolveEmulatorBinary(opts)
	if err != nil {
		return nil, err
	}

	avdDir := os.Getenv("ANDROID_AVD_HOME")
	if avdDir == "" {
		avdDir = filepath.Join(opts.HomeDir, ".android", "avd")
	}
	entries, err := os.ReadDir(avdDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []runtime.Template{}, nil
		}
		return nil, err
	}

	templates := make([]runtime.Template, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ini") {
			continue
		}

		avdName := strings.TrimSuffix(entry.Name(), ".ini")
		if strings.HasPrefix(avdName, instanceAVDPrefix) {
			continue
		}
		iniValues, err := readIniFile(filepath.Join(avdDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read avd ini for %s: %w", avdName, err)
		}

		avdPath := iniValues["path"]
		if avdPath == "" {
			avdPath = filepath.Join(avdDir, avdName+".avd")
		}

		configValues, err := readIniFile(filepath.Join(avdPath, "config.ini"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				configValues = map[string]string{}
			} else {
				return nil, fmt.Errorf("read avd config for %s: %w", avdName, err)
			}
		}

		metadata := map[string]string{
			"avdName":      avdName,
			"avdPath":      avdPath,
			"emulatorPath": emulatorPath,
		}
		for key, value := range configValues {
			metadata[key] = value
		}

		templates = append(templates, runtime.Template{
			TemplateID:   TemplateIDPrefix + avdName,
			RuntimeClass: RuntimeClass,
			DisplayName:  formatDisplayName(avdName),
			Metadata:     metadata,
		})
	}

	sort.Slice(templates, func(i, j int) bool {
		return templates[i].TemplateID < templates[j].TemplateID
	})

	return templates, nil
}

func readIniFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

func formatDisplayName(avdName string) string {
	words := strings.Split(avdName, "_")
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
