package androidstudio

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mako-worker/internal/runtime"
)

const (
	RuntimeClass                   = "android-studio-emulator"
	defaultSDKRelativeEmulatorPath = "emulator/emulator"
)

type DiscoveryOptions struct {
	HomeDir              string
	AndroidSDKRoot       string
	ExplicitEmulatorPath string
}

func ResolveEmulatorBinary(opts DiscoveryOptions) (string, error) {
	candidates := make([]string, 0, 4)
	if opts.ExplicitEmulatorPath != "" {
		candidates = append(candidates, opts.ExplicitEmulatorPath)
	}
	if opts.AndroidSDKRoot != "" {
		candidates = append(candidates, filepath.Join(opts.AndroidSDKRoot, defaultSDKRelativeEmulatorPath))
	}
	if opts.HomeDir != "" {
		candidates = append(candidates,
			filepath.Join(opts.HomeDir, "Library", "Android", "sdk", defaultSDKRelativeEmulatorPath),
			filepath.Join(opts.HomeDir, "Library", "Android", "sdk", "tools", defaultSDKRelativeEmulatorPath),
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

	return "", errors.New("android studio emulator binary not found")
}

func DiscoverTemplates(opts DiscoveryOptions) ([]runtime.Template, error) {
	emulatorPath, err := ResolveEmulatorBinary(opts)
	if err != nil {
		return nil, err
	}

	avdDir := filepath.Join(opts.HomeDir, ".android", "avd")
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
			TemplateID:   "macos-android-studio:" + avdName,
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
