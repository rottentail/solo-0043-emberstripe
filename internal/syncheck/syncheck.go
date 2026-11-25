// Package syncheck estimates the row offset between two optical lanes that
// carry the same calibration sequence. It works on the picture-row clock so a
// reported lag is directly convertible to listening time.
//
// The check never treats a single best lag as the whole truth. Repeating
// sequences produce equally good lags one period apart, missing sheets and
// clipped rows remove evidence, and blocky optical steps leave a plateau of
// plausible lags. Callers therefore receive clusters of competitive lags,
// bounded ranges, machine-readable caveats and the concrete rows that support
// the principal answer, so an operator can listen back to each piece of
// evidence instead of trusting an unverifiable number.
package syncheck

import (
	"math"
	"sort"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Confidence levels describe how far the material supports a single lag.
const (
	ConfidenceExact        = "exact"
	ConfidenceBounded      = "bounded"
	ConfidenceAmbiguous    = "ambiguous"
	ConfidenceInconclusive = "inconclusive"
)

// Caveat codes explain why an answer is wider or weaker than a single row.
const (
	CaveatPeriodic   = "periodic_sequence"
	CaveatMissing    = "missing_sheets"
	CaveatClipping   = "lane_clipped"
	CaveatThin       = "thin_evidence"
	CaveatSearchEdge = "search_limit"
)

// Anchor is a picture-row position whose left/right samples support the lag.
type Anchor struct {
	Frame       int    `json:"frame"`
	Row         int    `json:"row"`
	PictureRow  int    `json:"picture_row"`
	Lag         int    `json:"lag"`
	LeftSample  int16  `json:"left_sample"`
	RightSample int16  `json:"right_sample"`
	Edge        int    `json:"edge_weight"`
	Kind        string `json:"kind"`
}

// Cluster groups consecutive lags whose match quality is competitive.
type Cluster struct {
	MinLag      int     `json:"min_lag"`
	MaxLag      int     `json:"max_lag"`
	BestLag     int     `json:"best_lag"`
	Score       float64 `json:"score"`
	EdgeMatches int     `json:"edge_matches"`
	Support     int     `json:"support"`
}

// Caveat pairs a stable code with an operator-facing explanation in Chinese.
type Caveat struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result is the full time-offset verdict for one window.
type Result struct {
	MinLag      int       `json:"min_lag_rows"`
	MaxLag      int       `json:"max_lag_rows"`
	BestLag     int       `json:"best_lag_rows"`
	MinMillis   float64   `json:"min_lag_ms"`
	MaxMillis   float64   `json:"max_lag_ms"`
	BestMillis  float64   `json:"best_lag_ms"`
	Confidence  string    `json:"confidence"`
	SearchRows  int       `json:"search_rows"`
	Readable    int       `json:"readable_rows"`
	Support     int       `json:"support_pairs"`
	SkippedClip int       `json:"clipped_rows_skipped"`
	BestScore   float64   `json:"best_match_score"`
	Period      int       `json:"repetition_period_rows"`
	Clusters    []Cluster `json:"clusters"`
	Evidence    []Anchor  `json:"evidence"`
	Caveats     []Caveat  `json:"caveats"`
}

type lagScore struct {
	lag         int
	score       float64
	edges       int
	support     int
	clippedSkip int
}

// Analyze correlates the two lanes inside an already rendered window. The
// render supplies PCM, coverage and per-row clipping; searchRows bounds how
// many rows in each direction the right lane is allowed to lag.
func Analyze(reel *capture.Reel, render readout.Render, w register.Window, searchRows int) Result {
	origin := w.Start * reel.Rows
	length := (w.End - w.Start) * reel.Rows
	rate := register.Rate(reel.Header)
	readable := make([]bool, length)
	for _, run := range render.Coverage {
		if run.Status != register.Readable {
			continue
		}
		for row := max(origin, run.Start); row < min(origin+length, run.End); row++ {
			readable[row-origin] = true
		}
	}
	clipped := render.ClippedRows
	sampleAt := func(index, lane int) int16 { return render.Samples[index*2+lane] }

	readableCount := 0
	for _, ok := range readable {
		if ok {
			readableCount++
		}
	}
	result := Result{SearchRows: searchRows, Readable: readableCount, Confidence: ConfidenceInconclusive,
		Clusters: []Cluster{}, Evidence: []Anchor{}, Caveats: []Caveat{}}
	ms := func(rows int) float64 { return float64(rows) * 1000 / float64(rate) }
	if readableCount < 2 {
		result.MinLag, result.MaxLag, result.BestLag = 0, 0, 0
		result.MinMillis, result.MaxMillis, result.BestMillis = 0, 0, 0
		result.Caveats = []Caveat{{Code: CaveatThin, Message: "窗口内几乎没有两路同时可读的声槽行，无法判断时差。"}}
		return result
	}

	// Edge rows are transitions in the left lane. Optical check sequences are
	// blocky two-level steps, so the score combines a level mismatch rate over
	// every common row with an edge-hit rate. Plain Pearson correlation would
	// collapse here because the signal stays constant for most of its span.
	type edge struct{ at, weight int }
	edges := make([]edge, 0)
	for i := 1; i < length; i++ {
		if !readable[i] || !readable[i-1] {
			continue
		}
		step := int(sampleAt(i, 0)) - int(sampleAt(i-1, 0))
		if step != 0 {
			edges = append(edges, edge{i, int(math.Abs(float64(step)))})
		}
	}

	scoreLag := func(lag int) lagScore {
		var n, levelMismatch, clipSkip, edgeHits, edgeCompared int
		for i := 0; i < length; i++ {
			j := i + lag
			if j < 0 || j >= length || !readable[i] || !readable[j] {
				continue
			}
			if clipped[i][0] || clipped[j][1] {
				clipSkip++
				continue
			}
			n++
			// A coarse two-level comparison is robust to the lanes' separate
			// calibration endpoints while still catching a shifted step.
			if signLevel(sampleAt(i, 0)) != signLevel(sampleAt(j, 1)) {
				levelMismatch++
			}
		}
		for _, e := range edges {
			j := e.at + lag
			if j < 1 || j >= length || !readable[j] || !readable[j-1] || clipped[e.at][0] || clipped[j][1] {
				continue
			}
			edgeCompared++
			rightStep := math.Abs(float64(int(sampleAt(j, 1)) - int(sampleAt(j-1, 1))))
			if rightStep >= float64(e.weight)/2 {
				edgeHits++
			}
		}
		s := lagScore{lag: lag, support: n, edges: edgeHits, clippedSkip: clipSkip}
		if n > 0 {
			levelScore := 1 - float64(levelMismatch)/float64(n)
			edgeScore := 0.0
			if edgeCompared > 0 {
				edgeScore = float64(edgeHits) / float64(edgeCompared)
			}
			s.score = 0.6*levelScore + 0.4*edgeScore
		}
		return s
	}

	scores := make([]lagScore, 0, 2*searchRows+1)
	for lag := -searchRows; lag <= searchRows; lag++ {
		scores = append(scores, scoreLag(lag))
	}
	best := scores[0]
	for _, s := range scores[1:] {
		if s.score > best.score {
			best = s
		}
	}
	result.BestScore = best.score
	result.Support = best.support
	result.SkippedClip = best.clippedSkip

	if best.support < 30 || best.score < 0.7 {
		result.MinLag, result.MaxLag, result.BestLag = -searchRows, searchRows, best.lag
		result.MinMillis, result.MaxMillis, result.BestMillis = ms(-searchRows), ms(searchRows), ms(best.lag)
		result.Clusters = clusters(scores, best.score)
		result.Caveats = append(result.Caveats, Caveat{Code: CaveatThin, Message: "共同可读且未削顶的样本太少或左右序列相似度不足，时差只能按搜索范围给界。"})
		result.Evidence = collectEvidence(reel, origin, length, readable, clipped, sampleAt, best.lag)
		return result
	}

	groups := clusters(scores, best.score)
	// With several equally good clusters the sequence is periodic; the true
	// transfer lag is physically a small offset, so the principal cluster is
	// the one nearest zero. Edge sharpness only breaks zero-distance ties.
	principal := 0
	for i := range groups {
		c := groups[i]
		p := groups[principal]
		if abs(c.BestLag) < abs(p.BestLag) ||
			(abs(c.BestLag) == abs(p.BestLag) && c.EdgeMatches > p.EdgeMatches) ||
			(abs(c.BestLag) == abs(p.BestLag) && c.EdgeMatches == p.EdgeMatches && c.Score > p.Score) {
			principal = i
		}
	}
	main := groups[principal]
	result.Clusters = groups
	result.Period = detectPeriod(groups)

	switch {
	case len(groups) > 1:
		result.Confidence = ConfidenceAmbiguous
	case main.MaxLag-main.MinLag <= 2 && main.Score >= 0.85:
		result.Confidence = ConfidenceExact
	default:
		result.Confidence = ConfidenceBounded
	}
	result.MinLag, result.MaxLag, result.BestLag = main.MinLag, main.MaxLag, main.BestLag
	result.MinMillis, result.MaxMillis, result.BestMillis = ms(main.MinLag), ms(main.MaxLag), ms(main.BestLag)

	if len(groups) > 1 || result.Period > 0 {
		if result.Period > 0 {
			result.Caveats = append(result.Caveats, Caveat{
				Code:    CaveatPeriodic,
				Message: "校验序列周期性重复，相差整数个周期（约 " + strconv.Itoa(result.Period) + " 行）的位移同样吻合，不能把一个周期的位移当作准确时差；以下只采用离零最近的候选簇。",
			})
		} else {
			result.Caveats = append(result.Caveats, Caveat{
				Code:    CaveatPeriodic,
				Message: "窗口内出现多组同分候选位移，序列重复导致无法唯一确定时差；以下只采用离零最近的候选簇。",
			})
		}
	}

	missingRows := 0
	for _, run := range render.Coverage {
		if run.Status == register.Missing {
			missingRows += min(run.End, origin+length) - max(run.Start, origin)
		}
	}
	if missingRows > 0 {
		result.Caveats = append(result.Caveats, Caveat{
			Code:    CaveatMissing,
			Message: "窗口内有 " + strconv.Itoa(missingRows) + " 行因缺页无法比对，时差边界只由剩余可读段支撑，缺口两侧可能还存在未被观察到的位移。",
		})
	}
	if result.SkippedClip > 0 {
		result.Caveats = append(result.Caveats, Caveat{
			Code:    CaveatClipping,
			Message: "有 " + strconv.Itoa(result.SkippedClip) + " 行至少一侧削顶，已从相关性计算中剔除；削顶段无法区分真实电平，候选范围相应放宽。",
		})
	}
	if abs(main.BestLag) >= searchRows {
		result.Caveats = append(result.Caveats, Caveat{
			Code:    CaveatSearchEdge,
			Message: "最佳候选已到搜索范围边界（±" + strconv.Itoa(searchRows) + " 行），真实时差可能超出本窗可核验的范围。",
		})
	}
	if result.Confidence != ConfidenceExact && !hasCode(result.Caveats, CaveatPeriodic) {
		result.Caveats = append(result.Caveats, Caveat{
			Code:    CaveatThin,
			Message: "证据只能把时差约束在 " + strconv.Itoa(main.MinLag) + " 到 " + strconv.Itoa(main.MaxLag) + " 行之间，范围内各位移的吻合度没有显著差异。",
		})
	}
	result.Evidence = collectEvidence(reel, origin, length, readable, clipped, sampleAt, main.BestLag)
	return result
}

// clusters keeps the plateau of lags that match almost as well as the best one
// and groups adjacent lags. In a clean repeating window every period-shifted
// lag stays on the plateau, so the result is several clusters one period
// apart; a distinctive marker breaks the repetition and pushes those lags off
// the plateau, leaving a single cluster. Blocky steps merge adjacent lags into
// one bounded cluster instead of a falsely precise row.
func clusters(scores []lagScore, bestScore float64) []Cluster {
	// The plateau margin is set against measured optical behavior: edge losses
	// from finite window ends cost a periodic candidate about 0.09, while a
	// genuinely broken repetition (a distinctive marker) costs more.
	threshold := max(bestScore-0.09, 0.55)
	type point struct {
		lag int
		s   lagScore
	}
	competitive := make([]point, 0)
	for _, s := range scores {
		if s.score >= threshold {
			competitive = append(competitive, point{s.lag, s})
		}
	}
	sort.Slice(competitive, func(i, j int) bool { return competitive[i].lag < competitive[j].lag })
	out := make([]Cluster, 0)
	for _, p := range competitive {
		if n := len(out); n > 0 && p.lag == out[n-1].MaxLag+1 {
			c := &out[n-1]
			c.MaxLag = p.lag
			c.Support = max(c.Support, p.s.support)
			if p.s.edges > c.EdgeMatches || (p.s.edges == c.EdgeMatches && p.s.score > c.Score) {
				c.EdgeMatches, c.BestLag, c.Score = p.s.edges, p.lag, p.s.score
			}
			continue
		}
		out = append(out, Cluster{
			MinLag: p.lag, MaxLag: p.lag, BestLag: p.lag,
			Score: p.s.score, EdgeMatches: p.s.edges, Support: p.s.support,
		})
	}
	return out
}

// detectPeriod looks for a repeated spacing between clusters that is larger
// than the width of a single cluster; that spacing is the sequence period.
// When every cluster is itself a wide plateau the spacing is not a clean
// repetition, so no period is claimed.
func detectPeriod(groups []Cluster) int {
	if len(groups) < 2 {
		return 0
	}
	centers := make([]int, 0, len(groups))
	for _, c := range groups {
		centers = append(centers, c.BestLag)
	}
	sort.Ints(centers)
	gaps := make([]int, 0)
	for i := 1; i < len(centers); i++ {
		if d := centers[i] - centers[i-1]; d > 2 {
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Ints(gaps)
	median := gaps[len(gaps)/2]
	for _, c := range groups {
		if c.MaxLag-c.MinLag >= median/2 {
			return 0
		}
	}
	return median
}

func collectEvidence(reel *capture.Reel, origin, length int, readable []bool, clipped [][2]bool,
	sampleAt func(index, lane int) int16, lag int) []Anchor {
	type candidate struct {
		at, weight int
		kind       string
	}
	candidates := make([]candidate, 0)
	for i := 1; i < length; i++ {
		if !readable[i] || !readable[i-1] {
			continue
		}
		step := math.Abs(float64(int(sampleAt(i, 0)) - int(sampleAt(i-1, 0))))
		if step > 0 {
			candidates = append(candidates, candidate{i, int(step), "电平跳变"})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].weight > candidates[j].weight })
	anchors := make([]Anchor, 0, 4)
	seen := make(map[int]bool)
	for _, c := range candidates {
		j := c.at + lag
		if j < 0 || j >= length || !readable[j] || clipped[c.at][0] || clipped[j][1] {
			continue
		}
		absolute := origin + c.at
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		anchors = append(anchors, Anchor{
			Frame: absolute / reel.Rows, Row: absolute % reel.Rows, PictureRow: absolute,
			Lag: lag, LeftSample: sampleAt(c.at, 0), RightSample: sampleAt(j, 1),
			Edge: c.weight, Kind: c.kind,
		})
		if len(anchors) == 4 {
			break
		}
	}
	return anchors
}

func hasCode(caveats []Caveat, code string) bool {
	for _, c := range caveats {
		if c.Code == code {
			return true
		}
	}
	return false
}

// signLevel maps a calibrated sample to a coarse high/low level so the two
// lanes can be compared despite their independent dark/clear endpoints.
func signLevel(s int16) int {
	if s >= 0 {
		return 1
	}
	return -1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
