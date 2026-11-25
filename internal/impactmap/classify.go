package impactmap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

func unmapCeil(a affine, y int) int {
	num := (int64(y)-a.atY)*a.slope.den + a.atX*a.slope.num
	q := rational{num: num, den: a.slope.num}
	return q.ceil()
}

func buildSegment(orig, resc *capture.Reel, mark Mark, mapper mapper, gaps gapSet, profile string) (SegmentReport, error) {
	os, err := resolvePoint(orig.Header, mark.Start)
	if err != nil {
		return SegmentReport{}, invalid("start: %w", err)
	}
	oe, err := resolvePoint(orig.Header, mark.End)
	if err != nil {
		return SegmentReport{}, invalid("end: %w", err)
	}
	if oe <= os {
		return SegmentReport{}, invalid("requires start before end")
	}
	markRange := AbsRange{os, oe}

	origCols, origBasis, err := columnsFor(orig.Header, mark, nil)
	if err != nil {
		return SegmentReport{}, err
	}
	markedMapping := &MappingInfo{
		Status: MappingMarked, Corroborated: true, OriginalRange: markRange, ThisScanRange: markRange,
		Reason: "restorer circled this interval on the original scan",
	}
	originalSide := buildSide(orig, "original", markRange, origCols, origBasis, gaps.Original, markedMapping, profile, normalizedLanes(mark.Lanes))
	seg := SegmentReport{ID: mark.ID, Note: mark.Note, Original: originalSide, CorroboratedRanges: []AbsRange{}}

	originalUnready := scanCoverageUnready(orig, markRange, gaps.Original)
	for _, ur := range originalUnready {
		seg.FollowUps = append(seg.FollowUps, FollowUp{
			Reel: orig.Name, Kind: "original_page_unusable",
			Reason: "original scan page is missing or explicitly unscanned; the rescan cannot backfill original-scan evidence",
			From:   boundaryAt(orig.Header, ur.Start), To: boundaryAt(orig.Header, ur.End),
			FromRow: ur.Start, ToRow: ur.End,
		})
	}

	parts, err := mapper.mapRange(markRange)
	if err != nil {
		return seg, err
	}

	var (
		rescanSound     []MediaRun
		rescanPicture   []MediaRun
		rescanPO        []MediaRun
		rescanEvidence  []PageEvidence
		laneRuns        = map[int][]MediaRun{}
		rescanStart     = 0
		rescanEnd       = 0
		haveRescanRange bool
		readyRanges     []AbsRange
	)

	for _, part := range parts {
		mi := part.info
		if mi.Status == MappingUnmappable {
			seg.FollowUps = append(seg.FollowUps, FollowUp{
				Reel: resc.Name, Kind: MappingUnmappable, Reason: mi.Reason,
				From: boundaryAt(orig.Header, mi.OriginalRange.Start), To: boundaryAt(orig.Header, mi.OriginalRange.End),
				FromRow: mi.OriginalRange.Start, ToRow: mi.OriginalRange.End,
			})
			continue
		}

		if !haveRescanRange {
			rescanStart, rescanEnd = mi.ThisScanRange.Start, mi.ThisScanRange.End
			haveRescanRange = true
		} else {
			rescanStart = min(rescanStart, mi.ThisScanRange.Start)
			rescanEnd = max(rescanEnd, mi.ThisScanRange.End)
		}

		targetCols, basis, cerr := columnsFor(resc.Header, mark, &orig.Header)
		if cerr != nil {
			return seg, cerr
		}
		side := buildSide(resc, "rescan", mi.ThisScanRange, targetCols, basis, gaps.Rescan, &mi, profile, normalizedLanes(mark.Lanes))
		rescanSound = append(rescanSound, side.SoundRuns...)
		rescanPicture = append(rescanPicture, side.PictureRuns...)
		rescanPO = append(rescanPO, side.PictureOnlyRuns...)
		rescanEvidence = append(rescanEvidence, side.Evidence...)
		for _, lane := range side.LaneHits {
			laneRuns[lane.Lane] = append(laneRuns[lane.Lane], lane.FrameRuns...)
		}
		seg.MappingParts = append(seg.MappingParts, mi)

		if mi.Corroborated {
			partReady := mi.OriginalRange
			unreadyTarget := scanCoverageUnready(resc, mi.ThisScanRange, gaps.Rescan)
			var inverseUnready []AbsRange
			if part.hasModel {
				for _, ur := range unreadyTarget {
					inv := AbsRange{part.model.unmapFloor(ur.Start), unmapCeil(part.model, ur.End)}
					inverseUnready = append(inverseUnready, inv)
					seg.FollowUps = append(seg.FollowUps, FollowUp{
						Reel: resc.Name, Kind: "rescan_page_unusable",
						Reason: "projected rescan interval has missing/unscanned/out-of-bounds pages; rescan cannot prove original-scan bytes",
						From:   boundaryAt(orig.Header, inv.Start), To: boundaryAt(orig.Header, inv.End),
						FromRow: inv.Start, ToRow: inv.End,
					})
				}
			}
			readyRanges = append(readyRanges, subtractRanges([]AbsRange{partReady}, inverseUnready)...)
		} else {
			reason := mi.Reason
			kind := mi.Status
			seg.FollowUps = append(seg.FollowUps, FollowUp{
				Reel: resc.Name, Kind: kind, Reason: reason,
				From: boundaryAt(orig.Header, mi.OriginalRange.Start), To: boundaryAt(orig.Header, mi.OriginalRange.End),
				FromRow: mi.OriginalRange.Start, ToRow: mi.OriginalRange.End,
			})
		}
	}

	if len(seg.MappingParts) > 0 || len(laneRuns) > 0 {
		rescan := SideReport{
			Side: "rescan", Reel: resc.Name, ScanRange: AbsRange{rescanStart, rescanEnd},
			Start: boundaryAt(resc.Header, rescanStart), End: boundaryAt(resc.Header, rescanEnd),
			Columns:         firstColumns(laneRuns, resc, mark, orig.Header),
			ColumnBasis:     "technician columns scaled to rescan geometry",
			MappingParts:    seg.MappingParts,
			SoundRuns:       mergeMediaRuns(rescanSound),
			PictureRuns:     mergeMediaRuns(rescanPicture),
			PictureOnlyRuns: mergeMediaRuns(rescanPO),
			Evidence:        dedupeEvidence(rescanEvidence),
		}
		for laneID := 0; laneID < 2; laneID++ {
			runs := mergeMediaRuns(laneRuns[laneID])
			if len(runs) == 0 {
				continue
			}
			lr := LaneReport{
				Lane: laneID, Label: laneLabel(laneID),
				X0: resc.Lanes[laneID].X0, X1: resc.Lanes[laneID].X1,
				FrameRuns: runs,
			}
			for _, run := range runs {
				if run.Audible {
					lr.AudibleFrameRuns = append(lr.AudibleFrameRuns, AbsRange{run.FrameRowStart, run.FrameRowEnd})
					lr.AudibleRowCount += run.FrameRowEnd - run.FrameRowStart
				}
			}
			lr.AudibleFrameRuns = mergeRanges(lr.AudibleFrameRuns)
			rescan.LaneHits = append(rescan.LaneHits, lr)
			rescan.AudibleFrameRuns = append(rescan.AudibleFrameRuns, lr.AudibleFrameRuns...)
		}
		rescan.AudibleFrameRuns = mergeRanges(rescan.AudibleFrameRuns)
		rescan.AudibleRowCount = countRanges(rescan.AudibleFrameRuns)
		rescan.AuditionWindows = auditionWindows(resc, rescan.AudibleFrameRuns)
		rescan.AuditionURL = auditionURL(resc.Name, rescan.AuditionWindows, profile)
		seg.Rescan = &rescan
	}

	seg.CorroboratedRanges = subtractRanges(mergeRanges(readyRanges), originalUnready)
	unready := subtractRanges([]AbsRange{markRange}, seg.CorroboratedRanges)
	if len(unready) > 0 {
		last := unready[len(unready)-1]
		if last.End == markRange.End {
			b := boundaryAt(orig.Header, last.Start)
			seg.ConfirmOriginalFrom = &b
		}
		first := unready[0]
		if first.Start == markRange.Start {
			b := boundaryAt(orig.Header, first.End)
			seg.ConfirmOriginalBefore = &b
		}
	}

	seg.FollowUps = dedupeFollowUps(seg.FollowUps)
	if seg.FollowUps == nil {
		seg.FollowUps = []FollowUp{}
	}
	seg.Original = normalizeSide(seg.Original)
	if seg.Rescan != nil {
		normalized := normalizeSide(*seg.Rescan)
		*seg.Rescan = normalized
	}
	if seg.MappingParts == nil {
		seg.MappingParts = []MappingInfo{}
	}
	return seg, nil
}

