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
	EnvHTTPAddr     = "REUSERY_HTTP_ADDR"
	EnvLogLevel     = "REUSERY_LOG_LEVEL"
	EnvDatabaseURL  = "REUSERY_DATABASE_URL"
	EnvGitHubToken  = "REUSERY_GITHUB_TOKEN"
	EnvOpenAIAPIKey = "REUSERY_OPENAI_API_KEY"
	EnvOpenAIModel  = "REUSERY_OPENAI_MODEL"
	// EnvAPIEnableExternalOperations gates the HTTP operations that spend
	// model tokens or external provider quota. It affects the HTTP API only:
	// the equivalent CLI commands keep working regardless of its value.
	EnvAPIEnableExternalOperations = "REUSERY_API_ENABLE_EXTERNAL_OPERATIONS"
	// EnvMCPEnableExternalOperations gates the MCP discovery and enrichment
	// tools. It is deliberately separate from the HTTP switch: neither surface
	// silently enables the other, and an agent-facing protocol defaults to
	// read-only until someone says otherwise.
	EnvMCPEnableExternalOperations = "REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS"
)

// DefaultOpenAIModel is the default model for intent normalisation.
//
// It is gpt-6-luna: the model that Packet 6's two successful release-gate
// corpus runs actually used, the only model the evaluation account exposed,
// and the efficient cost-sensitive model in current OpenAI documentation —
// whose published token pricing is lower than the model it replaced.
// Intent normalisation is bounded structured work and Reusery has a
// first-class cost-saving objective, so the default is deliberately not the
// strongest available model. An explicit override exists for evaluation and
// future tuning.
const DefaultOpenAIModel = "gpt-6-luna"

// Configuration errors. The database URL itself is never included, because it
// may contain credentials.
var (
	ErrMissingDatabaseURL = errors.New("config: REUSERY_DATABASE_URL is required")
	ErrInvalidDatabaseURL = errors.New("config: REUSERY_DATABASE_URL is malformed")
	// ErrMissingOpenAIAPIKey reports a model-backed command invoked without a
	// key. Only model-backed commands need it: serve, seed, discover, resolve
	// and resolution keep working with no model key at all.
	ErrMissingOpenAIAPIKey = errors.New("config: REUSERY_OPENAI_API_KEY is required for model-backed commands")
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
	// APIEnableExternalOperations allows the HTTP API to run operations that
	// spend model tokens or external provider quota: POST /v1/normalize,
	// POST /v1/discover and POST /v1/enrich. It defaults to false so a freshly
	// started server never exposes an unauthenticated paid-model endpoint.
	// Offline HTTP operations (resolve, refine, inspection, health, ready) are
	// unaffected, and so are the equivalent CLI commands.
	APIEnableExternalOperations bool
	// MCPEnableExternalOperations allows the MCP server to run reusery_discover
	// and reusery_enrich, the only two tools that spend provider quota. It
	// defaults to false: an agent-facing protocol must be read-only until it is
	// explicitly enabled. catalog, resolve, refine, inspection and outcome
	// reporting are unaffected, and so are the equivalent CLI commands and the
	// separate HTTP switch.
	MCPEnableExternalOperations bool
}

// ParseStrictBool parses an on/off environment value.
//
// Only "true" and "false" are accepted, case-insensitively. An empty value
// means the variable is unset and falls back to fallback. Anything else is a
// configuration error rather than a silent default: a typo in a switch that
// guards paid operations must fail loudly.
func ParseStrictBool(raw string, fallback bool) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "":
		return fallback, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("config: strict boolean must be \"true\" or \"false\", got %q", raw)
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
	enableExternal, err := ParseStrictBool(os.Getenv(EnvAPIEnableExternalOperations), false)
	if err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", EnvAPIEnableExternalOperations, err)
	}
	enableMCPExternal, err := ParseStrictBool(os.Getenv(EnvMCPEnableExternalOperations), false)
	if err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", EnvMCPEnableExternalOperations, err)
	}
	return Config{
		HTTPAddr:                    envOr(EnvHTTPAddr, DefaultHTTPAddr),
		LogLevel:                    ParseLogLevel(os.Getenv(EnvLogLevel)),
		DatabaseURL:                 databaseURL,
		GitHubToken:                 strings.TrimSpace(os.Getenv(EnvGitHubToken)),
		APIEnableExternalOperations: enableExternal,
		MCPEnableExternalOperations: enableMCPExternal,
	}, nil
}

// ModelConfig is model-provider configuration only.
//
// It is deliberately separate from Config: natural-language structuring and
// the database are independent concerns, so `reusery normalize` and
// `reusery normalize-eval` work with no PostgreSQL configured at all.
type ModelConfig struct {
	// OpenAIAPIKey is the bearer token for the OpenAI API. It is optional
	// globally, required only by commands that actually invoke OpenAI. It is
	// never logged, never persisted and never included in an error.
	OpenAIAPIKey string
	// OpenAIModel overrides the default model. Empty means the default.
	OpenAIModel string
}

// LoadModel reads model-provider configuration from the environment. It never
// consults the database URL, so it succeeds where Load would not.
func LoadModel() (ModelConfig, error) {
	return ModelConfig{
		OpenAIAPIKey: strings.TrimSpace(os.Getenv(EnvOpenAIAPIKey)),
		OpenAIModel:  strings.TrimSpace(os.Getenv(EnvOpenAIModel)),
	}, nil
}

// Model returns the resolved model name, falling back to the Packet 6 default.
func (m ModelConfig) Model() string {
	if m.OpenAIModel != "" {
		return m.OpenAIModel
	}
	return DefaultOpenAIModel
}

// RequireAPIKey returns the trimmed key or a clear configuration error that
// never contains the key.
func (m ModelConfig) RequireAPIKey() (string, error) {
	if m.OpenAIAPIKey == "" {
		return "", ErrMissingOpenAIAPIKey
	}
	return m.OpenAIAPIKey, nil
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
