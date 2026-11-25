package lineaudit

import (
	"fmt"
	"net/url"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Compare aligns two same-spec captures on their raw scanner-row clock.
func Compare(a, b *capture.Reel, req Request) (*Report, error) {
	if a == nil || b == nil {
		return nil, fmt.Errorf("two scans are required")
	}
	req.A, req.B = a.Name, b.Name
	if a.Rows != b.Rows || a.Frames != b.Frames {
		return nil, fmt.Errorf("两份试扫必须有相同的每格行数和格数：%s 为 %d×%d，%s 为 %d×%d", a.Name, a.Frames, a.Rows, b.Name, b.Frames, b.Rows)
	}
	total := a.Rows * a.Frames
	maxWindow := register.MaxFrames * a.Rows
	if req.EndRow == 0 {
		req.EndRow = min(total, req.StartRow+maxWindow)
	}
	if req.StartRow < 0 || req.EndRow <= req.StartRow || req.EndRow > total || req.EndRow-req.StartRow > maxWindow {
		return nil, fmt.Errorf("expected 0 <= start_row < end_row <= %d with at most %d rows (%d frames)", total, maxWindow, register.MaxFrames)
	}
	if req.MaxDrift == 0 {
		req.MaxDrift = 1024
	}
	if req.MaxDrift < 2 || req.MaxDrift > total {
		return nil, fmt.Errorf("max_drift_rows is outside the compared capture")
	}
	if req.Profile != "" {
		if ga, err := gauge.Measure(a, req.Profile); err != nil {
			return nil, fmt.Errorf("A 卷标定不可用：%w", err)
		} else {
			req.Profile = ga.Profile
		}
	} else {
		req.Profile = usableProfile(a)
	}
	if req.ProfileB != "" {
		if gb, err := gauge.Measure(b, req.ProfileB); err != nil {
			return nil, fmt.Errorf("B 卷标定不可用：%w", err)
		} else {
			req.ProfileB = gb.Profile
		}
	} else {
		req.ProfileB = usableProfile(b)
	}

	report := &Report{
		Schema: "emberstripe.line-audit/v1",
		A:      sideInfo(a), B: sideInfo(b), Request: req,
		Truncated:      req.EndRow < total,
		LeadDifference: b.Lead - a.Lead,
		Findings:       []Finding{},
		Suspects:       []Finding{},
		Insertions:     []Finding{},
		Deletions:      []Finding{},
		MissingPages:   []Finding{},
		NormalRepeats:  []Finding{},
	}

	sig := pairSignal{}
	la := [2][]landmark{}
	lb := [2][]landmark{}
	laneGlobal := make([]int, 0, 2)
	for lane := 0; lane < 2; lane++ {
		sig.a[lane] = buildLane(a, lane, req.StartRow, req.EndRow)
		sig.b[lane] = buildLane(b, lane, req.StartRow, req.EndRow)
		la[lane] = landmarks(sig.a[lane], req.StartRow)
		lb[lane] = landmarks(sig.b[lane], req.StartRow)
		if len(la[lane]) >= 2 && len(lb[lane]) >= 2 {
			laneGlobal = append(laneGlobal, estimateShift(la[lane], lb[lane], sig.a[lane], sig.b[lane], req.StartRow))
		}
	}
	alignmentShift := 0
	if len(laneGlobal) > 0 {
		alignmentShift = medianInt(laneGlobal)
	}

	var allChanges []rawChange
	var bestMatches []match
	headShifts := make([]int, 0, 2)
	bestMatchCount := -1
	for lane := 0; lane < 2; lane++ {
		matches := matchLandmarks(la[lane], lb[lane], alignmentShift, req.MaxDrift)
		if len(matches) >= 4 {
			headN := min(8, len(matches))
			headShifts = append(headShifts, medianInt(shifts(matches[:headN])))
		}
		if len(matches) > bestMatchCount {
			bestMatchCount = len(matches)
			bestMatches = matches
		}
		changes := detectChanges(lane, matches, sig.a[lane], sig.b[lane], req.StartRow)
		allChanges = append(allChanges, changes...)
	}
	if len(headShifts) > 0 {
		report.GlobalShiftRows = medianInt(headShifts)
	} else {
		report.GlobalShiftRows = alignmentShift
	}
	for _, m := range bestMatches {
		if len(report.ShiftCurve) == 0 || m.ai%5 == 0 {
			report.ShiftCurve = append(report.ShiftCurve, ShiftPoint{ARow: m.aRow, Shift: m.shift})
		}
	}

	missing := collectMissing(a, b, req.StartRow, req.EndRow)
	for _, m := range missing {
		f := Finding{
			ID: "missing-" + intervalID(m.AInterval), Kind: KindMissingSheet, Classification: "physical_missing_page",
			Label: "实物缺页", Severity: "coverage", StartRow: m.AInterval.Start, EndRow: m.AInterval.End,
			AInterval: m.AInterval, BInterval: m.BInterval, RangeA: m.AInterval, RangeB: m.BInterval,
			Summary: m.note, Confidence: 1,
		}
		f.AddressA = addressAt(a, m.AInterval.Start, req.Profile)
		f.AddressB = addressAt(b, m.BInterval.Start, req.ProfileB)
		f.LinksA = intervalLinks(a, m.AInterval, req.Profile)
		f.LinksB = intervalLinks(b, m.BInterval, req.ProfileB)
		report.MissingPages = append(report.MissingPages, f)
		report.Findings = append(report.Findings, f)
	}

	allChanges = filterMissing(allChanges, missing)
	clusters := clusterChanges(allChanges)
	var transientRepeats []repeatResult
	clusters, transientRepeats = cancelTransientClusters(clusters)
	for i, cluster := range clusters {
		f := buildSuspect(i+1, cluster, a, b, req)
		report.Suspects = append(report.Suspects, f)
		if f.Kind == KindInsertion {
			report.Insertions = append(report.Insertions, f)
		} else {
			report.Deletions = append(report.Deletions, f)
		}
		report.Findings = append(report.Findings, f)
	}

	repeats := append(transientRepeats, detectNormalRepeats(la, lb, report.GlobalShiftRows)...)
	repeats = filterRepeatsMissing(repeats, missing)
	for i, rr := range repeats {
		summary := "两段声槽起落型相同且两份试扫的行偏移保持稳定，按正常重复声纹排除，不作为插行/丢行证据。"
		if rr.transient {
			summary = "该处出现一对大小相近、方向相反且很快复原的偏移；更符合重复声纹造成的对齐歧义，已从送片插行/丢行嫌疑中排除。若需要，可复听并点查两个原页区间。"
		}
		f := Finding{
			ID: fmt.Sprintf("repeat-%d", i+1), Kind: KindNormalRepeat, Classification: "normal_repeated_voiceprint",
			Label: "正常重复声纹（非送片插行）", Severity: "information",
			StartRow: rr.a.Start, EndRow: rr.a.End, AInterval: rr.a, BInterval: rr.b,
			RangeA: rr.a, RangeB: rr.b, Confidence: rr.score,
			Summary: summary,
		}
		f.AddressA = addressAt(a, rr.a.Start, req.Profile)
		f.AddressB = addressAt(b, rr.b.Start, req.ProfileB)
		f.LinksA = intervalLinks(a, rr.a, req.Profile)
		f.LinksB = intervalLinks(b, rr.b, req.ProfileB)
		report.NormalRepeats = append(report.NormalRepeats, f)
		report.Findings = append(report.Findings, f)
	}

	if absInt(report.GlobalShiftRows) >= 1 {
		s := report.GlobalShiftRows
		a0 := req.StartRow
		b0 := req.StartRow + s
		f := Finding{
			ID: "head-offset", Kind: KindHeadOffset, Classification: "reel_head_offset",
			Label: "卷首整体偏移", Severity: "information", StartRow: a0, EndRow: a0 + 1,
			AInterval: Interval{Start: a0, End: a0 + 1}, BInterval: Interval{Start: b0, End: b0 + 1},
			RangeA: Interval{Start: a0, End: min(req.EndRow, a0+64)}, RangeB: Interval{Start: b0, End: b0 + min(req.EndRow-req.StartRow, 64)},
			Confidence: 0.82,
			Summary:    fmt.Sprintf("两份试扫从卷首起存在约 %d 行的固定位移；这是整体对拍原点，不计为局部插行或丢行。清单 lead_rows 差为 %d。", s, report.LeadDifference),
		}
		f.AddressA = addressAt(a, a0, req.Profile)
		f.AddressB = addressAt(b, b0, req.ProfileB)
		report.Findings = append(report.Findings, f)
	}
	for _, slice := range [][]Finding{report.Findings, report.Suspects, report.Insertions, report.Deletions, report.MissingPages, report.NormalRepeats} {
		for i := range slice {
			slice[i].Type = slice[i].Kind
			slice[i].Verdict = shortVerdict(slice[i].Kind)
		}
	}
	return report, nil
}

func usableProfile(r *capture.Reel) string {
	for _, p := range r.Profiles {
		if _, err := gauge.Measure(r, p.ID); err == nil {
			return p.ID
		}
	}
	return ""
}

func shortVerdict(kind string) string {
	switch kind {
	case KindInsertion:
		return "inserted_or_duplicated"
	case KindDeletion:
		return "dropped_or_skipped"
	case KindHeadOffset:
		return "head_offset"
	case KindMissingSheet:
		return "physical_missing_page"
	case KindNormalRepeat:
		return "normal_repeated_voiceprint"
	default:
		return kind
	}
}

func sideInfo(r *capture.Reel) Side {
	return Side{Name: r.Name, Rows: r.Rows, Frames: r.Frames, TotalRows: r.Rows * r.Frames, FPS: r.FPS, LeadRows: r.Lead, Digest: r.Digest}
}

type missingPair struct {
	AInterval Interval
	BInterval Interval
	note      string
}

func collectMissing(a, b *capture.Reel, start, end int) []missingPair {
	frameLo := start / a.Rows
	frameHi := (end-1)/a.Rows + 1
	var out []missingPair
	for f := frameLo; f < frameHi; f++ {
		_, okA := a.Sheets[f]
		_, okB := b.Sheets[f]
		if okA && okB {
			continue
		}
		lo := max(start, f*a.Rows)
		hi := min(end, (f+1)*a.Rows)
		if hi <= lo {
			continue
		}
		note := ""
		switch {
		case !okA && !okB:
			note = fmt.Sprintf("第 %d 页在两份试扫中都缺失；这是实物缺页，不能当作跳行。", f)
		case !okA:
			note = fmt.Sprintf("A 卷第 %d 页实物缺失，B 卷同位置有页；已将该段与送片行距异常分开。", f)
		default:
			note = fmt.Sprintf("B 卷第 %d 页实物缺失，A 卷同位置有页；已将该段与送片行距异常分开。", f)
		}
		out = append(out, missingPair{AInterval: Interval{lo, hi}, BInterval: Interval{lo, hi}, note: note})
	}
	return out
}

func overlapsMissing(rc rawChange, missing []missingPair) bool {
	ci := Interval{Start: rc.a0, End: rc.a1 + 1}
	for _, m := range missing {
		if overlapRows(ci, m.AInterval) > 4 || overlapRows(ci, m.BInterval) > 4 {
			return true
		}
	}
	return false
}

func filterMissing(changes []rawChange, missing []missingPair) []rawChange {
	var out []rawChange
	for _, rc := range changes {
		if overlapsMissing(rc, missing) {
			rc.evidenceStatus = EvidenceMissing
			rc.note = "变化区间与实物缺页重叠，不作插行/丢行结论。"
		}
		out = append(out, rc)
	}
	return out
}

func filterRepeatsMissing(repeats []repeatResult, missing []missingPair) []repeatResult {
	var out []repeatResult
	for _, rr := range repeats {
		blocked := false
		for _, m := range missing {
			if overlapRows(rr.a, m.AInterval) > 4 || overlapRows(rr.b, m.BInterval) > 4 {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, rr)
		}
	}
	return out
}

func overlapRows(x, y Interval) int {
	return max(0, min(x.End, y.End)-max(x.Start, y.Start))
}

func intervalGap(x, y Interval) int {
	if overlapRows(x, y) > 0 {
		return 0
	}
	if x.End <= y.Start {
		return y.Start - x.End
	}
	return x.Start - y.End
}

type changeCluster []rawChange

func clusterChanges(changes []rawChange) []changeCluster {
	var clusters []changeCluster
	used := make([]bool, len(changes))
	for i, rc := range changes {
		if used[i] || rc.evidenceStatus == EvidenceMissing {
			continue
		}
		cl := changeCluster{rc}
		used[i] = true
		for j := i + 1; j < len(changes); j++ {
			if used[j] || changes[j].evidenceStatus == EvidenceMissing {
				continue
			}
			other := changes[j]
			if !sameSign(rc.delta, other.delta) {
				continue
			}
			if absInt(rc.delta-other.delta) > max(3, absInt(rc.delta)/3) {
				continue
			}
			ai := Interval{Start: rc.a0, End: rc.a1 + 1}
			aj := Interval{Start: other.a0, End: other.a1 + 1}
			bi := Interval{Start: rc.b0, End: rc.b1 + 1}
			bj := Interval{Start: other.b0, End: other.b1 + 1}
			proximity := max(160, absInt(rc.delta)*4)
			if intervalGap(ai, aj) <= proximity && intervalGap(bi, bj) <= proximity {
				cl = append(cl, other)
				used[j] = true
			}
		}
		clusters = append(clusters, cl)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i][0].a0 < clusters[j][0].a0 })
	return clusters
}

