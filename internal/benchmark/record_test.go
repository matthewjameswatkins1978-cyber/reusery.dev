package benchmark

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func boolPtr(value bool) *bool    { return &value }
func intPtr(value int) *int       { return &value }
func int64Ptr(value int64) *int64 { return &value }

func validPair() (Record, Record) {
	baseline := Record{
		SchemaVersion: SchemaVersion,
		TaskID:        "bounded-subprocess",
		Variant:       VariantBaseline,
		Model:         "agent-x",
		Measured: Measured{
			Success:         boolPtr(true),
			ElapsedMs:       int64Ptr(60_000),
			InputTokens:     intPtr(9_000),
			OutputTokens:    intPtr(2_100),
			ReasoningTokens: intPtr(400),
			ToolCalls:       intPtr(40),
			TestRuns:        intPtr(3),
			GeneratedLines:  intPtr(320),
		},
		Estimated: &Estimated{EngineeringMinutes: intPtr(180), Basis: "senior engineer estimate"},
		Notes:     "measured",
	}
	reusery := Record{
		SchemaVersion: SchemaVersion,
		TaskID:        "bounded-subprocess",
		Variant:       VariantReusery,
		Model:         "agent-x",
		Measured: Measured{
			Success:           boolPtr(true),
			ElapsedMs:         int64Ptr(30_000),
			InputTokens:       intPtr(3_000),
			OutputTokens:      intPtr(1_200),
			ReasoningTokens:   intPtr(150),
			ToolCalls:         intPtr(12),
			TestRuns:          intPtr(3),
			GeneratedLines:    intPtr(140),
			ReusedSpecimenIDs: []string{"fixture/process/bounded-subprocess/complete-dependency"},
			ResolutionID:      int64Ptr(7),
		},
	}
	return baseline, reusery
}

func TestValidPairProducesExpectedDeltas(t *testing.T) {
	baseline, reusery := validPair()
	comparison, err := Pair(&baseline, &reusery)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !comparison.Comparable {
		t.Fatalf("comparable = false, reasons %v", comparison.Reasons)
	}
	if comparison.SuccessMismatch {
		t.Error("success mismatch reported for two successful runs")
	}

	byName := map[string]Delta{}
	for _, delta := range comparison.Deltas {
		byName[delta.Metric] = delta
	}
	cases := map[string]struct {
		baseline, reusery int64
	}{
		"input_tokens":     {9_000, 3_000},
		"output_tokens":    {2_100, 1_200},
		"reasoning_tokens": {400, 150},
		"tool_calls":       {40, 12},
		"elapsed_ms":       {60_000, 30_000},
		"test_runs":        {3, 3},
		"generated_lines":  {320, 140},
	}
	for name, want := range cases {
		delta, ok := byName[name]
		if !ok {
			t.Fatalf("no delta for %s", name)
		}
		if delta.Baseline != want.baseline || delta.Reusery != want.reusery {
			t.Errorf("%s delta = %+v, want baseline %d reusery %d", name, delta, want.baseline, want.reusery)
		}
		if delta.SavingsPercent == nil {
			t.Errorf("%s has no savings percentage despite a non-zero baseline", name)
		}
	}
}

func TestMissingBaselineIsNotAComparison(t *testing.T) {
	_, reusery := validPair()
	comparison, err := Pair(nil, &reusery)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if comparison.Comparable {
		t.Fatal("a missing baseline must not produce a comparison")
	}
	if len(comparison.Deltas) != 0 {
		t.Errorf("deltas = %v, want none", comparison.Deltas)
	}
	if len(comparison.Reasons) == 0 || comparison.Reasons[0] != "baseline record is missing" {
		t.Errorf("reasons = %v", comparison.Reasons)
	}
	if comparison.TaskID != "bounded-subprocess" {
		t.Errorf("task id = %q", comparison.TaskID)
	}
}

func TestMissingReuseryRunIsNotAComparison(t *testing.T) {
	baseline, _ := validPair()
	comparison, err := Pair(&baseline, nil)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if comparison.Comparable {
		t.Fatal("a missing reusery run must not produce a comparison")
	}
	if len(comparison.Deltas) != 0 {
		t.Errorf("deltas = %v, want none", comparison.Deltas)
	}
	if comparison.Reasons[0] != "reusery record is missing" {
		t.Errorf("reasons = %v", comparison.Reasons)
	}
}

