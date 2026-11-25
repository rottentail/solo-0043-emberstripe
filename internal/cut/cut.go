// Package cut finds frame-aligned edit points from per-lane optical activity.
//
// A cut candidate is always a picture-frame boundary, never an arbitrary row.
// Activity is measured only on readable samples: missing sheets and rows
// outside the capture are padded to zero in the PCM but are reported as
// coverage evidence instead of being mistaken for audible silence.
package cut

import (
	"math"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const (
	// MaxCandidates bounds the clickable choices offered in one window.
	MaxCandidates = 5
	// guardFrames keeps one whole frame of evidence on each side of a cut.
	guardFrames = 1
	// listenFrames is the default editorial audition span on either side.
	listenFrames = 2
	// thresholdFloor is the smallest activity threshold, in PCM units.
	thresholdFloor = 32.0
	// thresholdRatio sets the lane threshold to 2% of the window RMS.
	thresholdRatio = 0.02
)

// Warning codes explain why a visually clean boundary may not be a valid cut.
const (
	WarnPrePaddedZero  = "pre_padded_zero"
	WarnPostPaddedZero = "post_padded_zero"
	WarnPreOutside     = "pre_outside_capture"
	WarnPostOutside    = "post_outside_capture"
	WarnPreActive      = "pre_lane_active"
	WarnPostInactive   = "post_lane_inactive"
	WarnWeakEvidence   = "weak_readable_evidence"
)

// FrameSpan is a half-open picture-frame interval usable by the audition API.
type FrameSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Side holds the evidence on one side of a cut, in absolute picture rows.
type Side struct {
	FrameStart     int        `json:"frame_start"`
	FrameEnd       int        `json:"frame_end"`
	RowStart       int        `json:"row_start"`
	RowEnd         int        `json:"row_end"`
	ReadableRows   int        `json:"readable_rows"`
	MissingRows    int        `json:"missing_rows"`
	OutsideRows    int        `json:"outside_rows"`
	PaddedZeroRows int        `json:"padded_zero_rows"`
	SilentReadable [2]int     `json:"silent_readable_rows"`
	Peak           [2]int     `json:"peak"`
	RMS            [2]float64 `json:"rms"`
}

// Candidate is one clickable frame boundary with its supporting evidence.
type Candidate struct {
	Frame      int        `json:"frame"`
	PictureRow int        `json:"picture_row"`
	Score      float64    `json:"score"`
	OnsetRow   [2]int     `json:"onset_row"`
	Contrast   [2]float64 `json:"contrast"`
	Pre        Side       `json:"pre"`
	Post       Side       `json:"post"`
	ListenPre  FrameSpan  `json:"listen_pre"`
	ListenPost FrameSpan  `json:"listen_post"`
	Warnings   []string   `json:"warnings"`
}

// Report is the complete cut analysis for an applied window.
type Report struct {
	Window       register.Window `json:"window"`
	Rate         int             `json:"rate"`
	RowsPerFrame int             `json:"rows_per_frame"`
	FPS          int             `json:"fps"`
	Profile      gauge.Gauge     `json:"profile"`
	Threshold    [2]float64      `json:"threshold"`
	WindowRMS    [2]float64      `json:"window_rms"`
	Clipped      [2]int          `json:"clipped_samples"`
	Candidates   []Candidate     `json:"candidates"`
}

// laneStats accumulates per-lane magnitudes over readable rows only.
type laneStats struct {
	silent [2]int
	peak   [2]int
	sumSq  [2]float64
}

func (s *laneStats) add(lane int, sample int16) {
	magnitude := int(math.Abs(float64(sample)))
	s.peak[lane] = max(s.peak[lane], magnitude)
	s.sumSq[lane] += float64(magnitude) * float64(magnitude)
	if sample == 0 {
		s.silent[lane]++
	}
}

func (s *laneStats) rms(readableRows int) [2]float64 {
	var out [2]float64
	if readableRows == 0 {
		return out
	}
	for lane := range out {
		out[lane] = math.Sqrt(s.sumSq[lane] / float64(readableRows))
	}
	return out
}

// Detect analyses a validated window and ranks its frame boundaries. The gauge
// must already be resolved; the caller owns parameter validation.
func Detect(reel *capture.Reel, g gauge.Gauge, w register.Window, render readout.Render) Report {
	rows := reel.Rows
	origin, last := w.Start*rows, w.End*rows
	length := last - origin
	statuses := statusPerRow(render.Coverage, origin, length)

	report := Report{
		Window: w, Rate: register.Rate(reel.Header), RowsPerFrame: rows, FPS: reel.FPS,
		Profile: g, Candidates: []Candidate{},
	}

	total := laneStats{}
	readableTotal := 0
	for offset := 0; offset < length; offset++ {
		if statuses[offset] != register.Readable {
			continue
		}
		readableTotal++
		for lane := range 2 {
			total.add(lane, render.Samples[offset*2+lane])
		}
	}
	report.WindowRMS = total.rms(readableTotal)
	report.Clipped = render.Clipped
	for lane := range report.Threshold {
		report.Threshold[lane] = max(thresholdFloor, thresholdRatio*report.WindowRMS[lane])
	}

	ranked := make([]Candidate, 0, w.End-w.Start-1)
	for frame := w.Start + guardFrames; frame <= w.End-guardFrames; frame++ {
		boundary := frame * rows
		pre := measureSide(w, render, statuses, origin, boundary-rows, boundary, frame-1, frame)
		post := measureSide(w, render, statuses, origin, boundary, boundary+rows, frame, frame+1)
		candidate := Candidate{
			Frame: frame, PictureRow: boundary, Warnings: make([]string, 0),
			Pre: pre, Post: post,
			ListenPre:  FrameSpan{Start: max(w.Start, frame-listenFrames), End: frame},
			ListenPost: FrameSpan{Start: frame, End: min(w.End, frame+listenFrames)},
		}
		for lane := range 2 {
			candidate.Contrast[lane] = post.RMS[lane] / (pre.RMS[lane] + report.Threshold[lane])
			candidate.OnsetRow[lane] = onset(render, statuses, origin, boundary, min(last, boundary+rows), lane, report.Threshold[lane])
		}
		candidate.Score = score(candidate, report.Threshold)
		candidate.Warnings = warnings(candidate, report.Threshold)
		ranked = append(ranked, candidate)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Frame < ranked[j].Frame
	})
	report.Candidates = selectCandidates(ranked)
	sort.Slice(report.Candidates, func(i, j int) bool { return report.Candidates[i].Frame < report.Candidates[j].Frame })
	return report
}