func clampAbs(r AbsRange, total int) AbsRange {
	return AbsRange{max(0, min(r.Start, total)), max(0, min(r.End, total))}
}

func firstColumns(_ map[int][]MediaRun, resc *capture.Reel, mark Mark, origHeader capture.Header) capture.Span {
	cols, _, err := columnsFor(resc.Header, mark, &origHeader)
	if err != nil {
		return capture.Span{}
	}
	return cols
}

func columnsFor(h capture.Header, mark Mark, source *capture.Header) (capture.Span, string, error) {
	zone := strings.ToLower(strings.TrimSpace(mark.Zone))
	switch zone {
	case "picture", "frame", "image", "video", "画面", "画格":
		return h.Picture, "declared picture span", nil
	case "left", "left_lane", "lane0", "左", "左声槽":
		return h.Lanes[0], "declared left lane", nil
	case "right", "right_lane", "lane1", "右", "右声槽":
		return h.Lanes[1], "declared right lane", nil
	case "sound", "lanes", "soundtrack", "audio", "声槽", "声道", "声轨", "声音":
		return capture.Span{X0: h.Lanes[0].X0, X1: h.Lanes[1].X1}, "declared lane envelope", nil
	case "full", "all", "width", "整页", "全部":
		return capture.Span{X0: 0, X1: h.Width}, "full width requested", nil
	case "edge", "sprocket", "sprocket_edge", "perforation", "perforation_edge", "hole_edge", "片孔", "片孔边", "片孔边缘", "齿孔", "齿孔边":
		if mark.X0 != nil && mark.X1 != nil {
			return scaledColumns(h, mark, source), "circled sprocket-edge columns", nil
		}
		// The strip manifest does not declare perforation columns.  Avoid
		// pretending an edge-only mark reaches media unless x says so.
		return capture.Span{X0: h.Width - 4, X1: h.Width}, "sprocket edge inferred at outer four columns; supply x0/x1 for exact hit test", nil
	case "":
		if mark.X0 != nil && mark.X1 != nil {
			return scaledColumns(h, mark, source), "technician-supplied columns", nil
		}
		return capture.Span{X0: 0, X1: h.Width}, "columns unspecified; conservatively testing full width", nil
	default:
		return capture.Span{}, "", invalid("unknown zone %q", mark.Zone)
	}
}

