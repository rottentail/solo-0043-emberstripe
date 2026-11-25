package readout

import (
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// ExtractRows renders an arbitrary half-open row interval. When sourceClock is
// true the row is an emulsion/source row; otherwise it is a picture-clock row
// and the reel's lead and coverage rules apply.
func ExtractRows(reel *capture.Reel, g gauge.Gauge, start, end int, sourceClock bool) []int16 {
	samples := make([]int16, 2*(end-start))
	for row := start; row < end; row++ {
		var address register.Address
		var status string
		if sourceClock {
			if row < 0 || row >= reel.Rows*reel.Frames {
				status = register.Outside
			} else {
				address = register.Address{Frame: row / reel.Rows, Row: row % reel.Rows}
				if _, ok := reel.Sheets[address.Frame]; ok {
					status = register.Readable
				} else {
					status = register.Missing
				}
			}
		} else {
			address, status = register.Locate(reel, row)
		}
		if status != register.Readable {
			continue
		}
		_, pcm, _ := measure(reel, address, g)
		offset := (row - start) * 2
		samples[offset] = pcm[0]
		samples[offset+1] = pcm[1]
	}
	return samples
}
