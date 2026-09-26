package benchmark

import (
	"errors"
	"fmt"
)

// Metric is one aggregated measurement. Missing is the count of records that
// did not observe the metric: unknown stays unknown rather than counting as
// zero.
type Metric struct {
	Sum     int64 `yaml:"sum" json:"sum"`
	Reports int   `yaml:"reports" json:"reports"`
	Missing int   `yaml:"missing" json:"missing"`
}

// MeasuredTotals is the sum of observed values only. Estimated values are
// structurally excluded: they live in a different section of the record and
// are aggregated separately, so an estimate can never inflate a measurement.
type MeasuredTotals struct {
	Records         int    `yaml:"records" json:"records"`
	Successes       int    `yaml:"successes" json:"successes"`
	Failures        int    `yaml:"failures" json:"failures"`
	SuccessUnknown  int    `yaml:"success_unknown" json:"success_unknown"`
	ElapsedMs       Metric `yaml:"elapsed_ms" json:"elapsed_ms"`
	InputTokens     Metric `yaml:"input_tokens" json:"input_tokens"`
	OutputTokens    Metric `yaml:"output_tokens" json:"output_tokens"`
	ReasoningTokens Metric `yaml:"reasoning_tokens" json:"reasoning_tokens"`
	ToolCalls       Metric `yaml:"tool_calls" json:"tool_calls"`
	TestRuns        Metric `yaml:"test_runs" json:"test_runs"`
	GeneratedLines  Metric `yaml:"generated_lines" json:"generated_lines"`
	ReusedSpecimens int    `yaml:"reused_specimen_ids" json:"reused_specimen_ids"`
	Resolutions     int    `yaml:"resolutions" json:"resolutions"`
}

// EstimatedTotals is the sum of modelled values only. It is a separate type on
// purpose: nothing here can be added to MeasuredTotals.
type EstimatedTotals struct {
	Records             int `yaml:"records" json:"records"`
	EngineeringMinutes  int `yaml:"engineering_minutes" json:"engineering_minutes"`
	RecordsWithEstimate int `yaml:"records_with_estimate" json:"records_with_estimate"`
}

// AggregateMeasured sums every observed field across records, preserving
// unknowns as a Missing count.
func AggregateMeasured(records []Record) MeasuredTotals {
	totals := MeasuredTotals{Records: len(records)}
	for _, record := range records {
		switch {
		case record.Measured.Success == nil:
			totals.SuccessUnknown++
		case *record.Measured.Success:
			totals.Successes++
		default:
			totals.Failures++
		}
		totals.ElapsedMs = accumulateMs(totals.ElapsedMs, record.Measured.ElapsedMs)
		totals.InputTokens = accumulate(totals.InputTokens, record.Measured.InputTokens)
		totals.OutputTokens = accumulate(totals.OutputTokens, record.Measured.OutputTokens)
		totals.ReasoningTokens = accumulate(totals.ReasoningTokens, record.Measured.ReasoningTokens)
		totals.ToolCalls = accumulate(totals.ToolCalls, record.Measured.ToolCalls)
		totals.TestRuns = accumulate(totals.TestRuns, record.Measured.TestRuns)
		totals.GeneratedLines = accumulate(totals.GeneratedLines, record.Measured.GeneratedLines)
		totals.ReusedSpecimens += len(record.Measured.ReusedSpecimenIDs)
		if record.Measured.ResolutionID != nil {
			totals.Resolutions++
		}
	}
	return totals
}

// AggregateEstimated sums modelled values only.
func AggregateEstimated(records []Record) EstimatedTotals {
	totals := EstimatedTotals{Records: len(records)}
	for _, record := range records {
		if record.Estimated == nil || record.Estimated.EngineeringMinutes == nil {
			continue
		}
		totals.RecordsWithEstimate++
		totals.EngineeringMinutes += *record.Estimated.EngineeringMinutes
	}
	return totals
}

func accumulate(metric Metric, value *int) Metric {
	if value == nil {
		metric.Missing++
		return metric
	}
	metric.Sum += int64(*value)
	metric.Reports++
	return metric
}

func accumulateMs(metric Metric, value *int64) Metric {
	if value == nil {
		metric.Missing++
		return metric
	}
	metric.Sum += *value
	metric.Reports++
	return metric
}

// Comparison is the inspectable result of pairing a baseline run with a
// Reusery run for the same task.
type Comparison struct {
	TaskID          string   `yaml:"task_id" json:"task_id"`
	Comparable      bool     `yaml:"comparable" json:"comparable"`
	Reasons         []string `yaml:"reasons,omitempty" json:"reasons,omitempty"`
	SuccessMismatch bool     `yaml:"success_mismatch" json:"success_mismatch"`
	Deltas          []Delta  `yaml:"deltas,omitempty" json:"deltas,omitempty"`
	Exploratory     bool     `yaml:"exploratory" json:"exploratory"`
}

