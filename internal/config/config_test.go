package config

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

const testDatabaseURL = "postgres://reusery:secret@localhost:5432/reusery?sslmode=disable"

func TestLoadDefaults(t *testing.T) {
	t.Setenv(EnvHTTPAddr, "")
	t.Setenv(EnvLogLevel, "")
	t.Setenv(EnvDatabaseURL, testDatabaseURL)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want default %q", cfg.HTTPAddr, DefaultHTTPAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.DatabaseURL != testDatabaseURL {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, testDatabaseURL)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv(EnvHTTPAddr, "127.0.0.1:9090")
	t.Setenv(EnvLogLevel, "debug")
	t.Setenv(EnvDatabaseURL, testDatabaseURL)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, "127.0.0.1:9090")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelDebug)
	}
}

func TestLoadIgnoresBlankAddr(t *testing.T) {
	t.Setenv(EnvHTTPAddr, "   ")
	t.Setenv(EnvDatabaseURL, testDatabaseURL)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want default %q", cfg.HTTPAddr, DefaultHTTPAddr)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")

	_, err := Load()
	if !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("error = %v, want %v", err, ErrMissingDatabaseURL)
	}
}

func TestLoadRejectsMalformedDatabaseURL(t *testing.T) {
	tests := []string{
		"not a url",
		"mysql://user:pass@localhost/db",
		"postgres://",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(EnvDatabaseURL, raw)

			_, err := Load()
			if !errors.Is(err, ErrInvalidDatabaseURL) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidDatabaseURL)
			}
		})
	}
}

func TestLoadAcceptsKeywordDSN(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "host=localhost port=5432 dbname=reusery")

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"nonsense", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseLogLevel(tt.input); got != tt.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGitHubTokenIsOptionalAndNeverLeaked(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		t.Setenv(EnvDatabaseURL, testDatabaseURL)
		t.Setenv(EnvGitHubToken, "")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.GitHubToken != "" {
			t.Errorf("GitHubToken = %q, want empty for public discovery", cfg.GitHubToken)
		}
	})

	t.Run("present and trimmed", func(t *testing.T) {
		t.Setenv(EnvDatabaseURL, testDatabaseURL)
		t.Setenv(EnvGitHubToken, "  ghp_example_token  ")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.GitHubToken != "ghp_example_token" {
			t.Errorf("GitHubToken = %q, want the trimmed token", cfg.GitHubToken)
		}
	})

	t.Run("never appears in configuration errors", func(t *testing.T) {
		t.Setenv(EnvDatabaseURL, "mysql://nope")
		t.Setenv(EnvGitHubToken, "ghp_example_token")

		_, err := Load()
		if err == nil {
			t.Fatal("Load succeeded, want a malformed database URL error")
		}
		if strings.Contains(err.Error(), "ghp_example_token") {
			t.Errorf("configuration error leaked the token: %q", err)
		}
	})
}
