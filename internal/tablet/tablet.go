// Package tablet evaluates step-strip ("试条") intervals against known
// relative intensities and the active lane calibration.
//
// The evaluation separates three concerns that are easy to conflate:
//   - intensity separation of adjacent steps (coverage-normalized means with
//     their row-to-row spread),
//   - how many valid scanned rows back each step (coverage),
//   - saturation against the calibration rails ("裁到平台").
//
// Every result is a pure function of the registered reel snapshot and the
// named calibration, so repeating a request returns the same verdict.
package tablet

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// ErrUndefined marks a reel whose manifest carries no tablet definition.
var ErrUndefined = errors.New("capture has no step tablet definition")

// Policy thresholds. They are exported so the page can explain the exact
// repeatable rule behind every verdict.
const (
	// MinValidRows is the smallest readable row count that can confirm a step.
	MinValidRows = 24
	// PlateauFraction is the share of rail-clipped rows that declares a plateau.
	PlateauFraction = 0.75
	// CoverageImbalance blocks a pair when one side has this multiple of rows.
	CoverageImbalance = 4.0
	// ReliabilityZ is the conservative standard-error multiplier (Welch).
	ReliabilityZ = 2.5
	// MinPracticalDelta ignores statistically non-zero but tiny gaps.
	MinPracticalDelta = 0.03
)

// Thresholds documents the policy used for one report.
type Thresholds struct {
	MinValidRows      int     `json:"min_valid_rows"`
	PlateauFraction   float64 `json:"plateau_fraction"`
	CoverageImbalance float64 `json:"coverage_imbalance_ratio"`
	ReliabilityZ      float64 `json:"reliability_z"`
	MinDelta          float64 `json:"min_practical_delta"`
}

// RowSpan is a half-open interval on the whole-reel picture clock.
type RowSpan struct {
	Start int `json:"start_row"`
	End   int `json:"end_row"`
}

// StepStat is one step's measured evidence on one lane.
type StepStat struct {
	Index        int      `json:"index"`
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Intensity    float64  `json:"intensity"`
	Span         RowSpan  `json:"span"`
	DeclaredRows int      `json:"declared_rows"`
	ValidRows    int      `json:"valid_rows"`
	MissingRows  int      `json:"missing_rows"`
	OutsideRows  int      `json:"outside_rows"`
	Coverage     float64  `json:"coverage"`
	MeanGray     float64  `json:"mean_gray"`
	StdGray      float64  `json:"std_gray"`
	MeanUnit     float64  `json:"mean_unit"`
	StdUnit      float64  `json:"std_unit"`
	ClippedLow   int      `json:"clipped_low_rows"`
	ClippedHigh  int      `json:"clipped_high_rows"`
	ClipLowFrac  float64  `json:"clipped_low_fraction"`
	ClipHighFrac float64  `json:"clipped_high_fraction"`
	Plateau      string   `json:"plateau,omitempty"` // "dark" | "clear"
	Eligible     bool     `json:"eligible"`
	Issues       []string `json:"issues,omitempty"`
	IssuesZh     []string `json:"issues_zh,omitempty"`
}

// BarWidth is a 0..100 display coordinate for the measured mean. Plateau-dark
// steps keep a small visible sliver instead of disappearing at width zero.
func (s StepStat) BarWidth() float64 {
	w := round1(math.Max(0, math.Min(1, s.MeanUnit)) * 100)
	if w < 2 && s.Plateau == "dark" {
		return 2
	}
	return w
}

// KnownMarker is a 0..100 display coordinate for the known intensity.
func (s StepStat) KnownMarker() float64 { return round1(s.Intensity * 100) }

// IntensityPct renders the known relative intensity as a whole percent.
func (s StepStat) IntensityPct() int { return int(math.Round(s.Intensity * 100)) }

// CoveragePct renders valid/declared coverage as a whole percent.
func (s StepStat) CoveragePct() int {
	if s.DeclaredRows == 0 {
		return 0
	}
	return int(math.Round(100 * float64(s.ValidRows) / float64(s.DeclaredRows)))
}

