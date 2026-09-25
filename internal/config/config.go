// Package config loads minimal environment-based configuration.
package config

import (
	"log/slog"
	"os"
	"strings"
)

// Development defaults.
const (
	DefaultHTTPAddr = ":8080"
	DefaultLogLevel = "info"
)

// Environment variable names.
const (
	EnvHTTPAddr = "REUSERY_HTTP_ADDR"
	EnvLogLevel = "REUSERY_LOG_LEVEL"
)

// Config holds the runtime configuration for the application.
type Config struct {
	// HTTPAddr is the TCP address the HTTP server listens on (e.g. ":8080").
	HTTPAddr string
	// LogLevel controls the verbosity of structured logs.
	LogLevel slog.Level
}

// Load reads configuration from the environment, falling back to defaults.
func Load() Config {
	return Config{
		HTTPAddr: envOr(EnvHTTPAddr, DefaultHTTPAddr),
		LogLevel: ParseLogLevel(os.Getenv(EnvLogLevel)),
	}
}

// ParseLogLevel maps a level name to a slog.Level. Empty or unknown values
// fall back to info so a typo never silences the server or crashes startup.
func ParseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
