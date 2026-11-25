package periodicity

import "math"

// Verdict values deliberately distinguish a supported shared mechanism from an
// unsupported programme tone and from material that cannot be decided yet.
const (
	VerdictSupported    = "shared_period_supported"
	VerdictDismissed    = "programme_only_not_supported"
	VerdictInconclusive = "inconclusive"
)

// minimumCycles is the lowest repetition count accepted as evidence. Sparse
// front and tail windows often contain fewer repeats and stay inconclusive.
const minimumCycles = 3.0

// minimumProminence requires a peak to clear the local half-spectrum floor.
const minimumProminence = 2.5

// minimumLaneAnswer is the smallest aligned lane response, relative to that
// lane's noise floor, accepted as an answer to the edge frequency.
const minimumLaneAnswer = 2.0

// Pair compares an edge-band peak with the lane component found at the edge
// frequency. The lane's own strongest programme peak stays visible but never
// decides the pair, and nearby frequencies are only merged inside the combined
// bin resolution.
type Pair struct {
	Lane             SeriesKind `json:"lane"`
	Verdict          string     `json:"verdict"`
	EdgeFrequency    float64    `json:"edge_frequency_hz"`
	LaneFrequency    float64    `json:"lane_frequency_hz"`
	LaneMainToneHz   float64    `json:"lane_dominant_programme_hz"`
	FrequencyGapHz   float64    `json:"frequency_gap_hz"`
	ResolutionLimit  float64    `json:"resolution_limit_hz"`
	EdgeCycles       float64    `json:"edge_cycles"`
	LaneCycles       float64    `json:"lane_aligned_cycles"`
	EdgeModulation   float64    `json:"edge_modulation_depth_gray"`
	LaneAnswerRatio  float64    `json:"lane_answer_to_floor"`
	EdgePeakMissing  bool       `json:"edge_peak_missing"`
	LanePeakMissing  bool       `json:"lane_peak_missing"`
	ShortSegmentRows int        `json:"shortest_segment_rows"`
	Reason           string     `json:"reason"`
	EdgeEvidence     *Peak      `json:"edge_peak,omitempty"`
	LaneEvidence     *Peak      `json:"lane_aligned_peak,omitempty"`
}

func comparePair(edge, lane Spectrum) Pair {
	pair := Pair{Lane: lane.Kind, ShortSegmentRows: min(edge.ShortestRows, lane.ShortestRows), EdgeModulation: edge.ModulationDepth}
	if edge.Peak != nil {
		pair.EdgeFrequency = edge.Peak.FrequencyHz
		pair.EdgeCycles = edge.Peak.CyclesInWindow
		pair.EdgeEvidence = edge.Peak
	} else {
		pair.EdgePeakMissing = true
	}
	if lane.Peak != nil {
		pair.LaneMainToneHz = lane.Peak.FrequencyHz
	}
	if lane.AlignedPeak != nil {
		pair.LaneFrequency = lane.AlignedPeak.FrequencyHz
		pair.LaneCycles = lane.AlignedPeak.CyclesInWindow
		pair.LaneEvidence = lane.AlignedPeak
		if lane.NoiseFloor > 0 {
			pair.LaneAnswerRatio = lane.AlignedPeak.Amplitude / lane.NoiseFloor
		}
	} else {
		pair.LanePeakMissing = true
	}
	switch {
	case pair.EdgePeakMissing || pair.LanePeakMissing:
		pair.Verdict = VerdictInconclusive
		pair.Reason = "一侧没有可测的周期峰，缺少对照证据。"
		return pair
	case edge.ModulationDepth < minimumModulation:
		pair.Verdict = VerdictInconclusive
		pair.Reason = "带边调制深度低于可采信灰阶门槛，证据不足以支持同一成因。"
		return pair
	case edge.PeakProminence < minimumProminence:
		pair.Verdict = VerdictInconclusive
		pair.Reason = "带边周期峰未明显高于本底，证据不足以支持同一成因。"
		return pair
	case lane.AlignedPeak.Amplitude == 0 || pair.LaneAnswerRatio < minimumLaneAnswer:
		pair.Verdict = VerdictDismissed
		pair.Reason = "声槽在带边频率邻域没有高出本底的应答峰，带边周期纹没有进入该声槽。"
		return pair
	case edge.Peak.CyclesInWindow < minimumCycles || pair.LaneCycles < minimumCycles:
		pair.Verdict = VerdictInconclusive
		pair.Reason = "可读材料内重复次数不足三次，前后窗样本太少。"
		return pair
	}
	pair.FrequencyGapHz = math.Abs(edge.Peak.FrequencyHz - lane.AlignedPeak.FrequencyHz)
	// Two tones can only be separated when the gap exceeds the coarser of the
	// two single-bin resolutions. Equal or neighboring bins are physically
	// unresolvable and stay inconclusive instead of being forced into one
	// cause, so close frequencies are never judged identical by proximity.
	resolutionLimit := math.Max(edge.Peak.BinResolution, lane.AlignedPeak.BinResolution)
	pair.ResolutionLimit = resolutionLimit
	switch {
	case pair.FrequencyGapHz <= resolutionLimit:
		if pair.FrequencyGapHz == 0 {
			pair.Verdict = VerdictSupported
			pair.Reason = "声槽在带边频率处有同频应答，重复次数与峰高均成立，支持同一送片成因。"
			return pair
		}
		pair.Verdict = VerdictInconclusive
		pair.Reason = "两个频率接近到分辨力以内但不完全同频，现有窗口无法判定是否同一成因，保留原卷位置待补扫复听。"
		return pair
	default:
		pair.Verdict = VerdictDismissed
		pair.Reason = "频差超出较粗一侧的频率分辨力，按不同成因处理，不把相近频率直接判为同一来源。"
		return pair
	}
}

// Report is the full verification answer. Every pair keeps the source peaks,
// positions and the reason behind the call, including inconclusive material.
type Report struct {
	Band       captureSpan `json:"reference_band"`
	Rate       int         `json:"sample_rate_hz"`
	Rows       int         `json:"rows_per_frame"`
	Window     windowShape `json:"window"`
	Spectra    []Spectrum  `json:"spectra"`
	Pairs      []Pair      `json:"pairs"`
	Overall    string      `json:"overall_verdict"`
	Readable   int         `json:"readable_rows"`
	Missing    int         `json:"missing_rows"`
	Outside    int         `json:"outside_rows"`
	Unreadable []Block     `json:"unreadable_blocks"`
}

// Block keeps the original reel positions that stop a window from being fully
// judged, so missing middle pages and outside material remain reviewable.
type Block struct {
	StartRow int    `json:"picture_row_start"`
	EndRow   int    `json:"picture_row_end"`
	Frame    int    `json:"frame"`
	Row      int    `json:"row"`
	Status   string `json:"status"`
}

type captureSpan struct {
	X0 int `json:"x0"`
	X1 int `json:"x1"`
}

type windowShape struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func overall(pairs []Pair) string {
	supported, dismissed := false, false
	for _, pair := range pairs {
		switch pair.Verdict {
		case VerdictSupported:
			supported = true
		case VerdictDismissed:
			dismissed = true
		}
	}
	switch {
	case supported && !dismissed:
		return VerdictSupported
	case dismissed && !supported:
		return VerdictDismissed
	default:
		return VerdictInconclusive
	}
}
