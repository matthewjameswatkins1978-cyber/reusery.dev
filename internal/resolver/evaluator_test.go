package resolver

import (
	"errors"
	"testing"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// boundedSubprocessContract mirrors the requirement IDs of
// contracts/process/bounded-subprocess-v1.yaml. Packet 2 does not load YAML;
// the contract is constructed directly so the evaluator is exercised against
// the real first-primitive semantics.
func boundedSubprocessContract() model.Contract {
	return model.Contract{
		ID:          "process/bounded-subprocess/v1",
		PrimitiveID: "process/bounded-subprocess",
		Version:     "1",
		Requirements: []model.Requirement{
			{ID: "starts-requested-program", Kind: "behavior", Required: true},
			{ID: "preserves-exit-status", Kind: "behavior", Required: true},
			{ID: "drains-stdout-stderr-concurrently", Kind: "invariant", Required: true},
			{ID: "bounds-stdout", Kind: "resource", Required: true},
			{ID: "bounds-stderr", Kind: "resource", Required: true},
			{ID: "reports-truncation", Kind: "behavior", Required: true},
			{ID: "supports-timeout", Kind: "lifecycle", Required: true},
			{ID: "supports-cancellation", Kind: "lifecycle", Required: true},
			{ID: "process-tree-semantics-explicit", Kind: "platform", Required: true},
			{ID: "no-orphaned-pipe-readers", Kind: "lifecycle", Required: true},
			{ID: "result-distinguishes-failure-kinds", Kind: "error-model", Required: true},
		},
	}
}

func ev(id, subject, appliesTo string, result model.EvidenceResult) model.Evidence {
	return model.Evidence{
		ID:        id,
		SubjectID: subject,
		AppliesTo: appliesTo,
		Result:    result,
	}
}

func idsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statusFor(t *testing.T, evaluation CandidateEvaluation, requirementID string) RequirementEvaluation {
	t.Helper()

	for _, req := range evaluation.Requirements {
		if req.RequirementID == requirementID {
			return req
		}
	}
	t.Fatalf("requirement %q missing from evaluation", requirementID)
	return RequirementEvaluation{}
}

// A. Evidence-backed satisfaction.
func TestEvidenceBackedSatisfaction(t *testing.T) {
	contract := boundedSubprocessContract()
	specimen := model.Specimen{ID: "spec-a"}

	got, err := Evaluate(contract, specimen, []model.Evidence{
		ev("ev-1", "spec-a", "bounds-stdout", model.EvidencePass),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	req := statusFor(t, got, "bounds-stdout")
	if req.Status != RequirementSatisfied {
		t.Errorf("status = %q, want %q", req.Status, RequirementSatisfied)
	}
	if !idsEqual(req.EvidenceIDs, []string{"ev-1"}) {
		t.Errorf("evidence IDs = %v, want [ev-1]", req.EvidenceIDs)
	}
}

// B. Missing evidence is unknown, never a pass.
func TestMissingEvidenceIsUnknown(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	for _, req := range got.Requirements {
		if req.Status != RequirementUnknown {
			t.Errorf("%s status = %q, want %q", req.RequirementID, req.Status, RequirementUnknown)
		}
		if len(req.EvidenceIDs) != 0 {
			t.Errorf("%s evidence IDs = %v, want none", req.RequirementID, req.EvidenceIDs)
		}
	}
}

// C. Explicit failure.
func TestExplicitFailure(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, []model.Evidence{
		ev("ev-1", "spec-a", "bounds-stderr", model.EvidenceFail),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	req := statusFor(t, got, "bounds-stderr")
	if req.Status != RequirementFailed {
		t.Errorf("status = %q, want %q", req.Status, RequirementFailed)
	}
	if !idsEqual(req.EvidenceIDs, []string{"ev-1"}) {
		t.Errorf("evidence IDs = %v, want [ev-1]", req.EvidenceIDs)
	}
}

// D. Conflicting observations preserve both sides in input order.
func TestConflictingObservations(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, []model.Evidence{
		ev("ev-fail", "spec-a", "supports-timeout", model.EvidenceFail),
		ev("ev-pass", "spec-a", "supports-timeout", model.EvidencePass),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	req := statusFor(t, got, "supports-timeout")
	if req.Status != RequirementConflicting {
		t.Errorf("status = %q, want %q", req.Status, RequirementConflicting)
	}
	if !idsEqual(req.EvidenceIDs, []string{"ev-fail", "ev-pass"}) {
		t.Errorf("evidence IDs = %v, want [ev-fail ev-pass]", req.EvidenceIDs)
	}
}

// E. INFO alone is not proof.
func TestInfoIsNotProof(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, []model.Evidence{
		ev("ev-info", "spec-a", "reports-truncation", model.EvidenceInfo),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	req := statusFor(t, got, "reports-truncation")
	if req.Status != RequirementUnknown {
		t.Errorf("status = %q, want %q", req.Status, RequirementUnknown)
	}
	if !idsEqual(req.EvidenceIDs, []string{"ev-info"}) {
		t.Errorf("evidence IDs = %v, want [ev-info]", req.EvidenceIDs)
	}
}

// E (continued). Explicit UNKNOWN alone is unknown.
func TestExplicitUnknownIsUnknown(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, []model.Evidence{
		ev("ev-unknown", "spec-a", "process-tree-semantics-explicit", model.EvidenceUnknown),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	req := statusFor(t, got, "process-tree-semantics-explicit")
	if req.Status != RequirementUnknown {
		t.Errorf("status = %q, want %q", req.Status, RequirementUnknown)
	}
	if !idsEqual(req.EvidenceIDs, []string{"ev-unknown"}) {
		t.Errorf("evidence IDs = %v, want [ev-unknown]", req.EvidenceIDs)
	}
}

// Section 4: UNKNOWN never erases a stronger observation.
func TestStrongerEvidenceSurvivesUnknown(t *testing.T) {
	tests := []struct {
		name     string
		evidence []model.Evidence
		want     RequirementStatus
		wantIDs  []string
	}{
		{
			"pass plus unknown",
			[]model.Evidence{
				ev("ev-pass", "spec-a", "supports-cancellation", model.EvidencePass),
				ev("ev-unknown", "spec-a", "supports-cancellation", model.EvidenceUnknown),
			},
			RequirementSatisfied,
			[]string{"ev-pass"},
		},
		{
			"unknown then pass",
			[]model.Evidence{
				ev("ev-unknown", "spec-a", "supports-cancellation", model.EvidenceUnknown),
				ev("ev-pass", "spec-a", "supports-cancellation", model.EvidencePass),
			},
			RequirementSatisfied,
			[]string{"ev-pass"},
		},
		{
			"fail plus unknown",
			[]model.Evidence{
				ev("ev-fail", "spec-a", "supports-cancellation", model.EvidenceFail),
				ev("ev-unknown", "spec-a", "supports-cancellation", model.EvidenceUnknown),
			},
			RequirementFailed,
			[]string{"ev-fail"},
		},
		{
			"pass plus fail plus unknown",
			[]model.Evidence{
				ev("ev-pass", "spec-a", "supports-cancellation", model.EvidencePass),
				ev("ev-unknown", "spec-a", "supports-cancellation", model.EvidenceUnknown),
				ev("ev-fail", "spec-a", "supports-cancellation", model.EvidenceFail),
			},
			RequirementConflicting,
			[]string{"ev-pass", "ev-fail"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, tt.evidence)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			req := statusFor(t, got, "supports-cancellation")
			if req.Status != tt.want {
				t.Errorf("status = %q, want %q", req.Status, tt.want)
			}
			if !idsEqual(req.EvidenceIDs, tt.wantIDs) {
				t.Errorf("evidence IDs = %v, want %v", req.EvidenceIDs, tt.wantIDs)
			}
		})
	}
}

// F. Evidence about another specimen must not leak.
func TestEvidenceIsolation(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, []model.Evidence{
		ev("ev-other", "spec-b", "bounds-stdout", model.EvidencePass),
		ev("ev-other-fail", "spec-b", "bounds-stderr", model.EvidenceFail),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	for _, req := range got.Requirements {
		if req.Status != RequirementUnknown {
			t.Errorf("%s status = %q, want %q (evidence belongs to another specimen)", req.RequirementID, req.Status, RequirementUnknown)
		}
	}
}

// Evidence without a requirement ID must not satisfy anything.
func TestEvidenceWithoutAppliesToIsInert(t *testing.T) {
	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: "spec-a"}, []model.Evidence{
		ev("ev-1", "spec-a", "", model.EvidencePass),
		ev("ev-2", "spec-a", "some-other-requirement", model.EvidencePass),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	for _, req := range got.Requirements {
		if req.Status != RequirementUnknown {
			t.Errorf("%s status = %q, want %q", req.RequirementID, req.Status, RequirementUnknown)
		}
	}
}

// G. Result ordering must match contract requirement ordering.
func TestRequirementOrdering(t *testing.T) {
	contract := model.Contract{
		ID: "ordering/v1",
		Requirements: []model.Requirement{
			{ID: "third", Required: true},
			{ID: "first", Required: true},
			{ID: "second", Required: true},
		},
	}
	// Evidence is supplied in an unrelated order.
	evidence := []model.Evidence{
		ev("ev-2", "spec-a", "second", model.EvidencePass),
		ev("ev-3", "spec-a", "third", model.EvidencePass),
		ev("ev-1", "spec-a", "first", model.EvidencePass),
	}

	got, err := Evaluate(contract, model.Specimen{ID: "spec-a"}, evidence)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	want := []string{"third", "first", "second"}
	if len(got.Requirements) != len(want) {
		t.Fatalf("got %d requirements, want %d", len(got.Requirements), len(want))
	}
	for i, id := range want {
		if got.Requirements[i].RequirementID != id {
			t.Errorf("requirements[%d] = %q, want %q", i, got.Requirements[i].RequirementID, id)
		}
	}
}

// H. Duplicate requirement IDs are malformed input.
func TestDuplicateRequirementIDs(t *testing.T) {
	contract := model.Contract{
		ID: "duplicate/v1",
		Requirements: []model.Requirement{
			{ID: "bounds-stdout", Required: true},
			{ID: "bounds-stdout", Required: true},
		},
	}

	_, err := Evaluate(contract, model.Specimen{ID: "spec-a"}, nil)
	if !errors.Is(err, ErrDuplicateRequirementID) {
		t.Fatalf("error = %v, want %v", err, ErrDuplicateRequirementID)
	}
}

func TestValidation(t *testing.T) {
	valid := boundedSubprocessContract()

	tests := []struct {
		name     string
		contract model.Contract
		specimen model.Specimen
		wantErr  error
	}{
		{"empty specimen ID", valid, model.Specimen{}, ErrEmptySpecimenID},
		{"empty contract ID", model.Contract{ID: ""}, model.Specimen{ID: "spec-a"}, ErrEmptyContractID},
		{
			"empty requirement ID",
			model.Contract{ID: "c/v1", Requirements: []model.Requirement{{ID: ""}}},
			model.Specimen{ID: "spec-a"},
			ErrEmptyRequirementID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Evaluate(tt.contract, tt.specimen, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// I. Full first-primitive style matrix: relevant is not proven.
func TestFullBoundedSubprocessMatrix(t *testing.T) {
	const specimenID = "spec-bounded-subprocess"

	evidence := []model.Evidence{
		ev("ev-01", specimenID, "starts-requested-program", model.EvidencePass),
		ev("ev-02", specimenID, "preserves-exit-status", model.EvidencePass),
		ev("ev-03", specimenID, "drains-stdout-stderr-concurrently", model.EvidencePass),
		ev("ev-04", specimenID, "bounds-stdout", model.EvidencePass),
		ev("ev-05", specimenID, "bounds-stderr", model.EvidencePass),
		ev("ev-06", specimenID, "reports-truncation", model.EvidenceFail),
		ev("ev-07", specimenID, "supports-timeout", model.EvidencePass),
		ev("ev-08", specimenID, "supports-timeout", model.EvidenceFail),
		ev("ev-09", specimenID, "supports-cancellation", model.EvidencePass),
		ev("ev-10", specimenID, "supports-cancellation", model.EvidenceUnknown),
		ev("ev-11", specimenID, "process-tree-semantics-explicit", model.EvidenceUnknown),
		ev("ev-12", specimenID, "result-distinguishes-failure-kinds", model.EvidenceInfo),
		ev("ev-other", "another-specimen", "bounds-stdout", model.EvidenceFail),
	}

	got, err := Evaluate(boundedSubprocessContract(), model.Specimen{ID: specimenID}, evidence)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	want := []RequirementEvaluation{
		{RequirementID: "starts-requested-program", Status: RequirementSatisfied, EvidenceIDs: []string{"ev-01"}},
		{RequirementID: "preserves-exit-status", Status: RequirementSatisfied, EvidenceIDs: []string{"ev-02"}},
		{RequirementID: "drains-stdout-stderr-concurrently", Status: RequirementSatisfied, EvidenceIDs: []string{"ev-03"}},
		{RequirementID: "bounds-stdout", Status: RequirementSatisfied, EvidenceIDs: []string{"ev-04"}},
		{RequirementID: "bounds-stderr", Status: RequirementSatisfied, EvidenceIDs: []string{"ev-05"}},
		{RequirementID: "reports-truncation", Status: RequirementFailed, EvidenceIDs: []string{"ev-06"}},
		{RequirementID: "supports-timeout", Status: RequirementConflicting, EvidenceIDs: []string{"ev-07", "ev-08"}},
		{RequirementID: "supports-cancellation", Status: RequirementSatisfied, EvidenceIDs: []string{"ev-09"}},
		{RequirementID: "process-tree-semantics-explicit", Status: RequirementUnknown, EvidenceIDs: []string{"ev-11"}},
		{RequirementID: "no-orphaned-pipe-readers", Status: RequirementUnknown},
		{RequirementID: "result-distinguishes-failure-kinds", Status: RequirementUnknown, EvidenceIDs: []string{"ev-12"}},
	}

	if len(got.Requirements) != len(want) {
		t.Fatalf("got %d requirements, want %d", len(got.Requirements), len(want))
	}
	for i := range want {
		actual := got.Requirements[i]
		if actual.RequirementID != want[i].RequirementID || actual.Status != want[i].Status {
			t.Errorf("requirements[%d] = {%s, %s}, want {%s, %s}",
				i, actual.RequirementID, actual.Status, want[i].RequirementID, want[i].Status)
		}
		if !idsEqual(actual.EvidenceIDs, want[i].EvidenceIDs) {
			t.Errorf("%s evidence IDs = %v, want %v", actual.RequirementID, actual.EvidenceIDs, want[i].EvidenceIDs)
		}
	}

	if !got.HasFailures() {
		t.Error("HasFailures() = false, want true")
	}
	if !got.HasUnknowns() {
		t.Error("HasUnknowns() = false, want true")
	}
	if got.FullySatisfiesRequired(boundedSubprocessContract()) {
		t.Error("FullySatisfiesRequired() = true, want false for a partially proven specimen")
	}
}

func TestFullySatisfiesRequired(t *testing.T) {
	contract := model.Contract{
		ID: "required/v1",
		Requirements: []model.Requirement{
			{ID: "must-pass", Required: true},
			{ID: "optional", Required: false},
		},
	}

	tests := []struct {
		name     string
		evidence []model.Evidence
		want     bool
	}{
		{
			"all required satisfied, optional unknown",
			[]model.Evidence{ev("ev-1", "spec-a", "must-pass", model.EvidencePass)},
			true,
		},
		{
			"required unknown",
			nil,
			false,
		},
		{
			"required failed",
			[]model.Evidence{ev("ev-1", "spec-a", "must-pass", model.EvidenceFail)},
			false,
		},
		{
			"required conflicting",
			[]model.Evidence{
				ev("ev-1", "spec-a", "must-pass", model.EvidencePass),
				ev("ev-2", "spec-a", "must-pass", model.EvidenceFail),
			},
			false,
		},
		{
			"failed optional does not block",
			[]model.Evidence{
				ev("ev-1", "spec-a", "must-pass", model.EvidencePass),
				ev("ev-2", "spec-a", "optional", model.EvidenceFail),
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(contract, model.Specimen{ID: "spec-a"}, tt.evidence)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got := got.FullySatisfiesRequired(contract); got != tt.want {
				t.Errorf("FullySatisfiesRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}