// PairVerdict classifies one adjacent step boundary.
type PairVerdict struct {
	Index         int     `json:"index"` // pair between step Index and Index+1
	StepA         string  `json:"step_a"`
	StepB         string  `json:"step_b"`
	Status        string  `json:"status"`
	LabelZh       string  `json:"label_zh"`
	Distinct      bool    `json:"distinct"`
	MeanDelta     float64 `json:"mean_delta"`     // signed B-A in calibrated units
	AbsDelta      float64 `json:"abs_delta"`      // absolute calibrated gap
	StandardError float64 `json:"standard_error"` // Welch SE
	Score         float64 `json:"score"`          // |delta| / SE
	CoverageRatio float64 `json:"coverage_ratio"` // max/min valid rows
	DetailZh      string  `json:"detail_zh"`
}

// ConfirmedRange is a maximal run of distinct adjacent boundaries.
type ConfirmedRange struct {
	FirstIndex     int     `json:"first_index"`
	LastIndex      int     `json:"last_index"`
	FirstID        string  `json:"first_id"`
	LastID         string  `json:"last_id"`
	FirstLabel     string  `json:"first_label"`
	LastLabel      string  `json:"last_label"`
	FirstIntensity float64 `json:"first_intensity"`
	LastIntensity  float64 `json:"last_intensity"`
	Steps          int     `json:"steps"`
	Boundaries     int     `json:"boundaries"`
	TitleZh        string  `json:"title_zh"`
	IntensityZh    string  `json:"intensity_zh"`
}

// RescanItem points at a step or boundary that cannot be confirmed as scanned.
type RescanItem struct {
	Code        string   `json:"code"`
	Sides       []string `json:"sides,omitempty"`
	Lane        int      `json:"lane,omitempty"`
	StepIndices []int    `json:"step_indices"`
	StepIDs     []string `json:"step_ids"`
	PairIndex   *int     `json:"pair_index,omitempty"`
	TitleZh     string   `json:"title_zh"`
	AdviceZh    string   `json:"advice_zh"`
}

// LaneReport is one side (left/right lane) evaluation.
type LaneReport struct {
	Lane            int              `json:"lane"`
	Side            string           `json:"side"`
	SideZh          string           `json:"side_zh"`
	Dark            float64          `json:"dark"`
	Clear           float64          `json:"clear"`
	Steps           []StepStat       `json:"steps"`
	Pairs           []PairVerdict    `json:"pairs"`
	ConfirmedRanges []ConfirmedRange `json:"confirmed_ranges"`
	Rescan          []RescanItem     `json:"rescan"`
}

// LadderRow joins a step with the verdict immediately after it for rendering.
type LadderRow struct {
	Step      StepStat     `json:"-"`
	PairAfter *PairVerdict `json:"-"`
}

// Rows returns steps paired with their following boundary for templates.
func (l LaneReport) Rows() []LadderRow {
	rows := make([]LadderRow, len(l.Steps))
	for i := range l.Steps {
		rows[i].Step = l.Steps[i]
		if i < len(l.Pairs) {
			p := l.Pairs[i]
			rows[i].PairAfter = &p
		}
	}
	return rows
}

// Report is the full repeatable evaluation of a registered tablet reel.
type Report struct {
	Reel       string        `json:"reel"`
	Source     string        `json:"sha256"`
	Profile    string        `json:"profile"`
	StepCount  int           `json:"step_count"`
	Thresholds Thresholds    `json:"thresholds"`
	Lanes      [2]LaneReport `json:"lanes"`
	Left       *LaneReport   `json:"left"`
	Right      *LaneReport   `json:"right"`
	Rescan     []RescanItem  `json:"rescan"`
}

// Evaluate runs the tablet policy over the reel using one measured calibration.
func Evaluate(reel *capture.Reel, g gauge.Gauge) (*Report, error) {
	if reel.Tablet == nil {
		return nil, ErrUndefined
	}
	steps := reel.Tablet.Steps
	geometry := collectGeometry(reel, steps)

	report := &Report{
		Reel: reel.Name, Source: reel.Digest, Profile: g.Profile, StepCount: len(steps),
		Thresholds: Thresholds{MinValidRows, PlateauFraction, CoverageImbalance, ReliabilityZ, MinPracticalDelta},
	}
	for lane := 0; lane < 2; lane++ {
		lr := evaluateLane(reel, g, lane, geometry)
		report.Lanes[lane] = lr
	}
	report.Left = &report.Lanes[0]
	report.Right = &report.Lanes[1]
	report.Rescan = aggregateRescan(report.Lanes)
	return report, nil
}

