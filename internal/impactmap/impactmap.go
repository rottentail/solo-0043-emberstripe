// Package impactmap builds lineage-aware damage maps between two scans.
//
// The package deliberately refuses to treat repeating perforation texture as
// evidence: cross-scan correspondence only comes from technician-supplied
// reference points.  Every reported media range keeps the reel and scan page
// that supplied its evidence.
package impactmap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
)

const Schema = "emberstripe.impact-map/v1"

// Status values extend the register package's coverage vocabulary with the
// partial-page coverage holes allowed by a hand-marked survey.
const (
	StatusReadable  = "readable"
	StatusMissing   = "missing_sheet"
	StatusOutside   = "outside_capture"
	StatusUnscanned = "unscanned_segment"
)

const (
	MappingMarked        = "marked_on_scan"
	MappingInterpolation = "reference_interpolation"
	MappingReferenceRow  = "verified_reference_row"
	MappingOneAnchor     = "single_reference_geometry"
	MappingExtrapolated  = "reference_extrapolation"
	MappingUnmappable    = "unmappable"
)

// Point is a zero-based scanner page/row location.  Row is a continuous row
// coordinate; end points may use row == rows_per_frame as a page boundary.
type Point struct {
	Page     int  `json:"page"`
	Row      int  `json:"row"`
	Absolute *int `json:"absolute_row,omitempty"`
}

// Reference is a technician-confirmed identical physical position in both
// scans.  Similar-looking sprocket texture is intentionally not matched here.
type Reference struct {
	ID       string `json:"id,omitempty"`
	Original Point  `json:"original"`
	Rescan   Point  `json:"rescan"`
}

// Mark is one circled tear segment on the original scan.  Start is inclusive
// and End is exclusive.
type Mark struct {
	ID    string `json:"id,omitempty"`
	Note  string `json:"note,omitempty"`
	Start Point  `json:"start"`
	End   Point  `json:"end"`

	// X0/X1 are scanner columns, left closed and right open.  If omitted and
	// Zone is empty, the full width is conservatively treated as affected.
	X0   *int   `json:"x0,omitempty"`
	X1   *int   `json:"x1,omitempty"`
	Zone string `json:"zone,omitempty"`

	// Lanes optionally states technician-observed lanes directly: 0=left,
	// 1=right, or "left"/"right" after parsing.
	Lanes []int `json:"lanes,omitempty"`
}

// Gap declares a scan interval that has no usable pixels even if a caller's
// catalog happens to contain a placeholder.  Missing catalog sheets are also
// handled automatically.
type Gap struct {
	Side   string `json:"side,omitempty"`
	Reel   string `json:"reel,omitempty"`
	Reason string `json:"reason,omitempty"`
	Start  Point  `json:"start"`
	End    Point  `json:"end"`
}

// Survey is the technician's hand-marked input.
type Survey struct {
	Original   string      `json:"original"`
	Rescan     string      `json:"rescan"`
	References []Reference `json:"references"`
	Marks      []Mark      `json:"marks"`
	Gaps       []Gap       `json:"gaps,omitempty"`

	// Profile only controls optional audition URLs; geometry and lineage do
	// not depend on a gauge being usable.
	Profile string `json:"profile,omitempty"`

	// Extrapolate allows a tentative projection beyond the outer reference
	// pair.  Such ranges are never marked corroborated.
	Extrapolate bool `json:"extrapolate,omitempty"`
}

type UnknownReelError struct{ Name string }

func (e *UnknownReelError) Error() string { return "unknown scan reel: " + e.Name }

type ValidationError struct{ err error }

func (e *ValidationError) Error() string { return e.err.Error() }
func (e *ValidationError) Unwrap() error { return e.err }

func invalid(format string, args ...any) error {
	return &ValidationError{err: fmt.Errorf(format, args...)}
}

type AbsRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (r AbsRange) empty() bool { return r.End <= r.Start }

type Boundary struct {
	Page        int `json:"page"`
	Row         int `json:"row"`
	AbsoluteRow int `json:"absolute_row"`
}

type ReelSummary struct {
	Side           string `json:"side"`
	Name           string `json:"name"`
	SHA256         string `json:"sha256"`
	Width          int    `json:"width"`
	RowsPerFrame   int    `json:"rows_per_frame"`
	Frames         int    `json:"frames"`
	LeadRows       int    `json:"lead_rows"`
	FPS            int    `json:"fps"`
	SampleRate     int    `json:"sample_rate"`
	AvailablePages []int  `json:"available_pages"`
}