func scaledColumns(h capture.Header, mark Mark, source *capture.Header) capture.Span {
	x0, x1 := *mark.X0, *mark.X1
	if source != nil && source.Width > 0 && source.Width != h.Width {
		num := int64(h.Width)
		den := int64(source.Width)
		x0 = rational{num: int64(x0) * num, den: den}.floor()
		x1 = rational{num: int64(x1) * num, den: den}.ceil()
	}
	return capture.Span{X0: max(0, x0), X1: min(h.Width, x1)}
}

func buildSide(reel *capture.Reel, side string, scan AbsRange, cols capture.Span, basis string, gaps []AbsRange, mapping *MappingInfo, profile string, manualLanes []int) SideReport {
	report := SideReport{
		Side: side, Reel: reel.Name, ScanRange: scan,
		Start: boundaryAt(reel.Header, scan.Start), End: boundaryAt(reel.Header, scan.End),
		Columns: cols, ColumnBasis: basis, Mapping: mapping,
		Evidence: scanEvidence(reel, side, scan, gaps),
	}

	pictureHit := overlap(cols, reel.Picture)
	if pictureHit.X1 > pictureHit.X0 {
		report.PictureRuns = mediaRuns(reel, scan, gaps, "picture", nil, mapping, side, 0)
	}

	soundUnion := []AbsRange{}
	for laneID, laneSpan := range reel.Lanes {
		hit := overlap(cols, laneSpan)
		manual := containsInt(manualLanes, laneID)
		if hit.X1 <= hit.X0 && !manual {
			continue
		}
		laneCols := hit
		if hit.X1 <= hit.X0 {
			laneCols = laneSpan
		}
		frameRange := AbsRange{scan.Start - reel.Lead, scan.End - reel.Lead}
		runs := mediaRuns(reel, frameRange, gaps, "sound", &laneID, mapping, side, reel.Lead)
		lr := LaneReport{Lane: laneID, Label: laneLabel(laneID), X0: laneCols.X0, X1: laneCols.X1, FrameRuns: runs}
		for _, run := range runs {
			if run.Audible {
				lr.AudibleFrameRuns = append(lr.AudibleFrameRuns, AbsRange{run.FrameRowStart, run.FrameRowEnd})
				lr.AudibleRowCount += run.FrameRowEnd - run.FrameRowStart
			}
			report.SoundRuns = append(report.SoundRuns, run)
			soundUnion = append(soundUnion, AbsRange{run.FrameRowStart, run.FrameRowEnd})
		}
		lr.AudibleFrameRuns = mergeRanges(lr.AudibleFrameRuns)
		report.LaneHits = append(report.LaneHits, lr)
	}

	// The helper above cannot see mark.Lanes through MappingInfo.  Manual
	// lanes are handled by callers via enrichManualLanes; keep soundUnion from
	// geometric hits here.
	report.SoundRuns = mergeMediaRuns(report.SoundRuns)
	report.AudibleFrameRuns = audibleRuns(report.SoundRuns)
	report.AudibleRowCount = countRanges(report.AudibleFrameRuns)

	if pictureHit.X1 > pictureHit.X0 {
		pictureIntervals := make([]AbsRange, 0, len(report.PictureRuns))
		for _, run := range report.PictureRuns {
			pictureIntervals = append(pictureIntervals, AbsRange{run.FrameRowStart, run.FrameRowEnd})
		}
		only := subtractRanges(mergeRanges(pictureIntervals), mergeRanges(soundUnion))
		for _, r := range only {
			report.PictureOnlyRuns = append(report.PictureOnlyRuns, mediaRuns(reel, r, gaps, "picture_only", nil, mapping, side, 0)...)
		}
		report.PictureOnlyRuns = mergeMediaRuns(report.PictureOnlyRuns)
	}

	report.AuditionWindows = auditionWindows(reel, report.AudibleFrameRuns)
	report.AuditionURL = auditionURL(reel.Name, report.AuditionWindows, profile)
	return report
}