func clusterHull(cl changeCluster) (Interval, Interval) {
	var ai, bi Interval
	for i, rc := range cl {
		a := Interval{rc.a0, rc.a1 + 1}
		b := Interval{rc.b0, rc.b1 + 1}
		if i == 0 {
			ai, bi = a, b
		} else {
			ai.Start, ai.End = min(ai.Start, a.Start), max(ai.End, a.End)
			bi.Start, bi.End = min(bi.Start, b.Start), max(bi.End, b.End)
		}
	}
	return ai, bi
}

func cancelTransientClusters(clusters []changeCluster) ([]changeCluster, []repeatResult) {
	var kept []changeCluster
	var repeats []repeatResult
	for i := 0; i < len(clusters); i++ {
		if i+1 < len(clusters) {
			x, y := clusters[i], clusters[i+1]
			dx, dy := clusterDelta(x), clusterDelta(y)
			ax, bx := clusterHull(x)
			ay, by := clusterHull(y)
			opposite := (dx > 0) != (dy > 0)
			similar := absInt(absInt(dx)-absInt(dy)) <= max(4, absInt(dx)/5)
			gap := max(intervalGap(ax, ay), intervalGap(bx, by))
			// A self-similarity excursion returns within a short phrase. Do
			// not cancel two independent faults separated by a long interval.
			gapLimit := max(360, absInt(dx)*8)
			if opposite && similar && gap <= gapLimit {
				repeats = append(repeats, repeatResult{
					a:         Interval{Start: min(ax.Start, ay.Start), End: max(ax.End, ay.End)},
					b:         Interval{Start: min(bx.Start, by.Start), End: max(bx.End, by.End)},
					score:     0.86,
					transient: true,
				})
				i++
				continue
			}
		}
		kept = append(kept, clusters[i])
	}
	return kept, repeats
}

