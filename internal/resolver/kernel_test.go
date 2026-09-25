package resolver

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

var kernelResolvedAt = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

func kernelPrimitive() model.Primitive {
	return model.Primitive{
		ID:         "process/bounded-subprocess",
		ContractID: "process/bounded-subprocess/v1",
	}
}

// kernelContract is a small contract with required requirements so rejection
// and acceptance rules stay readable.
func kernelContract() model.Contract {
	return model.Contract{
		ID:          "process/bounded-subprocess/v1",
		PrimitiveID: "process/bounded-subprocess",
		Version:     "1",
		Requirements: []model.Requirement{
			{ID: "bounds-stdout", Kind: "resource", Required: true},
			{ID: "bounds-stderr", Kind: "resource", Required: true},
			{ID: "supports-timeout", Kind: "lifecycle", Required: true},
		},
	}
}

func kernelSpecimen(id string, modes ...model.ReuseMode) model.Specimen {
	return model.Specimen{
		ID:          id,
		PrimitiveID: "process/bounded-subprocess",
		ReuseMode:   modes,
	}
}

func evidenceFor(specimenID, requirementID, id string, result model.EvidenceResult) model.Evidence {
	return model.Evidence{
		ID:          id,
		SubjectID:   specimenID,
		AppliesTo:   requirementID,
		Result:      result,
		ObservedAt:  kernelResolvedAt,
		Methodology: "kernel unit test",
	}
}

// passEvidence returns PASS evidence for every required requirement.
func passEvidence(specimenID string, contract model.Contract) []model.Evidence {
	evidence := make([]model.Evidence, 0, len(contract.Requirements))
	for _, req := range contract.Requirements {
		if !req.Required {
			continue
		}
		evidence = append(evidence, evidenceFor(specimenID, req.ID, "ev/"+specimenID+"/"+req.ID, model.EvidencePass))
	}
	return evidence
}

func baseInput() ResolveInput {
	return ResolveInput{
		Primitive:  kernelPrimitive(),
		Contract:   kernelContract(),
		Candidates: nil,
		ResolvedAt: kernelResolvedAt,
	}
}

// A. The first acceptable candidate wins after an unsuitable one is rejected.
func TestResolveSelectsFirstAcceptableCandidate(t *testing.T) {
	contract := kernelContract()

	bad := kernelSpecimen("spec-bad", model.ReuseCopy)
	badEvidence := []model.Evidence{
		evidenceFor(bad.ID, "bounds-stdout", "ev/bad/stdout", model.EvidenceFail),
		evidenceFor(bad.ID, "bounds-stderr", "ev/bad/stderr", model.EvidencePass),
		evidenceFor(bad.ID, "supports-timeout", "ev/bad/timeout", model.EvidencePass),
	}

	good := kernelSpecimen("spec-good", model.ReuseDependency)

	in := baseInput()
	in.Candidates = []CandidateOption{
		{Specimen: bad, ReuseMode: model.ReuseCopy, Evidence: badEvidence},
		{Specimen: good, ReuseMode: model.ReuseDependency, Evidence: passEvidence(good.ID, contract)},
	}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(decision.Considered) != 2 {
		t.Fatalf("considered = %d candidates, want 2", len(decision.Considered))
	}
	if decision.Considered[0].Selected {
		t.Error("unsuitable candidate was selected")
	}
	if !decision.Considered[1].Selected {
		t.Error("acceptable candidate was not selected")
	}
	if got := decision.Resolution.Outcome; got != model.OutcomeDepend {
		t.Errorf("outcome = %q, want %q", got, model.OutcomeDepend)
	}
	if decision.Resolution.SpecimenID != good.ID {
		t.Errorf("specimen = %q, want %q", decision.Resolution.SpecimenID, good.ID)
	}
	if len(decision.Resolution.Rejected) != 1 || decision.Resolution.Rejected[0].SpecimenID != bad.ID {
		t.Errorf("rejected = %#v, want only %q", decision.Resolution.Rejected, bad.ID)
	}
}

