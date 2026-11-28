package lightbench

import (
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

func TestReviewBoundariesMergeKinds(t *testing.T) {
	rows := 100
	reel := &capture.Reel{
		Header: capture.Header{Rows: rows},
		Sheets: map[int]capture.Sheet{
			0: {Depth: 16},
			1: {Depth: 8}, // picture cut at row 100; sound cut coincides there
		},
	}
	coverage := []register.Run{
		{Start: 0, End: 100, Status: register.Readable},
		{Start: 100, End: 200, Status: register.Missing},
	}
	got := reviewBoundaries(reel, coverage, rows, 0, 2)
	if len(got) != 1 {
		t.Fatalf("expected one merged boundary, got %d: %+v", len(got), got)
	}
	b := got[0]
	if b.Row != 100 || b.Kinds != "sound,picture" || b.From != register.Readable || b.To != register.Missing {
		t.Fatalf("merge mismatch: %+v", b)
	}
	if b.FromDepth != 16 || b.ToDepth != 8 {
		t.Fatalf("depth mismatch: %+v", b)
	}

	// Window head is never reported as a boundary: one coverage run starting
	// at the head and uniform picture sheets produce no cuts.
	uniform := &capture.Reel{
		Header: capture.Header{Rows: rows},
		Sheets: map[int]capture.Sheet{0: {Depth: 16}, 1: {Depth: 16}},
	}
	single := []register.Run{{Start: 0, End: 200, Status: register.Readable}}
	head := reviewBoundaries(uniform, single, rows, 0, 2)
	if len(head) != 0 {
		t.Fatalf("window head must not be a boundary, got %+v", head)
	}
}
