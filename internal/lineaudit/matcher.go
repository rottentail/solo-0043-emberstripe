package lineaudit

import (
	"math"
	"sort"
)

type match struct {
	ai, bi int
	aRow   int
	bRow   int
	shift  int
	cost   float64
}

type rawChange struct {
	lane           int
	delta          int
	beforeShift    int
	afterShift     int
	a0, a1         int
	b0, b1         int
	matches        int
	score          float64
	bestError      float64
	support        float64
	quality        float64
	valid          bool
	plausibleA     Interval
	precisionRows  int
	evidenceStatus string
	note           string
}

func landmarkCost(a, b landmark) float64 {
	if a.kind != b.kind {
		return math.Inf(1)
	}
	c := 0.70 * math.Min(2, math.Abs(a.level-b.level))
	c += 0.18 * math.Min(2, math.Abs(a.prominence-b.prominence))
	slopeScale := 0.035
	c += 0.06 * math.Min(2, math.Abs(a.leftSlope-b.leftSlope)/slopeScale)
	c += 0.06 * math.Min(2, math.Abs(a.rightSlope-b.rightSlope)/slopeScale)
	return c
}

func estimateShift(la, lb []landmark, a, b laneSignal, startRow int) int {
	type vote struct {
		shift  int
		weight float64
	}
	var votes []vote
	for _, x := range la {
		for _, y := range lb {
			c := landmarkCost(x, y)
			if c < 1.35 {
				votes = append(votes, vote{shift: y.row - x.row, weight: 1 / (1 + c)})
			}
		}
	}
	if len(votes) == 0 {
		return 0
	}
	// Bin by eight rows so one-pivot jitter does not split the true mode.
	bins := map[int]float64{}
	for _, v := range votes {
		bin := int(math.Round(float64(v.shift) / 8))
		bins[bin] += v.weight
	}
	type binVote struct {
		bin    int
		weight float64
	}
	var ranked []binVote
	for k, w := range bins {
		ranked = append(ranked, binVote{k, w})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].weight > ranked[j].weight })
	limit := min(4, len(ranked))
	bestShift, bestScore := 0, math.Inf(1)
	for k := 0; k < limit; k++ {
		center := ranked[k].bin * 8
		var shifts []int
		var weights []float64
		var total float64
		for _, v := range votes {
			if absInt(v.shift-center) <= 5 {
				shifts = append(shifts, v.shift)
				weights = append(weights, v.weight)
				total += v.weight
			}
		}
		if len(shifts) == 0 {
			continue
		}
		shift := weightedMedianInt(shifts, weights)
		score := alignmentError(a, b, startRow, shift)
		// Periodic voiceprints can make a whole-period shift equally valid;
		// prefer the smaller head displacement when quality is tied.
		selection := score + 0.0008*float64(absInt(shift))
		if selection < bestScore {
			bestScore = selection
			bestShift = shift
		}
	}
	return bestShift
}

func weightedMedianInt(values []int, weights []float64) int {
	type pair struct {
		v int
		w float64
	}
	pairs := make([]pair, len(values))
	var total float64
	for i := range values {
		pairs[i] = pair{values[i], weights[i]}
		total += weights[i]
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v < pairs[j].v })
	var acc float64
	for _, p := range pairs {
		acc += p.w
		if acc >= total/2 {
			return p.v
		}
	}
	return pairs[len(pairs)-1].v
}

func alignmentError(a, b laneSignal, startRow, shift int) float64 {
	const samples = 1600
	n := min(len(a.values), len(b.values))
	step := max(1, n/samples)
	var sum float64
	var count int
	for i := 0; i < n; i += step {
		j := i + shift // Both signals use the same absolute start row.
		if j < 0 || j >= n || !a.ok[i] || !b.ok[j] {
			continue
		}
		sum += math.Abs(a.values[i] - b.values[j])
		count++
	}
	if count < 24 {
		return math.Inf(1)
	}
	return sum / float64(count)
}