func overlap(a, b capture.Span) capture.Span {
	return capture.Span{X0: max(a.X0, b.X0), X1: min(a.X1, b.X1)}
}

func laneLabel(lane int) string {
	if lane == 0 {
		return "left"
	}
	return "right"
}

func mediaRuns(reel *capture.Reel, pr AbsRange, gaps []AbsRange, kind string, lane *int, mapping *MappingInfo, side string, sourceLead int) []MediaRun {
	if pr.End <= pr.Start {
		return nil
	}
	totalFrames := reel.Frames * reel.Rows
	totalScan := totalFrames
	var runs []MediaRun
	for p := pr.Start; p < pr.End; {
		run := MediaRun{
			Kind: kind, Lane: lane, FrameRowStart: p,
			StartAddress: boundaryAt(reel.Header, p), Mapping: mapping,
		}
		if lane != nil {
			run.LaneLabel = laneLabel(*lane)
		}
		next := pr.End
		status := StatusReadable
		reason := ""

		switch {
		case p < 0:
			status = StatusOutside
			next = min(pr.End, 0)
			reason = "negative picture clock"
		case p >= totalFrames:
			status = StatusOutside
			next = pr.End
			reason = "beyond declared frames"
		default:
			s := p + sourceLead
			switch {
			case s < 0:
				status = StatusOutside
				next = min(pr.End, -sourceLead)
				reason = "source row before first scan page"
			case s >= totalScan:
				status = StatusOutside
				next = pr.End
				reason = "source row after last declared scan row"
			default:
				page := s / reel.Rows
				next = min(pr.End, (page+1)*reel.Rows-sourceLead)
				for _, g := range gaps {
					if s >= g.Start && s < g.End {
						status = StatusUnscanned
						reason = "declared coverage gap"
						next = min(next, g.End-sourceLead)
						break
					}
					if g.Start > s {
						next = min(next, g.Start-sourceLead)
					}
				}
				if status == StatusReadable {
					if _, ok := reel.Sheets[page]; !ok {
						status = StatusMissing
						reason = "emulsion page absent from catalog snapshot"
					}
				}
			}
		}

		if next <= p {
			next = p + 1
		}
		run.FrameRowEnd = next
		run.EndAddress = boundaryAt(reel.Header, next)
		run.SourceScanStart = p + sourceLead
		run.SourceScanEnd = next + sourceLead
		run.Status = status
		run.Reason = reason
		run.Audible = kind == "sound" && status == StatusReadable
		run.Reproducible = run.Audible
		if status == StatusUnscanned {
			run.RegisterStatus = StatusMissing
		} else {
			run.RegisterStatus = status
		}
		if run.SourceScanStart >= 0 && run.SourceScanEnd <= totalScan && run.SourceScanEnd > run.SourceScanStart {
			run.Evidence = append(run.Evidence, scanEvidence(reel, side, AbsRange{run.SourceScanStart, run.SourceScanEnd}, gaps)...)
		}
		runs = append(runs, run)
		p = next
	}
	return runs
}