type PageEvidence struct {
	Reel               string `json:"reel"`
	Side               string `json:"side"`
	Page               int    `json:"page"`
	StartRow           int    `json:"start_row"`
	EndRow             int    `json:"end_row"`
	Present            bool   `json:"present"`
	Depth              int    `json:"depth,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	Reason             string `json:"reason,omitempty"`
	ProvesOriginalScan bool   `json:"proves_original_scan"`
}

type MappingInfo struct {
	Status           string   `json:"status"`
	Corroborated     bool     `json:"corroborated"`
	ReferenceIDs     []string `json:"reference_ids,omitempty"`
	OriginalRange    AbsRange `json:"original_range"`
	ThisScanRange    AbsRange `json:"this_scan_range,omitempty"`
	ScaleNumerator   int64    `json:"scale_numerator,omitempty"`
	ScaleDenominator int64    `json:"scale_denominator,omitempty"`
	Reason           string   `json:"reason,omitempty"`
}

type MediaRun struct {
	Kind            string         `json:"kind"`
	Lane            *int           `json:"lane,omitempty"`
	LaneLabel       string         `json:"lane_label,omitempty"`
	FrameRowStart   int            `json:"frame_row_start"`
	FrameRowEnd     int            `json:"frame_row_end"`
	StartAddress    Boundary       `json:"start_address"`
	EndAddress      Boundary       `json:"end_address"`
	SourceScanStart int            `json:"source_scan_start"`
	SourceScanEnd   int            `json:"source_scan_end"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	Audible         bool           `json:"audible"`
	Reproducible    bool           `json:"reproducible"`
	RegisterStatus  string         `json:"register_status"`
	Evidence        []PageEvidence `json:"evidence"`
	Mapping         *MappingInfo   `json:"mapping,omitempty"`
}

type LaneReport struct {
	Lane             int        `json:"lane"`
	Label            string     `json:"label"`
	X0               int        `json:"x0"`
	X1               int        `json:"x1"`
	FrameRuns        []MediaRun `json:"frame_runs"`
	AudibleFrameRuns []AbsRange `json:"audible_frame_runs"`
	AudibleRowCount  int        `json:"audible_row_count"`
}

type SideReport struct {
	Side              string         `json:"side"`
	Reel              string         `json:"reel"`
	ScanRange         AbsRange       `json:"scan_range"`
	Start             Boundary       `json:"start"`
	End               Boundary       `json:"end"`
	Columns           capture.Span   `json:"columns"`
	ColumnBasis       string         `json:"column_basis"`
	Mapping           *MappingInfo   `json:"mapping,omitempty"`
	MappingParts      []MappingInfo  `json:"mapping_parts,omitempty"`
	LaneHits          []LaneReport   `json:"lane_hits"`
	SoundLanes        []LaneReport   `json:"sound_lanes"`
	SoundRuns         []MediaRun     `json:"sound_runs"`
	AudibleFrameRuns  []AbsRange     `json:"audible_frame_runs"`
	ReproducibleRuns  []AbsRange     `json:"reproducible_frame_runs"`
	AudibleRows       []AbsRange     `json:"audible_rows"`
	AudibleRowCount   int            `json:"audible_row_count"`
	PictureRuns       []MediaRun     `json:"picture_runs"`
	PictureOnlyRuns   []MediaRun     `json:"picture_only_runs"`
	PictureOnlyRanges []AbsRange     `json:"picture_only_ranges"`
	Evidence          []PageEvidence `json:"evidence"`
	AuditionWindows   []AbsRange     `json:"audition_frame_windows,omitempty"`
	AuditionURL       string         `json:"audition_url,omitempty"`
}

type FollowUp struct {
	Reel    string   `json:"reel"`
	Kind    string   `json:"kind"`
	Reason  string   `json:"reason"`
	From    Boundary `json:"from_original"`
	To      Boundary `json:"to_original"`
	FromRow int      `json:"from_absolute_row"`
	ToRow   int      `json:"to_absolute_row"`
}

type SegmentReport struct {
	ID                    string        `json:"id"`
	Note                  string        `json:"note,omitempty"`
	MappingParts          []MappingInfo `json:"mapping_parts,omitempty"`
	Original              SideReport    `json:"original"`
	Rescan                *SideReport   `json:"rescan,omitempty"`
	CorroboratedRanges    []AbsRange    `json:"corroborated_original_ranges"`
	FollowUps             []FollowUp    `json:"follow_ups,omitempty"`
	ConfirmOriginalFrom   *Boundary     `json:"confirm_original_from,omitempty"`
	ConfirmOriginalBefore *Boundary     `json:"confirm_original_before,omitempty"`
}

