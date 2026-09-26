package policy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writePolicy(t *testing.T, content string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	path = filepath.Join(root, "profile.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return root, "profile.yaml"
}

const validPolicyYAML = `
schema_version: 1
id: test/v1
reuse:
  allowed: [copy, dependency, adapt, reference]
  preferred: []
license:
  allow: [MIT]
  deny: [GPL-3.0]
  unknown: review
  multiple: review
  unlisted: allow
security:
  known_advisory: deny
  unknown: review
dependencies:
  unknown: review
  max_direct: 5
maintenance:
  archived: deny
  deprecated: review
  stale: review
  unknown: review
  max_days_since_push: 365
source:
  require_revision_for: [copy, dependency, adapt]
  missing_revision: review
selection:
  max_options: 3
`

func TestLoadValidPolicy(t *testing.T) {
	root, path := writePolicy(t, validPolicyYAML)
	policy, err := Load(root, path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if policy.ID != "test/v1" {
		t.Errorf("id = %q", policy.ID)
	}
	if policy.Licence.Unknown != ActionReview || policy.Security.KnownAdvisory != ActionDeny {
		t.Errorf("actions = %+v / %+v", policy.Licence, policy.Security)
	}
	if policy.Dependencies.MaxDirect == nil || *policy.Dependencies.MaxDirect != 5 {
		t.Errorf("max_direct = %v", policy.Dependencies.MaxDirect)
	}
	if policy.Maintenance.MaxDaysSincePush == nil || *policy.Maintenance.MaxDaysSincePush != 365 {
		t.Errorf("max_days_since_push = %v", policy.Maintenance.MaxDaysSincePush)
	}
	if policy.Selection.MaxOptions != 3 {
		t.Errorf("max_options = %d", policy.Selection.MaxOptions)
	}
}

func TestRepositoryBaselineProfileLoads(t *testing.T) {
	policy, err := Load("../..", "policies/public-go-baseline-v1.yaml")
	if err != nil {
		t.Fatalf("Load baseline: %v", err)
	}
	if policy.ID != "public-go-baseline/v1" {
		t.Errorf("id = %q", policy.ID)
	}
	if policy.Selection.MaxOptions != 3 {
		t.Errorf("max_options = %d, want 3", policy.Selection.MaxOptions)
	}
	if policy.Dependencies.MaxDirect != nil || policy.Maintenance.MaxDaysSincePush != nil {
		t.Error("the baseline must carry no numeric threshold")
	}
}

func TestLoadRejectsStructuralProblems(t *testing.T) {
	cases := map[string]string{
		"unknown field": `
schema_version: 1
id: test/v1
reuse: {allowed: [copy]}
selection: {max_options: 3}
popularity_matters: true
`,
		"unsupported schema version": `
schema_version: 2
id: test/v1
selection: {max_options: 3}
`,
		"empty id": `
schema_version: 1
id: " "
selection: {max_options: 3}
`,
		"invalid action": `
schema_version: 1
id: test/v1
license: {unknown: maybe}
selection: {max_options: 3}
`,
		"duplicate reuse mode": `
schema_version: 1
id: test/v1
reuse: {allowed: [copy, copy]}
selection: {max_options: 3}
`,
		"invalid reuse mode": `
schema_version: 1
id: test/v1
reuse: {allowed: [copy, vendor]}
selection: {max_options: 3}
`,
		"preferred mode not allowed": `
schema_version: 1
id: test/v1
reuse:
  allowed: [copy]
  preferred: [dependency]
selection: {max_options: 3}
`,
		"duplicate licence entries": `
schema_version: 1
id: test/v1
license: {allow: [MIT, MIT]}
selection: {max_options: 3}
`,
		"same licence in allow and deny": `
schema_version: 1
id: test/v1
license: {allow: [MIT], deny: [MIT]}
selection: {max_options: 3}
`,
		"negative max_direct": `
schema_version: 1
id: test/v1
dependencies: {max_direct: -1}
selection: {max_options: 3}
`,
		"negative day threshold": `
schema_version: 1
id: test/v1
maintenance: {max_days_since_push: -5}
selection: {max_options: 3}
`,
		"max_options zero": `
schema_version: 1
id: test/v1
selection: {max_options: 0}
`,
		"max_options above ceiling": `
schema_version: 1
id: test/v1
selection: {max_options: 6}
`,
		"not a mapping": `
schema_version: 1
id: test/v1
reuse: copy
selection: {max_options: 3}
`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			root, path := writePolicy(t, content)
			if _, err := Load(root, path); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	for _, attempt := range []string{
		"../outside.yaml",
		"..\\outside.yaml",
		filepath.Join(root, "..", "outside.yaml"),
		"/etc/passwd",
	} {
		if _, err := Load(root, attempt); !errors.Is(err, ErrPathEscape) {
			t.Errorf("Load(%q) err = %v, want ErrPathEscape", attempt, err)
		}
	}
}

func TestLoadRejectsAnEmptyPath(t *testing.T) {
	if _, err := Load(t.TempDir(), "  "); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}

func TestOmittedActionsDefaultToReview(t *testing.T) {
	root, path := writePolicy(t, `
schema_version: 1
id: test/v1
selection: {max_options: 3}
`)
	policy, err := Load(root, path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if policy.Licence.Unknown != ActionReview || policy.Security.Unknown != ActionReview ||
		policy.Maintenance.Archived != ActionReview || policy.Source.MissingRevision != ActionReview {
		t.Errorf("an incomplete profile must default to review, got %+v", policy)
	}
}

func TestLoadFeedbackAcceptsEverySupportedReason(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feedback.json"), []byte(`{
  "feedback": [
    {"candidate_id": "a", "reason": "not_quite"},
    {"candidate_id": "b", "reason": "too_many_dependencies"},
    {"candidate_id": "c", "reason": "licence_not_allowed"},
    {"candidate_id": "d", "reason": "avoid_dependency"},
    {"candidate_id": "e", "reason": "avoid_reference"},
    {"candidate_id": "f", "reason": "archived_project"}
  ]
}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	feedback, err := LoadFeedback(root, "feedback.json")
	if err != nil {
		t.Fatalf("LoadFeedback: %v", err)
	}
	if len(feedback) != 6 {
		t.Fatalf("feedback = %d entries, want 6", len(feedback))
	}
	for index, reason := range FeedbackReasons() {
		if feedback[index].Reason != reason {
			t.Errorf("feedback[%d] = %q, want %q", index, feedback[index].Reason, reason)
		}
	}
}

func TestLoadFeedbackRejectsUnsupportedReasonsAndStructuralProblems(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	cases := map[string]string{
		"unsupported reason": `{"feedback":[{"candidate_id":"a","reason":"prefer_stdlib"}]}`,
		"unknown field":      `{"feedback":[{"candidate_id":"a","reason":"not_quite"}],"rank":5}`,
		"empty candidate":    `{"feedback":[{"candidate_id":"","reason":"not_quite"}]}`,
		"empty list":         `{"feedback":[]}`,
		"not json":           `{`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			write("bad.json", content)
			if _, err := LoadFeedback(root, "bad.json"); !errors.Is(err, ErrInvalidFeedback) {
				t.Errorf("err = %v, want ErrInvalidFeedback", err)
			}
		})
	}

	if _, err := LoadFeedback(root, "../escape.json"); !errors.Is(err, ErrPathEscape) {
		t.Errorf("err = %v, want ErrPathEscape", err)
	}
}