func matchLandmarks(la, lb []landmark, globalShift, maxDrift int) []match {
	const skipCost = 1.15
	na, nb := len(la), len(lb)
	if na == 0 || nb == 0 {
		return nil
	}
	width := nb + 1
	dp := make([]float64, (na+1)*(nb+1))
	parent := make([]byte, (na+1)*(nb+1))
	idx := func(i, j int) int { return i*width + j }
	for i := 1; i <= na; i++ {
		dp[idx(i, 0)] = float64(i) * skipCost
		parent[idx(i, 0)] = 2
	}
	for j := 1; j <= nb; j++ {
		dp[idx(0, j)] = float64(j) * skipCost
		parent[idx(0, j)] = 3
	}
	for i := 1; i <= na; i++ {
		for j := 1; j <= nb; j++ {
			best := dp[idx(i-1, j)] + skipCost
			p := byte(2) // skip A landmark
			if v := dp[idx(i, j-1)] + skipCost; v < best {
				best = v
				p = 3 // skip B landmark
			}
			ai, bi := la[i-1], lb[j-1]
			shift := bi.row - ai.row
			if absInt(shift-globalShift) <= maxDrift {
				if c := landmarkCost(ai, bi); c < 1.8 {
					if v := dp[idx(i-1, j-1)] + c; v < best {
						best = v
						p = 1
					}
				}
			}
			dp[idx(i, j)] = best
			parent[idx(i, j)] = p
		}
	}
	var rev []match
	i, j := na, nb
	for i > 0 || j > 0 {
		switch parent[idx(i, j)] {
		case 1:
			ai, bi := la[i-1], lb[j-1]
			rev = append(rev, match{ai: i - 1, bi: j - 1, aRow: ai.row, bRow: bi.row, shift: bi.row - ai.row, cost: landmarkCost(ai, bi)})
			i--
			j--
		case 2:
			i--
		default:
			j--
		}
	}
	for l, r := 0, len(rev)-1; l < r; l, r = l+1, r-1 {
		rev[l], rev[r] = rev[r], rev[l]
	}
	return rev
}

func detectChanges(lane int, matches []match, a, b laneSignal, startRow int) []rawChange {
	if len(matches) < 5 {
		return nil
	}
	const window = 4
	type candidate struct {
		cut    int
		delta  int
		before int
		after  int
		score  float64
	}
	var candidates []candidate
	for cut := 2; cut <= len(matches)-2; cut++ {
		lo := max(0, cut-window)
		hi := min(len(matches), cut+window)
		left := shifts(matches[lo:cut])
		right := shifts(matches[cut:hi])
		if len(left) < 2 || len(right) < 2 {
			continue
		}
		before := medianInt(left)
		after := medianInt(right)
		delta := after - before
		if absInt(delta) < 2 {
			continue
		}
		leftMad := medianAbsDev(left, before)
		rightMad := medianAbsDev(right, after)
		if leftMad > 4 || rightMad > 4 {
			continue
		}
		score := float64(absInt(delta)) + 2.5 - 0.6*float64(leftMad+rightMad)
		candidates = append(candidates, candidate{cut: cut, delta: delta, before: before, after: after, score: score})
	}
	// Merge adjacent cut observations into one change point.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	var chosen []candidate
	for _, c := range candidates {
		merged := false
		for k := range chosen {
			if absInt(chosen[k].cut-c.cut) <= window && sameSign(chosen[k].delta, c.delta) && absInt(chosen[k].delta-c.delta) <= max(3, absInt(chosen[k].delta)/4) {
				if c.score > chosen[k].score {
					chosen[k] = c
				}
				merged = true
				break
			}
		}
		if !merged {
			chosen = append(chosen, c)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].cut < chosen[j].cut })
	var changes []rawChange
	for _, c := range chosen {
		m0 := matches[c.cut-1]
		m1 := matches[c.cut]
		rc := rawChange{
			lane: lane, delta: c.delta, beforeShift: c.before, afterShift: c.after,
			a0: m0.aRow, a1: m1.aRow, b0: m0.bRow, b1: m1.bRow,
			matches: len(matches), score: min(1, max(0.15, c.score/12)),
			evidenceStatus: EvidenceRangeOnly,
		}
		refineChange(&rc, a, b, startRow)
		if rc.valid {
			changes = append(changes, rc)
		}
	}
	return changes
}