// B. Candidates are considered in supplied order. This asserts consideration
// order, not ranking quality: Packet 4 does not rank.
func TestResolveConsidersCandidatesInSuppliedOrder(t *testing.T) {
	contract := kernelContract()
	first := kernelSpecimen("spec-first", model.ReuseCopy)
	second := kernelSpecimen("spec-second", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{
		{Specimen: first, ReuseMode: model.ReuseCopy, Evidence: passEvidence(first.ID, contract)},
		{Specimen: second, ReuseMode: model.ReuseCopy, Evidence: passEvidence(second.ID, contract)},
	}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.SpecimenID != first.ID {
		t.Errorf("selected = %q, want the first supplied candidate %q", decision.Resolution.SpecimenID, first.ID)
	}
	if len(decision.Considered) != 1 {
		t.Fatalf("considered = %d, want 1 (evaluation stops at the selection)", len(decision.Considered))
	}

	// Reversing the supplied order reverses the selection.
	in.Candidates[0], in.Candidates[1] = in.Candidates[1], in.Candidates[0]
	decision, err = Resolve(in)
	if err != nil {
		t.Fatalf("Resolve reversed: %v", err)
	}
	if decision.Resolution.SpecimenID != second.ID {
		t.Errorf("selected = %q, want %q after reversal", decision.Resolution.SpecimenID, second.ID)
	}
}

// C. Required FAIL blocks selection.
func TestResolveRequiredFailBlocksSelection(t *testing.T) {
	specimen := kernelSpecimen("spec-fail", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{{
		Specimen: specimen, ReuseMode: model.ReuseCopy,
		Evidence: []model.Evidence{
			evidenceFor(specimen.ID, "bounds-stdout", "ev/fail/stdout", model.EvidenceFail),
			evidenceFor(specimen.ID, "bounds-stderr", "ev/fail/stderr", model.EvidencePass),
			evidenceFor(specimen.ID, "supports-timeout", "ev/fail/timeout", model.EvidencePass),
		},
	}}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", decision.Resolution.Outcome)
	}
	if len(decision.Resolution.Rejected) != 1 {
		t.Fatalf("rejected = %#v, want 1", decision.Resolution.Rejected)
	}
	want := `required requirement "bounds-stdout" failed`
	if !contains(decision.Resolution.Rejected[0].Reasons, want) {
		t.Errorf("rejection reasons = %v, want %q", decision.Resolution.Rejected[0].Reasons, want)
	}
}

// D. Required UNKNOWN blocks selection and is aggregated into Unknowns.
func TestResolveRequiredUnknownBlocksSelection(t *testing.T) {
	specimen := kernelSpecimen("spec-unknown", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{{
		Specimen: specimen, ReuseMode: model.ReuseCopy,
		// No evidence at all for any requirement.
	}}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", decision.Resolution.Outcome)
	}
	want := `required requirement "bounds-stdout" unknown`
	if !contains(decision.Resolution.Rejected[0].Reasons, want) {
		t.Errorf("rejection reasons = %v, want %q", decision.Resolution.Rejected[0].Reasons, want)
	}
	wantUnknown := "spec-unknown: bounds-stdout unknown"
	if !contains(decision.Resolution.Unknowns, wantUnknown) {
		t.Errorf("unknowns = %v, want %q", decision.Resolution.Unknowns, wantUnknown)
	}
}

// E. Required CONFLICTING blocks selection and is aggregated into Unknowns.
func TestResolveRequiredConflictingBlocksSelection(t *testing.T) {
	specimen := kernelSpecimen("spec-conflict", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{{
		Specimen: specimen, ReuseMode: model.ReuseCopy,
		Evidence: []model.Evidence{
			evidenceFor(specimen.ID, "bounds-stdout", "ev/conflict/pass", model.EvidencePass),
			evidenceFor(specimen.ID, "bounds-stdout", "ev/conflict/fail", model.EvidenceFail),
			evidenceFor(specimen.ID, "bounds-stderr", "ev/conflict/stderr", model.EvidencePass),
			evidenceFor(specimen.ID, "supports-timeout", "ev/conflict/timeout", model.EvidencePass),
		},
	}}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", decision.Resolution.Outcome)
	}
	want := `required requirement "bounds-stdout" conflicting`
	if !contains(decision.Resolution.Rejected[0].Reasons, want) {
		t.Errorf("rejection reasons = %v, want %q", decision.Resolution.Rejected[0].Reasons, want)
	}
	wantUnknown := "spec-conflict: bounds-stdout conflicting"
	if !contains(decision.Resolution.Unknowns, wantUnknown) {
		t.Errorf("unknowns = %v, want %q", decision.Resolution.Unknowns, wantUnknown)
	}
}

// contractWithOptional adds an optional requirement to the base contract.
func contractWithOptional() model.Contract {
	contract := kernelContract()
	contract.Requirements = append(contract.Requirements, model.Requirement{
		ID: "reports-extra-metrics", Kind: "behavior", Required: false,
	})
	return contract
}

// F. Optional FAIL does not block selection but stays inspectable.
func TestResolveOptionalFailDoesNotBlock(t *testing.T) {
	contract := contractWithOptional()
	specimen := kernelSpecimen("spec-optional-fail", model.ReuseAdapt)

	in := baseInput()
	in.Contract = contract
	in.Candidates = []CandidateOption{{
		Specimen: specimen, ReuseMode: model.ReuseAdapt,
		Evidence: append(passEvidence(specimen.ID, contract),
			evidenceFor(specimen.ID, "reports-extra-metrics", "ev/opt/fail", model.EvidenceFail),
		),
	}}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeAdapt {
		t.Fatalf("outcome = %q, want adapt", decision.Resolution.Outcome)
	}
	want := `optional requirement "reports-extra-metrics" failed (known limitation)`
	if !contains(decision.Resolution.Reasons, want) {
		t.Errorf("reasons = %v, want known limitation %q", decision.Resolution.Reasons, want)
	}
	if contains(decision.Resolution.Unknowns, "reports-extra-metrics unknown") {
		t.Errorf("known failure must not be reported as unknown: %v", decision.Resolution.Unknowns)
	}
}

// G. Optional UNKNOWN does not block selection and remains inspectable.
func TestResolveOptionalUnknownDoesNotBlock(t *testing.T) {
	contract := contractWithOptional()
	specimen := kernelSpecimen("spec-optional-unknown", model.ReuseCopy)

	in := baseInput()
	in.Contract = contract
	in.Candidates = []CandidateOption{{
		Specimen: specimen, ReuseMode: model.ReuseCopy,
		Evidence: append(passEvidence(specimen.ID, contract),
			evidenceFor(specimen.ID, "reports-extra-metrics", "ev/opt/unknown", model.EvidenceUnknown),
		),
	}}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeReuse {
		t.Fatalf("outcome = %q, want reuse", decision.Resolution.Outcome)
	}
	wantUnknown := "spec-optional-unknown: reports-extra-metrics unknown"
	if !contains(decision.Resolution.Unknowns, wantUnknown) {
		t.Errorf("unknowns = %v, want %q", decision.Resolution.Unknowns, wantUnknown)
	}
}

// H. Every candidate rejected yields BUILD LOCALLY as a successful result.
func TestResolveAllCandidatesRejectedIsBuildLocally(t *testing.T) {
	badA := kernelSpecimen("spec-a", model.ReuseCopy)
	badB := kernelSpecimen("spec-b", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{
		{Specimen: badA, ReuseMode: model.ReuseCopy, Evidence: nil},
		{Specimen: badB, ReuseMode: model.ReuseCopy, Evidence: nil},
	}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve must not error on BUILD LOCALLY: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", decision.Resolution.Outcome)
	}
	if decision.Resolution.SpecimenID != "" {
		t.Errorf("specimen = %q, want empty for BUILD LOCALLY", decision.Resolution.SpecimenID)
	}
	want := "no candidate option satisfied all required contract requirements"
	if len(decision.Resolution.Reasons) != 1 || decision.Resolution.Reasons[0] != want {
		t.Errorf("reasons = %v, want [%q]", decision.Resolution.Reasons, want)
	}
	if len(decision.Resolution.Rejected) != 2 {
		t.Errorf("rejected = %#v, want both candidates", decision.Resolution.Rejected)
	}
	if len(decision.Considered) != 2 {
		t.Errorf("considered = %d, want 2", len(decision.Considered))
	}
}

// I. Zero candidates yields BUILD LOCALLY with a precise reason.
func TestResolveZeroCandidatesIsBuildLocally(t *testing.T) {
	decision, err := Resolve(baseInput())
	if err != nil {
		t.Fatalf("Resolve must not error with zero candidates: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", decision.Resolution.Outcome)
	}
	want := "no candidate options were supplied"
	if len(decision.Resolution.Reasons) != 1 || decision.Resolution.Reasons[0] != want {
		t.Errorf("reasons = %v, want [%q]", decision.Resolution.Reasons, want)
	}
	if len(decision.Considered) != 0 {
		t.Errorf("considered = %d, want 0", len(decision.Considered))
	}
}

// J. Reuse mode maps mechanically to the outcome; no mode wins automatically.
func TestResolveReuseModeOutcomeMapping(t *testing.T) {
	tests := []struct {
		mode    model.ReuseMode
		outcome model.Outcome
	}{
		{model.ReuseCopy, model.OutcomeReuse},
		{model.ReuseDependency, model.OutcomeDepend},
		{model.ReuseAdapt, model.OutcomeAdapt},
		{model.ReuseReference, model.OutcomeReference},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			contract := kernelContract()
			specimen := kernelSpecimen("spec-mode", tt.mode)

			in := baseInput()
			in.Candidates = []CandidateOption{{
				Specimen: specimen, ReuseMode: tt.mode, Evidence: passEvidence(specimen.ID, contract),
			}}

			decision, err := Resolve(in)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if decision.Resolution.Outcome != tt.outcome {
				t.Errorf("outcome = %q for mode %q, want %q", decision.Resolution.Outcome, tt.mode, tt.outcome)
			}
		})
	}
}