// Delta is one paired metric difference. SavingsPercent is only populated when
// the baseline value exists and is non-zero: a percentage is never invented
// from a missing baseline.
type Delta struct {
	Metric         string   `json:"metric"`
	Baseline       int64    `json:"baseline"`
	Reusery        int64    `json:"reusery"`
	Difference     int64    `json:"difference"`
	SavingsPercent *float64 `json:"savings_percent,omitempty"`
}

// ErrPairMismatch reports two records that are not two sides of one
// experiment.
var ErrPairMismatch = errors.New("benchmark: records are not a baseline/reusery pair for one task")

// Pair compares a baseline and a Reusery record for one task.
//
// A missing side is not a comparison: it reports comparable=false with a
// reason instead of manufacturing a delta. Metric deltas are computed only
// where both sides actually observed the value, and a savings percentage is
// only computed when the baseline exists and is non-zero.
func Pair(baseline, reusery *Record) (Comparison, error) {
	if baseline == nil && reusery == nil {
		return Comparison{}, errors.New("benchmark: both records are missing")
	}

	comparison := Comparison{Reasons: []string{}}
	switch {
	case baseline == nil:
		comparison.TaskID = taskIDOf(reusery)
		comparison.Reasons = append(comparison.Reasons, "baseline record is missing")
		return comparison, nil
	case reusery == nil:
		comparison.TaskID = taskIDOf(baseline)
		comparison.Reasons = append(comparison.Reasons, "reusery record is missing")
		return comparison, nil
	}

	comparison.TaskID = baseline.TaskID
	if baseline.TaskID != reusery.TaskID {
		return Comparison{}, fmt.Errorf("%w: %q vs %q", ErrPairMismatch, baseline.TaskID, reusery.TaskID)
	}
	if baseline.Variant != VariantBaseline || reusery.Variant != VariantReusery {
		return Comparison{}, fmt.Errorf("%w: variants are %q and %q",
			ErrPairMismatch, baseline.Variant, reusery.Variant)
	}
	comparison.Exploratory = baseline.Exploratory || reusery.Exploratory

	comparison.Comparable = true

	if baseline.Measured.Success == nil || reusery.Measured.Success == nil {
		comparison.Comparable = false
		comparison.Reasons = append(comparison.Reasons, "success outcome was not measured on both sides")
	}
	if baseline.Model != reusery.Model {
		comparison.Reasons = append(comparison.Reasons,
			fmt.Sprintf("model differs (%q vs %q)", baseline.Model, reusery.Model))
	}
	if comparison.Comparable && *baseline.Measured.Success != *reusery.Measured.Success {
		comparison.SuccessMismatch = true
		comparison.Reasons = append(comparison.Reasons, "success outcomes differ")
	}

	comparison.Deltas = []Delta{}
	comparison.Deltas = appendDeltaMs(comparison.Deltas, "elapsed_ms", baseline.Measured.ElapsedMs, reusery.Measured.ElapsedMs)
	comparison.Deltas = appendDelta(comparison.Deltas, "input_tokens", baseline.Measured.InputTokens, reusery.Measured.InputTokens)
	comparison.Deltas = appendDelta(comparison.Deltas, "output_tokens", baseline.Measured.OutputTokens, reusery.Measured.OutputTokens)
	comparison.Deltas = appendDelta(comparison.Deltas, "reasoning_tokens", baseline.Measured.ReasoningTokens, reusery.Measured.ReasoningTokens)
	comparison.Deltas = appendDelta(comparison.Deltas, "tool_calls", baseline.Measured.ToolCalls, reusery.Measured.ToolCalls)
	comparison.Deltas = appendDelta(comparison.Deltas, "test_runs", baseline.Measured.TestRuns, reusery.Measured.TestRuns)
	comparison.Deltas = appendDelta(comparison.Deltas, "generated_lines", baseline.Measured.GeneratedLines, reusery.Measured.GeneratedLines)
	return comparison, nil
}

func appendDelta(deltas []Delta, metric string, baseline, reusery *int) []Delta {
	if baseline == nil || reusery == nil {
		return deltas
	}
	entry := Delta{
		Metric:     metric,
		Baseline:   int64(*baseline),
		Reusery:    int64(*reusery),
		Difference: int64(*reusery) - int64(*baseline),
	}
	if *baseline != 0 {
		percent := (float64(*baseline) - float64(*reusery)) / float64(*baseline) * 100
		entry.SavingsPercent = &percent
	}
	return append(deltas, entry)
}

func appendDeltaMs(deltas []Delta, metric string, baseline, reusery *int64) []Delta {
	if baseline == nil || reusery == nil {
		return deltas
	}
	entry := Delta{
		Metric:     metric,
		Baseline:   *baseline,
		Reusery:    *reusery,
		Difference: *reusery - *baseline,
	}
	if *baseline != 0 {
		percent := (float64(*baseline) - float64(*reusery)) / float64(*baseline) * 100
		entry.SavingsPercent = &percent
	}
	return append(deltas, entry)
}

func taskIDOf(record *Record) string {
	if record == nil {
		return ""
	}
	return record.TaskID
}
