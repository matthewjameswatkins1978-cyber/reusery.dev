package config

import (
	"errors"
	"strings"
	"testing"
)

func TestParseStrictBool(t *testing.T) {
	cases := []struct {
		raw      string
		fallback bool
		want     bool
	}{
		{"", false, false},
		{"", true, true},
		{"true", false, true},
		{"TRUE", false, true},
		{" True ", false, true},
		{"false", true, false},
		{"FALSE", true, false},
	}
	for _, test := range cases {
		got, err := ParseStrictBool(test.raw, test.fallback)
		if err != nil {
			t.Errorf("ParseStrictBool(%q) error = %v", test.raw, err)
			continue
		}
		if got != test.want {
			t.Errorf("ParseStrictBool(%q, %v) = %v, want %v", test.raw, test.fallback, got, test.want)
		}
	}
}

// TestParseStrictBoolRejectsNonsense proves a typo in a switch that guards
// paid HTTP operations fails loudly rather than silently defaulting.
func TestParseStrictBoolRejectsNonsense(t *testing.T) {
	for _, raw := range []string{"yes-please", "1", "no", "on", "maybe"} {
		if _, err := ParseStrictBool(raw, false); err == nil {
			t.Errorf("ParseStrictBool(%q) accepted nonsense", raw)
		}
	}
}

func TestExternalOperationsDefaultsToDisabled(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "postgres://user:pass@localhost:5432/reusery")
	t.Setenv(EnvAPIEnableExternalOperations, "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIEnableExternalOperations {
		t.Error("REUSERY_API_ENABLE_EXTERNAL_OPERATIONS must default to false")
	}
}

func TestExternalOperationsOptIn(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "postgres://user:pass@localhost:5432/reusery")
	t.Setenv(EnvAPIEnableExternalOperations, "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.APIEnableExternalOperations {
		t.Error("REUSERY_API_ENABLE_EXTERNAL_OPERATIONS=true was not honoured")
	}
}

func TestExternalOperationsRejectsNonsense(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "postgres://user:pass@localhost:5432/reusery")
	t.Setenv(EnvAPIEnableExternalOperations, "yes-please")

	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted a nonsense external-operations value")
	}
	if !strings.Contains(err.Error(), EnvAPIEnableExternalOperations) {
		t.Errorf("error does not name the variable: %v", err)
	}
	// The database URL must never appear in a configuration error.
	if strings.Contains(err.Error(), "user:pass") {
		t.Errorf("error leaked the database URL: %v", err)
	}
	if errors.Is(err, ErrMissingDatabaseURL) {
		t.Errorf("unexpected error kind: %v", err)
	}
}
