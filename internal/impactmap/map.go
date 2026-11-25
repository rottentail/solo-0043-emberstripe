package impactmap

import (
	"fmt"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

type rational struct {
	num int64
	den int64 // always > 0
}

func (q rational) floor() int {
	n, d := q.num, q.den
	if n >= 0 {
		return int(n / d)
	}
	return int(-((-n + d - 1) / d))
}

func (q rational) ceil() int {
	n, d := q.num, q.den
	if n >= 0 {
		return int((n + d - 1) / d)
	}
	return int(-((-n) / d))
}

type affine struct {
	slope rational // target units per source unit
	atX   int64
	atY   int64
}

func (a affine) mapAt(x int) rational {
	dx := int64(x) - a.atX
	return rational{num: a.atY*a.slope.den + a.slope.num*dx, den: a.slope.den}
}

func (a affine) mapFloor(x int) int { return a.mapAt(x).floor() }
func (a affine) mapCeil(x int) int  { return a.mapAt(x).ceil() }

// unmapFloor maps a target boundary back to a conservative source boundary.
func (a affine) unmapFloor(y int) int {
	// y = atY + slope*(x-atX).  Inverse has denominator slope.num; slopes are
	// positive for physically monotonic scans.
	num := (int64(y)-a.atY)*a.slope.den + a.atX*a.slope.num
	q := rational{num: num, den: a.slope.num}
	return q.floor()
}

type mappedPart struct {
	info     MappingInfo
	model    affine
	hasModel bool
}

type mapper struct {
	source      capture.Header
	target      capture.Header
	refs        []preparedRef
	extrapolate bool
}

func newMapper(source, target capture.Header, refs []preparedRef, extrapolate bool) mapper {
	return mapper{source: source, target: target, refs: refs, extrapolate: extrapolate}
}

func (m mapper) geometryModel() affine {
	// Each page is one film frame in the scanner format, so the declared
	// rows-per-frame gives the scale when only one tie point is available.
	num := int64(m.target.Rows)
	den := int64(m.source.Rows)
	return affine{slope: rational{num: num, den: den}, atX: 0, atY: 0}
}

func modelBetween(a, b preparedRef) affine {
	dx := int64(b.O - a.O)
	dy := int64(b.R - a.R)
	return affine{slope: rational{num: dy, den: dx}, atX: int64(a.O), atY: int64(a.R)}
}

// mapRange projects a source half-open interval to target intervals.  Source
// reference rows are used as break points so piecewise interpolation survives
// page joins and local scan stretch.
func (m mapper) mapRange(src AbsRange) ([]mappedPart, error) {
	if src.End <= src.Start {
		return nil, invalid("damage range must contain at least one row")
	}
	if len(m.refs) == 0 {
		return []mappedPart{{info: MappingInfo{
			Status: MappingUnmappable, Corroborated: false, OriginalRange: src,
			Reason: "no technician-confirmed reference pair",
		}}}, nil
	}

	breakpointSet := map[int]struct{}{src.Start: {}, src.End: {}}
	for _, ref := range m.refs {
		if ref.O >= src.Start && ref.O < src.End {
			// Keep the verified reference row as its own one-row piece so the
			// anchor itself is corroborated even at the end of a hull.
			breakpointSet[ref.O] = struct{}{}
			breakpointSet[ref.O+1] = struct{}{}
		}
	}
	breakpoints := make([]int, 0, len(breakpointSet))
	for x := range breakpointSet {
		breakpoints = append(breakpoints, x)
	}
	sort.Ints(breakpoints)

	var parts []mappedPart
	for i := 0; i+1 < len(breakpoints); i++ {
		a, b := breakpoints[i], breakpoints[i+1]
		if b <= a {
			continue
		}
		part, err := m.mapPiece(AbsRange{a, b})
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func (m mapper) mapPiece(src AbsRange) (mappedPart, error) {
	// Exact reference point when a piece has zero width is not produced by
	// mapRange; all pieces contain at least one boundary span.
	switch {
	case len(m.refs) == 1:
		ref := m.refs[0]
		model := m.geometryModel()
		// Translate geometry model so the confirmed pair coincides exactly.
		model.atX = int64(ref.O)
		model.atY = int64(ref.R)
		tr := AbsRange{model.mapFloor(src.Start), model.mapCeil(src.End)}
		return mappedPart{
			hasModel: true, model: model,
			info: MappingInfo{
				Status: MappingOneAnchor, Corroborated: true,
				ReferenceIDs: []string{ref.ID}, OriginalRange: src, ThisScanRange: tr,
				ScaleNumerator: model.slope.num, ScaleDenominator: model.slope.den,
				Reason: "one verified tie point plus declared rows-per-frame scale",
			},
		}, nil
	default:
		if src.End == src.Start+1 {
			for i, ref := range m.refs {
				if ref.O != src.Start {
					continue
				}
				var model affine
				switch {
				case i > 0:
					model = modelBetween(m.refs[i-1], ref)
				case i+1 < len(m.refs):
					model = modelBetween(ref, m.refs[i+1])
				default:
					model = m.geometryModel()
					model.atX = int64(ref.O)
					model.atY = int64(ref.R)
				}
				tr := AbsRange{model.mapFloor(src.Start), model.mapCeil(src.End)}
				return mappedPart{hasModel: true, model: model, info: MappingInfo{
					Status: MappingReferenceRow, Corroborated: true,
					ReferenceIDs: []string{ref.ID}, OriginalRange: src, ThisScanRange: tr,
					ScaleNumerator: model.slope.num, ScaleDenominator: model.slope.den,
					Reason: "this row is itself a technician-verified reference",
				}}, nil
			}
		}
		// Find references surrounding the piece midpoint.  Break points are
		// reference positions, so a piece lies in one open interval or in an
		// exterior region.
		mid := (src.Start + src.End) / 2
		idx := 0
		for idx+1 < len(m.refs) && m.refs[idx+1].O <= mid {
			idx++
		}
		var model affine
		var ids []string
		var status string
		corroborated := false
		if idx+1 < len(m.refs) && m.refs[idx].O <= src.Start && src.End <= m.refs[idx+1].O+1 {
			// Interior pair.  +1 accepts a piece whose end boundary is the
			// next reference row itself.
			model = modelBetween(m.refs[idx], m.refs[idx+1])
			ids = []string{m.refs[idx].ID, m.refs[idx+1].ID}
			status = MappingInterpolation
			corroborated = true
		} else if src.Start >= m.refs[len(m.refs)-1].O || src.End <= m.refs[0].O+1 {
			last := src.Start >= m.refs[len(m.refs)-1].O
			var a, b preparedRef
			if last {
				a, b = m.refs[len(m.refs)-2], m.refs[len(m.refs)-1]
			} else {
				a, b = m.refs[0], m.refs[1]
			}
			model = modelBetween(a, b)
			ids = []string{a.ID, b.ID}
			status = MappingExtrapolated
			if m.extrapolate {
				corroborated = false
			}
		} else {
			// Should not happen because breakpoints include every reference.
			return mappedPart{}, fmt.Errorf("cannot bracket source range [%d,%d)", src.Start, src.End)
		}
		tr := AbsRange{model.mapFloor(src.Start), model.mapCeil(src.End)}
		return mappedPart{hasModel: true, model: model, info: MappingInfo{
			Status: status, Corroborated: corroborated, ReferenceIDs: ids,
			OriginalRange: src, ThisScanRange: tr,
			ScaleNumerator: model.slope.num, ScaleDenominator: model.slope.den,
			Reason: map[bool]string{true: "bracketed by two verified references", false: "outside verified reference hull"}[corroborated || status == MappingInterpolation],
		}}, nil
	}
}

// scanCoverageUnready returns subranges without usable scan pages.
func scanCoverageUnready(reel *capture.Reel, target AbsRange, gaps []AbsRange) []AbsRange {
	if target.End <= target.Start {
		return nil
	}
	// Outside reel bounds cannot be supported by a scan page.
	total := reel.Frames * reel.Rows
	if target.Start < 0 || target.End > total {
		// Keep this simple: classify the whole projected part as unready if
		// it leaves the catalog envelope.  Callers still emit detailed runs.
		return []AbsRange{target}
	}
	var missing []AbsRange
	for s := target.Start; s < target.End; {
		page := s / reel.Rows
		pageEnd := min(target.End, (page+1)*reel.Rows)
		if _, ok := reel.Sheets[page]; !ok {
			missing = append(missing, AbsRange{s, pageEnd})
		}
		s = pageEnd
	}
	missing = append(missing, intersectRanges([]AbsRange{target}, gaps)...)
	return mergeRanges(missing)
}