type ImpactMap struct {
	Schema        string          `json:"schema"`
	SurveySHA256  string          `json:"survey_sha256"`
	LineagePolicy []string        `json:"lineage_policy"`
	Original      ReelSummary     `json:"original_reel"`
	Rescan        ReelSummary     `json:"rescan_reel"`
	Segments      []SegmentReport `json:"segments"`
	Warnings      []string        `json:"warnings,omitempty"`
}

// Build validates survey and constructs the lineage-aware impact map.
func Build(catalog map[string]*capture.Reel, survey Survey) (*ImpactMap, error) {
	origReel, ok := catalog[survey.Original]
	if !ok {
		return nil, &UnknownReelError{Name: survey.Original}
	}
	rescReel, ok := catalog[survey.Rescan]
	if !ok {
		return nil, &UnknownReelError{Name: survey.Rescan}
	}
	if len(survey.Marks) == 0 {
		return nil, invalid("at least one damage mark is required")
	}

	surveyDigest := sha256.Sum256(mustJSON(survey))
	out := &ImpactMap{
		Schema:       Schema,
		SurveySHA256: hex.EncodeToString(surveyDigest[:]),
		LineagePolicy: []string{
			"每条判断只使用其 evidence 中点名的扫描卷和页。",
			"补扫页可定位或佐证伤痕，但不能作为原扫页证，也不能替原扫缺页补证。",
			"片孔重复外观不用于自动配对；跨扫描同一位置只采用修复员提供的参照点。",
		},
		Original: summary("original", origReel),
		Rescan:   summary("rescan", rescReel),
	}

	if survey.Profile != "" {
		for _, reel := range []*capture.Reel{origReel, rescReel} {
			if _, err := gauge.Measure(reel, survey.Profile); err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("reel %s profile %q is not auditionable: %v", reel.Name, survey.Profile, err))
			}
		}
	}

	refs, err := prepareReferences(origReel, rescReel, survey.References)
	if err != nil {
		return nil, err
	}
	gaps, err := prepareGaps(origReel, rescReel, survey.Gaps)
	if err != nil {
		return nil, err
	}
	mapper := newMapper(origReel.Header, rescReel.Header, refs, survey.Extrapolate)

	for i, mark := range survey.Marks {
		if mark.ID == "" {
			mark.ID = fmt.Sprintf("tear-%02d", i+1)
		}
		if err := validateMarkGeometry(origReel.Header, mark); err != nil {
			return nil, fmt.Errorf("mark %s: %w", mark.ID, err)
		}
		seg, err := buildSegment(origReel, rescReel, mark, mapper, gaps, survey.Profile)
		if err != nil {
			return nil, fmt.Errorf("mark %s: %w", mark.ID, err)
		}
		out.Segments = append(out.Segments, seg)
	}
	return out, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func validateMarkGeometry(h capture.Header, mark Mark) error {
	if (mark.X0 == nil) != (mark.X1 == nil) {
		return invalid("x0 and x1 must be supplied together")
	}
	if mark.X0 != nil {
		x0, x1 := *mark.X0, *mark.X1
		if x0 < 0 || x1 <= x0 || x1 > h.Width {
			return invalid("columns must satisfy 0 <= x0 < x1 <= %d", h.Width)
		}
	}
	for _, lane := range mark.Lanes {
		if lane != 0 && lane != 1 {
			return invalid("lane must be 0 (left) or 1 (right)")
		}
	}
	return nil
}

func summary(side string, reel *capture.Reel) ReelSummary {
	return ReelSummary{
		Side: side, Name: reel.Name, SHA256: reel.Digest, Width: reel.Width,
		RowsPerFrame: reel.Rows, Frames: reel.Frames, LeadRows: reel.Lead,
		FPS: reel.FPS, SampleRate: reel.FPS * reel.Rows,
		AvailablePages: reel.Available(),
	}
}

type preparedRef struct {
	ID string
	O  int
	R  int
}

func prepareReferences(o, r *capture.Reel, in []Reference) ([]preparedRef, error) {
	refs := make([]preparedRef, 0, len(in))
	seenID := map[string]bool{}
	for i, ref := range in {
		id := ref.ID
		if id == "" {
			id = fmt.Sprintf("ref-%02d", i+1)
		}
		if seenID[id] {
			return nil, invalid("duplicate reference id %q", id)
		}
		seenID[id] = true
		op, err := resolvePoint(o.Header, ref.Original)
		if err != nil {
			return nil, invalid("reference %s original: %w", id, err)
		}
		rp, err := resolvePoint(r.Header, ref.Rescan)
		if err != nil {
			return nil, invalid("reference %s rescan: %w", id, err)
		}
		refs = append(refs, preparedRef{ID: id, O: op, R: rp})
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].O < refs[j].O })
	for i := 1; i < len(refs); i++ {
		if refs[i].O == refs[i-1].O {
			return nil, invalid("two references claim different identity at original row %d", refs[i].O)
		}
		if refs[i].R <= refs[i-1].R {
			return nil, invalid("reference rescan positions must increase with original positions")
		}
	}
	return refs, nil
}

