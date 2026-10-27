package main

import (
	"os"
	"testing"
)

func TestAdminPortFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		set     bool
		value   string
		want    int
		wantErr bool
	}{
		{name: "unset uses default", want: defaultAdminPort},
		{name: "blank uses default", set: true, value: "  ", want: defaultAdminPort},
		{name: "valid port", set: true, value: "3011", want: 3011},
		{name: "trims surrounding spaces", set: true, value: " 3011 ", want: 3011},
		{name: "zero is invalid", set: true, value: "0", wantErr: true},
		{name: "above range is invalid", set: true, value: "65536", wantErr: true},
		{name: "negative is invalid", set: true, value: "-1", wantErr: true},
		{name: "non-numeric is invalid", set: true, value: "admin", wantErr: true},
		{name: "fraction is invalid", set: true, value: "3001.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("MAKO_ADMIN_PORT", tt.value)
			} else {
				unsetEnvForTest(t, "MAKO_ADMIN_PORT")
			}

			got, err := adminPortFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("adminPortFromEnv() = %d, nil; want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("adminPortFromEnv() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("adminPortFromEnv() = %d, want %d", got, tt.want)
			}
		})
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
