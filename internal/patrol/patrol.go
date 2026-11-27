// Package patrol evaluates marked reference segments to judge whether both
// optical lanes still resolve weak changes after a scan.
//
// A patrol declaration names frame ranges the workstation already trusts:
// "static" backgrounds should stay level, "modulated" ranges are known to
// carry a varying signal. The run turns those expectations into per-lane
// verdicts, anchors every failed rule to a clickable picture-row region, and
// separates evidence that still supports listening from data that only tracks
// a trend.
package patrol

import (
	"errors"
	"math"
	"sort"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Kind values describe what the workstation already knows about a segment.
const (
	KindStatic    = "static"
	KindModulated = "modulated"
)

// Verdict values communicate how far a segment can still be trusted.
const (
	// VerdictHealthy means every expectation for the segment held on both lanes.
	VerdictHealthy = "healthy"
	// VerdictTrend means the lane is not silent but can no longer resolve weak
	// changes; it is only suitable for tracking the direction of a trend.
	VerdictTrend = "trend_only"
	// VerdictFailed means at least one rule the segment was marked to prove is
	// violated, so conclusions about faint modulation must not be drawn from it.
	VerdictFailed = "failed"
)

// Rule identifiers name the judgement that did not hold.
const (
	RuleShortContent = "short_content"
	RuleBackground   = "background_step"
	RulePlateau      = "lane_plateau"
	RuleClip         = "clipped_platform"
)

// Severity separates a broken rule from data that is still trend-usable.
const (
	SeverityFail  = "fail"
	SeverityTrend = "trend"
)

// Finding is one rule that did not hold with the region to re-open.
type Finding struct {
	Rule     string          `json:"rule"`
	Severity string          `json:"severity"`
	Lane     int             `json:"lane"`
	Frame    int             `json:"frame"`
	Row      int             `json:"row"`
	Window   register.Window `json:"review_window"`
	Readable int             `json:"readable_rows"`
	Detail   string          `json:"detail"`
}

// Thresholds are expressed in normalized 16-bit PCM steps so the same numbers
// apply to both lanes regardless of their calibrated endpoints.
const (
	// MinReadableRows rejects windows that cannot describe a stable baseline.
	MinReadableRows = 64
	// StepFloor is the smallest adjacent sample jump counted as a level step.
	StepFloor = 128
	// DriftFloor is the median shift inside a static lane that proves a step.
	DriftFloor = 256
	// WeakLevels marks a varying lane whose distinct levels no longer reveal
	// fine modulation but still move enough to follow a coarse trend.
	WeakLevels = 6
	// StrongLevels is the minimum distinct levels expected of a healthy signal.
	StrongLevels = 8
	// ClipRun marks a sustained platform pinned against a calibration endpoint.
	ClipRun = 8
)

// Segment declares a trusted frame range inside the capture.
type Segment struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Start int    `json:"start_frame"`
	End   int    `json:"end_frame"`
}

// LaneVerdict carries the measurements behind one lane's conclusion.
type LaneVerdict struct {
	Verdict       string    `json:"verdict"`
	ReadableRows  int       `json:"readable_rows"`
	DistinctLevel int       `json:"distinct_levels"`
	ChangedRatio  float64   `json:"changed_ratio"`
	Median        int16     `json:"median_sample"`
	MedianDrift   int       `json:"median_drift"`
	MaxStep       int       `json:"max_adjacent_step"`
	ClippedRows   int       `json:"clipped_rows"`
	Findings      []Finding `json:"findings"`
}

// SegmentResult is the per-segment inspection outcome on the picture clock.
type SegmentResult struct {
	Segment Segment        `json:"segment"`
	Verdict string         `json:"verdict"`
	Lanes   [2]LaneVerdict `json:"lanes"`
}

// Report is a complete patrol run for one capture and calibration.
type Report struct {
	Capture  string          `json:"capture"`
	Profile  string          `json:"profile"`
	Verdict  string          `json:"verdict"`
	Segments []SegmentResult `json:"segments"`
}

// Measure converts one readable source address into normalized PCM samples.
type Measure func(a register.Address) (samples [2]int16, clipped [2]bool)