func clusterDelta(cl changeCluster) int {
	ds := make([]int, len(cl))
	for i, rc := range cl {
		ds[i] = rc.delta
	}
	return medianInt(ds)
}

func buildSuspect(num int, cl changeCluster, a, b *capture.Reel, req Request) Finding {
	deltaValues := make([]int, 0, len(cl))
	var aI, bI Interval
	var focusA Interval
	haveFocus := false
	bestByLane := map[int]rawChange{}
	for _, rc := range cl {
		deltaValues = append(deltaValues, rc.delta)
		if aI.Start == 0 && aI.End == 0 {
			aI = Interval{rc.a0, rc.a1 + 1}
			bI = Interval{rc.b0, rc.b1 + 1}
		} else {
			aI.Start = min(aI.Start, rc.a0)
			aI.End = max(aI.End, rc.a1+1)
			bI.Start = min(bI.Start, rc.b0)
			bI.End = max(bI.End, rc.b1+1)
		}
		if prior, ok := bestByLane[rc.lane]; !ok || rc.score > prior.score || (rc.evidenceStatus == EvidencePrecise && prior.evidenceStatus != EvidencePrecise) {
			bestByLane[rc.lane] = rc
		}
	}
	lanes := make([]int, 0, len(bestByLane))
	for lane := range bestByLane {
		lanes = append(lanes, lane)
	}
	sort.Ints(lanes)
	confidence := 0.0
	preciseLanes := 0
	evidence := make([]Evidence, 0, len(lanes))
	for _, lane := range lanes {
		rc := bestByLane[lane]
		if rc.evidenceStatus == EvidencePrecise {
			preciseLanes++
		}
		if rc.plausibleA.End > rc.plausibleA.Start {
			if !haveFocus {
				focusA = rc.plausibleA
				haveFocus = true
			} else {
				inter := Interval{Start: max(focusA.Start, rc.plausibleA.Start), End: min(focusA.End, rc.plausibleA.End)}
				if inter.End > inter.Start {
					focusA = inter
				}
			}
		}
		confidence += rc.score
		ev := Evidence{
			Lane: rc.lane, Status: rc.evidenceStatus, Note: rc.note, DeltaRows: rc.delta,
			BeforeShiftRows: rc.beforeShift, AfterShiftRows: rc.afterShift,
			AInterval: Interval{rc.a0, rc.a1 + 1}, BInterval: Interval{rc.b0, rc.b1 + 1},
			Score: rc.score, Matches: rc.matches,
		}
		if rc.plausibleA.End > rc.plausibleA.Start {
			ev.PlausibleA = rc.plausibleA
			ev.PrecisionRows = rc.precisionRows
		}
		evidence = append(evidence, ev)
	}
	delta := medianInt(deltaValues)
	if len(lanes) > 0 {
		confidence /= float64(len(lanes))
	}
	if len(lanes) == 2 {
		confidence += 0.12
	}
	fusedNarrow := haveFocus && focusA.End-focusA.Start <= 12
	if preciseLanes > 0 || fusedNarrow {
		confidence += 0.06
	}
	confidence = min(0.96, confidence)
	if haveFocus {
		// Row-level refinement searches slightly beyond the landmark gap;
		// keep the advertised broad interval consistent with its focus.
		aI.Start = min(aI.Start, focusA.Start)
		aI.End = max(aI.End, focusA.End)
	}
	kind := KindInsertion
	label := "可疑插行/重复行区间"
	if delta < 0 {
		kind = KindDeletion
		label = "可疑丢行/跳行区间"
	}
	scope := "声槽证据只能围出一个可怀疑范围"
	switch {
	case len(lanes) == 2 && preciseLanes == 1:
		scope = "一个声槽受重复声纹影响只能给范围，另一个声槽提供了更准证据"
	case fusedNarrow:
		scope = "两个声槽的可行边界相交，把范围收窄到少量扫描行"
	case preciseLanes >= 1:
		scope = "至少一个声槽给出较准边界"
	}
	f := Finding{
		ID: fmt.Sprintf("suspect-%d", num), Kind: kind, Classification: kind, Label: label,
		Severity: "suspect", Confidence: confidence, DeltaRows: delta,
		StartRow: aI.Start, EndRow: aI.End, AInterval: aI, BInterval: bI,
		RangeA: aI, RangeB: bI, Evidence: evidence,
		Summary: fmt.Sprintf("B 相对 A 的行偏移在该处变化 %+d 行；%s。结论保留可怀疑区间，不把正常重复声纹或缺页计入。", delta, scope),
	}
	if haveFocus {
		f.FocusA = &focusA
	}
	f.AddressA = addressAt(a, aI.Start, req.Profile)
	f.AddressB = addressAt(b, bI.Start, req.ProfileB)
	f.LinksA = intervalLinks(a, aI, req.Profile)
	f.LinksB = intervalLinks(b, bI, req.ProfileB)
	return f
}