type gapSet struct {
	Original []AbsRange
	Rescan   []AbsRange
}

func prepareGaps(o, r *capture.Reel, in []Gap) (gapSet, error) {
	var g gapSet
	for i, gap := range in {
		side := gap.Side
		if side == "" {
			switch gap.Reel {
			case o.Name:
				side = "original"
			case r.Name:
				side = "rescan"
			default:
				return g, invalid("gap %d must set side=original|rescan or name a catalog reel", i+1)
			}
		}
		var h capture.Header
		switch side {
		case "original", o.Name:
			h = o.Header
			side = "original"
		case "rescan", r.Name:
			h = r.Header
			side = "rescan"
		default:
			return g, invalid("gap %d has unknown side %q", i+1, side)
		}
		a, err := resolvePoint(h, gap.Start)
		if err != nil {
			return g, invalid("gap %d start: %w", i+1, err)
		}
		b, err := resolvePoint(h, gap.End)
		if err != nil {
			return g, invalid("gap %d end: %w", i+1, err)
		}
		if b <= a {
			return g, invalid("gap %d requires start < end", i+1)
		}
		if side == "original" {
			g.Original = append(g.Original, AbsRange{a, b})
		} else {
			g.Rescan = append(g.Rescan, AbsRange{a, b})
		}
	}
	g.Original = mergeRanges(g.Original)
	g.Rescan = mergeRanges(g.Rescan)
	return g, nil
}

func resolvePoint(h capture.Header, p Point) (int, error) {
	total := h.Frames * h.Rows
	if p.Absolute != nil {
		v := *p.Absolute
		if v < 0 || v > total {
			return 0, fmt.Errorf("absolute row %d outside 0..%d", v, total)
		}
		return v, nil
	}
	if p.Page < 0 || p.Page > h.Frames || p.Row < 0 || p.Row > h.Rows || p.Page == h.Frames && p.Row != 0 {
		return 0, fmt.Errorf("page %d row %d outside capture", p.Page, p.Row)
	}
	v := p.Page*h.Rows + p.Row
	if v > total {
		return 0, fmt.Errorf("page %d row %d outside capture", p.Page, p.Row)
	}
	return v, nil
}

func boundaryAt(h capture.Header, abs int) Boundary {
	if abs < 0 {
		return Boundary{Page: -1, Row: abs, AbsoluteRow: abs}
	}
	if abs > h.Frames*h.Rows {
		return Boundary{Page: h.Frames, Row: abs - h.Frames*h.Rows, AbsoluteRow: abs}
	}
	return Boundary{Page: abs / h.Rows, Row: abs % h.Rows, AbsoluteRow: abs}
}

func mergeRanges(in []AbsRange) []AbsRange {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool {
		if in[i].Start != in[j].Start {
			return in[i].Start < in[j].Start
		}
		return in[i].End < in[j].End
	})
	out := []AbsRange{in[0]}
	for _, r := range in[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func subtractRanges(base []AbsRange, removed []AbsRange) []AbsRange {
	base = mergeRanges(base)
	removed = mergeRanges(removed)
	var out []AbsRange
	for _, b := range base {
		cuts := []AbsRange{b}
		for _, r := range removed {
			var next []AbsRange
			for _, c := range cuts {
				if r.End <= c.Start || r.Start >= c.End {
					next = append(next, c)
					continue
				}
				if r.Start > c.Start {
					next = append(next, AbsRange{c.Start, min(c.End, r.Start)})
				}
				if r.End < c.End {
					next = append(next, AbsRange{max(c.Start, r.End), c.End})
				}
			}
			cuts = next
		}
		out = append(out, cuts...)
	}
	return mergeRanges(out)
}

func intersectRanges(a, b []AbsRange) []AbsRange {
	var out []AbsRange
	for _, x := range a {
		for _, y := range b {
			lo, hi := max(x.Start, y.Start), min(x.End, y.End)
			if hi > lo {
				out = append(out, AbsRange{lo, hi})
			}
		}
	}
	return mergeRanges(out)
}
