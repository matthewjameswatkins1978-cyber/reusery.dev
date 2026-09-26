package project

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// factEvidence is one informational observation carrying the artifact the
// fact extractor reads.
func factEvidence(subject, kind string, value any) model.Evidence {
	return model.Evidence{
		ID:         "ev/" + subject + "/" + kind,
		SubjectID:  subject,
		Kind:       kind,
		Result:     model.EvidenceInfo,
		Artifact:   factArtifact(value),
		ObservedAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	}
}

// factArtifact renders the machine-readable artifact Packet 7 reads.
func factArtifact(value any) string {
	encoded, err := json.Marshal(map[string]any{"schema_version": 1, "value": value})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// mustJSON renders a value for assertions.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// mustHash returns the fingerprint digest or fails the test.
func mustHash(t *testing.T, fingerprint Fingerprint) string {
	t.Helper()
	hash, err := FingerprintHash(fingerprint)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return hash
}

// productionSources returns every non-test Go file in this package so
// behavioural boundaries can be asserted against the actual sources.
func productionSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(data)
	}
	if len(out) == 0 {
		t.Fatal("no production sources found")
	}
	return out
}
