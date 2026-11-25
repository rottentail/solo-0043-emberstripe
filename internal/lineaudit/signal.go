package lineaudit

import (
	"math"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
)

type laneSignal struct {
	values []float64
	ok     []bool
	mean   float64
	std    float64
}

type pairSignal struct {
	a, b [2]laneSignal
}

func buildLane(reel *capture.Reel, lane, start, end int) laneSignal {
	n := end - start
	s := laneSignal{values: make([]float64, n), ok: make([]bool, n)}
	var sum, sum2, count float64
	for idx := 0; idx < n; idx++ {
		row := start + idx
		frame, inFrame := row/reel.Rows, row%reel.Rows
		sheet, exists := reel.Sheets[frame]
		if !exists || isReferenceRow(reel, row) {
			continue
		}
		gray := gauge.Mean(sheet, reel.Lanes[lane], capture.Span{X0: inFrame, X1: inFrame + 1})
		// Alignment only needs affine-invariant contour shape; robust
		// standardization below removes scanner/exposure scale differences.
		v := gray
		s.values[idx] = v
		s.ok[idx] = true
		sum += v
		sum2 += v * v
		count++
	}
	if count > 0 {
		s.mean = sum / count
		variance := math.Max(0, sum2/count-s.mean*s.mean)
		s.std = math.Sqrt(variance)
	}
	if s.std < 1e-6 {
		s.std = 1
	}
	for idx := range s.values {
		if s.ok[idx] {
			s.values[idx] = (s.values[idx] - s.mean) / s.std
		}
	}
	smoothed := make([]float64, n)
	const radius = 2
	for i := 0; i < n; i++ {
		if !s.ok[i] {
			continue
		}
		var total float64
		var k float64
		for j := max(0, i-radius); j <= min(n-1, i+radius); j++ {
			if s.ok[j] {
				total += s.values[j]
				k++
			}
		}
		smoothed[i] = total / max(1, k)
	}
	s.values = smoothed
	return s
}

func isReferenceRow(reel *capture.Reel, row int) bool {
	for _, profile := range reel.Profiles {
		for _, patch := range []capture.Patch{profile.Dark, profile.Clear} {
			lo := patch.Frame*reel.Rows + patch.Rows.X0
			hi := patch.Frame*reel.Rows + patch.Rows.X1
			if row >= lo && row < hi {
				return true
			}
		}
	}
	return false
}

type landmark struct {
	index      int
	row        int
	kind       int // +1 peak, -1 trough
	level      float64
	leftSlope  float64
	rightSlope float64
	prominence float64
}

func landmarks(s laneSignal, startRow int) []landmark {
	n := len(s.values)
	var out []landmark
	const threshold = 0.42
	const slopeWindow = 8
	i := 0
	for i < n {
		if !s.ok[i] {
			i++
			continue
		}
		runEnd := i
		for runEnd < n && s.ok[runEnd] {
			runEnd++
		}
		out = append(out, zigzag(s.values, i, runEnd, threshold, slopeWindow, startRow)...)
		i = runEnd
	}
	sort.Slice(out, func(i, j int) bool { return out[i].index < out[j].index })
	return out
}

func zigzag(v []float64, lo, hi int, threshold float64, slopeWindow int, startRow int) []landmark {
	if hi-lo < 8 {
		return nil
	}
	var out []landmark
	dir := 0
	high, low := lo, lo
	for i := lo + 1; i < hi; i++ {
		if v[i] > v[high] {
			high = i
		}
		if v[i] < v[low] {
			low = i
		}
		if dir != -1 && high < i && v[high]-v[i] >= threshold {
			out = appendPivot(out, v, high, +1, lo, hi, slopeWindow, startRow)
			dir = -1
			high, low = i, i
			continue
		}
		if dir != 1 && low < i && v[i]-v[low] >= threshold {
			out = appendPivot(out, v, low, -1, lo, hi, slopeWindow, startRow)
			dir = 1
			high, low = i, i
			continue
		}
		if dir == -1 && v[i] < v[low] {
			low = i
		}
		if dir == 1 && v[i] > v[high] {
			high = i
		}
	}
	return out
}

func appendPivot(out []landmark, v []float64, idx, kind, lo, hi, w int, startRow int) []landmark {
	left := idx - w
	right := idx + w
	var ls, rs float64
	if left >= lo {
		ls = (v[idx] - v[left]) / float64(idx-left)
	}
	if right < hi {
		rs = (v[right] - v[idx]) / float64(right-idx)
	}
	prom := 0.0
	if left >= lo && right < hi {
		base := math.Min(v[left], v[right])
		if kind == 1 {
			prom = v[idx] - base
		} else {
			prom = base - v[idx]
		}
	}
	return append(out, landmark{
		index: idx, row: startRow + idx, kind: kind, level: v[idx],
		leftSlope: ls, rightSlope: rs, prominence: math.Abs(prom),
	})
}