func refineChange(rc *rawChange, a, b laneSignal, startRow int) {
	const context = 64
	lo, hi := rc.a0-32, rc.a1+64
	if hi <= lo {
		rc.evidenceStatus = EvidenceNoSignal
		return
	}
	best := math.Inf(1)
	bestP := lo
	type scored struct {
		p   int
		err float64
	}
	var good []scored
	eval := func(p int) (float64, bool) {
		before := shiftedContextError(a, b, p-context, p, rc.beforeShift, startRow)
		after := shiftedContextError(a, b, p, p+context, rc.afterShift, startRow)
		if math.IsInf(before, 1) || math.IsInf(after, 1) {
			return 0, false
		}
		return before + after, true
	}
	for p := lo; p <= hi; p++ {
		err, ok := eval(p)
		if !ok {
			continue
		}
		if err < best {
			best, bestP = err, p
		}
	}
	if math.IsInf(best, 1) {
		rc.evidenceStatus = EvidenceMissing
		rc.note = "可疑区间落在缺页或不可读行，无法做行级收窄。"
		return
	}
	for p := lo; p <= hi; p++ {
		err, ok := eval(p)
		if ok && err <= best*1.14+0.035 {
			good = append(good, scored{p, err})
		}
	}
	if len(good) == 0 {
		rc.evidenceStatus = EvidenceMissing
		return
	}
	rc.bestError = best / 2
	rc.quality = 1 / (1 + rc.bestError)
	rc.plausibleA = Interval{Start: good[0].p, End: good[len(good)-1].p + 1}
	rc.precisionRows = rc.plausibleA.End - rc.plausibleA.Start
	if rc.precisionRows <= 8 {
		rc.evidenceStatus = EvidencePrecise
	} else {
		rc.evidenceStatus = EvidenceRangeOnly
	}

	// A real offset step must be preferred on both sides of the boundary.
	// This rejects periodic/self-similar matches that briefly choose the
	// neighboring repetition and then return to the stable displacement.
	preBefore := shiftedContextError(a, b, bestP-260, bestP-28, rc.beforeShift, startRow)
	preAfter := shiftedContextError(a, b, bestP-260, bestP-28, rc.afterShift, startRow)
	postBefore := shiftedContextError(a, b, bestP+28, bestP+260, rc.beforeShift, startRow)
	postAfter := shiftedContextError(a, b, bestP+28, bestP+260, rc.afterShift, startRow)
	preMargin, postMargin := 0.0, 0.0
	if !math.IsInf(preBefore, 1) && !math.IsInf(preAfter, 1) {
		preMargin = preAfter - preBefore
		rc.support += preMargin
	}
	if !math.IsInf(postBefore, 1) && !math.IsInf(postAfter, 1) {
		postMargin = postBefore - postAfter
		rc.support += postMargin
	}
	// Both sides must agree. A positive total alone can be produced by one
	// long context when DP has merely rediscovered the already-changed offset.
	rc.valid = preMargin >= -0.03 && postMargin >= 0.015 && rc.quality >= 0.55
	rc.score = min(0.97, max(0.25, 0.50+0.25*rc.quality+min(0.22, min(preMargin, postMargin)*0.5)))
}

func shiftedContextError(a, b laneSignal, lo, hi, shift, startRow int) float64 {
	n := len(a.values)
	if hi <= lo {
		return math.Inf(1)
	}
	var sum float64
	var count int
	for row := lo; row < hi; row++ {
		i := row - startRow
		j := i + shift
		if i < 0 || i >= n || j < 0 || j >= n || !a.ok[i] || !b.ok[j] {
			continue
		}
		sum += math.Abs(a.values[i] - b.values[j])
		count++
	}
	if count < (hi-lo)/3 {
		return math.Inf(1)
	}
	return sum / float64(count)
}

func shifts(ms []match) []int {
	out := make([]int, len(ms))
	for i := range ms {
		out[i] = ms[i].shift
	}
	return out
}

func medianInt(xs []int) int {
	cp := append([]int(nil), xs...)
	sort.Ints(cp)
	return cp[len(cp)/2]
}

func medianAbsDev(xs []int, median int) int {
	ds := make([]int, len(xs))
	for i, x := range xs {
		ds[i] = absInt(x - median)
	}
	sort.Ints(ds)
	return ds[len(ds)/2]
}

func sameSign(a, b int) bool { return (a > 0 && b > 0) || (a < 0 && b < 0) }
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