// K. An unsupported reuse mode is malformed input.
func TestResolveUnsupportedReuseModeIsError(t *testing.T) {
	in := baseInput()
	in.Candidates = []CandidateOption{{
		Specimen:  kernelSpecimen("spec-bad-mode", "borrow"),
		ReuseMode: "borrow",
	}}

	if _, err := Resolve(in); !errors.Is(err, ErrUnsupportedReuseMode) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedReuseMode)
	}
}

// L. A mode the specimen does not declare is malformed input.
func TestResolveModeNotDeclaredBySpecimenIsError(t *testing.T) {
	in := baseInput()
	in.Candidates = []CandidateOption{{
		Specimen:  kernelSpecimen("spec-declares-copy", model.ReuseCopy),
		ReuseMode: model.ReuseAdapt,
	}}

	if _, err := Resolve(in); !errors.Is(err, ErrReuseModeNotDeclared) {
		t.Fatalf("error = %v, want %v", err, ErrReuseModeNotDeclared)
	}
}

// M. Duplicate candidate specimen IDs are malformed input.
func TestResolveDuplicateCandidateIDsIsError(t *testing.T) {
	specimen := kernelSpecimen("spec-dupe", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{
		{Specimen: specimen, ReuseMode: model.ReuseCopy},
		{Specimen: specimen, ReuseMode: model.ReuseCopy},
	}

	if _, err := Resolve(in); !errors.Is(err, ErrDuplicateCandidateID) {
		t.Fatalf("error = %v, want %v", err, ErrDuplicateCandidateID)
	}
}

// N. Primitive/contract relationship mismatches and bad metadata are rejected.
func TestResolveInputValidation(t *testing.T) {
	t.Run("empty primitive ID", func(t *testing.T) {
		in := baseInput()
		in.Primitive.ID = ""
		if _, err := Resolve(in); !errors.Is(err, ErrEmptyPrimitiveID) {
			t.Fatalf("error = %v, want %v", err, ErrEmptyPrimitiveID)
		}
	})

	t.Run("empty contract ID", func(t *testing.T) {
		in := baseInput()
		in.Contract.ID = ""
		if _, err := Resolve(in); !errors.Is(err, ErrEmptyContractID) {
			t.Fatalf("error = %v, want %v", err, ErrEmptyContractID)
		}
	})

	t.Run("primitive contract mismatch", func(t *testing.T) {
		in := baseInput()
		in.Primitive.ContractID = "process/other/v1"
		if _, err := Resolve(in); !errors.Is(err, ErrPrimitiveContractMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrPrimitiveContractMismatch)
		}
	})

	t.Run("contract primitive mismatch", func(t *testing.T) {
		in := baseInput()
		in.Contract.PrimitiveID = "process/other"
		if _, err := Resolve(in); !errors.Is(err, ErrContractPrimitiveMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrContractPrimitiveMismatch)
		}
	})

	t.Run("zero resolved time", func(t *testing.T) {
		in := baseInput()
		in.ResolvedAt = time.Time{}
		if _, err := Resolve(in); !errors.Is(err, ErrZeroResolvedAt) {
			t.Fatalf("error = %v, want %v", err, ErrZeroResolvedAt)
		}
	})

	t.Run("malformed contract never manufactures a decision", func(t *testing.T) {
		in := baseInput()
		in.Contract.Requirements = []model.Requirement{
			{ID: "dupe", Required: true},
			{ID: "dupe", Required: true},
		}
		if _, err := Resolve(in); !errors.Is(err, ErrDuplicateRequirementID) {
			t.Fatalf("error = %v, want %v", err, ErrDuplicateRequirementID)
		}
	})
}

