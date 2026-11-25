package periodicity

import (
	"math"
)

// maxDFTPoints bounds the direct Fourier transform cost. Longer readable
// material is block-averaged first, which keeps the native cadence and trades
// only the upper analysis frequency.
const maxDFTPoints = 8192

// alignedBinSpan is the half-width, in analysis bins, of the neighborhood in
// which a lane may answer an edge-band frequency. The lane keeps its own peak
// position inside this neighborhood; a tone further away never snaps onto the
// edge frequency and cannot manufacture a false agreement.
const alignedBinSpan = 2

// Peak is one prominent periodic component measured in a series.
type Peak struct {
	FrequencyHz    float64 `json:"frequency_hz"`
	PeriodRows     float64 `json:"period_rows"`
	Amplitude      float64 `json:"amplitude"`
	Bin            int     `json:"bin"`
	BinResolution  float64 `json:"bin_resolution_hz"`
	CyclesInWindow float64 `json:"cycles_in_readable_material"`
}

// minimumModulation is the smallest 16-bit-gray peak amplitude accepted as
// edge evidence. A faint mark below roughly half a gray code cannot support a
// transport-mechanism claim even when its tone is spectrally clean.
const minimumModulation = 128.0

// Spectrum describes one evidence stream after removing its local mean.
type Spectrum struct {
	Kind            SeriesKind `json:"kind"`
	ReadableRows    int        `json:"readable_rows"`
	SegmentCount    int        `json:"segment_count"`
	ShortestRows    int        `json:"shortest_segment_rows"`
	MeanLevel       float64    `json:"mean_level"`
	NoiseFloor      float64    `json:"noise_floor"`
	Peak            *Peak      `json:"peak,omitempty"`
	AlignedPeak     *Peak      `json:"aligned_peak,omitempty"`
	PeakProminence  float64    `json:"peak_prominence_ratio"`
	ModulationDepth float64    `json:"modulation_depth_gray"`
	Segments        []Segment  `json:"segments"`

	magnitudes []float64
	resolution float64
	points     int
	rate       int
}

type sampledSeries struct {
	values   []float64
	readable []bool
	factor   int
}

// prepare averages each readable run into blocks so the DFT stays bounded.
// Missing rows keep a false mask and are excluded from every statistic.
func prepare(values []float64, segments []Segment, windowOrigin int) sampledSeries {
	readableRows := 0
	for _, segment := range segments {
		readableRows += segment.Samples
	}
	factor := 1
	for readableRows/factor > maxDFTPoints {
		factor++
	}
	readability := make([]bool, len(values))
	for _, segment := range segments {
		for row := segment.StartRow; row < segment.EndRow; row++ {
			readability[row-windowOrigin] = true
		}
	}
	if factor == 1 {
		return sampledSeries{values: values, readable: readability, factor: 1}
	}
	count := (len(values) + factor - 1) / factor
	down := make([]float64, count)
	mask := make([]bool, count)
	for i := range down {
		var sum float64
		hits := 0
		for step := 0; step < factor; step++ {
			index := i*factor + step
			if index < len(values) && readability[index] {
				sum += values[index]
				hits++
			}
		}
		if hits > 0 {
			down[i] = sum / float64(hits)
			mask[i] = true
		}
	}
	return sampledSeries{values: down, readable: mask, factor: factor}
}

