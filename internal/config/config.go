// Package config loads minimal environment-based configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	EnvHTTPAddr    = "REUSERY_HTTP_ADDR"
	EnvLogLevel    = "REUSERY_LOG_LEVEL"
	EnvDatabaseURL = "REUSERY_DATABASE_URL"
	EnvGitHubToken = "REUSERY_GITHUB_TOKEN"
)

// Configuration errors. The database URL itself is never included, because it
// may contain credentials.
var (
	ErrMissingDatabaseURL = errors.New("config: REUSERY_DATABASE_URL is required")
	ErrInvalidDatabaseURL = errors.New("config: REUSERY_DATABASE_URL is malformed")
)

// Config holds the runtime configuration for the application.
type Config struct {
	// HTTPAddr is the TCP address the HTTP server listens on (e.g. ":8080").
	HTTPAddr string
	// LogLevel controls the verbosity of structured logs.
	LogLevel slog.Level
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string
	// GitHubToken is an OPTIONAL bearer token for GitHub's public API. It
	// raises rate limits when present; public unauthenticated discovery works
	// without it. Readiness never depends on it, it is never logged and it is
	// never included in a configuration error.
	GitHubToken string
}

// Load reads configuration from the environment, falling back to defaults.
// The database URL is required and validated; it is never logged. The GitHub
// token is optional and may legitimately be empty.
func Load() (Config, error) {
	databaseURL := strings.TrimSpace(os.Getenv(EnvDatabaseURL))
	if databaseURL == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:    envOr(EnvHTTPAddr, DefaultHTTPAddr),
		LogLevel:    ParseLogLevel(os.Getenv(EnvLogLevel)),
		DatabaseURL: databaseURL,
		GitHubToken: strings.TrimSpace(os.Getenv(EnvGitHubToken)),
	}, nil
}

// validateDatabaseURL rejects obviously malformed values so startup can fail
// clearly. The connection string is never echoed in the error.
func validateDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDatabaseURL, err)
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		if u.Host == "" && u.Path == "" {
			return ErrInvalidDatabaseURL
		}
		return nil
	case "":
		// libpq keyword/value form, e.g. "host=localhost dbname=reusery".
		if strings.Contains(raw, "=") {
			return nil
		}
		return ErrInvalidDatabaseURL
	default:
		return ErrInvalidDatabaseURL
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
