// Package silence reviews a picture window that is expected to stay quiet. It
// separates readable low-level optical rows from the synthetic zero holes used
// for missing material, keeps narrow pulses and cross-frame quiet stretches
// inspectable, and records signed verdicts whose calibration lineage can be
// revalidated after the catalog is rescanned or the gauge changes.
package silence

import (
	"errors"
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Review statuses partition the window rows. Holes are the zero-filled
// coverage holes (missing sheet or outside the capture); silence marks
// genuinely readable material whose both lanes stay under the level limit;
// pulse marks any readable row where at least one lane exceeds the limit.
const (
	StatusSilence = "silence"
	StatusPulse   = "pulse"
	StatusHole    = "zero_hole"
)

// DefaultLevel is the default per-lane PCM magnitude treated as quiet.
const DefaultLevel = 128

// Thresholds controls the quiet review in calibrated 16-bit PCM units.
type Thresholds struct {
	Level    int `json:"level"`
	MaxPulse int `json:"max_pulse_rows"`
}

// LaneLevel reports the strongest readable magnitude seen on one lane.
type LaneLevel struct {
	Peak      int `json:"peak_abs_sample"`
	OverLimit int `json:"over_limit_rows"`
}

// Segment is a maximal run of rows sharing one review status. Runs merge
// across picture frame boundaries, so one quiet stretch spanning frames stays
// a single inspectable segment.
type Segment struct {
	Start        int            `json:"picture_row_start"`
	End          int            `json:"picture_row_end"`
	Frames       [2]int         `json:"frames"`
	Status       string         `json:"status"`
	Rows         int            `json:"rows"`
	CrossesFrame bool           `json:"crosses_frame"`
	Peak         [2]int         `json:"peak_abs_sample"`
	Lanes        [2]LaneLevel   `json:"lanes"`
	Coverage     []register.Run `json:"coverage,omitempty"`
}

// FrameRows counts the review outcomes inside one picture frame.
type FrameRows struct {
	Frame   int `json:"frame"`
	Silence int `json:"silence_rows"`
	Pulse   int `json:"pulse_rows"`
	Hole    int `json:"zero_hole_rows"`
}

// Analysis is the review result for one window and calibration.
type Analysis struct {
	Capture          string          `json:"capture"`
	Window           register.Window `json:"window"`
	Profile          string          `json:"profile"`
	Rate             int             `json:"rate"`
	Rows             int             `json:"rows_per_frame"`
	Thresholds       Thresholds      `json:"thresholds"`
	Totals           map[string]int  `json:"totals"`
	Lanes            [2]LaneLevel    `json:"lanes"`
	PulseWithinLimit bool            `json:"pulse_within_limit"`
	Frames           []FrameRows     `json:"frames"`
	Segments         []Segment       `json:"segments"`
}

// Baseline freezes the lineage a verdict was taken against.
type Baseline struct {
	SourceDigest string      `json:"source_sha256"`
	Gauge        gauge.Gauge `json:"gauge"`
}

// Verdict is a saved quiet-leader sign-off that the next shift can reopen.
type Verdict struct {
	ID         string          `json:"id"`
	Capture    string          `json:"capture"`
	Window     register.Window `json:"window"`
	Profile    string          `json:"profile"`
	Thresholds Thresholds      `json:"thresholds"`
	Baseline   Baseline        `json:"baseline"`
	Analysis   Analysis        `json:"analysis"`
	SignedBy   string          `json:"signed_by"`
	Note       string          `json:"note"`
	Approved   bool            `json:"approved"`
	CreatedAt  string          `json:"created_at"`
}

// Revalidation accompanies a reopened verdict with the live lineage check.
type Revalidation struct {
	Valid   bool     `json:"valid"`
	Reasons []string `json:"reasons,omitempty"`
}

// ValidateThresholds rejects limits outside the signed 16-bit sample range.
func ValidateThresholds(t Thresholds) error {
	if t.Level < 0 || t.Level > 32767 {
		return errors.New("level must be between 0 and 32767 PCM units")
	}
	if t.MaxPulse < 0 || t.MaxPulse > 64 {
		return errors.New("max_pulse_rows must be between 0 and 64")
	}
	return nil
}

func absSample(v int16) int {
	if v < 0 {
		return -int(v)
	}
	return int(v)
}

// Analyze classifies every row of an extracted window. Readable rows are read
// from the calibrated render; unreadable coverage stays a zero hole instead of
// being mistaken for quiet optical material. rowsPerFrame and rate describe the
// native picture clock so segments can be annotated with frame coordinates.
func Analyze(captureName string, rowsPerFrame, rate int, g gauge.Gauge, render readout.Render, w register.Window, t Thresholds) Analysis {
	result := Analysis{
		Capture: captureName, Window: w, Profile: g.Profile,
		Rate: rate, Rows: rowsPerFrame, Thresholds: t,
		Totals: map[string]int{StatusSilence: 0, StatusPulse: 0, StatusHole: 0},
	}
	origin := w.Start * rowsPerFrame
	length := (w.End - w.Start) * rowsPerFrame
	frameCounts := make([]FrameRows, w.End-w.Start)
	for i := range frameCounts {
		frameCounts[i] = FrameRows{Frame: w.Start + i}
	}

	// statusAt walks the same coverage partition used to build the PCM.
	runIndex := 0
	statusAt := func(row int) (string, register.Run) {
		for render.Coverage[runIndex].End <= row {
			runIndex++
		}
		run := render.Coverage[runIndex]
		if run.Status != register.Readable {
			return StatusHole, run
		}
		offset := (row - origin) * 2
		if absSample(render.Samples[offset]) > t.Level || absSample(render.Samples[offset+1]) > t.Level {
			return StatusPulse, run
		}
		return StatusSilence, run
	}

	type openSegment struct {
		Segment
		coverage []register.Run
	}
	var segments []*openSegment
	var current *openSegment
	for row := origin; row < origin+length; row++ {
		status, coverageRun := statusAt(row)
		if current == nil || current.Status != status {
			current = &openSegment{Segment: Segment{Start: row, End: row, Status: status}}
			segments = append(segments, current)
		}
		current.End = row + 1
		if n := len(current.coverage); n == 0 || current.coverage[n-1].Start != coverageRun.Start {
			current.coverage = append(current.coverage, coverageRun)
		}
		if status != StatusHole {
			offset := (row - origin) * 2
			for lane := 0; lane < 2; lane++ {
				magnitude := absSample(render.Samples[offset+lane])
				current.Lanes[lane].Peak = max(current.Lanes[lane].Peak, magnitude)
				current.Peak[lane] = max(current.Peak[lane], magnitude)
				if magnitude > t.Level {
					current.Lanes[lane].OverLimit++
				}
				result.Lanes[lane].Peak = max(result.Lanes[lane].Peak, magnitude)
				if magnitude > t.Level {
					result.Lanes[lane].OverLimit++
				}
			}
		}
		result.Totals[status]++
		frameCounts[row/rowsPerFrame-w.Start].bump(status)
	}

	for _, open := range segments {
		open.Rows = open.End - open.Start
		open.Frames = [2]int{open.Start / rowsPerFrame, (open.End - 1) / rowsPerFrame}
		open.CrossesFrame = open.Frames[0] != open.Frames[1]
		open.Coverage = open.coverage
		result.Segments = append(result.Segments, open.Segment)
	}
	result.Frames = frameCounts
	result.PulseWithinLimit = result.Totals[StatusPulse] <= t.MaxPulse
	return result
}

func (f *FrameRows) bump(status string) {
	switch status {
	case StatusSilence:
		f.Silence++
	case StatusPulse:
		f.Pulse++
	case StatusHole:
		f.Hole++
	}
}

// Revalidate reports whether a saved verdict still matches the live catalog
// and calibration. A changed source digest means rescanned pages; a changed
// gauge means the calibration endpoints moved. Either invalidates the verdict.
func Revalidate(v Verdict, sourceDigest string, g gauge.Gauge) Revalidation {
	var reasons []string
	if v.Baseline.SourceDigest != sourceDigest {
		reasons = append(reasons, fmt.Sprintf("扫描源摘要已变化（判定 %s ／当前 %s）", v.Baseline.SourceDigest, sourceDigest))
	}
	if v.Baseline.Gauge.Profile != g.Profile || v.Baseline.Gauge.Lanes != g.Lanes {
		reasons = append(reasons, fmt.Sprintf("标定 %s 的左右声槽端点已变化", v.Profile))
	}
	return Revalidation{Valid: len(reasons) == 0, Reasons: reasons}
}
