package validation

import (
	"strings"
	"testing"
)

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid port 80", 80, false},
		{"valid port 443", 443, false},
		{"valid port 8080", 8080, false},
		{"valid port 65535", 65535, false},
		{"port 0 is invalid", 0, true},
		{"port -1 is invalid", -1, true},
		{"port 65536 is invalid", 65536, true},
		{"port 1 is valid", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePort(tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
			}
		})
	}
}

func TestValidateHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{"empty host is valid", "", false},
		{"localhost", "localhost", false},
		{"127.0.0.1", "127.0.0.1", false},
		{"::1", "::1", false},
		{"valid domain", "example.com", false},
		{"long hostname", strings.Repeat("a", 254), true},
		{"invalid char in hostname", "exam!ple.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHost(%q) error = %v, wantErr %v", tt.host, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAddress(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		port    int
		wantErr bool
	}{
		{"valid address", "localhost", 8080, false},
		{"invalid port", "localhost", 0, true},
		{"invalid host", "exam!ple.com", 8080, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAddress(tt.host, tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAddress(%q, %d) error = %v, wantErr %v", tt.host, tt.port, err, tt.wantErr)
			}
		})
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"valid port string", "8080", 8080, false},
		{"invalid port string", "abc", 0, true},
		{"port 0", "0", 0, true},
		{"port 65536", "65536", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, err := ParsePort(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParsePort(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if port != tt.want {
				t.Errorf("ParsePort(%q) = %d, want %d", tt.input, port, tt.want)
			}
		})
	}
}
