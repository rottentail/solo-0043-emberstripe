// Package readout converts calibrated optical area to signed stereo samples.
package readout

import (
	"math"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

type Value struct {
	Status  string            `json:"status"`
	Source  *register.Address `json:"source,omitempty"`
	Gray    [2]float64        `json:"gray"`
	Sample  [2]int16          `json:"sample"`
	Clipped [2]bool           `json:"clipped"`
}

type Render struct {
	Samples  []int16        `json:"-"`
	Coverage []register.Run `json:"coverage"`
	Clipped  [2]int         `json:"clipped"`
}

type Difference struct {
	Compared int        `json:"compared_frames"`
	Changed  [2]int     `json:"changed_samples"`
	Peak     [2]int     `json:"peak_delta"`
	RMS      [2]float64 `json:"rms_delta"`
}

func Trace(reel *capture.Reel, g gauge.Gauge, row int) Value {
	a, status := register.Locate(reel, row)
	value := Value{Status: status}
	if status != register.Outside {
		value.Source = &a
	}
	if status != register.Readable {
		return value
	}
	value.Gray, value.Sample, value.Clipped = measure(reel, a, g)
	return value
}

func measure(reel *capture.Reel, a register.Address, g gauge.Gauge) (gray [2]float64, pcm [2]int16, clipped [2]bool) {
	sheet := reel.Sheets[a.Frame]
	for lane, columns := range reel.Lanes {
		gray[lane] = gauge.Mean(sheet, columns, capture.Span{X0: a.Row, X1: a.Row + 1})
		ref := g.Lanes[lane]
		unit := (gray[lane] - ref.Dark) / (ref.Clear - ref.Dark)
		clipped[lane] = unit < 0 || unit > 1
		pcm[lane] = int16(math.Round((2*min(1, max(0, unit)) - 1) * 32767))
	}
	return
}

// MeasureAt evaluates one readable source address under the given calibration.
// It shares the normalization and clip policy used by full window extraction.
func MeasureAt(reel *capture.Reel, g gauge.Gauge, a register.Address) ([2]int16, [2]bool) {
	_, pcm, clipped := measure(reel, a, g)
	return pcm, clipped
}

func Extract(reel *capture.Reel, g gauge.Gauge, w register.Window) Render {
	result := Render{Samples: make([]int16, 2*(w.End-w.Start)*reel.Rows), Coverage: register.Coverage(reel, w)}
	origin := w.Start * reel.Rows
	for _, run := range result.Coverage {
		if run.Status != register.Readable {
			continue
		}
		for row := run.Start; row < run.End; row++ {
			a, _ := register.Locate(reel, row)
			_, sample, clipped := measure(reel, a, g)
			offset := (row - origin) * 2
			copy(result.Samples[offset:offset+2], sample[:])
			for lane, over := range clipped {
				if over {
					result.Clipped[lane]++
				}
			}
		}
	}
	return result
}

func Compare(a, b Render, origin int) Difference {
	var d Difference
	for _, run := range a.Coverage {
		if run.Status != register.Readable {
			continue
		}
		d.Compared += run.End - run.Start
		for row := run.Start; row < run.End; row++ {
			for lane := 0; lane < 2; lane++ {
				i := (row-origin)*2 + lane
				step := int(a.Samples[i]) - int(b.Samples[i])
				if step != 0 {
					d.Changed[lane]++
				}
				d.Peak[lane] = max(d.Peak[lane], int(math.Abs(float64(step))))
				d.RMS[lane] += float64(step) * float64(step)
			}
		}
	}
	if d.Compared != 0 {
		for lane := range d.RMS {
			d.RMS[lane] = math.Sqrt(d.RMS[lane] / float64(d.Compared))
		}
	}
	return d
}
