package lightbench

import (
	"image"
	"image/color"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// fixture builds a three-frame reel whose middle sound sheet is missing, so the
// readable/missing boundary falls inside a single applied frame window. Each
// frame is ten picture rows tall and every sound row is uniformly dark above
// row five and clear below it, giving both lanes well-separated endpoints and a
// sample value that changes once per frame.
func fixture() *capture.Reel {
	bounds := image.Rect(0, 0, 12, 10)
	sheet := image.NewGray16(bounds)
	for y := 0; y < 10; y++ {
		value := uint16(2000)
		if y >= 5 {
			value = 60000
		}
		for x := 0; x < 12; x++ {
			sheet.Set(x, y, color.Gray16{Y: value})
		}
	}
	reel := &capture.Reel{Header: capture.Header{
		Name: "test", Width: 12, Rows: 10, FPS: 16, Frames: 3, Lead: 0,
		Picture: capture.Span{X0: 0, X1: 4},
		Lanes:   [2]capture.Span{{X0: 4, X1: 8}, {X0: 8, X1: 12}},
		Profiles: []capture.Profile{{
			ID:    "g",
			Dark:  capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: 2}},
			Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 8, X1: 10}},
		}},
	}, Sheets: map[int]capture.Sheet{
		0: {Bitmap: sheet, Depth: 16},
		2: {Bitmap: sheet, Depth: 16},
	}}
	return reel
}

// checkBins asserts the invariants every envelope must hold: bins are nonempty,
// never straddle a coverage edge, report no extrema for unreadable rows, and
// name an extreme row that lies inside the bin and its readable span.
func checkBins(t *testing.T, bins []amplitudeBin, runs []register.Run) {
	t.Helper()
	edges := map[int]bool{}
	for _, run := range runs {
		edges[run.Start], edges[run.End] = true, true
	}
	for _, bin := range bins {
		if bin.Start >= bin.End {
			t.Fatalf("empty bin %+v", bin)
		}
		for edge := range edges {
			if bin.Start < edge && edge < bin.End {
				t.Fatalf("bin %+v straddles coverage edge %d", bin, edge)
			}
		}
		if bin.Readable == 0 {
			if bin.MinRow != [2]int{-1, -1} || bin.MaxRow != [2]int{-1, -1} {
				t.Fatalf("unreadable bin reports extrema rows: %+v", bin)
			}
			continue
		}
		if bin.Readable != bin.End-bin.Start {
			t.Fatalf("readable bin has partial rows: %+v", bin)
		}
		for channel := range 2 {
			for _, row := range []int{bin.MinRow[channel], bin.MaxRow[channel]} {
				if row < bin.Start || row >= bin.End {
					t.Fatalf("extreme row %d outside bin %+v", row, bin)
				}
			}
		}
	}
}

func TestSummarizeBinsDoNotCrossCoverage(t *testing.T) {
	reel := fixture()
	g, err := gauge.Measure(reel, "g")
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	runs := register.Coverage(reel, register.Window{Start: 0, End: 3})
	bins := summarize(reel, g, runs, 0, 30)
	if len(bins) == 0 {
		t.Fatal("no bins")
	}
	checkBins(t, bins, runs)
	// The missing middle frame must appear as its own readable-free bin range.
	gap := 0
	for _, bin := range bins {
		if bin.Start >= 10 && bin.End <= 20 {
			if bin.Readable != 0 {
				t.Fatalf("gap bin reports readable rows: %+v", bin)
			}
			gap++
		}
	}
	if gap == 0 {
		t.Fatal("missing sheet not represented in envelope")
	}
}

func TestSummarizeNarrowIntervalStaysBounded(t *testing.T) {
	reel := fixture()
	g, err := gauge.Measure(reel, "g")
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	runs := register.Coverage(reel, register.Window{Start: 0, End: 3})
	// A narrow interval spanning the tail of frame 0, the whole gap and the
	// head of frame 2 keeps its exact bounds while staying row-resolved.
	narrow := summarize(reel, g, runs, 8, 22)
	checkBins(t, narrow, runs)
	if narrow[0].Start != 8 || narrow[len(narrow)-1].End != 22 {
		t.Fatalf("narrow interval not bounded: %d..%d", narrow[0].Start, narrow[len(narrow)-1].End)
	}
	readable := 0
	for _, bin := range narrow {
		readable += bin.Readable
	}
	if readable != 4 {
		t.Fatalf("expected four readable rows in 8..22, got %d", readable)
	}
}

func TestSummarizeEmptyAndOutsideIntervals(t *testing.T) {
	reel := fixture()
	g, _ := gauge.Measure(reel, "g")
	runs := register.Coverage(reel, register.Window{Start: 0, End: 3})
	if bins := summarize(reel, g, runs, 5, 5); bins != nil {
		t.Fatalf("expected nil for empty interval, got %d bins", len(bins))
	}
	if bins := summarize(reel, g, runs, 40, 50); len(bins) != 0 {
		t.Fatalf("expected no bins outside the window, got %d", len(bins))
	}
}

func TestSummarizeBinCountIsBudgeted(t *testing.T) {
	reel := fixture()
	g, _ := gauge.Measure(reel, "g")
	runs := register.Coverage(reel, register.Window{Start: 0, End: 3})
	// Widening from a single row to the whole window must not grow the reply
	// beyond the fixed bin budget; each bin can carry only a few integers.
	for _, span := range [][2]int{{0, 1}, {0, 30}, {0, 10}, {10, 20}, {20, 30}} {
		bins := summarize(reel, g, runs, span[0], span[1])
		if len(bins) > targetBins+len(runs) {
			t.Fatalf("span %v produced %d bins over budget", span, len(bins))
		}
	}
}
