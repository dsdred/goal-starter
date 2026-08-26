package config

import (
	"fmt"
	"log/slog"
	"strings"
)

// LogLevel parses a configured log level. An empty value is the default
// (info). Accepted values are case-insensitive.
func LogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logLevel must be one of debug, info, warn, error (got %q)", value)
}
