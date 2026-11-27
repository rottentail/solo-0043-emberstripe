package tablet

import (
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
)

func TestTabletValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []capture.TabletStep
		ok    bool
	}{
		{"two ordered", []capture.TabletStep{{ID: "a", Intensity: 0, StartRow: 0, EndRow: 4}, {ID: "b", Intensity: 1, StartRow: 4, EndRow: 8}}, true},
		{"too few", []capture.TabletStep{{ID: "a", Intensity: 0, StartRow: 0, EndRow: 4}}, false},
		{"overlap", []capture.TabletStep{{ID: "a", Intensity: 0, StartRow: 0, EndRow: 6}, {ID: "b", Intensity: 1, StartRow: 4, EndRow: 8}}, false},
		{"bad intensity", []capture.TabletStep{{ID: "a", Intensity: -0.2, StartRow: 0, EndRow: 4}, {ID: "b", Intensity: 1, StartRow: 4, EndRow: 8}}, false},
		{"duplicate id", []capture.TabletStep{{ID: "a", Intensity: 0, StartRow: 0, EndRow: 4}, {ID: "a", Intensity: 1, StartRow: 4, EndRow: 8}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&capture.Tablet{Steps: tc.steps}).Validate(16)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestConfirmedRangesOnlyChainDistinct(t *testing.T) {
	steps := []StepStat{{ID: "a", Label: "1"}, {ID: "b", Label: "2"}, {ID: "c", Label: "3"}, {ID: "d", Label: "4"}}
	pairs := []PairVerdict{{Distinct: true}, {Distinct: false, Status: "thin_coverage"}, {Distinct: true}}
	ranges := confirmedRanges(steps, pairs)
	if len(ranges) != 2 {
		t.Fatalf("expected two singleton-boundary ranges, got %+v", ranges)
	}
	if ranges[0].FirstID != "a" || ranges[0].LastID != "b" || ranges[0].Boundaries != 1 {
		t.Fatalf("bad first range: %+v", ranges[0])
	}
	if ranges[1].FirstID != "c" || ranges[1].LastID != "d" {
		t.Fatalf("bad second range: %+v", ranges[1])
	}
}

func TestEvaluateUndefinedWithoutTablet(t *testing.T) {
	reel := &capture.Reel{Header: capture.Header{Name: "plain"}}
	if _, err := Evaluate(reel, gauge.Gauge{}); err != ErrUndefined {
		t.Fatalf("want ErrUndefined, got %v", err)
	}
}

func TestCoveragePrecedenceAndRescan(t *testing.T) {
	// Geometry-only construction of pair precedence.
	steps := []StepStat{
		{ID: "a", ValidRows: 0},
		{ID: "b", ValidRows: 10},
		{ID: "c", ValidRows: 100},
		{ID: "d", ValidRows: 100},
	}
	pairs := evaluatePairs(steps)
	if pairs[0].Status != "missing" {
		t.Fatalf("zero rows must be missing, got %s", pairs[0].Status)
	}
	if pairs[1].Status != "thin_coverage" {
		t.Fatalf("under minimum rows must be thin_coverage, got %s", pairs[1].Status)
	}
	// 10 vs 100 is already caught by thin coverage; balanced large samples
	// with no separation stay inseparable rather than distinct.
	balanced := evaluatePairs([]StepStat{
		{ID: "c", ValidRows: 100, MeanUnit: 0.5, StdUnit: 0.02},
		{ID: "d", ValidRows: 100, MeanUnit: 0.501, StdUnit: 0.02},
	})
	if balanced[0].Status != "inseparable" || balanced[0].Distinct {
		t.Fatalf("tiny gap must be inseparable, got %+v", balanced[0])
	}
	confounded := evaluatePairs([]StepStat{
		{ID: "e", ValidRows: 30, MeanUnit: 0.5, StdUnit: 0.01},
		{ID: "f", ValidRows: 300, MeanUnit: 0.7, StdUnit: 0.01},
	})
	if confounded[0].Status != "coverage_confound" || confounded[0].Distinct {
		t.Fatalf("10x coverage must be confounded, got %+v", confounded[0])
	}
}
