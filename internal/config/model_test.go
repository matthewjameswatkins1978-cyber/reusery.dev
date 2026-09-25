package config

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadModelNeverRequiresPostgreSQL(t *testing.T) {
	// No database URL is set for this test: natural-language structuring and
	// the database are independent concerns.
	t.Setenv(EnvDatabaseURL, "")
	t.Setenv(EnvOpenAIAPIKey, "")
	t.Setenv(EnvOpenAIModel, "")

	cfg, err := LoadModel()
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if cfg.OpenAIAPIKey != "" {
		t.Errorf("OpenAIAPIKey = %q, want empty", cfg.OpenAIAPIKey)
	}
	if cfg.Model() != DefaultOpenAIModel {
		t.Errorf("Model() = %q, want %q", cfg.Model(), DefaultOpenAIModel)
	}
	if DefaultOpenAIModel != "gpt-5.6-luna" {
		t.Errorf("DefaultOpenAIModel = %q, want gpt-5.6-luna", DefaultOpenAIModel)
	}
}

func TestLoadModelReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvOpenAIAPIKey, "  sk-example  ")
	t.Setenv(EnvOpenAIModel, "  gpt-5.6-terra  ")

	cfg, err := LoadModel()
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if cfg.OpenAIAPIKey != "sk-example" {
		t.Errorf("OpenAIAPIKey = %q, want the trimmed key", cfg.OpenAIAPIKey)
	}
	if cfg.Model() != "gpt-5.6-terra" {
		t.Errorf("Model() = %q, want the override", cfg.Model())
	}
}

func TestRequireAPIKeyIsClearAndNeverEchoesTheKey(t *testing.T) {
	missing := ModelConfig{}
	_, err := missing.RequireAPIKey()
	if !errors.Is(err, ErrMissingOpenAIAPIKey) {
		t.Fatalf("err = %v, want ErrMissingOpenAIAPIKey", err)
	}

	present := ModelConfig{OpenAIAPIKey: "sk-should-not-appear"}
	key, err := present.RequireAPIKey()
	if err != nil || key != "sk-should-not-appear" {
		t.Fatalf("key=%q err=%v", key, err)
	}

	if strings.Contains(ErrMissingOpenAIAPIKey.Error(), "sk-") {
		t.Error("the sentinel error contains a key")
	}
}

func TestModelConfigurationIsKeptOutOfLoad(t *testing.T) {
	// Load must keep failing without a database URL even when a model key is
	// present, proving the two configuration paths are genuinely separate.
	t.Setenv(EnvDatabaseURL, "")
	t.Setenv(EnvOpenAIAPIKey, "sk-example")

	if _, err := Load(); !errors.Is(err, ErrMissingDatabaseURL) {
		t.Errorf("Load err = %v, want ErrMissingDatabaseURL", err)
	}
	if _, err := LoadModel(); err != nil {
		t.Errorf("LoadModel err = %v, want success without a database", err)
	}
}