func intervalID(i Interval) string { return fmt.Sprintf("%d-%d", i.Start, i.End) }

func addressAt(r *capture.Reel, row int, profile string) *Address {
	total := r.Rows * r.Frames
	a := &Address{Row: row}
	if row < 0 || row >= total {
		a.SheetAvailable = false
		return a
	}
	a.Valid = true
	a.Frame = row / r.Rows
	a.RowInFrame = row % r.Rows
	_, a.SheetAvailable = r.Sheets[a.Frame]
	a.StripURL = fmt.Sprintf("/lightbench/%s/strip?frame=%d", url.PathEscape(r.Name), a.Frame)
	pr := row - r.Lead
	a.PictureRow = pr
	if pr >= 0 && pr < total {
		a.PictureValid = true
		a.PictureFrame = pr / r.Rows
		a.PictureRowInFrame = pr % r.Rows
		start := max(0, a.PictureFrame-2)
		end := min(r.Frames, max(a.PictureFrame+3, start+1))
		if end-start > 48 {
			start = max(0, end-48)
		}
		q := url.Values{}
		q.Set("frame", fmt.Sprint(a.PictureFrame))
		q.Set("row", fmt.Sprint(a.PictureRowInFrame))
		q.Set("start", fmt.Sprint(start))
		q.Set("end", fmt.Sprint(end))
		if profile != "" {
			q.Set("profile", profile)
		}
		a.WorkbenchURL = fmt.Sprintf("/lightbench/%s?%s", url.PathEscape(r.Name), q.Encode())
	}
	pad := 2 * r.Rows
	lo := max(0, row-pad)
	hi := min(total, row+pad+1)
	q := url.Values{}
	q.Set("start_row", fmt.Sprint(lo))
	q.Set("end_row", fmt.Sprint(hi))
	if profile != "" {
		q.Set("profile", profile)
	}
	a.AuditionURL = fmt.Sprintf("/captures/%s/row-audition?%s", url.PathEscape(r.Name), q.Encode())
	return a
}

func intervalLinks(r *capture.Reel, in Interval, profile string) LinkSet {
	total := r.Rows * r.Frames
	pad := 2 * r.Rows
	lo := max(0, in.Start-pad)
	hi := min(total, in.End+pad)
	if hi <= lo {
		hi = min(total, lo+1)
	}
	q := url.Values{}
	q.Set("start_row", fmt.Sprint(lo))
	q.Set("end_row", fmt.Sprint(hi))
	if profile != "" {
		q.Set("profile", profile)
	}
	l := LinkSet{Reel: r.Name, AuditionURL: fmt.Sprintf("/captures/%s/row-audition?%s", url.PathEscape(r.Name), q.Encode())}
	addr := addressAt(r, in.Start, profile)
	l.WorkbenchURL = addr.WorkbenchURL
	l.StripURL = addr.StripURL
	return l
}
