package diffreview

import (
	"fmt"
	"math"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

type segment struct {
	z0, z1  int64
	x0, x1  float64
	frame   int
	statusA string
	statusB string
	readA   bool
	readB   bool
	mark    string
	cov     *CoverageEvidence
	aco     *AcousticEvidence
	ali     *AlignmentNote
}

// scaleD is the common integer scale: one frame equals D units. A absolute
// picture row p maps to z = p*rowsB; B row q maps to z = q*rowsA.
func scaleD(a, b *Source) int64 {
	return int64(a.Index.Rows) * int64(b.Index.Rows)
}

func segmentAndMeasure(rep *Report, l, u int) []segment {
	a, b := rep.src[0], rep.src[1]
	ra, rb := a.Index.Rows, b.Index.Rows
	D := scaleD(a, b)
	zL, zU := int64(l)*D, int64(u)*D

	points := map[int64]bool{zL: true, zU: true}
	for f := l; f <= u; f++ {
		points[int64(f)*D] = true
	}
	for _, run := range a.Index.Coverage {
		for _, z := range []int64{int64(run.Start) * int64(rb), int64(run.End) * int64(rb)} {
			if z >= zL && z <= zU {
				points[z] = true
			}
		}
	}
	for _, run := range b.Index.Coverage {
		for _, z := range []int64{int64(run.Start) * int64(ra), int64(run.End) * int64(ra)} {
			if z >= zL && z <= zU {
				points[z] = true
			}
		}
	}
	zs := make([]int64, 0, len(points))
	for z := range points {
		zs = append(zs, z)
	}
	sort.Slice(zs, func(i, j int) bool { return zs[i] < zs[j] })

	findings := make([]segment, 0, len(zs)-1)
	for k := 0; k+1 < len(zs); k++ {
		z0, z1 := zs[k], zs[k+1]
		if z1 <= z0 {
			continue
		}
		zm := (z0 + z1) / 2
		rowA := int(zm / int64(rb))
		rowB := int(zm / int64(ra))
		stA := statusAt(a.Index.Coverage, rowA)
		stB := statusAt(b.Index.Coverage, rowB)
		x0 := float64(z0) / float64(D)
		x1 := float64(z1) / float64(D)
		frame := int(z0 / D)
		if frame < l {
			frame = l
		}
		seg := segment{
			z0: z0, z1: z1, x0: x0, x1: x1, frame: frame,
			statusA: stA, statusB: stB,
			readA: stA == register.Readable, readB: stB == register.Readable,
		}
		classifySegment(rep, &seg)
		findings = append(findings, seg)
	}
	return findings
}

func statusAt(runs []register.Run, row int) string {
	for _, run := range runs {
		if row >= run.Start && row < run.End {
			return run.Status
		}
	}
	return "outside_window"
}

func classifySegment(rep *Report, seg *segment) {
	a, b := rep.src[0], rep.src[1]
	fps := a.Index.FPS
	if fps != b.Index.FPS {
		fps = a.Index.FPS // frame-coordinate reporting; cues/seconds stay per-side
	}
	span := makeSpan(seg.x0, seg.x1, fps)
	cues := CuePairAt(rep, seg.x0)

	if !(seg.readA && seg.readB) {
		if seg.statusA != seg.statusB {
			audibleXor := seg.readA != seg.readB
			seg.mark = MarkReadability
			seg.cov = &CoverageEvidence{
				Kind: "coverage_change", Frame: seg.frame, Span: span,
				StatusA: seg.statusA, StatusB: seg.statusB,
				StatusAZh: statusZh(seg.statusA), StatusBZh: statusZh(seg.statusB),
				ReadableA: seg.readA, ReadableB: seg.readB,
				AudibleStateChanged: audibleXor,
				Confirmation:        "register.json coverage partitions intersect on frame time",
				Cues:                cues,
			}
			return
		}
		// Same non-readable status on both sides: equivalent silence by coverage.
		seg.mark = MarkSame
		return
	}

	// Both sides readable: gap zeros are not in play; measure acoustics.
	if rep.Timebase.SameGrid {
		classifyExact(rep, seg, span, cues)
	} else {
		classifyResampled(rep, seg, span, cues)
	}
}

func statusZh(s string) string {
	if z, ok := StatusZh[s]; ok {
		return z
	}
	return s
}

func pcmRangeForX(src *Source, x0, x1 float64) (int, int) {
	r := src.Index.Rows
	first := src.Index.Window.Start * r
	i0 := int(math.Round(x0*float64(r))) - first
	i1 := int(math.Round(x1*float64(r))) - first
	i0 = clampInt(i0, 0, src.Header.Frames)
	i1 = clampInt(i1, 0, src.Header.Frames)
	if i1 < i0 {
		i1 = i0
	}
	return i0, i1
}

func classifyExact(rep *Report, seg *segment, span Span, cues CuePair) {
	a, b := rep.src[0], rep.src[1]
	i0a, i1a := pcmRangeForX(a, seg.x0, seg.x1)
	i0b, i1b := pcmRangeForX(b, seg.x0, seg.x1)
	n := minInt(i1a-i0a, i1b-i0b)
	if n <= 0 {
		seg.mark = MarkSame
		return
	}
	var changed [2]int
	var peak, sumSq, sumA, sumB [2]float64
	for k := 0; k < n; k++ {
		for lane := 0; lane < 2; lane++ {
			va := float64(a.PCM[2*(i0a+k)+lane])
			vb := float64(b.PCM[2*(i0b+k)+lane])
			d := va - vb
			if va != vb {
				changed[lane]++
			}
			peak[lane] = math.Max(peak[lane], math.Abs(d))
			sumSq[lane] += d * d
			sumA[lane] += va * va
			sumB[lane] += vb * vb
		}
	}
	aco := &AcousticEvidence{
		Kind: "acoustic_comparison", Frame: seg.frame, Span: span,
		Basis: "sample_exact", ComparisonRate: a.Header.Rate,
		ComparedFrames: n, ChangedSamples: changed, Cues: cues,
		Conclusion: MarkSame, EvidenceSufficient: true,
	}
	for lane := 0; lane < 2; lane++ {
		aco.RMSDelta[lane] = math.Sqrt(sumSq[lane] / float64(n))
		aco.RMSA[lane] = math.Sqrt(sumA[lane] / float64(n))
		aco.RMSB[lane] = math.Sqrt(sumB[lane] / float64(n))
		aco.PeakDelta[lane] = peak[lane]
		aco.RMSDeltaNorm[lane] = aco.RMSDelta[lane] / 32768.0
		aco.PeakDeltaNorm[lane] = aco.PeakDelta[lane] / 32767.0
		if n > 0 {
			aco.ChangedFraction[lane] = float64(changed[lane]) / float64(n)
		}
	}
	totalChanged := changed[0] + changed[1]
	if totalChanged > 0 {
		aco.Conclusion = MarkContent
		seg.mark = MarkContent
	} else {
		seg.mark = MarkSame
	}
	seg.aco = aco
}

// interpolatedSample returns stereo PCM at fractional source-frame index u.
func interpolatedSample(pcm []int16, frames int, u float64) (float64, float64) {
	if frames <= 0 {
		return 0, 0
	}
	if u <= 0 {
		return float64(pcm[0]), float64(pcm[1])
	}
	if u >= float64(frames-1) {
		i := 2 * (frames - 1)
		return float64(pcm[i]), float64(pcm[i+1])
	}
	i := int(math.Floor(u))
	frac := u - float64(i)
	l0 := float64(pcm[2*i])
	l1 := float64(pcm[2*(i+1)])
	r0 := float64(pcm[2*i+1])
	r1 := float64(pcm[2*(i+1)+1])
	return l0 + (l1-l0)*frac, r0 + (r1-r0)*frac
}

// sampleAtFrameTime evaluates source S at continuous absolute frame coord x.
func sampleAtFrameTime(s *Source, x float64) (float64, float64) {
	u := (x-float64(s.Index.Window.Start))*float64(s.Index.Rows) - 0.5
	return interpolatedSample(s.PCM, s.Header.Frames, u)
}

func classifyResampled(rep *Report, seg *segment, span Span, cues CuePair) {
	a, b := rep.src[0], rep.src[1]
	fps := a.Index.FPS
	rMax := rep.rc / fps
	if rMax <= 0 {
		rMax = maxInt(a.Index.Rows, b.Index.Rows)
	}
	m := int(math.Round((seg.x1 - seg.x0) * float64(rMax)))
	if m < 1 {
		m = 1
	}
	var peak, sumSq, sumA, sumB [2]float64
	for k := 0; k < m; k++ {
		x := seg.x0 + (float64(k)+0.5)/float64(rMax)
		la, ra2 := sampleAtFrameTime(a, x)
		lb, rb2 := sampleAtFrameTime(b, x)
		va := [2]float64{la, ra2}
		vb := [2]float64{lb, rb2}
		for lane := 0; lane < 2; lane++ {
			d := va[lane] - vb[lane]
			peak[lane] = math.Max(peak[lane], math.Abs(d))
			sumSq[lane] += d * d
			sumA[lane] += va[lane] * va[lane]
			sumB[lane] += vb[lane] * vb[lane]
		}
	}
	opt := rep.opt
	aco := &AcousticEvidence{
		Kind: "acoustic_comparison", Frame: seg.frame, Span: span,
		Basis: "resampled_continuous_time", ComparisonRate: rep.rc,
		ComparedFrames: m, Cues: cues,
	}
	minSamples := int(opt.MinSpanSeconds * float64(rep.rc))
	strongChange := false
	confidentSame := m >= minSamples
	for lane := 0; lane < 2; lane++ {
		aco.RMSDelta[lane] = math.Sqrt(sumSq[lane] / float64(m))
		aco.RMSA[lane] = math.Sqrt(sumA[lane] / float64(m))
		aco.RMSB[lane] = math.Sqrt(sumB[lane] / float64(m))
		aco.PeakDelta[lane] = peak[lane]
		aco.RMSDeltaNorm[lane] = aco.RMSDelta[lane] / 32768.0
		aco.PeakDeltaNorm[lane] = aco.PeakDelta[lane] / 32767.0
		if aco.RMSDeltaNorm[lane] >= opt.ChangeRMS || aco.PeakDeltaNorm[lane] >= opt.ChangePeak {
			strongChange = true
		}
		if aco.RMSDeltaNorm[lane] > opt.SameRMS || aco.PeakDeltaNorm[lane] > opt.SamePeak {
			confidentSame = false
		}
	}

	switch {
	case strongChange:
		aco.Conclusion = MarkContent
		aco.EvidenceSufficient = true
		seg.mark = MarkContent
		seg.aco = aco
	case confidentSame:
		aco.Conclusion = MarkSame
		aco.EvidenceSufficient = true
		seg.mark = MarkSame
		seg.aco = aco
	default:
		reason := "acoustic_evidence_inconclusive"
		detail := "异网格重采样后的声学差异落在一致与确认变化阈值之间，证据不足，不强行判定内容是否相同。"
		if m < minSamples {
			reason = "joint_readable_span_too_short"
			detail = "两包在该位置的共同可读时间跨度过短，不足以在异网格上确认声学一致性。"
		}
		aco.Conclusion = "inconclusive"
		aco.EvidenceSufficient = false
		aco.Reason = reason
		seg.mark = MarkUnaligned
		seg.aco = aco
		seg.ali = &AlignmentNote{
			Frame: seg.frame, Span: span, Reason: reason, Detail: detail, Cues: cues,
		}
	}
}

func buildFrameMarks(rep *Report, findings []segment, l, u int) {
	rep.Frames = make([]FrameMark, 0, u-l)
	for f := l; f < u; f++ {
		mark := MarkSame
		fm := FrameMark{
			Frame: f, MarkZh: MarkZh[MarkSame],
			GridAligned: rep.Timebase.SameGrid,
			Cues:        CuePairAt(rep, float64(f)),
		}
		for i := range findings {
			s := &findings[i]
			if s.frame != f {
				continue
			}
			if s.cov != nil {
				fm.CoverageChanges = append(fm.CoverageChanges, *s.cov)
			}
			if s.aco != nil && s.aco.Conclusion != MarkSame {
				fm.Acoustic = append(fm.Acoustic, *s.aco)
			}
			if s.ali != nil {
				fm.Unaligned = append(fm.Unaligned, *s.ali)
			}
			switch s.mark {
			case MarkContent:
				mark = MarkContent
			case MarkReadability:
				if mark != MarkContent {
					mark = MarkReadability
				}
			case MarkUnaligned:
				if mark != MarkContent && mark != MarkReadability {
					mark = MarkUnaligned
				}
			}
		}
		fm.Mark = mark
		fm.Verdict = mark
		fm.MarkZh = MarkZh[mark]
		rep.Frames = append(rep.Frames, fm)
	}
}

type cluster struct {
	segs []int
	z0   int64
	z1   int64
}

func buildRegions(rep *Report, findings []segment) {
	opt := rep.opt
	D := scaleD(rep.src[0], rep.src[1])
	fps := rep.src[0].Index.FPS
	mergeZ := int64(0)
	if opt.MergeGapSeconds > 0 {
		mergeZ = int64(opt.MergeGapSeconds * float64(fps) * float64(D))
	}

	actionable := make([]int, 0)
	for i := range findings {
		if findings[i].mark != MarkSame {
			actionable = append(actionable, i)
		}
	}
	if len(actionable) == 0 {
		return
	}

	clusters := make([]cluster, 0)
	cur := cluster{segs: []int{actionable[0]}, z0: findings[actionable[0]].z0, z1: findings[actionable[0]].z1}
	for _, idx := range actionable[1:] {
		s := findings[idx]
		if s.z0 <= cur.z1+mergeZ {
			cur.segs = append(cur.segs, idx)
			cur.z1 = maxInt64(cur.z1, s.z1)
		} else {
			clusters = append(clusters, cur)
			cur = cluster{segs: []int{idx}, z0: s.z0, z1: s.z1}
		}
	}
	clusters = append(clusters, cur)

	rep.Regions = make([]Region, 0, len(clusters))
	for ci, cl := range clusters {
		r := Region{ID: fmt.Sprintf("R%02d", ci+1), GridAligned: rep.Timebase.SameGrid}
		mark := MarkSame
		markSet := map[string]bool{}
		frames := map[int]bool{}
		for _, si := range cl.segs {
			s := &findings[si]
			frames[s.frame] = true
			markSet[s.mark] = true
			if s.cov != nil {
				r.CoverageEvidence = append(r.CoverageEvidence, *s.cov)
			}
			if s.aco != nil && s.aco.Conclusion != MarkSame {
				r.AcousticEvidence = append(r.AcousticEvidence, *s.aco)
			}
			if s.ali != nil {
				r.Unaligned = append(r.Unaligned, *s.ali)
			}
			switch s.mark {
			case MarkContent:
				mark = MarkContent
			case MarkReadability:
				if mark != MarkContent {
					mark = MarkReadability
				}
			case MarkUnaligned:
				if mark != MarkContent && mark != MarkReadability {
					mark = MarkUnaligned
				}
			}
		}
		// If cluster started from a lower-precedence mark but later found content.
		if mark == MarkSame {
			if markSet[MarkReadability] {
				mark = MarkReadability
			} else if markSet[MarkUnaligned] {
				mark = MarkUnaligned
			}
		}
		for _, m := range []string{MarkContent, MarkReadability, MarkUnaligned} {
			if markSet[m] && m != mark {
				r.AlsoMarks = append(r.AlsoMarks, m)
			}
		}
		for f := range frames {
			r.Frames = append(r.Frames, f)
		}
		sort.Ints(r.Frames)
		x0 := float64(cl.z0) / float64(D)
		x1 := float64(cl.z1) / float64(D)
		r.Span = makeSpan(x0, x1, fps)
		padFrames := opt.PadSeconds * float64(fps)
		l := rep.Overlap.Start
		u := rep.Overlap.End
		cx0 := math.Max(float64(l), x0-padFrames)
		cx1 := math.Min(float64(u), x1+padFrames)
		r.ClipSpan = makeSpan(cx0, cx1, fps)
		r.Mark = mark
		r.Verdict = mark
		r.MarkZh = MarkZh[mark]
		r.Cues = CuePairAt(rep, x0)
		r.ListeningHintZh = hintFor(mark, r.AlsoMarks)
		rep.Regions = append(rep.Regions, r)

		for fi := range rep.Frames {
			for _, f := range r.Frames {
				if rep.Frames[fi].Frame == f && rep.Frames[fi].RegionID == "" {
					rep.Frames[fi].RegionID = r.ID
				}
			}
		}
	}

	// Keep padded handles from bleeding across adjacent extracted regions:
	// clamp each clip span to the gap between its neighboring exact spans.
	for i := range rep.Regions {
		cx0 := rep.Regions[i].ClipSpan.FrameStart
		cx1 := rep.Regions[i].ClipSpan.FrameEnd
		if i > 0 {
			cx0 = math.Max(cx0, rep.Regions[i-1].Span.FrameEnd)
		}
		if i+1 < len(rep.Regions) {
			cx1 = math.Min(cx1, rep.Regions[i+1].Span.FrameStart)
		}
		if cx1 < rep.Regions[i].Span.FrameEnd {
			cx1 = rep.Regions[i].Span.FrameEnd
		}
		if cx0 > rep.Regions[i].Span.FrameStart {
			cx0 = rep.Regions[i].Span.FrameStart
		}
		rep.Regions[i].ClipSpan = makeSpan(cx0, cx1, fps)
	}
}

func hintFor(mark string, also []string) string {
	base := map[string]string{
		MarkContent:     "两包在该区间均有覆盖，声学计量确认内容差异；请对照 A/B 原包片段与计量值复听。",
		MarkReadability: "覆盖分区确认可读性不同；不可读侧的零值是覆盖占位而非声学内容，请核对缺页/卷外状态。",
		MarkUnaligned:   "异网格且证据不足，未强行判定内容异同；请用两侧 cue 回到两个原包的 sound.wav 位置人工复听。",
	}[mark]
	if len(also) > 0 {
		base += " 同区还包含："
		for _, m := range also {
			base += MarkZh[m] + "；"
		}
	}
	return base
}

func buildExclusive(a, b *Source, l, u int) []ExclusiveSpan {
	var out []ExclusiveSpan
	wa, wb := a.Index.Window, b.Index.Window
	add := func(side string, s *Source, fs, fe int) {
		if fe <= fs {
			return
		}
		zh := "该画格区间只在" + sideName(side) + "原包窗口内，不属于共同画格对照范围；仅保留该包复听位置。"
		out = append(out, ExclusiveSpan{
			Side: side, FrameStart: fs, FrameEnd: fe, Frames: fe - fs,
			NoteZh: zh, Cue: cueAt(s, float64(fs), side),
		})
	}
	if wa.Start < l {
		add("a", a, wa.Start, minInt(wa.End, l))
	}
	if wa.End > u {
		add("a", a, maxInt(wa.Start, u), wa.End)
	}
	if wb.Start < l {
		add("b", b, wb.Start, minInt(wb.End, l))
	}
	if wb.End > u {
		add("b", b, maxInt(wb.Start, u), wb.End)
	}
	return out
}

func sideName(side string) string {
	if side == "a" {
		return "A"
	}
	return "B"
}

func fillSummary(rep *Report, findings []segment, l, u int) {
	s := &rep.Summary
	s.OverlapFrames = u - l
	for _, fm := range rep.Frames {
		s.FramesByMark[fm.Mark]++
	}
	s.Regions = len(rep.Regions)
	s.RegionsByMark = map[string]int{}
	for _, r := range rep.Regions {
		s.RegionsByMark[r.Mark]++
	}
	for _, seg := range findings {
		switch seg.mark {
		case MarkSame:
			s.SameIntervals++
		case MarkUnaligned:
			s.UnalignedIntervals++
		}
		if seg.cov != nil {
			s.CoverageChanges++
		}
		if seg.aco != nil && seg.aco.Conclusion == MarkContent {
			s.AcousticChanges++
		}
	}
	for _, ex := range rep.Exclusive {
		if ex.Side == "a" {
			s.ExclusiveFramesA += ex.Frames
		} else {
			s.ExclusiveFramesB += ex.Frames
		}
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