type rowGeometry struct {
	readable []register.Address
	missing  int
	outside  int
}

func collectGeometry(reel *capture.Reel, steps []capture.TabletStep) []rowGeometry {
	out := make([]rowGeometry, len(steps))
	for i, step := range steps {
		geo := rowGeometry{}
		for row := step.StartRow; row < step.EndRow; row++ {
			addr, status := register.Locate(reel, row)
			switch status {
			case register.Readable:
				geo.readable = append(geo.readable, addr)
			case register.Missing:
				geo.missing++
			default:
				geo.outside++
			}
		}
		out[i] = geo
	}
	return out
}

func evaluateLane(reel *capture.Reel, g gauge.Gauge, lane int, geometry []rowGeometry) LaneReport {
	side, sideZh := sideName(lane)
	ref := g.Lanes[lane]
	lr := LaneReport{Lane: lane, Side: side, SideZh: sideZh, Dark: ref.Dark, Clear: ref.Clear}

	for i, spec := range reel.Tablet.Steps {
		geo := geometry[i]
		stat := StepStat{
			Index: i, ID: spec.ID, Label: spec.Label, Intensity: spec.Intensity,
			Span: RowSpan{spec.StartRow, spec.EndRow}, DeclaredRows: spec.EndRow - spec.StartRow,
			ValidRows: len(geo.readable), MissingRows: geo.missing, OutsideRows: geo.outside,
		}
		if stat.Label == "" {
			stat.Label = stat.ID
		}
		if stat.DeclaredRows > 0 {
			stat.Coverage = round3(float64(stat.ValidRows) / float64(stat.DeclaredRows))
		}
		units, grays := measureRows(reel, lane, ref, geo.readable, &stat)
		stat.MeanGray, stat.StdGray = meanStd(grays)
		stat.MeanUnit, stat.StdUnit = meanStd(units)
		n := stat.ValidRows
		if n > 0 {
			stat.ClipLowFrac = round3(float64(stat.ClippedLow) / float64(n))
			stat.ClipHighFrac = round3(float64(stat.ClippedHigh) / float64(n))
		}
		switch {
		case n == 0:
			stat.Issues = append(stat.Issues, "no_valid_rows")
			stat.IssuesZh = append(stat.IssuesZh, "没有有效行")
		case n < MinValidRows:
			stat.Issues = append(stat.Issues, "thin_coverage")
			stat.IssuesZh = append(stat.IssuesZh, "有效行不足")
		}
		switch {
		case n > 0 && stat.ClipLowFrac >= PlateauFraction:
			stat.Plateau = "dark"
			stat.Issues = append(stat.Issues, "plateau_dark")
			stat.IssuesZh = append(stat.IssuesZh, "裁到暗端平台")
		case n > 0 && stat.ClipHighFrac >= PlateauFraction:
			stat.Plateau = "clear"
			stat.Issues = append(stat.Issues, "plateau_clear")
			stat.IssuesZh = append(stat.IssuesZh, "裁到亮端平台")
		}
		stat.Eligible = n >= MinValidRows && stat.Plateau == ""
		lr.Steps = append(lr.Steps, stat)
	}

	lr.Pairs = evaluatePairs(lr.Steps)
	lr.ConfirmedRanges = confirmedRanges(lr.Steps, lr.Pairs)
	lr.Rescan = laneRescan(lr)
	return lr
}

// measureRows converts row means to calibration units while counting rails.
func measureRows(reel *capture.Reel, lane int, ref gauge.Range, addresses []register.Address, stat *StepStat) (units, grays []float64) {
	span := reel.Lanes[lane]
	units = make([]float64, 0, len(addresses))
	grays = make([]float64, 0, len(addresses))
	denom := ref.Clear - ref.Dark
	for _, addr := range addresses {
		sheet := reel.Sheets[addr.Frame]
		gray := gauge.Mean(sheet, span, capture.Span{X0: addr.Row, X1: addr.Row + 1})
		grays = append(grays, gray)
		unit := (gray - ref.Dark) / denom
		if unit < 0 {
			stat.ClippedLow++
		}
		if unit > 1 {
			stat.ClippedHigh++
		}
		units = append(units, unit)
	}
	return units, grays
}

