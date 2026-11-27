package lightbench

import (
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
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
	// MinRow and MaxRow are the absolute picture rows that produced the
	// extrema. The browser anchors each spike there instead of stretching it
	// over the whole bin. Unreadable bins use -1.
	MinRow [2]int `json:"min_row"`
	MaxRow [2]int `json:"max_row"`
}

type envelopeSegment struct {
	start  int
	end    int
	status string
}

// summarize reports bounded, half-open amplitude bins for the absolute
// picture-row interval [lo, hi). Bins never cross a coverage boundary, so a
// readable bin cannot paint a spike over a missing or out-of-capture gap. Each
// bin also keeps the row of every extreme, which lets a zoomed view place a
// few-row pulse on the row where it can actually occur. Raw PCM never leaves
// this function; requesting a narrower interval only raises the bin
// resolution, it does not widen the payload.
func summarize(reel *capture.Reel, g gauge.Gauge, runs []register.Run, lo, hi int) []amplitudeBin {
	if hi <= lo {
		return nil
	}
	segments := clipRuns(runs, lo, hi)
	if len(segments) == 0 {
		return nil
	}
	span := hi - lo
	goal := min(span, targetBins)
	bins := make([]amplitudeBin, 0, goal+len(segments))
	segment := 0
	for index := 0; index < goal; index++ {
		first, last := lo+index*span/goal, lo+(index+1)*span/goal
		for segment < len(segments) && first >= segments[segment].end {
			segment++
		}
		row := first
		for part := segment; part < len(segments) && row < last; part++ {
			start, end := max(row, segments[part].start), min(last, segments[part].end)
			if start < end {
				bins = append(bins, measureBin(reel, g, start, end, segments[part].status))
			}
			row = end
		}
	}
	return bins
}

func clipRuns(runs []register.Run, lo, hi int) []envelopeSegment {
	segments := make([]envelopeSegment, 0, len(runs))
	for _, run := range runs {
		start, end := max(lo, run.Start), min(hi, run.End)
		if start < end {
			segments = append(segments, envelopeSegment{start: start, end: end, status: run.Status})
		}
	}
	return segments
}

func measureBin(reel *capture.Reel, g gauge.Gauge, start, end int, status string) amplitudeBin {
	bin := amplitudeBin{Start: start, End: end, MinRow: [2]int{-1, -1}, MaxRow: [2]int{-1, -1}}
	if status != register.Readable {
		return bin
	}
	for row := start; row < end; row++ {
		sample, ok := readout.Peek(reel, g, row)
		if !ok {
			continue
		}
		if bin.Readable == 0 {
			for channel := range 2 {
				bin.Min[channel], bin.Max[channel] = sample[channel], sample[channel]
				bin.MinRow[channel], bin.MaxRow[channel] = row, row
			}
		} else {
			for channel := range 2 {
				if sample[channel] < bin.Min[channel] {
					bin.Min[channel], bin.MinRow[channel] = sample[channel], row
				}
				if sample[channel] > bin.Max[channel] {
					bin.Max[channel], bin.MaxRow[channel] = sample[channel], row
				}
			}
		}
		bin.Readable++
	}
	if bin.Readable == 0 {
		bin.MinRow, bin.MaxRow = [2]int{-1, -1}, [2]int{-1, -1}
	}
	return bin
}
