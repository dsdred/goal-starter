package validation

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	minPort = 1
	maxPort = 65535
)

// ValidatePort checks if the given port is in the valid range (1-65535).
func ValidatePort(port int) error {
	if port < minPort || port > maxPort {
		return fmt.Errorf("port must be between %d and %d, got %d", minPort, maxPort, port)
	}
	return nil
}

// ValidateHost checks if the given host is valid.
// Empty host is allowed (means bind to all interfaces).
// Otherwise, must be a valid IP address or hostname.
func ValidateHost(host string) error {
	if host == "" {
		return nil // empty host is allowed
	}

	// Try parsing as IP address first.
	if ip := net.ParseIP(host); ip != nil {
		return nil // valid IP address
	}

	// Check if it's a valid hostname.
	if len(host) > 253 {
		return fmt.Errorf("hostname must be at most 253 characters")
	}

	// Hostname labels must be 63 characters or less.
	// Each label can contain only ASCII letters, digits, and hyphens.
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("hostname label must be 1-63 characters")
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return fmt.Errorf("hostname label contains invalid character: %c", c)
			}
		}
	}

	return nil
}

// ValidateAddress validates both host and port together.
func ValidateAddress(host string, port int) error {
	if err := ValidateHost(host); err != nil {
		return fmt.Errorf("invalid host: %w", err)
	}
	if err := ValidatePort(port); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	return nil
}

// ParsePort parses a string port and validates it.
func ParsePort(portStr string) (int, error) {
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("port must be a number")
	}
	if err := ValidatePort(port); err != nil {
		return 0, err
	}
	return port, nil
}
