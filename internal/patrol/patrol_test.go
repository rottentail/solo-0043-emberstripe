package patrol

import (
	"image"
	"image/color"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const testRows = 100

func testReel(t *testing.T, sheets map[int]func(y int) [2]uint16) *capture.Reel {
	t.Helper()
	reel := &capture.Reel{
		Header: capture.Header{
			Name: "test-reel", Width: 40, Rows: testRows, FPS: 24, Frames: 4, Lead: 0,
			Picture:  capture.Span{X0: 0, X1: 8},
			Lanes:    [2]capture.Span{{X0: 10, X1: 20}, {X0: 24, X1: 34}},
			Profiles: []capture.Profile{{ID: "bench", Dark: capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: 4}}, Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 4, X1: 8}}}},
		},
		Sheets: map[int]capture.Sheet{},
	}
	for frame, fill := range sheets {
		img := image.NewGray16(image.Rect(0, 0, reel.Width, testRows))
		for y := 0; y < testRows; y++ {
			levels := fill(y)
			for lane, band := range reel.Lanes {
				for x := band.X0; x < band.X1; x++ {
					img.SetGray16(x, y, color.Gray16{Y: levels[lane]})
				}
			}
		}
		reel.Sheets[frame] = capture.Sheet{Bitmap: img, Depth: 16, Digest: "sheet"}
	}
	return reel
}

func testGauge() gauge.Gauge {
	return gauge.Gauge{Profile: "bench", Lanes: [2]gauge.Range{{Dark: 1000, Clear: 60000}, {Dark: 1000, Clear: 60000}}}
}

func runReport(t *testing.T, reel *capture.Reel, segments []Segment) Report {
	t.Helper()
	report, err := Run(reel, testGauge(), segments, func(a register.Address) ([2]int16, [2]bool) {
		sheet := reel.Sheets[a.Frame]
		var samples [2]int16
		var clipped [2]bool
		for lane, band := range reel.Lanes {
			gray := gauge.Mean(sheet, band, capture.Span{X0: a.Row, X1: a.Row + 1})
			unit := (gray - 1000) / (60000 - 1000)
			clipped[lane] = unit < 0 || unit > 1
			if unit < 0 {
				unit = 0
			}
			if unit > 1 {
				unit = 1
			}
			samples[lane] = int16((2*unit - 1) * 32767)
		}
		return samples, clipped
	})
	if err != nil {
		t.Fatalf("patrol run: %v", err)
	}
	return report
}

func TestRunDistinguishesHealthyTrendAndFailure(t *testing.T) {
	reel := testReel(t, map[int]func(y int) [2]uint16{
		0: func(y int) [2]uint16 { return [2]uint16{30000, 30000} },
		1: func(y int) [2]uint16 {
			// Lane 0 alternates between only two narrow levels; lane 1 stays flat.
			if y%2 == 0 {
				return [2]uint16{12000, 30000}
			}
			return [2]uint16{9000, 30000}
		},
		2: func(y int) [2]uint16 {
			// Lane 0 is pinned above the clear endpoint for a sustained run.
			if y >= 10 && y < 30 {
				return [2]uint16{64000, 30000}
			}
			return [2]uint16{30000, 30000}
		},
		3: func(y int) [2]uint16 { return [2]uint16{30000, 30000} },
	})
	report := runReport(t, reel, []Segment{
		{ID: "two-level", Kind: KindModulated, Start: 1, End: 2},
		{ID: "clipped", Kind: KindModulated, Start: 2, End: 3},
		{ID: "level-bg", Kind: KindStatic, Start: 3, End: 4},
	})
	if report.Verdict != VerdictFailed {
		t.Fatalf("overall verdict = %q, want failed", report.Verdict)
	}
	twoLevel := report.Segments[0]
	if twoLevel.Lanes[0].Verdict != VerdictTrend || twoLevel.Lanes[1].Verdict != VerdictFailed {
		t.Fatalf("two-level verdicts = %q / %q, want trend_only / failed", twoLevel.Lanes[0].Verdict, twoLevel.Lanes[1].Verdict)
	}
	clipped := report.Segments[1]
	if clipped.Lanes[0].Verdict != VerdictFailed || clipped.Lanes[0].Findings[0].Rule != RuleClip {
		t.Fatalf("clipped lane = %+v, want clipped_platform failure", clipped.Lanes[0])
	}
	level := report.Segments[2]
	if level.Verdict != VerdictHealthy {
		t.Fatalf("level background = %q, want healthy", level.Verdict)
	}
}

func TestRunFlagsShortAndMissingContent(t *testing.T) {
	// Only the first frame exists; the modulated window lands entirely on a gap.
	reel := testReel(t, map[int]func(y int) [2]uint16{
		0: func(y int) [2]uint16 { return [2]uint16{30000, 30000} },
	})
	report := runReport(t, reel, []Segment{{ID: "gap", Kind: KindModulated, Start: 2, End: 3}})
	lane := report.Segments[0].Lanes[0]
	if lane.Verdict != VerdictFailed || lane.Findings[0].Rule != RuleShortContent {
		t.Fatalf("gap verdict = %+v, want short_content failure", lane)
	}
}

func TestValidateRejectsOverlapAndBadKind(t *testing.T) {
	reel := testReel(t, map[int]func(y int) [2]uint16{0: func(y int) [2]uint16 { return [2]uint16{30000, 30000} }})
	cases := []struct {
		name     string
		segments []Segment
	}{
		{"overlap", []Segment{{ID: "a", Kind: KindStatic, Start: 0, End: 2}, {ID: "b", Kind: KindStatic, Start: 1, End: 3}}},
		{"bad kind", []Segment{{ID: "a", Kind: "unknown", Start: 0, End: 1}}},
		{"outside", []Segment{{ID: "a", Kind: KindStatic, Start: 3, End: 5}}},
		{"duplicate id", []Segment{{ID: "a", Kind: KindStatic, Start: 0, End: 1}, {ID: "a", Kind: KindStatic, Start: 1, End: 2}}},
	}
	for _, tc := range cases {
		if err := Validate(reel.Header, tc.segments); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}
