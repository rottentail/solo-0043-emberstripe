// Package readout converts calibrated optical area to signed stereo samples.
package readout

import (
	"math"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

type Value struct {
	Status  string            `json:"status"`
	Source  *register.Address `json:"source,omitempty"`
	Gray    [2]float64        `json:"gray"`
	Sample  [2]int16          `json:"sample"`
	Clipped [2]bool           `json:"clipped"`
}

type Render struct {
	Samples  []int16        `json:"-"`
	Coverage []register.Run `json:"coverage"`
	Clipped  [2]int         `json:"clipped"`
}

type Difference struct {
	Compared      int            `json:"compared_frames"`
	Changed       [2]int         `json:"changed_samples"`
	Peak          [2]int         `json:"peak_delta"`
	RMS           [2]float64     `json:"rms_delta"`
	Segments      []Segment      `json:"segments"`
	Contributions []Contribution `json:"contributions"`
}

// Segment is one contiguous stretch of readable picture rows with a common
// deviation character. Gaps in coverage are never crossed, so a segment always
// lies inside a single readable run.
type Segment struct {
	Start     int    `json:"picture_row_start"`
	End       int    `json:"picture_row_end"`
	Frame     int    `json:"frame"`
	Row       int    `json:"row"`
	EndFrame  int    `json:"end_frame"`
	EndRow    int    `json:"end_row"`
	PeakFrame int    `json:"peak_frame"`
	PeakRow   int    `json:"peak_row"`
	Compared  int    `json:"compared_rows"`
	Kind      string `json:"kind"`

	Changed     [2]int     `json:"changed_samples"`
	Peak        [2]int     `json:"peak_delta"`
	RMS         [2]float64 `json:"rms_delta"`
	EnergyShare float64    `json:"energy_share"`
	RowShare    float64    `json:"row_share"`

	sumsq         [2]float64
	peakMagnitude int
}

// Contribution aggregates every segment of one kind. EnergyShare is the share
// of the summed squared sample deltas, which is exactly what the window RMS is
// built from; RowShare is the share of compared rows. Together they show whether
// a kind matters through brief high amplitude or through long duration.
type Contribution struct {
	Kind     string `json:"kind"`
	Segments int    `json:"segments"`

	Compared    int        `json:"compared_rows"`
	Changed     [2]int     `json:"changed_samples"`
	Peak        [2]int     `json:"peak_delta"`
	RMS         [2]float64 `json:"rms_delta"`
	EnergyShare float64    `json:"energy_share"`
	RowShare    float64    `json:"row_share"`

	sumsq [2]float64
}

const (
	// SegmentBurst marks a short stretch whose peak clearly exceeds the window level.
	SegmentBurst = "burst"
	// SegmentModerate marks a stretch whose deviation is sustained near the window's reference level.
	SegmentModerate = "moderate"
	// SegmentBackground marks readable rows below the window level.
	SegmentBackground = "background"

	envelopeFrameDiv    = 16
	quietSpanFrameDiv   = 32
	quietRatio          = 0.95
	moderateRatio       = 0.9
	burstFloorRatio     = 1.25
	nestedCrestRatio    = 1.6
	burstLengthFrameDiv = 8
	maxBurstsPerRun     = 24
	maxSegments         = 96
)

func Trace(reel *capture.Reel, g gauge.Gauge, row int) Value {
	a, status := register.Locate(reel, row)
	value := Value{Status: status}
	if status != register.Outside {
		value.Source = &a
	}
	if status != register.Readable {
		return value
	}
	value.Gray, value.Sample, value.Clipped = measure(reel, a, g)
	return value
}

func measure(reel *capture.Reel, a register.Address, g gauge.Gauge) (gray [2]float64, pcm [2]int16, clipped [2]bool) {
	sheet := reel.Sheets[a.Frame]
	for lane, columns := range reel.Lanes {
		gray[lane] = gauge.Mean(sheet, columns, capture.Span{X0: a.Row, X1: a.Row + 1})
		ref := g.Lanes[lane]
		unit := (gray[lane] - ref.Dark) / (ref.Clear - ref.Dark)
		clipped[lane] = unit < 0 || unit > 1
		pcm[lane] = int16(math.Round((2*min(1, max(0, unit)) - 1) * 32767))
	}
	return
}

func Extract(reel *capture.Reel, g gauge.Gauge, w register.Window) Render {
	result := Render{Samples: make([]int16, 2*(w.End-w.Start)*reel.Rows), Coverage: register.Coverage(reel, w)}
	origin := w.Start * reel.Rows
	for _, run := range result.Coverage {
		if run.Status != register.Readable {
			continue
		}
		for row := run.Start; row < run.End; row++ {
			a, _ := register.Locate(reel, row)
			_, sample, clipped := measure(reel, a, g)
			offset := (row - origin) * 2
			copy(result.Samples[offset:offset+2], sample[:])
			for lane, over := range clipped {
				if over {
					result.Clipped[lane]++
				}
			}
		}
	}
	return result
}

// Compare measures the same readable window under two calibrations. Besides
// the window totals it partitions the compared rows into contiguous segments so
// the largest contributors can be located. Segments never cross a coverage gap.
func Compare(a, b Render, origin, rowsPerFrame int) Difference {
	rowsPerFrame = max(rowsPerFrame, 1)
	rows := min(len(a.Samples), len(b.Samples)) / 2
	grid := deltaGrid{delta: make([]int, 2*rows), origin: origin}
	for i := range grid.delta {
		grid.delta[i] = int(a.Samples[i]) - int(b.Samples[i])
	}
	var d Difference
	var total [2]float64
	magnitudes := make([]int, 0, rows)
	for _, run := range a.Coverage {
		if run.Status != register.Readable {
			continue
		}
		for row := run.Start; row < run.End; row++ {
			if !grid.covers(row) {
				continue
			}
			d.Compared++
			magnitudes = append(magnitudes, grid.magnitude(row))
			for lane := 0; lane < 2; lane++ {
				step := grid.at(row, lane)
				if step != 0 {
					d.Changed[lane]++
				}
				d.Peak[lane] = max(d.Peak[lane], absInt(step))
				total[lane] += float64(step) * float64(step)
			}
		}
	}
	if d.Compared != 0 {
		for lane := range d.RMS {
			d.RMS[lane] = math.Sqrt(total[lane] / float64(d.Compared))
		}
	}
	d.Segments = compareSegments(grid, a.Coverage, referenceMagnitude(magnitudes), rowsPerFrame)
	finalizeSegments(d.Segments, d.Compared, total)
	d.Contributions = contributions(d.Segments, d.Compared)
	return d
}

// deltaGrid holds the per-sample difference once so segmentation does not
// re-read the PCM arrays while walking rows several times.
type deltaGrid struct {
	delta  []int
	origin int
}

func (g deltaGrid) covers(row int) bool {
	index := 2 * (row - g.origin)
	return index >= 0 && index+1 < len(g.delta)
}

func (g deltaGrid) at(row, lane int) int {
	return g.delta[2*(row-g.origin)+lane]
}

func (g deltaGrid) magnitude(row int) int {
	return max(absInt(g.at(row, 0)), absInt(g.at(row, 1)))
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// referenceMagnitude is the median row deviation. Using the median keeps one
// loud transient from raising the level that the rest of the window is
// compared against, which is what separates a brief crest from a long drift.
func referenceMagnitude(magnitudes []int) float64 {
	if len(magnitudes) == 0 {
		return 0
	}
	sorted := append([]int(nil), magnitudes...)
	sort.Ints(sorted)
	if reference := float64(sorted[len(sorted)/2]); reference > 0 {
		return reference
	}
	sum := 0
	for _, value := range sorted {
		sum += value
	}
	return float64(sum) / float64(len(sorted))
}

type crestSpan struct{ start, end, peak int }

const (
	labelBackground uint8 = iota
	labelModerate
	labelBurst
)

// compareSegments partitions every readable row of the window. A coverage gap
// is always a hard boundary. Inside each readable run the rows are split at
// quiet valleys of the smoothed deviation envelope; short high crests inside an
// episode are then lifted out as bursts. The rest of an episode is moderate when
// its own RMS stays near the window's median row deviation, background otherwise.
func compareSegments(grid deltaGrid, coverage []register.Run, reference float64, rowsPerFrame int) []Segment {
	segments := make([]Segment, 0)
	for _, run := range coverage {
		if run.Status != register.Readable || run.End <= run.Start {
			continue
		}
		magnitudes := make([]int, run.End-run.Start)
		for i := range magnitudes {
			if grid.covers(run.Start + i) {
				magnitudes[i] = grid.magnitude(run.Start + i)
			}
		}
		episodes := episodeBounds(magnitudes, reference, rowsPerFrame)
		for _, episode := range episodes {
			addEpisode(&segments, grid, rowsPerFrame, run.Start, episode, magnitudes, reference)
		}
	}
	return limitSegments(mergeAdjacent(segments))
}

// episodeBounds returns half-open [start, end) offsets inside one readable run.
// Boundaries fall in the middle of quiet stretches of the smoothed envelope,
// which keeps a long elevation from being cut into frame-sized shards.
func episodeBounds(magnitudes []int, reference float64, rowsPerFrame int) [][2]int {
	n := len(magnitudes)
	if n == 0 {
		return nil
	}
	width := max(1, rowsPerFrame/envelopeFrameDiv)
	minQuiet := max(1, rowsPerFrame/quietSpanFrameDiv)
	prefix := make([]int, n+1)
	for i, value := range magnitudes {
		prefix[i+1] = prefix[i] + value
	}
	envelope := func(index int) float64 {
		lo, hi := max(0, index-width/2), min(n, index+width/2+1)
		return float64(prefix[hi]-prefix[lo]) / float64(hi-lo)
	}
	quiet := func(index int) bool {
		return reference <= 0 || envelope(index) < quietRatio*reference
	}
	bounds := [][2]int{{0, n}}
	if reference <= 0 {
		return bounds
	}
	// Collect quiet stretches first, then cut the run between them.
	cuts := []int{0}
	for start := 0; start < n; {
		if !quiet(start) {
			start++
			continue
		}
		end := start
		for end < n && quiet(end) {
			end++
		}
		if end-start >= minQuiet && start > 0 && end < n {
			cuts = append(cuts, start, end)
		}
		start = end
	}
	cuts = append(cuts, n)
	sort.Ints(cuts)
	episodes := make([][2]int, 0, len(cuts)-1)
	for i := 0; i+1 < len(cuts); i++ {
		if cuts[i] < cuts[i+1] {
			episodes = append(episodes, [2]int{cuts[i], cuts[i+1]})
		}
	}
	return episodes
}

// addEpisode splits the short crests out of one episode and classifies the
// remainder, appending the resulting segments in row order.
func addEpisode(segments *[]Segment, grid deltaGrid, rowsPerFrame, offset int, episode [2]int, magnitudes []int, reference float64) {
	crests := burstSpans(magnitudes[episode[0]:episode[1]], reference, rowsPerFrame)
	bounds := []int{episode[0], episode[1]}
	for _, crest := range crests {
		bounds = append(bounds, episode[0]+crest[0], episode[0]+crest[1])
	}
	sort.Ints(bounds)
	// The whole episode RMS decides moderate versus background, so a short
	// crest does not flip the label of the sustained stretch around it.
	episodeRMS := rmsOf(magnitudes[episode[0]:episode[1]])
	for i := 0; i+1 < len(bounds); i++ {
		start, end := bounds[i], bounds[i+1]
		if end <= start {
			continue
		}
		label := labelBackground
		switch {
		case isCrest(bounds, i, episode[0], crests):
			label = labelBurst
		case reference > 0 && episodeRMS >= moderateRatio*reference:
			label = labelModerate
		}
		*segments = append(*segments, buildSegment(grid, rowsPerFrame, offset+start, offset+end, label))
	}
}

func isCrest(bounds []int, index, base int, crests [][2]int) bool {
	for _, crest := range crests {
		if bounds[index] == base+crest[0] && bounds[index+1] == base+crest[1] {
			return true
		}
	}
	return false
}

func rmsOf(magnitudes []int) float64 {
	if len(magnitudes) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range magnitudes {
		sum += float64(value) * float64(value)
	}
	return math.Sqrt(sum / float64(len(magnitudes)))
}

// burstSpans finds short runs whose per-row deviation clears a crest well above
// the window's robust reference. A run longer than a set fraction of a frame is
// a sustained elevation, not a transient, so it stays part of the episode; a
// short crest nested inside such an elevation is split out when it stands well
// above the elevation's own median.
func burstSpans(magnitudes []int, reference float64, rowsPerFrame int) [][2]int {
	if reference <= 0 {
		return nil
	}
	maxLength := max(1, rowsPerFrame/burstLengthFrameDiv)
	crest := burstFloorRatio * reference
	found := make([]crestSpan, 0)
	for start := 0; start < len(magnitudes); start++ {
		if float64(magnitudes[start]) < crest {
			continue
		}
		end, peak := start, 0
		for end < len(magnitudes) && float64(magnitudes[end]) >= crest {
			peak = max(peak, magnitudes[end])
			end++
		}
		if end-start <= maxLength {
			found = append(found, crestSpan{start, end, peak})
		} else {
			for _, nested := range nestedCrests(magnitudes[start:end], maxLength) {
				nested.start += start
				nested.end += start
				found = append(found, nested)
			}
		}
		start = end - 1
	}
	if len(found) > maxBurstsPerRun {
		sort.Slice(found, func(i, j int) bool {
			if found[i].peak != found[j].peak {
				return found[i].peak > found[j].peak
			}
			return found[i].start < found[j].start
		})
		found = found[:maxBurstsPerRun]
		sort.Slice(found, func(i, j int) bool { return found[i].start < found[j].start })
	}
	spans := make([][2]int, len(found))
	for i, entry := range found {
		spans[i] = [2]int{entry.start, entry.end}
	}
	return spans
}

// nestedCrests looks inside one sustained stretch for short, clearly stronger
// peaks. Offsets are relative to the stretch, so the caller can add its base.
func nestedCrests(magnitudes []int, maxLength int) []crestSpan {
	median := medianMagnitude(magnitudes)
	if median <= 0 {
		return nil
	}
	crest := nestedCrestRatio * median
	found := make([]crestSpan, 0)
	for start := 0; start < len(magnitudes); start++ {
		if float64(magnitudes[start]) < crest {
			continue
		}
		end, peak := start, 0
		for end < len(magnitudes) && float64(magnitudes[end]) >= crest {
			peak = max(peak, magnitudes[end])
			end++
		}
		if end-start <= maxLength {
			found = append(found, crestSpan{start, end, peak})
		}
		start = end - 1
	}
	return found
}

func medianMagnitude(magnitudes []int) float64 {
	if len(magnitudes) == 0 {
		return 0
	}
	sorted := append([]int(nil), magnitudes...)
	sort.Ints(sorted)
	return float64(sorted[len(sorted)/2])
}

func buildSegment(grid deltaGrid, rowsPerFrame, start, end int, label uint8) Segment {
	segment := Segment{Start: start, End: end, Compared: end - start, Kind: kindName(label), peakMagnitude: -1}
	segment.Frame, segment.Row = address(start, rowsPerFrame)
	segment.EndFrame, segment.EndRow = address(end, rowsPerFrame)
	for row := start; row < end; row++ {
		magnitude := 0
		for lane := 0; lane < 2; lane++ {
			step := grid.at(row, lane)
			if step != 0 {
				segment.Changed[lane]++
			}
			segment.Peak[lane] = max(segment.Peak[lane], absInt(step))
			segment.sumsq[lane] += float64(step) * float64(step)
			magnitude = max(magnitude, absInt(step))
		}
		if magnitude > segment.peakMagnitude {
			segment.peakMagnitude = magnitude
			segment.PeakFrame, segment.PeakRow = address(row, rowsPerFrame)
		}
	}
	for lane := range segment.RMS {
		segment.RMS[lane] = math.Sqrt(segment.sumsq[lane] / float64(segment.Compared))
	}
	return segment
}

func kindName(label uint8) string {
	switch label {
	case labelBurst:
		return SegmentBurst
	case labelModerate:
		return SegmentModerate
	default:
		return SegmentBackground
	}
}

// mergeAdjacent joins adjacent segments of the same kind. Segments from
// different readable runs can never meet because a gap always separates them.
func mergeAdjacent(segments []Segment) []Segment {
	merged := make([]Segment, 0, len(segments))
	for _, segment := range segments {
		if n := len(merged); n > 0 && merged[n-1].Kind == segment.Kind && merged[n-1].End == segment.Start {
			merged[n-1].absorb(segment)
			continue
		}
		merged = append(merged, segment)
	}
	return merged
}

func (s *Segment) absorb(other Segment) {
	s.End = other.End
	s.EndFrame, s.EndRow = other.EndFrame, other.EndRow
	s.Compared += other.Compared
	for lane := 0; lane < 2; lane++ {
		s.Changed[lane] += other.Changed[lane]
		s.Peak[lane] = max(s.Peak[lane], other.Peak[lane])
		s.sumsq[lane] += other.sumsq[lane]
		s.RMS[lane] = math.Sqrt(s.sumsq[lane] / float64(s.Compared))
	}
	if other.peakMagnitude > s.peakMagnitude {
		s.peakMagnitude = other.peakMagnitude
		s.PeakFrame, s.PeakRow = other.PeakFrame, other.PeakRow
	}
}

// limitSegments bounds the response size by combining neighbouring segments.
// Combining happens only inside one readable run, so gaps stay separate. The
// grouping is widened until the response fits, unless a window has more
// separate readable runs than the limit itself.
func limitSegments(segments []Segment) []Segment {
	if len(segments) <= maxSegments {
		return segments
	}
	for groupSize := (len(segments) + maxSegments - 1) / maxSegments; ; groupSize++ {
		limited := make([]Segment, 0, maxSegments)
		for i := 0; i < len(segments); {
			group := []Segment{segments[i]}
			j := i + 1
			for len(group) < groupSize && j < len(segments) && segments[j].Start == segments[j-1].End {
				group = append(group, segments[j])
				j++
			}
			limited = append(limited, combineSegments(group))
			i = j
		}
		if len(limited) <= maxSegments || groupSize >= len(segments) {
			return limited
		}
	}
}

func combineSegments(group []Segment) Segment {
	combined := group[0]
	for _, segment := range group[1:] {
		combined.absorb(segment)
	}
	best, peak := -1.0, -1
	for _, segment := range group {
		if energy := segment.sumsq[0] + segment.sumsq[1]; energy > best {
			best, combined.Kind = energy, segment.Kind
		}
		if segment.peakMagnitude > peak {
			peak = segment.peakMagnitude
			combined.peakMagnitude = segment.peakMagnitude
			combined.PeakFrame, combined.PeakRow = segment.PeakFrame, segment.PeakRow
		}
	}
	return combined
}

func finalizeSegments(segments []Segment, compared int, total [2]float64) {
	for i := range segments {
		segment := &segments[i]
		if total[0]+total[1] > 0 {
			segment.EnergyShare = (segment.sumsq[0] + segment.sumsq[1]) / (total[0] + total[1])
		}
		if compared > 0 {
			segment.RowShare = float64(segment.Compared) / float64(compared)
		}
	}
}

// contributions sums the segments of each kind so callers can compare a brief
// high-amplitude burst with a long moderate stretch by energy and by duration.
func contributions(segments []Segment, compared int) []Contribution {
	kinds := []string{SegmentBurst, SegmentModerate, SegmentBackground}
	byKind := make(map[string]*Contribution, len(kinds))
	for _, kind := range kinds {
		byKind[kind] = &Contribution{Kind: kind}
	}
	for _, segment := range segments {
		entry := byKind[segment.Kind]
		entry.Segments++
		entry.Compared += segment.Compared
		for lane := 0; lane < 2; lane++ {
			entry.Changed[lane] += segment.Changed[lane]
			entry.Peak[lane] = max(entry.Peak[lane], segment.Peak[lane])
			entry.sumsq[lane] += segment.sumsq[lane]
		}
		entry.EnergyShare += segment.EnergyShare
	}
	result := make([]Contribution, 0, len(kinds))
	for _, kind := range kinds {
		entry := byKind[kind]
		if entry.Segments == 0 {
			continue
		}
		for lane := range entry.RMS {
			entry.RMS[lane] = math.Sqrt(entry.sumsq[lane] / float64(entry.Compared))
		}
		if compared > 0 {
			entry.RowShare = float64(entry.Compared) / float64(compared)
		}
		result = append(result, *entry)
	}
	return result
}

func address(pictureRow, rowsPerFrame int) (int, int) {
	return pictureRow / rowsPerFrame, pictureRow % rowsPerFrame
}
