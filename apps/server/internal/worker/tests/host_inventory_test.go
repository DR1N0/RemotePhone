package tests

import (
	"testing"

	"remotephone/internal/worker/host"
)

func TestNormalizeGOOS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "darwin maps to macos", input: "darwin", want: "macos"},
		{name: "linux stays linux", input: "linux", want: "linux"},
		{name: "windows stays windows", input: "windows", want: "windows"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := host.NormalizeGOOS(tt.input); got != tt.want {
				t.Fatalf("NormalizeGOOS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
