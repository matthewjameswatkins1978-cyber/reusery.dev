package discovery

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

const (
	repoRoot           = "../.."
	repositoryProfile  = "discovery/process/bounded-subprocess-v1.yaml"
	validProfileFields = `schema_version: 1
primitive_id: process/bounded-subprocess
contract_id: process/bounded-subprocess/v1
providers:
  - id: pkg.go.dev
    queries:
      - text: subprocess cancellation
        limit: 4
`
)

// writeProfile writes profile YAML under a temporary repository root and
// returns the root-relative path to load.
func writeProfile(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "profile.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return filepath.Join(root, "profile.yaml")
}

func TestDefaultBudgetIsBounded(t *testing.T) {
	budget := DefaultBudget()
	if budget.MaxProviders != 3 || budget.MaxQueries != 3 || budget.MaxResults != 6 ||
		budget.MaxHTTPRequests != 20 || budget.MaxCandidates != 24 {
		t.Errorf("count budgets = %#v, want the packet's fixed values", budget)
	}
	if budget.Timeout != 15*time.Second {
		t.Errorf("Timeout = %v, want 15s", budget.Timeout)
	}
	if budget.MaxResponseBytes != 2<<20 {
		t.Errorf("MaxResponseBytes = %d, want 2 MiB", budget.MaxResponseBytes)
	}
}

func TestLoadRepositoryProfile(t *testing.T) {
	profile, err := LoadProfile(repoRoot, filepath.FromSlash(repositoryProfile))
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.PrimitiveID != "process/bounded-subprocess" {
		t.Errorf("primitive_id = %q", profile.PrimitiveID)
	}
	if profile.ContractID != "process/bounded-subprocess/v1" {
		t.Errorf("contract_id = %q", profile.ContractID)
	}
	if len(profile.Providers) != 3 {
		t.Fatalf("providers = %d, want 3", len(profile.Providers))
	}
	seen := make(map[string]bool)
	for _, plan := range profile.Providers {
		seen[plan.ID] = true
	}
	for _, id := range KnownProviderIDs() {
		if !seen[id] {
			t.Errorf("repository profile is missing provider %q", id)
		}
	}
}

func TestLoadValidProfile(t *testing.T) {
	profile, err := LoadProfile(filepath.Dir(writeProfile(t, validProfileFields)), "profile.yaml")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", profile.SchemaVersion)
	}
	if len(profile.Providers) != 1 || len(profile.Providers[0].Queries) != 1 {
		t.Errorf("plan = %#v, want one provider with one query", profile.Providers)
	}
}

func TestLoadProfileRejectsMalformedProfiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    error
	}{
		{
			name: "unsupported schema version",
			content: `schema_version: 9
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 1}]
`,
			want: ErrUnsupportedSchema,
		},
		{
			name: "unknown yaml field",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 1}]
typo_field: true
`,
			want: ErrProfile,
		},
		{
			name: "empty primitive id",
			content: `schema_version: 1
primitive_id: ""
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 1}]
`,
			want: ErrProfile,
		},
		{
			name: "empty contract id",
			content: `schema_version: 1
primitive_id: p
contract_id: ""
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 1}]
`,
			want: ErrProfile,
		},
		{
			name: "no providers",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers: []
`,
			want: ErrProfile,
		},
		{
			name: "unknown provider id",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: sourcegraph
    queries: [{text: q, limit: 1}]
`,
			want: ErrProfile,
		},
		{
			name: "duplicate provider id",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 1}]
  - id: pkg.go.dev
    queries: [{text: other, limit: 1}]
`,
			want: ErrProfile,
		},
		{
			name: "zero queries",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: []
`,
			want: ErrProfile,
		},
		{
			name: "too many queries",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries:
      - {text: one, limit: 1}
      - {text: two, limit: 1}
      - {text: three, limit: 1}
      - {text: four, limit: 1}
`,
			want: ErrProfile,
		},
		{
			name: "blank query",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: "   ", limit: 1}]
`,
			want: ErrProfile,
		},
		{
			name: "duplicate query",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries:
      - {text: same, limit: 1}
      - {text: same, limit: 2}
`,
			want: ErrProfile,
		},
		{
			name: "limit zero",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 0}]
`,
			want: ErrProfile,
		},
		{
			name: "limit above maximum",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: q, limit: 99}]
`,
			want: ErrProfile,
		},
		{
			name: "query text too long",
			content: `schema_version: 1
primitive_id: p
contract_id: c
providers:
  - id: pkg.go.dev
    queries: [{text: "` + strings.Repeat("a", MaxQueryTextLength+1) + `", limit: 1}]
`,
			want: ErrProfile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadProfile(filepath.Dir(writeProfile(t, tt.content)), "profile.yaml")
			if err == nil {
				t.Fatal("LoadProfile succeeded, want error")
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want it to wrap %v", err, tt.want)
			}
		})
	}
}

func TestLoadProfileRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadProfile(root, "../outside.yaml"); !errors.Is(err, ErrPathEscape) {
		t.Errorf("error = %v, want ErrPathEscape", err)
	}
	if _, err := LoadProfile(root, "/etc/passwd"); !errors.Is(err, ErrPathEscape) {
		t.Errorf("absolute error = %v, want ErrPathEscape", err)
	}
	if _, err := LoadProfile(root, ""); !errors.Is(err, ErrProfile) {
		t.Errorf("empty error = %v, want ErrProfile", err)
	}
}

func TestValidateProfileDomainRejectsMismatches(t *testing.T) {
	primitive := model.Primitive{
		ID:         "process/bounded-subprocess",
		ContractID: "process/bounded-subprocess/v1",
	}
	contract := model.Contract{
		ID:          "process/bounded-subprocess/v1",
		PrimitiveID: "process/bounded-subprocess",
	}
	profile := Profile{
		SchemaVersion: SchemaVersion,
		PrimitiveID:   primitive.ID,
		ContractID:    contract.ID,
		Providers: []ProviderPlan{
			{ID: ProviderPkgGoDev, Queries: []Query{{Text: "q", Limit: 1}}},
		},
	}
	if err := ValidateProfileDomain(profile, primitive, contract); err != nil {
		t.Fatalf("valid domain: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Profile, *model.Primitive, *model.Contract)
	}{
		{"primitive mismatch", func(p *Profile, _ *model.Primitive, _ *model.Contract) {
			p.PrimitiveID = "other/primitive"
		}},
		{"contract mismatch", func(p *Profile, _ *model.Primitive, _ *model.Contract) {
			p.ContractID = "other/contract/v9"
		}},
		{"primitive declares another contract", func(p *Profile, prim *model.Primitive, _ *model.Contract) {
			prim.ContractID = "elsewhere/v1"
		}},
		{"contract declares another primitive", func(p *Profile, _ *model.Primitive, con *model.Contract) {
			con.PrimitiveID = "elsewhere"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changedProfile, changedPrimitive, changedContract := profile, primitive, contract
			tt.mutate(&changedProfile, &changedPrimitive, &changedContract)
			err := ValidateProfileDomain(changedProfile, changedPrimitive, changedContract)
			if !errors.Is(err, ErrProfile) {
				t.Errorf("error = %v, want it to wrap ErrProfile", err)
			}
		})
	}
}
