package main

import "testing"

func TestClientListenAddrFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		setAddr  bool
		port     string
		setPort  bool
		wantAddr string
		wantPort int
		wantErr  bool
	}{
		{name: "default", wantAddr: "127.0.0.1:3000", wantPort: 3000},
		{name: "address port", setAddr: true, addr: "0.0.0.0:3010", wantAddr: "0.0.0.0:3010", wantPort: 3010},
		{name: "port override", setAddr: true, addr: "0.0.0.0:3010", setPort: true, port: "3011", wantAddr: "0.0.0.0:3011", wantPort: 3011},
		{name: "port alone", setPort: true, port: "3012", wantAddr: "127.0.0.1:3012", wantPort: 3012},
		{name: "blank port falls back", setAddr: true, addr: "0.0.0.0:3010", setPort: true, port: "  ", wantAddr: "0.0.0.0:3010", wantPort: 3010},
		{name: "ipv6 address", setAddr: true, addr: "[::1]:3010", wantAddr: "[::1]:3010", wantPort: 3010},
		{name: "missing address port", setAddr: true, addr: "127.0.0.1", wantErr: true},
		{name: "non-numeric address port", setAddr: true, addr: "127.0.0.1:abc", wantErr: true},
		{name: "address port out of range", setAddr: true, addr: "127.0.0.1:65536", wantErr: true},
		{name: "invalid override", setPort: true, port: "0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setAddr {
				t.Setenv("MAKO_CLIENT_ADDR", tt.addr)
			} else {
				unsetEnvForTest(t, "MAKO_CLIENT_ADDR")
			}
			if tt.setPort {
				t.Setenv("MAKO_CLIENT_PORT", tt.port)
			} else {
				unsetEnvForTest(t, "MAKO_CLIENT_PORT")
			}

			addr, port, err := clientListenAddrFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("clientListenAddrFromEnv() = (%q, %d), nil; want an error", addr, port)
				}
				return
			}
			if err != nil {
				t.Fatalf("clientListenAddrFromEnv() error = %v", err)
			}
			if addr != tt.wantAddr || port != tt.wantPort {
				t.Fatalf("clientListenAddrFromEnv() = (%q, %d), want (%q, %d)", addr, port, tt.wantAddr, tt.wantPort)
			}
		})
	}
}