func statusPerRow(runs []register.Run, origin, length int) []string {
	statuses := make([]string, length)
	for _, run := range runs {
		for row := max(origin, run.Start); row < min(origin+length, run.End); row++ {
			statuses[row-origin] = run.Status
		}
	}
	return statuses
}

func measureSide(w register.Window, render readout.Render, statuses []string, origin, startRow, endRow, startFrame, endFrame int) Side {
	side := Side{
		FrameStart: startFrame, FrameEnd: endFrame, RowStart: startRow, RowEnd: endRow,
	}
	stats := laneStats{}
	for row := startRow; row < endRow; row++ {
		offset := row - origin
		if statuses[offset] == register.Readable {
			side.ReadableRows++
			for lane := range 2 {
				stats.add(lane, render.Samples[offset*2+lane])
			}
		} else if statuses[offset] == register.Missing {
			side.MissingRows++
		} else {
			side.OutsideRows++
		}
	}
	side.PaddedZeroRows = side.MissingRows + side.OutsideRows
	side.SilentReadable = stats.silent
	side.Peak = stats.peak
	side.RMS = stats.rms(side.ReadableRows)
	return side
}

func onset(render readout.Render, statuses []string, origin, startRow, endRow, lane int, threshold float64) int {
	for row := startRow; row < endRow; row++ {
		offset := row - origin
		if statuses[offset] != register.Readable {
			continue
		}
		if math.Abs(float64(render.Samples[offset*2+lane])) >= threshold {
			return row
		}
	}
	return -1
}

func score(c Candidate, threshold [2]float64) float64 {
	var laneScore float64
	for lane := range 2 {
		normalized := c.Contrast[lane] / (1 + c.Contrast[lane])
		postWeight := min(1, c.Post.RMS[lane]/(threshold[lane]*4))
		readableWeight := float64(c.Post.ReadableRows) / float64(c.Post.RowEnd-c.Post.RowStart)
		laneScore += normalized * postWeight * readableWeight
	}
	evidenceRows := (c.Pre.RowEnd - c.Pre.RowStart) + (c.Post.RowEnd - c.Post.RowStart)
	readableFactor := float64(c.Pre.ReadableRows+c.Post.ReadableRows) / float64(evidenceRows)
	return laneScore / 2 * readableFactor
}

func warnings(c Candidate, threshold [2]float64) []string {
	found := make([]string, 0)
	if c.Pre.MissingRows > 0 || c.Pre.OutsideRows > 0 {
		found = append(found, WarnPrePaddedZero)
	}
	if c.Post.MissingRows > 0 || c.Post.OutsideRows > 0 {
		found = append(found, WarnPostPaddedZero)
	}
	if c.Pre.OutsideRows > 0 {
		found = append(found, WarnPreOutside)
	}
	if c.Post.OutsideRows > 0 {
		found = append(found, WarnPostOutside)
	}
	for lane := range 2 {
		if c.Pre.RMS[lane] >= threshold[lane] {
			found = append(found, WarnPreActive)
		}
		if c.OnsetRow[lane] < 0 {
			found = append(found, WarnPostInactive)
		}
	}
	evidenceRows := (c.Pre.RowEnd - c.Pre.RowStart) + (c.Post.RowEnd - c.Post.RowStart)
	if c.Pre.ReadableRows+c.Post.ReadableRows < evidenceRows/2 {
		found = append(found, WarnWeakEvidence)
	}
	return found
}

// selectCandidates keeps the strongest well-separated boundaries, then relaxes
// the spacing so short windows still offer several choices.
func selectCandidates(ranked []Candidate) []Candidate {
	chosen := make([]Candidate, 0, MaxCandidates)
	used := make(map[int]bool)
	for _, candidate := range ranked {
		if len(chosen) >= MaxCandidates {
			break
		}
		separated := true
		for frame := range used {
			if abs(frame-candidate.Frame) < 2 {
				separated = false
				break
			}
		}
		if separated {
			chosen = append(chosen, candidate)
			used[candidate.Frame] = true
		}
	}
	for _, candidate := range ranked {
		if len(chosen) >= MaxCandidates {
			break
		}
		if !used[candidate.Frame] {
			chosen = append(chosen, candidate)
			used[candidate.Frame] = true
		}
	}
	return chosen
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
