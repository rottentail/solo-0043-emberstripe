package lineaudit

import "sort"

type repeatResult struct {
	a         Interval
	b         Interval
	score     float64
	lane      int
	transient bool
}

func detectNormalRepeats(la, lb [2][]landmark, globalShift int) []repeatResult {
	var raw []repeatResult
	const motifLen = 5
	for lane := 0; lane < 2; lane++ {
		A, B := la[lane], lb[lane]
		for i := 0; i+motifLen < len(A); i++ {
			for k := i + motifLen; k+motifLen <= len(A); k++ {
				// A voiceprint phrase is normally repeated within a few scan
				// frames; avoid quadratic matches across an entire long reel.
				if A[k].row-A[i].row > 6000 {
					break
				}
				if !motifMatches(A, i, k, motifLen) {
					continue
				}
				bi := nearestLandmark(B, A[i].row+globalShift, 24)
				bk := nearestLandmark(B, A[k].row+globalShift, 24)
				if bi < 0 || bk < 0 || bk+motifLen > len(B) || !motifMatches(B, bi, bk, motifLen) {
					continue
				}
				raw = append(raw, repeatResult{
					a:     Interval{Start: A[i].row, End: A[k+motifLen-1].row + 1},
					b:     Interval{Start: B[bi].row, End: B[bk+motifLen-1].row + 1},
					score: 0.72, lane: lane,
				})
				break // one repetition per starting motif is enough
			}
		}
	}
	return mergeRepeats(raw)
}

func motifMatches(xs []landmark, i, k, m int) bool {
	if i+m > len(xs) || k+m > len(xs) {
		return false
	}
	for t := 0; t < m; t++ {
		if xs[i+t].kind != xs[k+t].kind {
			return false
		}
		if absFloat(xs[i+t].level-xs[k+t].level) > 0.62 {
			return false
		}
		if t > 0 {
			d1 := xs[i+t].row - xs[i+t-1].row
			d2 := xs[k+t].row - xs[k+t-1].row
			if absInt(d1-d2) > max(5, d1/5) {
				return false
			}
		}
	}
	return true
}

func nearestLandmark(xs []landmark, row, tolerance int) int {
	best := -1
	bestD := tolerance + 1
	for i, x := range xs {
		d := absInt(x.row - row)
		if d < bestD {
			bestD = d
			best = i
		}
	}
	return best
}

func mergeRepeats(in []repeatResult) []repeatResult {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].a.Start < in[j].a.Start })
	out := []repeatResult{in[0]}
	for _, r := range in[1:] {
		last := &out[len(out)-1]
		if r.a.Start <= last.a.End+240 {
			last.a.End = max(last.a.End, r.a.End)
			last.b.End = max(last.b.End, r.b.End)
			last.score = max(last.score, r.score)
			if r.lane != last.lane {
				last.score = min(0.95, last.score+0.08)
			}
		} else {
			out = append(out, r)
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
