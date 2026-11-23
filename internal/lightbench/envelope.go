package lightbench

import (
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const targetBins = 256

type amplitudeBin struct {
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Readable int      `json:"readable"`
	Min      [2]int16 `json:"min"`
	Max      [2]int16 `json:"max"`
}

// summarize walks runs and PCM in the same absolute row space. An unreadable
// interval keeps its full width but contributes no synthetic zero extrema.
func summarize(render readout.Render, origin int) []amplitudeBin {
	length := len(render.Samples) / 2
	count := min(length, targetBins)
	bins := make([]amplitudeBin, 0, count)
	runIndex := 0
	for index := 0; index < count; index++ {
		bin := amplitudeBin{Start: origin + index*length/count, End: origin + (index+1)*length/count}
		for row := bin.Start; row < bin.End; row++ {
			for row >= render.Coverage[runIndex].End {
				runIndex++
			}
			if render.Coverage[runIndex].Status != register.Readable {
				continue
			}
			offset := (row - origin) * 2
			for channel := range 2 {
				sample := render.Samples[offset+channel]
				if bin.Readable == 0 {
					bin.Min[channel], bin.Max[channel] = sample, sample
				} else {
					bin.Min[channel] = min(bin.Min[channel], sample)
					bin.Max[channel] = max(bin.Max[channel], sample)
				}
			}
			bin.Readable++
		}
		bins = append(bins, bin)
	}
	return bins
}
