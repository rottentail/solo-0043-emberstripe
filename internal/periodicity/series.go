// Package periodicity checks whether a faint periodic tone comes from the
// transport mechanism (edge marks) or from the programme itself. It compares a
// reference band outside the sound lanes with the lane traces on the same
// source clock, and keeps every reel position that supports or blocks a call.
package periodicity

import (
	"errors"
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// SeriesKind names the three evidence streams used by a verification.
type SeriesKind string

const (
	Edge  SeriesKind = "edge"
	Left  SeriesKind = "left_lane"
	Right SeriesKind = "right_lane"
)

// Segment is one continuous readable span inside a series. Missing sheets and
// the capture envelope split segments, so sparse windows stay auditable.
type Segment struct {
	StartRow int `json:"picture_row_start"`
	EndRow   int `json:"picture_row_end"`
	Frame    int `json:"source_frame"`
	Row      int `json:"source_row"`
	Samples  int `json:"samples"`
}

// SegmentSource converts one picture row into its readable source address.
func SegmentSource(reel *capture.Reel, pictureRow int) (register.Address, bool) {
	source := pictureRow + reel.Lead
	if source < 0 || source >= reel.Rows*reel.Frames {
		return register.Address{}, false
	}
	address := register.Address{Frame: source / reel.Rows, Row: source % reel.Rows}
	_, ok := reel.Sheets[address.Frame]
	return address, ok
}

// bandMean averages the reference columns of one readable source row.
func bandMean(reel *capture.Reel, address register.Address, band capture.Span) float64 {
	sheet := reel.Sheets[address.Frame]
	var total uint64
	for x := band.X0; x < band.X1; x++ {
		total += uint64(sheet.Gray(x, address.Row))
	}
	return float64(total) / float64(band.Width())
}

// BuildSeries extracts one evidence stream over a picture window. Unreadable
// rows are left at zero and never contribute to statistics or segments.
func BuildSeries(reel *capture.Reel, w register.Window, kind SeriesKind, band capture.Span) ([]float64, []Segment, error) {
	origin := w.Start * reel.Rows
	length := (w.End - w.Start) * reel.Rows
	values := make([]float64, length)
	segments := make([]Segment, 0)
	var current *Segment
	for index := 0; index < length; index++ {
		pictureRow := origin + index
		address, readable := SegmentSource(reel, pictureRow)
		if !readable {
			current = nil
			continue
		}
		switch kind {
		case Edge:
			values[index] = bandMean(reel, address, band)
		case Left, Right:
			lane := reel.Lanes[0]
			if kind == Right {
				lane = reel.Lanes[1]
			}
			values[index] = bandMean(reel, address, lane)
		default:
			return nil, nil, fmt.Errorf("unknown evidence series %q", kind)
		}
		if current == nil || current.EndRow != pictureRow {
			segments = append(segments, Segment{StartRow: pictureRow, EndRow: pictureRow + 1, Frame: address.Frame, Row: address.Row, Samples: 1})
			current = &segments[len(segments)-1]
		} else {
			current.EndRow = pictureRow + 1
			current.Samples++
		}
	}
	return values, segments, nil
}

// DefaultBand picks the wider column gap that is neither picture nor a sound
// lane, so the reference sits outside both optical tracks.
func DefaultBand(reel *capture.Reel) (capture.Span, error) {
	candidates := []capture.Span{
		{X0: reel.Picture.X1, X1: reel.Lanes[0].X0},
		{X0: reel.Lanes[1].X1, X1: reel.Width},
	}
	best := -1
	for i, candidate := range candidates {
		if candidate.Width() > 0 && (best == -1 || candidate.Width() > candidates[best].Width()) {
			best = i
		}
	}
	if best == -1 {
		return capture.Span{}, errors.New("卷内没有声槽以外的参照列区")
	}
	return candidates[best], nil
}

// ValidBand rejects reference bands that overlap the picture or sound lanes,
// because only columns outside the tracks count as independent edge evidence.
func ValidBand(reel *capture.Reel, band capture.Span) bool {
	if band.X0 < 0 || band.X1 <= band.X0 || band.X1 > reel.Width {
		return false
	}
	disjoint := func(span capture.Span) bool { return band.X1 <= span.X0 || band.X0 >= span.X1 }
	return disjoint(reel.Picture) && disjoint(reel.Lanes[0]) && disjoint(reel.Lanes[1])
}