func scanEvidence(reel *capture.Reel, side string, r AbsRange, gaps []AbsRange) []PageEvidence {
	if r.End <= r.Start {
		return nil
	}
	total := reel.Frames * reel.Rows
	lo, hi := max(0, r.Start), min(total, r.End)
	var evidence []PageEvidence
	for s := lo; s < hi; {
		page := s / reel.Rows
		pageStart := page * reel.Rows
		pageEnd := min(hi, (page+1)*reel.Rows)
		ev := PageEvidence{
			Reel: reel.Name, Side: side, Page: page,
			StartRow: s - pageStart, EndRow: pageEnd - pageStart,
			Present: true, ProvesOriginalScan: side == "original",
		}
		if sheet, ok := reel.Sheets[page]; ok {
			ev.Depth = sheet.Depth
			ev.SHA256 = sheet.Digest
		} else {
			ev.Present = false
			ev.ProvesOriginalScan = false
			ev.Reason = "sheet file missing from read-only catalog snapshot"
		}
		for _, g := range gaps {
			if s >= g.Start && s < g.End {
				ev.Present = false
				ev.SHA256 = ""
				ev.ProvesOriginalScan = false
				if ev.Reason == "" {
					ev.Reason = "declared unscanned interval"
				}
			}
		}
		evidence = append(evidence, ev)
		s = pageEnd
	}
	return dedupeEvidence(evidence)
}

func dedupeEvidence(in []PageEvidence) []PageEvidence {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Page != in[j].Page {
			return in[i].Page < in[j].Page
		}
		if in[i].StartRow != in[j].StartRow {
			return in[i].StartRow < in[j].StartRow
		}
		return in[i].EndRow < in[j].EndRow
	})
	out := in[:0:0]
	for _, ev := range in {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if last.Reel == ev.Reel && last.Page == ev.Page && last.StartRow == ev.StartRow && last.EndRow == ev.EndRow {
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}

func mergeMediaRuns(in []MediaRun) []MediaRun {
	if len(in) == 0 {
		return nil
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].FrameRowStart != in[j].FrameRowStart {
			return in[i].FrameRowStart < in[j].FrameRowStart
		}
		return in[i].FrameRowEnd < in[j].FrameRowEnd
	})
	out := []MediaRun{in[0]}
	for _, run := range in[1:] {
		last := &out[len(out)-1]
		same := last.Kind == run.Kind && last.Status == run.Status &&
			((last.Lane == nil && run.Lane == nil) || (last.Lane != nil && run.Lane != nil && *last.Lane == *run.Lane))
		if same && run.FrameRowStart <= last.FrameRowEnd {
			if run.FrameRowEnd > last.FrameRowEnd {
				last.FrameRowEnd = run.FrameRowEnd
				last.EndAddress = run.EndAddress
			}
			last.Audible = last.Audible || run.Audible
			last.Reproducible = last.Audible
			last.Evidence = dedupeEvidence(append(last.Evidence, run.Evidence...))
			continue
		}
		out = append(out, run)
	}
	return out
}

