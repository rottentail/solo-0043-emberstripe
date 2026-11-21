// Package register owns the relationship between picture and optical sample coordinates.
package register

import (
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

const MaxFrames = 48

type Window struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type Address struct {
	Frame int `json:"frame"`
	Row   int `json:"row"`
}
type Run struct {
	Start  int    `json:"picture_row_start"`
	End    int    `json:"picture_row_end"`
	Status string `json:"status"`
}

const (
	Readable = "readable"
	Missing  = "missing_sheet"
	Outside  = "outside_capture"
)

func Validate(h capture.Header, w Window) error {
	if w.Start < 0 || w.End <= w.Start || w.End > h.Frames || w.End-w.Start > MaxFrames {
		return fmt.Errorf("expected 0 <= start < end <= %d with at most %d frames", h.Frames, MaxFrames)
	}
	return nil
}

func Rate(h capture.Header) int { return h.Rows * h.FPS }

func Locate(reel *capture.Reel, pictureRow int) (Address, string) {
	source := pictureRow + reel.Lead
	if source < 0 || source >= reel.Rows*reel.Frames {
		return Address{}, Outside
	}
	a := Address{Frame: source / reel.Rows, Row: source % reel.Rows}
	if _, ok := reel.Sheets[a.Frame]; !ok {
		return a, Missing
	}
	return a, Readable
}

func Coverage(reel *capture.Reel, w Window) []Run {
	runs := make([]Run, 0)
	first, last := w.Start*reel.Rows, w.End*reel.Rows
	for row := first; row < last; {
		a, status := Locate(reel, row)
		end := last
		switch {
		case status == Outside && row+reel.Lead < 0:
			end = min(last, -reel.Lead)
		case status != Outside:
			end = min(last, row+reel.Rows-a.Row)
		}
		if n := len(runs); n > 0 && runs[n-1].Status == status {
			runs[n-1].End = end
		} else {
			runs = append(runs, Run{Start: row, End: end, Status: status})
		}
		row = end
	}
	return runs
}