func TestSuccessMismatchIsSurfaced(t *testing.T) {
	baseline, reusery := validPair()
	reusery.Measured.Success = boolPtr(false)

	comparison, err := Pair(&baseline, &reusery)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !comparison.SuccessMismatch {
		t.Fatal("success mismatch was not surfaced")
	}
	if len(comparison.Reasons) == 0 {
		t.Fatal("mismatch produced no reason")
	}
}

func TestUnknownMetricsStayUnknownThroughAggregation(t *testing.T) {
	baseline, reusery := validPair()
	reusery.Measured.InputTokens = nil
	reusery.Measured.Success = nil

	totals := AggregateMeasured([]Record{baseline, reusery})
	if totals.InputTokens.Reports != 1 || totals.InputTokens.Missing != 1 {
		t.Errorf("input tokens reports=%d missing=%d, want 1/1", totals.InputTokens.Reports, totals.InputTokens.Missing)
	}
	if totals.InputTokens.Sum != 9_000 {
		t.Errorf("input token sum = %d, want 9000", totals.InputTokens.Sum)
	}
	if totals.Successes != 1 || totals.Failures != 0 || totals.SuccessUnknown != 1 {
		t.Errorf("success tally = %d/%d/%d, want 1/0/1", totals.Successes, totals.Failures, totals.SuccessUnknown)
	}
}

func TestEstimatedFieldsAreNeverMixedIntoMeasuredTotals(t *testing.T) {
	baseline, reusery := validPair()

	measured := AggregateMeasured([]Record{baseline, reusery})
	estimated := AggregateEstimated([]Record{baseline, reusery})

	if estimated.EngineeringMinutes != 180 {
		t.Errorf("estimated minutes = %d, want 180", estimated.EngineeringMinutes)
	}
	if estimated.RecordsWithEstimate != 1 {
		t.Errorf("records with estimate = %d, want 1", estimated.RecordsWithEstimate)
	}
	// The measured struct has no field that could hold modelled minutes: assert
	// the measured aggregate still reports only observed sums.
	if measured.InputTokens.Sum != 12_000 || measured.ToolCalls.Sum != 52 {
		t.Errorf("measured sums were disturbed: tokens=%d tool_calls=%d", measured.InputTokens.Sum, measured.ToolCalls.Sum)
	}
	if measured.ReusedSpecimens != 1 || measured.Resolutions != 1 {
		t.Errorf("reused specimens=%d resolutions=%d, want 1/1", measured.ReusedSpecimens, measured.Resolutions)
	}
}

func TestMismatchedTaskOrVariantIsRejected(t *testing.T) {
	baseline, reusery := validPair()
	reusery.TaskID = "something-else"
	if _, err := Pair(&baseline, &reusery); !errors.Is(err, ErrPairMismatch) {
		t.Errorf("err = %v, want ErrPairMismatch", err)
	}

	baseline, reusery = validPair()
	reusery.Variant = VariantBaseline
	if _, err := Pair(&baseline, &reusery); !errors.Is(err, ErrPairMismatch) {
		t.Errorf("err = %v, want ErrPairMismatch", err)
	}
}

func TestLoadRecordRejectsUnknownFieldsAndBadVariants(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	valid := `
schema_version: 1
task_id: t1
variant: baseline
model: agent-x
measured:
  success: true
  input_tokens: 10
`
	if _, err := LoadRecord(write("valid.yaml", valid)); err != nil {
		t.Fatalf("valid record: %v", err)
	}

	cases := map[string]string{
		"unknown field": "schema_version: 1\ntask_id: t1\nvariant: baseline\nmodel: m\nrank: 5\n",
		"bad variant":   "schema_version: 1\ntask_id: t1\nvariant: best\nmodel: m\n",
		"bad schema":    "schema_version: 2\ntask_id: t1\nvariant: baseline\nmodel: m\n",
		"missing task":  "schema_version: 1\ntask_id: ''\nvariant: baseline\nmodel: m\n",
		"missing model": "schema_version: 1\ntask_id: t1\nvariant: baseline\nmodel: ''\n",
		"negative est":  "schema_version: 1\ntask_id: t1\nvariant: baseline\nmodel: m\nestimated:\n  engineering_minutes: -5\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadRecord(write("bad.yaml", content)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadRecordsReadsADirectoryInOrder(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.yaml", "b.yaml"}
	for _, name := range files {
		content := "schema_version: 1\ntask_id: " + name + "\nvariant: baseline\nmodel: m\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("nope"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	records, err := LoadRecords(dir)
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].TaskID != "a.yaml" || records[1].TaskID != "b.yaml" {
		t.Errorf("order = %q, %q", records[0].TaskID, records[1].TaskID)
	}
}