func evaluatePairs(steps []StepStat) []PairVerdict {
	pairs := make([]PairVerdict, 0, max(0, len(steps)-1))
	for i := 0; i+1 < len(steps); i++ {
		a, b := steps[i], steps[i+1]
		p := PairVerdict{Index: i, StepA: a.ID, StepB: b.ID}
		p.MeanDelta = round4(b.MeanUnit - a.MeanUnit)
		p.AbsDelta = round4(math.Abs(p.MeanDelta))
		na, nb := a.ValidRows, b.ValidRows
		if na > 0 && nb > 0 {
			small, large := min(na, nb), max(na, nb)
			p.CoverageRatio = round2(float64(large) / float64(small))
			p.StandardError = round4(math.Sqrt(a.StdUnit*a.StdUnit/float64(na) + b.StdUnit*b.StdUnit/float64(nb)))
			if p.StandardError > 0 {
				p.Score = round2(p.AbsDelta / p.StandardError)
			}
		}
		switch {
		case na == 0 || nb == 0:
			p.Status = "missing"
			p.LabelZh = "缺页无有效行"
			p.DetailZh = "至少一档在卷内没有可读行，无法比较，需补扫缺页。"
		case na < MinValidRows || nb < MinValidRows:
			p.Status = "thin_coverage"
			p.LabelZh = "有效行太少"
			p.DetailZh = "至少一档的有效行低于置信下限，均值不可靠，需补扫该档。"
		case p.CoverageRatio >= CoverageImbalance:
			p.Status = "coverage_confound"
			p.LabelZh = "覆盖量悬殊"
			p.DetailZh = "两档有效覆盖相差超过 4 倍，表面差值可能来自覆盖偏差而非强度，需按相近覆盖重扫。"
		case a.Plateau != "" || b.Plateau != "":
			p.Status = "plateau"
			p.LabelZh = "裁到平台"
			if (a.Plateau == "dark" || b.Plateau == "dark") && !(a.Plateau == "clear" || b.Plateau == "clear") {
				p.DetailZh = "至少一档压在暗端平台，相邻暗档无法分开，需调整曝光或暗场标定后重扫。"
			} else if (a.Plateau == "clear" || b.Plateau == "clear") && !(a.Plateau == "dark" || b.Plateau == "dark") {
				p.DetailZh = "至少一档顶在亮端平台，相邻亮档无法分开，需调整曝光或亮场标定后重扫。"
			} else {
				p.DetailZh = "至少一档落在标定平台区，平台上的档位不能确认可辨，需调整曝光后重扫。"
			}
		case p.Score >= ReliabilityZ && p.AbsDelta >= MinPracticalDelta:
			p.Status = "distinct"
			p.LabelZh = "可辨"
			p.Distinct = true
			p.DetailZh = "均值差相对行内波动与样本量足够显著，当前标定下可可靠区分。"
		default:
			p.Status = "inseparable"
			p.LabelZh = "仍不可分"
			p.DetailZh = "覆盖正常，但差值相对噪声或最小实用间隔不足，当前标定下不能可靠区分。"
		}
		pairs = append(pairs, p)
	}
	return pairs
}

func confirmedRanges(steps []StepStat, pairs []PairVerdict) []ConfirmedRange {
	ranges := make([]ConfirmedRange, 0)
	start := -1
	flush := func(endPair int) {
		if start < 0 {
			return
		}
		last := endPair + 1
		ranges = append(ranges, ConfirmedRange{
			FirstIndex: start, LastIndex: last,
			FirstID: steps[start].ID, LastID: steps[last].ID,
			FirstLabel: steps[start].Label, LastLabel: steps[last].Label,
			FirstIntensity: steps[start].Intensity, LastIntensity: steps[last].Intensity,
			Steps: last - start + 1, Boundaries: last - start,
			TitleZh:     "第 " + steps[start].Label + "–" + steps[last].Label + " 档",
			IntensityZh: intensityText(steps[start].Intensity) + "–" + intensityText(steps[last].Intensity),
		})
		start = -1
	}
	for i, p := range pairs {
		if p.Distinct {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i - 1)
	}
	if len(pairs) > 0 {
		flush(len(pairs) - 1)
	}
	return ranges
}

