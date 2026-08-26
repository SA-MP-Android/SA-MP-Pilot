package main

import "testing"

func TestValidateListenSecurity(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		insecure    bool
		authEnabled bool
		wantError   bool
	}{
		{name: "loopback without auth", address: "127.0.0.1:8080"},
		{name: "ipv6 loopback without auth", address: "[::1]:8080"},
		{name: "localhost without auth", address: "localhost:8080"},
		{name: "remote with auth", address: "192.168.1.10:8080", authEnabled: true},
		{name: "remote insecure", address: "0.0.0.0:8080", insecure: true},
		{name: "remote without auth", address: "0.0.0.0:8080", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateListenSecurity(tt.address, tt.insecure, tt.authEnabled)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError = %v", err, tt.wantError)
			}
		})
	}
}

func TestIsLoopbackListenAddress(t *testing.T) {
	if isLoopbackListenAddress("0.0.0.0:8080") {
		t.Fatal("0.0.0.0 must not be treated as loopback")
	}
	if isLoopbackListenAddress("127.0.0.1") {
		t.Fatal("address without a port must be rejected")
	}
}
