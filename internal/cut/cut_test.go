package cut

import (
	"image"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

func newGrayImage(width, height int, value uint16) *image.Gray16 {
	img := image.NewGray16(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		// Gray16 stores big-endian 16-bit values, two bytes per pixel.
		if i%2 == 0 {
			img.Pix[i] = byte(value >> 8)
		} else {
			img.Pix[i] = byte(value)
		}
	}
	return img
}

// syntheticReel builds a reel whose lanes are dark (negative full-scale) until
// the onset frame, then clear (positive full-scale). A missing sheet and a
// negative lead exercise padded-zero and outside-capture paths.
func syntheticReel(t *testing.T, frames, rows, lead, gapFrame int) *capture.Reel {
	t.Helper()
	const width = 40
	lane0 := capture.Span{X0: 16, X1: 24}
	lane1 := capture.Span{X0: 28, X1: 36}
	reel := &capture.Reel{
		Header: capture.Header{
			Name: "test-reel", Width: width, Rows: rows, FPS: 24, Frames: frames,
			Lead: lead, Picture: capture.Span{X0: 0, X1: 8},
			Lanes: [2]capture.Span{lane0, lane1},
			Profiles: []capture.Profile{{
				ID:    "g",
				Dark:  capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: rows}},
				Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: rows}},
			}},
		},
		Sheets: map[int]capture.Sheet{},
	}
	dark := graySheet(width, rows, 12000)
	clear := graySheet(width, rows, 52000)
	for frame := 0; frame < frames; frame++ {
		if frame == gapFrame {
			continue
		}
		if frame < onsetFrame {
			reel.Sheets[frame] = dark
		} else {
			reel.Sheets[frame] = clear
		}
	}
	// Reference calibration reads the same constant sheet for both endpoints,
	// but the contrast guard needs distinct values; emulate usable endpoints.
	return reel
}

const onsetFrame = 3

func graySheet(width, rows, value int) capture.Sheet {
	img := newGrayImage(width, rows, uint16(value))
	return capture.Sheet{Bitmap: img, Depth: 16, Digest: "dummy"}
}

func renderReport(t *testing.T, reel *capture.Reel, w register.Window) Report {
	t.Helper()
	g := gauge.Gauge{Profile: "g", Lanes: [2]gauge.Range{
		{Dark: 10000, Clear: 54000}, {Dark: 10000, Clear: 54000},
	}}
	render := readout.Extract(reel, g, w)
	return Detect(reel, g, w, render)
}

func TestCandidatesAreFrameAligned(t *testing.T) {
	reel := syntheticReel(t, 8, 100, 0, -1)
	report := renderReport(t, reel, register.Window{Start: 1, End: 7})
	if len(report.Candidates) == 0 {
		t.Fatal("expected candidates")
	}
	for _, c := range report.Candidates {
		if c.PictureRow != c.Frame*reel.Rows {
			t.Fatalf("candidate %d not on a frame boundary: row %d", c.Frame, c.PictureRow)
		}
		if c.Frame <= report.Window.Start || c.Frame >= report.Window.End {
			t.Fatalf("candidate frame %d outside window %v", c.Frame, report.Window)
		}
	}
}

func TestPaddedZeroIsNotReadableSilence(t *testing.T) {
	const rows, gap = 100, 4
	reel := syntheticReel(t, 8, rows, 0, gap)
	report := renderReport(t, reel, register.Window{Start: 1, End: 7})
	var atGap *Candidate
	for i := range report.Candidates {
		if report.Candidates[i].Frame == gap {
			atGap = &report.Candidates[i]
		}
	}
	if atGap == nil {
		t.Fatalf("expected a candidate at gap frame %d; got %+v", gap, report.Candidates)
	}
	// The post-cut frame is the missing sheet: all rows padded, none readable.
	if atGap.Post.ReadableRows != 0 || atGap.Post.MissingRows != rows {
		t.Fatalf("missing sheet side mislabeled: %+v", atGap.Post)
	}
	if atGap.Post.PaddedZeroRows != rows {
		t.Fatalf("padded zero rows = %d, want %d", atGap.Post.PaddedZeroRows, rows)
	}
	if !contains(atGap.Warnings, WarnPostPaddedZero) {
		t.Fatalf("missing padded-zero warning: %v", atGap.Warnings)
	}
	// Every side must partition its frame exactly.
	for _, side := range []Side{atGap.Pre, atGap.Post} {
		if got := side.ReadableRows + side.MissingRows + side.OutsideRows; got != rows {
			t.Fatalf("side rows %d do not partition one frame (%d)", got, rows)
		}
	}
}

func TestOutsideCaptureIsReportedSeparately(t *testing.T) {
	const rows, lead = 100, -40
	reel := syntheticReel(t, 8, rows, lead, -1)
	report := renderReport(t, reel, register.Window{Start: 0, End: 4})
	first := report.Candidates[0]
	// Negative lead pushes the first 40 picture rows outside the capture.
	if first.Pre.OutsideRows == 0 {
		t.Fatalf("expected outside rows on pre side, got %+v", first.Pre)
	}
	if first.Pre.PaddedZeroRows != first.Pre.OutsideRows+first.Pre.MissingRows {
		t.Fatal("padded-zero must equal missing plus outside")
	}
	if !contains(first.Warnings, WarnPreOutside) || !contains(first.Warnings, WarnPrePaddedZero) {
		t.Fatalf("missing outside warnings: %v", first.Warnings)
	}
}

func TestOnsetOnlyPointsAtReadableRows(t *testing.T) {
	reel := syntheticReel(t, 8, 100, 0, -1)
	report := renderReport(t, reel, register.Window{Start: 1, End: 7})
	for _, c := range report.Candidates {
		for lane, row := range c.OnsetRow {
			if row >= 0 && row < c.PictureRow {
				t.Fatalf("lane %d onset %d precedes its cut boundary %d", lane, row, c.PictureRow)
			}
		}
	}
}

func TestAtMostMaxCandidatesAndSorted(t *testing.T) {
	reel := syntheticReel(t, 12, 100, 0, -1)
	report := renderReport(t, reel, register.Window{Start: 0, End: 12})
	if len(report.Candidates) > MaxCandidates {
		t.Fatalf("got %d candidates, limit %d", len(report.Candidates), MaxCandidates)
	}
	for i := 1; i < len(report.Candidates); i++ {
		if report.Candidates[i].Frame < report.Candidates[i-1].Frame {
			t.Fatal("candidates must be returned in frame order")
		}
	}
}

func contains(set []string, want string) bool {
	for _, v := range set {
		if v == want {
			return true
		}
	}
	return false
}