func laneRescan(lr LaneReport) []RescanItem {
	items := make([]RescanItem, 0)
	for _, s := range lr.Steps {
		switch {
		case s.ValidRows == 0:
			items = append(items, RescanItem{
				Code: "missing_sheet", Lane: lr.Lane, StepIndices: []int{s.Index}, StepIDs: []string{s.ID},
				TitleZh:  "档 " + s.Label + " 没有有效行",
				AdviceZh: "该档全部落在缺页或卷外，先向扫描工位补回对应页，再重跑评估。",
			})
		case s.ValidRows < MinValidRows:
			items = append(items, RescanItem{
				Code: "thin_coverage", Lane: lr.Lane, StepIndices: []int{s.Index}, StepIDs: []string{s.ID},
				TitleZh:  "档 " + s.Label + " 只有 " + itoa(s.ValidRows) + " 行有效",
				AdviceZh: "有效行少于 " + itoa(MinValidRows) + " 行的置信下限，补扫被缺页吞掉的区间后再确认。",
			})
		case s.Plateau == "dark":
			items = append(items, RescanItem{
				Code: "plateau_dark", Lane: lr.Lane, StepIndices: []int{s.Index}, StepIDs: []string{s.ID},
				TitleZh:  "档 " + s.Label + " 裁到暗端平台",
				AdviceZh: "多数行低于暗场端点，暗部档位会糊成一片；降低曝光或重选暗场参考后重扫。",
			})
		case s.Plateau == "clear":
			items = append(items, RescanItem{
				Code: "plateau_clear", Lane: lr.Lane, StepIndices: []int{s.Index}, StepIDs: []string{s.ID},
				TitleZh:  "档 " + s.Label + " 裁到亮端平台",
				AdviceZh: "多数行高于亮场端点，亮部档位会顶成白纸；降低曝光或重选亮场参考后重扫。",
			})
		}
	}
	for _, p := range lr.Pairs {
		if p.Status != "coverage_confound" {
			continue
		}
		idx := p.Index
		items = append(items, RescanItem{
			Code: "coverage_confound", Lane: lr.Lane, PairIndex: &idx,
			StepIndices: []int{p.Index, p.Index + 1}, StepIDs: []string{p.StepA, p.StepB},
			TitleZh:  "档 " + stepsLabel(lr.Steps, p.Index) + "–" + stepsLabel(lr.Steps, p.Index+1) + " 覆盖量相差 " + ftoa(p.CoverageRatio) + " 倍",
			AdviceZh: "两档有效覆盖过于悬殊，均值差可能由覆盖偏差造成；按相近行覆盖补扫这两档后再判定。",
		})
	}
	return items
}

// aggregateRescan merges geometry-only items shared by both lanes and keeps
// lane-specific plateau verdicts attached to their side.
func aggregateRescan(lanes [2]LaneReport) []RescanItem {
	out := make([]RescanItem, 0)
	indexByKey := make(map[string]int)
	for lane := range lanes {
		for _, item := range lanes[lane].Rescan {
			side := lanes[lane].Side
			if item.Code == "plateau_dark" || item.Code == "plateau_clear" {
				item.Sides = []string{side}
				out = append(out, item)
				continue
			}
			key := item.Code + ":" + joinIDs(item.StepIDs)
			if idx, ok := indexByKey[key]; ok {
				out[idx].Sides = append(out[idx].Sides, side)
				continue
			}
			item.Lane = 0
			item.Sides = []string{side}
			indexByKey[key] = len(out)
			out = append(out, item)
		}
	}
	return out
}

func meanStd(values []float64) (float64, float64) {
	n := len(values)
	if n == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)
	if n == 1 {
		return round4(mean), 0
	}
	var acc float64
	for _, v := range values {
		d := v - mean
		acc += d * d
	}
	return round4(mean), round4(math.Sqrt(acc / float64(n-1)))
}

func sideName(lane int) (string, string) {
	if lane == 0 {
		return "left", "左"
	}
	return "right", "右"
}

func stepsLabel(steps []StepStat, index int) string {
	if index < 0 || index >= len(steps) {
		return "?"
	}
	return steps[index].Label
}

func joinIDs(ids []string) string { return strings.Join(ids, "|") }

func itoa(v int) string { return strconv.Itoa(v) }

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func intensityText(v float64) string { return strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
