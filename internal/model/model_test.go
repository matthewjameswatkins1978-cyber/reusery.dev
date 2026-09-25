package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResolutionPreservesUnknownsAndRejections(t *testing.T) {
	r := Resolution{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Outcome:     OutcomeBuildLocally,
		Reasons:     []string{"no candidate has verified process-tree semantics"},
		Rejected: []Rejection{{
			SpecimenID: "example/specimen",
			Reasons:    []string{"process-tree termination unknown on Windows"},
		}},
		Unknowns:   []string{"macOS behaviour not verified"},
		ResolvedAt: time.Unix(0, 0).UTC(),
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var got Resolution
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	if got.Outcome != OutcomeBuildLocally {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeBuildLocally)
	}
	if len(got.Unknowns) != 1 || len(got.Rejected) != 1 {
		t.Fatalf("resolution lost negative knowledge: %#v", got)
	}
}