// O. A candidate specimen of another primitive is malformed input.
func TestResolveCandidatePrimitiveMismatchIsError(t *testing.T) {
	specimen := kernelSpecimen("spec-foreign", model.ReuseCopy)
	specimen.PrimitiveID = "process/something-else"

	in := baseInput()
	in.Candidates = []CandidateOption{{Specimen: specimen, ReuseMode: model.ReuseCopy}}

	if _, err := Resolve(in); !errors.Is(err, ErrCandidatePrimitiveMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrCandidatePrimitiveMismatch)
	}
}

// P. Rejection reasons follow contract requirement order deterministically.
func TestResolveRejectionReasonOrdering(t *testing.T) {
	specimen := kernelSpecimen("spec-all-bad", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{{Specimen: specimen, ReuseMode: model.ReuseCopy, Evidence: nil}}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := decision.Resolution.Rejected[0].Reasons
	want := []string{
		`required requirement "bounds-stdout" unknown`,
		`required requirement "bounds-stderr" unknown`,
		`required requirement "supports-timeout" unknown`,
	}
	if len(got) != len(want) {
		t.Fatalf("reasons = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reasons[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Re-running produces identical output.
	again, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve again: %v", err)
	}
	for i := range want {
		if again.Resolution.Rejected[0].Reasons[i] != got[i] {
			t.Errorf("non-deterministic reason at %d: %q vs %q", i, again.Resolution.Rejected[0].Reasons[i], got[i])
		}
	}
}

// Q. Evidence IDs follow candidate order, then requirement order, then
// evaluator evidence order, de-duplicated on first occurrence.
func TestResolveEvidenceIDOrderingAndDedup(t *testing.T) {

	// Contract order is bounds-stdout, bounds-stderr, supports-timeout; the
	// evidence is supplied deliberately in reverse order. Neither candidate
	// supplies supports-timeout, so both are rejected and both are considered.
	candidateA := kernelSpecimen("spec-a", model.ReuseCopy)
	evidenceA := []model.Evidence{
		evidenceFor(candidateA.ID, "bounds-stderr", "ev-a-stderr", model.EvidencePass),
		evidenceFor(candidateA.ID, "bounds-stdout", "ev-a-stdout", model.EvidencePass),
		// Duplicate of an already present observation.
		evidenceFor(candidateA.ID, "bounds-stdout", "ev-a-stdout", model.EvidencePass),
	}

	candidateB := kernelSpecimen("spec-b", model.ReuseCopy)
	evidenceB := []model.Evidence{
		evidenceFor(candidateB.ID, "bounds-stderr", "ev-b-stderr", model.EvidencePass),
		evidenceFor(candidateB.ID, "bounds-stdout", "ev-b-stdout", model.EvidencePass),
	}

	in := baseInput()
	in.Candidates = []CandidateOption{
		// Both are rejected because each is missing a required observation.
		{Specimen: candidateA, ReuseMode: model.ReuseCopy, Evidence: evidenceA},
		{Specimen: candidateB, ReuseMode: model.ReuseCopy, Evidence: evidenceB},
	}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.Outcome != model.OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want build_locally", decision.Resolution.Outcome)
	}

	want := []string{
		"ev-a-stdout", "ev-a-stderr",
		"ev-b-stdout", "ev-b-stderr",
	}
	got := decision.Resolution.EvidenceIDs
	if len(got) != len(want) {
		t.Fatalf("evidence IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("evidenceIDs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// R. Candidates after the selected candidate are never considered.
func TestResolveStopsAtSelectedCandidate(t *testing.T) {
	contract := kernelContract()
	selected := kernelSpecimen("spec-first", model.ReuseCopy)
	never := kernelSpecimen("spec-never", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{
		{Specimen: selected, ReuseMode: model.ReuseCopy, Evidence: passEvidence(selected.ID, contract)},
		{Specimen: never, ReuseMode: model.ReuseCopy, Evidence: passEvidence(never.ID, contract)},
	}

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(decision.Considered) != 1 {
		t.Fatalf("considered = %d, want 1", len(decision.Considered))
	}
	if decision.Considered[0].SpecimenID != selected.ID {
		t.Errorf("considered[0] = %q, want %q", decision.Considered[0].SpecimenID, selected.ID)
	}
	for _, evidenceID := range decision.Resolution.EvidenceIDs {
		if strings.Contains(evidenceID, never.ID) {
			t.Errorf("evidence from an unconsidered candidate leaked: %q", evidenceID)
		}
	}
}

// S. ResolvedAt is exactly the supplied timestamp.
func TestResolveUsesSuppliedTimestamp(t *testing.T) {
	contract := kernelContract()
	specimen := kernelSpecimen("spec-time", model.ReuseCopy)

	in := baseInput()
	in.Candidates = []CandidateOption{{
		Specimen: specimen, ReuseMode: model.ReuseCopy, Evidence: passEvidence(specimen.ID, contract),
	}}
	want := time.Date(2026, time.November, 3, 9, 30, 0, 0, time.UTC)
	in.ResolvedAt = want

	decision, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Resolution.ResolvedAt != want {
		t.Errorf("resolvedAt = %v, want exactly %v", decision.Resolution.ResolvedAt, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
