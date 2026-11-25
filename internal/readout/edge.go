package readout

import (
	"math"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const edgeBins = 256

// edgeMinRows is the smallest readable sample count that still lets a bin
// support a lateral-bias conclusion. Single-row bright spots fall below it.
const edgeMinRows = 8

// LaneEdge describes one optical lane's left/right edge imbalance over a bin.
// Values are normalized by the lane's own calibration contrast so lanes of
// different widths and reference light levels stay comparable.
type LaneEdge struct {
	EdgeWidth int     `json:"edge_width"`
	Rows      int     `json:"rows"`
	Mean      float64 `json:"mean"`
	Peak      float64 `json:"peak"`
	// Persistent is the fraction of contributing rows sharing the mean's sign.
	Persistent float64 `json:"persistent"`
	Spread     float64 `json:"spread"`
}

// EdgeBin covers the same row interval as an envelope bin. Depths records how
// many readable rows came from each native bit depth (8 or 16). Bins mixing
// depths cannot separate a depth switch from genuine lateral bias.
type EdgeBin struct {
	Start    int         `json:"start"`
	End      int         `json:"end"`
	Readable int         `json:"readable"`
	Depths   map[int]int `json:"depths"`
	Lanes    [2]LaneEdge `json:"lanes"`
}

type edgeAccumulator struct {
	sum, squareSum, peak float64
	positive, negative   int
	rows                 int
}

// EdgeBalance walks the window on the picture clock and, for every readable
// row, compares the mean gray of the left and right column halves inside each
// optical lane. Unreadable intervals keep their bins but contribute nothing,
// so missing pages and out-of-reel spans remain visibly unsupported.
func EdgeBalance(reel *capture.Reel, g gauge.Gauge, w register.Window) []EdgeBin {
	length := (w.End - w.Start) * reel.Rows
	count := min(length, edgeBins)
	origin := w.Start * reel.Rows
	result := make([]EdgeBin, count)
	accs := make([][2]edgeAccumulator, count)
	edgeWidth := [2]int{}
	for lane, span := range reel.Lanes {
		// A single-column lane has no two lateral sides to compare.
		if span.Width() >= 2 {
			edgeWidth[lane] = max(1, span.Width()/2)
		}
	}
	for index := range result {
		result[index] = EdgeBin{
			Start:  origin + index*length/count,
			End:    origin + (index+1)*length/count,
			Depths: map[int]int{},
		}
	}
	for _, run := range register.Coverage(reel, w) {
		if run.Status != register.Readable {
			continue
		}
		for row := run.Start; row < run.End; row++ {
			address, _ := register.Locate(reel, row)
			sheet := reel.Sheets[address.Frame]
			index := (row - origin) * count / length
			bin := &result[index]
			bin.Readable++
			bin.Depths[sheet.Depth]++
			for lane, span := range reel.Lanes {
				width := edgeWidth[lane]
				if width == 0 {
					continue
				}
				left := edgeMean(sheet, span.X0, address.Row, width)
				right := edgeMean(sheet, span.X1-width, address.Row, width)
				contrast := g.Lanes[lane].Clear - g.Lanes[lane].Dark
				delta := (right - left) / contrast
				acc := &accs[index][lane]
				acc.rows++
				acc.sum += delta
				acc.squareSum += delta * delta
				acc.peak = max(acc.peak, math.Abs(delta))
				if delta > 0 {
					acc.positive++
				} else if delta < 0 {
					acc.negative++
				}
			}
		}
	}
	for index := range result {
		for lane := range result[index].Lanes {
			acc := accs[index][lane]
			entry := LaneEdge{EdgeWidth: edgeWidth[lane], Rows: acc.rows, Peak: acc.peak}
			if acc.rows > 0 {
				entry.Mean = acc.sum / float64(acc.rows)
				variance := acc.squareSum/float64(acc.rows) - entry.Mean*entry.Mean
				entry.Spread = math.Sqrt(max(0, variance))
				same := acc.negative
				if entry.Mean >= 0 {
					same = acc.positive
				}
				entry.Persistent = float64(same) / float64(acc.rows)
			}
			result[index].Lanes[lane] = entry
		}
	}
	return result
}

// edgeMean averages width columns of one source row, kept on the shared
// 16-bit intensity scale so 8-bit and 16-bit sheets use the same arithmetic.
func edgeMean(sheet capture.Sheet, x0, y, width int) float64 {
	var total uint64
	for x := x0; x < x0+width; x++ {
		total += uint64(sheet.Gray(x, y))
	}
	return float64(total) / float64(width)
}