// Validate checks the patrol manifest against the capture geometry. Segments
// are frame windows, half-open, ordered, non-overlapping and inside the reel.
func Validate(h capture.Header, segments []Segment) error {
	if len(segments) == 0 {
		return errors.New("patrol requires at least one marked segment")
	}
	if len(segments) > 32 {
		return errors.New("patrol accepts at most 32 marked segments")
	}
	seen := make(map[string]bool, len(segments))
	previous := -1
	for _, segment := range segments {
		if !captureName(segment.ID) || seen[segment.ID] {
			return errors.New("invalid or duplicate patrol segment id")
		}
		seen[segment.ID] = true
		if segment.Kind != KindStatic && segment.Kind != KindModulated {
			return errors.New("patrol segment kind must be static or modulated")
		}
		if segment.Start < 0 || segment.End <= segment.Start || segment.End > h.Frames || segment.Start < previous {
			return errors.New("patrol segment frames must be ordered, half-open and inside the capture")
		}
		previous = segment.End
	}
	return nil
}

func captureName(id string) bool {
	if id == "" || len(id) > 48 || !(id[0] >= 'a' && id[0] <= 'z') {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// Run evaluates every marked segment. Readable rows are walked once; missing
// sheets and rows outside the capture keep their place in the clock but never
// contribute synthetic zero values.
func Run(reel *capture.Reel, g gauge.Gauge, segments []Segment, measure Measure) (Report, error) {
	if err := Validate(reel.Header, segments); err != nil {
		return Report{}, err
	}
	report := Report{Capture: reel.Name, Profile: g.Profile, Verdict: VerdictHealthy}
	for _, segment := range segments {
		result := SegmentResult{Segment: segment, Verdict: VerdictHealthy}
		first := segment.Start * reel.Rows
		last := segment.End * reel.Rows
		series := [2][]int16{{}, {}}
		anchors := [2][]int{{}, {}}
		clipped := [2][]bool{{}, {}}
		for row := first; row < last; {
			address, status := register.Locate(reel, row)
			end := last
			if status != register.Outside {
				end = min(last, row+reel.Rows-address.Row)
			}
			if status == register.Readable {
				for pictureRow := row; pictureRow < end; pictureRow++ {
					source, _ := register.Locate(reel, pictureRow)
					sample, clip := measure(source)
					for lane := 0; lane < 2; lane++ {
						series[lane] = append(series[lane], sample[lane])
						anchors[lane] = append(anchors[lane], pictureRow)
						clipped[lane] = append(clipped[lane], clip[lane])
					}
				}
			}
			row = end
		}
		for lane := 0; lane < 2; lane++ {
			result.Lanes[lane] = judge(segment, lane, reel.Rows, series[lane], anchors[lane], clipped[lane])
		}
		result.Verdict = combine(result.Lanes)
		if rank(result.Verdict) > rank(report.Verdict) {
			report.Verdict = result.Verdict
		}
		report.Segments = append(report.Segments, result)
	}
	return report, nil
}

func judge(segment Segment, lane, rows int, samples []int16, anchors []int, clipped []bool) LaneVerdict {
	verdict := LaneVerdict{Verdict: VerdictHealthy, ReadableRows: len(samples), Findings: []Finding{}}
	if len(samples) == 0 {
		verdict.Verdict = VerdictFailed
		verdict.Findings = append(verdict.Findings, Finding{
			Rule: RuleShortContent, Severity: SeverityFail, Lane: lane, Frame: segment.Start, Row: 0,
			Window: register.Window{Start: segment.Start, End: segment.End},
			Detail: "标记区间内没有可读声槽行",
		})
		return verdict
	}
	verdict.Median, verdict.MedianDrift = medianStats(samples)
	verdict.DistinctLevel = distinctLevels(samples)
	verdict.ChangedRatio, verdict.MaxStep = changeStats(samples)
	verdict.ClippedRows = clipCount(clipped)
	anchorFrame, anchorRow := anchors[0]/rows, anchors[0]%rows
	hasFail, hasTrend := false, false
	add := func(rule, severity string, frame, row int, detail string) {
		verdict.Findings = append(verdict.Findings, Finding{
			Rule: rule, Severity: severity, Lane: lane, Frame: frame, Row: row,
			Window: reviewWindow(segment, frame), Readable: len(samples), Detail: detail,
		})
		if severity == SeverityTrend {
			hasTrend = true
		} else {
			hasFail = true
		}
	}
	if len(samples) < MinReadableRows {
		add(RuleShortContent, SeverityFail, anchorFrame, anchorRow, "有效内容过短，无法建立稳定基线")
	}
	switch segment.Kind {
	case KindStatic:
		// A trusted background must stay on one level. A sustained median shift
		// or a real adjacent step proves the background itself carries a stair.
		if stepRow, step := firstStep(samples, anchors, rows); step >= StepFloor {
			add(RuleBackground, SeverityFail, stepRow/rows, stepRow%rows, "静态背景出现相邻台阶，跳变 "+strconv.Itoa(step)+" 个 PCM 步长")
		} else if verdict.MedianDrift >= DriftFloor {
			add(RuleBackground, SeverityFail, anchorFrame, anchorRow, "静态背景中值漂移 "+strconv.Itoa(verdict.MedianDrift)+" 个 PCM 步长")
		}
		if verdict.DistinctLevel > 1 && verdict.DistinctLevel <= WeakLevels {
			add(RulePlateau, SeverityFail, anchorFrame, anchorRow, "声槽只在少数平台间切换，微弱变化被平台掩盖")
		}
	case KindModulated:
		if clipRow, length := firstClipRun(clipped, anchors, rows); length >= ClipRun {
			add(RuleClip, SeverityFail, clipRow/rows, clipRow%rows, "连续 "+strconv.Itoa(length)+" 行被削到标定端点形成平台")
		}
		if verdict.DistinctLevel < StrongLevels {
			// Some movement survives, but the lane cannot resolve fine changes:
			// it can still follow the direction of a trend, not faint detail.
			severity := SeverityTrend
			if verdict.ChangedRatio == 0 {
				severity = SeverityFail
			}
			add(RulePlateau, severity, anchorFrame, anchorRow, "有效调制只呈现 "+strconv.Itoa(verdict.DistinctLevel)+" 个电平，微弱变化不可辨")
		}
	}
	switch {
	case hasFail:
		verdict.Verdict = VerdictFailed
	case hasTrend:
		verdict.Verdict = VerdictTrend
	default:
		verdict.Verdict = VerdictHealthy
	}
	return verdict
}

func combine(lanes [2]LaneVerdict) string {
	worst := VerdictHealthy
	for _, lane := range lanes {
		if rank(lane.Verdict) > rank(worst) {
			worst = lane.Verdict
		}
	}
	return worst
}

func rank(verdict string) int {
	switch verdict {
	case VerdictHealthy:
		return 0
	case VerdictTrend:
		return 1
	default:
		return 2
	}
}

// reviewWindow keeps the click target bounded by the station's frame limits.
func reviewWindow(segment Segment, frame int) register.Window {
	start := max(segment.Start, frame-1)
	end := min(segment.End, frame+2)
	if end <= start {
		start, end = segment.Start, segment.End
	}
	return register.Window{Start: start, End: end}
}

func medianStats(samples []int16) (median int16, drift int) {
	ordered := append([]int16(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	median = ordered[len(ordered)/2]
	mid := len(samples) / 2
	first := medianOf(samples[:mid])
	second := medianOf(samples[mid:])
	return median, int(math.Abs(float64(int(second) - int(first))))
}

func medianOf(part []int16) int16 {
	if len(part) == 0 {
		return 0
	}
	ordered := append([]int16(nil), part...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered[len(ordered)/2]
}

func distinctLevels(samples []int16) int {
	levels := make(map[int16]struct{}, len(samples))
	for _, sample := range samples {
		levels[sample] = struct{}{}
	}
	return len(levels)
}

func changeStats(samples []int16) (ratio float64, maxStep int) {
	changes := 0
	for i := 1; i < len(samples); i++ {
		step := int(math.Abs(float64(int(samples[i]) - int(samples[i-1]))))
		if step != 0 {
			changes++
		}
		maxStep = max(maxStep, step)
	}
	return float64(changes) / float64(max(1, len(samples)-1)), maxStep
}

func firstStep(samples []int16, anchors []int, rows int) (pictureRow, step int) {
	for i := 1; i < len(samples); i++ {
		jump := int(math.Abs(float64(int(samples[i]) - int(samples[i-1]))))
		if jump >= StepFloor {
			return anchors[i], jump
		}
	}
	return -1, 0
}

func clipCount(clipped []bool) int {
	total := 0
	for _, value := range clipped {
		if value {
			total++
		}
	}
	return total
}

func firstClipRun(clipped []bool, anchors []int, rows int) (pictureRow, length int) {
	for i := 0; i < len(clipped); i++ {
		if !clipped[i] {
			continue
		}
		run := 0
		for i+run < len(clipped) && clipped[i+run] {
			run++
		}
		if run >= ClipRun {
			return anchors[i], run
		}
	}
	return -1, 0
}