func audibleRuns(runs []MediaRun) []AbsRange {
	var ranges []AbsRange
	for _, run := range runs {
		if run.Audible {
			ranges = append(ranges, AbsRange{run.FrameRowStart, run.FrameRowEnd})
		}
	}
	return mergeRanges(ranges)
}

func countRanges(ranges []AbsRange) int {
	total := 0
	for _, r := range ranges {
		total += r.End - r.Start
	}
	return total
}

func auditionWindows(reel *capture.Reel, rows []AbsRange) []AbsRange {
	var windows []AbsRange
	for _, r := range rows {
		if r.End <= r.Start {
			continue
		}
		f0 := max(0, r.Start/reel.Rows)
		f1 := min(reel.Frames, (r.End-1)/reel.Rows+1)
		for f := f0; f < f1; f += 48 {
			windows = append(windows, AbsRange{f, min(f1, f+48)})
		}
	}
	return mergeRanges(windows)
}

func auditionURL(reel string, windows []AbsRange, profile string) string {
	if len(windows) == 0 {
		return ""
	}
	w := windows[0]
	url := fmt.Sprintf("/captures/%s/audition?start=%d&end=%d", reel, w.Start, w.End)
	if profile != "" {
		url += "&profile=" + profile
	}
	return url
}

func dedupeFollowUps(in []FollowUp) []FollowUp {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].FromRow != in[j].FromRow {
			return in[i].FromRow < in[j].FromRow
		}
		return in[i].Kind < in[j].Kind
	})
	out := in[:0:0]
	for _, f := range in {
		if len(out) > 0 {
			last := out[len(out)-1]
			if last.Reel == f.Reel && last.Kind == f.Kind && last.FromRow == f.FromRow && last.ToRow == f.ToRow {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

func normalizeSide(s SideReport) SideReport {
	if s.LaneHits == nil {
		s.LaneHits = []LaneReport{}
	}
	s.SoundLanes = s.LaneHits
	if s.SoundRuns == nil {
		s.SoundRuns = []MediaRun{}
	}
	if s.AudibleFrameRuns == nil {
		s.AudibleFrameRuns = []AbsRange{}
	}
	s.AudibleRows = s.AudibleFrameRuns
	s.ReproducibleRuns = s.AudibleFrameRuns
	for i := range s.SoundRuns {
		s.SoundRuns[i].Reproducible = s.SoundRuns[i].Audible
	}
	for i := range s.PictureRuns {
		s.PictureRuns[i].Reproducible = false
	}
	for i := range s.PictureOnlyRuns {
		s.PictureOnlyRuns[i].Reproducible = false
	}
	if s.PictureRuns == nil {
		s.PictureRuns = []MediaRun{}
	}
	if s.PictureOnlyRuns == nil {
		s.PictureOnlyRuns = []MediaRun{}
	}
	s.PictureOnlyRanges = make([]AbsRange, 0, len(s.PictureOnlyRuns))
	for _, run := range s.PictureOnlyRuns {
		s.PictureOnlyRanges = append(s.PictureOnlyRanges, AbsRange{run.FrameRowStart, run.FrameRowEnd})
	}
	if s.Evidence == nil {
		s.Evidence = []PageEvidence{}
	}
	if s.AuditionWindows == nil {
		s.AuditionWindows = []AbsRange{}
	}
	for i := range s.LaneHits {
		if s.LaneHits[i].FrameRuns == nil {
			s.LaneHits[i].FrameRuns = []MediaRun{}
		}
		if s.LaneHits[i].AudibleFrameRuns == nil {
			s.LaneHits[i].AudibleFrameRuns = []AbsRange{}
		}
		for j := range s.LaneHits[i].FrameRuns {
			s.LaneHits[i].FrameRuns[j].Reproducible = s.LaneHits[i].FrameRuns[j].Audible
		}
	}
	return s
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func normalizedLanes(in []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, lane := range in {
		if (lane == 0 || lane == 1) && !seen[lane] {
			seen[lane] = true
			out = append(out, lane)
		}
	}
	sort.Ints(out)
	return out
}