// Analyze measures the strongest periodic component of one series. The native
// sample rate is preserved; decimation only changes the analysis point count.
func Analyze(kind SeriesKind, values []float64, segments []Segment, rate, windowOrigin int) Spectrum {
	result := Spectrum{Kind: kind, Segments: segments, ShortestRows: shortest(segments), rate: rate}
	for _, segment := range segments {
		result.ReadableRows += segment.Samples
	}
	result.SegmentCount = len(segments)
	if result.ReadableRows < 2 {
		return result
	}
	prepared := prepare(values, segments, windowOrigin)
	analysisRate := float64(rate) / float64(prepared.factor)
	points := len(prepared.values)
	result.points = points
	mean, variance := 0.0, 0.0
	readable := 0
	for i, ok := range prepared.readable {
		if !ok {
			continue
		}
		readable++
		mean += prepared.values[i]
	}
	mean /= float64(readable)
	centered := make([]float64, points)
	lowest, highest := 0.0, 0.0
	for i, ok := range prepared.readable {
		if ok {
			centered[i] = prepared.values[i] - mean
			variance += centered[i] * centered[i]
			lowest = math.Min(lowest, centered[i])
			highest = math.Max(highest, centered[i])
		}
	}
	result.MeanLevel = mean
	result.ModulationDepth = highest - lowest
	std := math.Sqrt(variance / float64(readable))
	if std == 0 {
		return result
	}
	// A half-spectrum magnitude estimate. Readable rows only are fed to the
	// transform so missing sheets cannot manufacture a periodic component.
	limit := points/2 + 1
	magnitudes := make([]float64, limit)
	for bin := 1; bin < limit; bin++ {
		cosPart, sinPart := 0.0, 0.0
		angle := 2 * math.Pi * float64(bin) / float64(points)
		for i, ok := range prepared.readable {
			if !ok {
				continue
			}
			phase := angle * float64(i)
			cosPart += centered[i] * math.Cos(phase)
			sinPart += centered[i] * math.Sin(phase)
		}
		magnitudes[bin] = math.Hypot(cosPart, sinPart) / float64(readable)
	}
	strongest, floor := 1, 0.0
	floorCount := 0
	for bin := 1; bin < limit; bin++ {
		if magnitudes[bin] > magnitudes[strongest] {
			strongest = bin
		}
		floor += magnitudes[bin]
		floorCount++
	}
	floor /= float64(floorCount)
	resolution := analysisRate / float64(points)
	result.magnitudes = magnitudes
	result.resolution = resolution
	result.NoiseFloor = floor
	result.Peak = result.peakAt(strongest)
	if floor > 0 {
		result.PeakProminence = magnitudes[strongest] / floor
	}
	return result
}

func (s *Spectrum) peakAt(bin int) *Peak {
	if bin <= 0 || bin >= len(s.magnitudes) {
		return nil
	}
	frequency := float64(bin) * s.resolution
	return &Peak{
		FrequencyHz:    frequency,
		PeriodRows:     float64(s.rate) / frequency,
		Amplitude:      s.magnitudes[bin],
		Bin:            bin,
		BinResolution:  s.resolution,
		CyclesInWindow: frequency * float64(s.ReadableRows) / float64(s.rate),
	}
}

// AlignWith looks for the lane's own local maximum inside a narrow
// neighborhood of the edge-band frequency. A genuine tone is a local peak;
// spectral leakage from a tone outside the neighborhood is only a monotonic
// slope and is ignored, so it cannot manufacture a false agreement.
func (s *Spectrum) AlignWith(edge *Spectrum) {
	if edge == nil || edge.Peak == nil || len(s.magnitudes) == 0 {
		return
	}
	targetBin := max(1, min(len(s.magnitudes)-1, int(math.Round(edge.Peak.FrequencyHz/s.resolution))))
	low := max(1, targetBin-alignedBinSpan)
	high := min(len(s.magnitudes)-1, targetBin+alignedBinSpan)
	best := targetBin
	for bin := low; bin <= high; bin++ {
		left, right := s.magnitudes[bin], s.magnitudes[bin]
		if bin > 1 {
			left = s.magnitudes[bin-1]
		}
		if bin+1 < len(s.magnitudes) {
			right = s.magnitudes[bin+1]
		}
		if s.magnitudes[bin] >= left && s.magnitudes[bin] >= right && s.magnitudes[bin] > s.magnitudes[best] {
			best = bin
		}
	}
	// When no local peak exists, best stays on the target bin as a weak
	// leakage record; the answer-ratio check then dismisses it.
	s.AlignedPeak = s.peakAt(best)
}

func shortest(segments []Segment) int {
	best := 0
	for _, segment := range segments {
		if best == 0 || segment.Samples < best {
			best = segment.Samples
		}
	}
	return best
}
