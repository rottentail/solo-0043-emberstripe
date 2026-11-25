package periodicity

import (
	"errors"
	"math"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// ErrInvalidBand means the requested reference columns overlap picture or
// sound-lane material and cannot act as independent edge evidence.
var ErrInvalidBand = errors.New("reference band must stay outside the picture and both sound lanes")

// buildMono renders one evidence series as mono PCM on the window clock.
// Unreadable rows stay silent; each series scales on its own readable range so
// a quiet edge band remains audible without altering lane levels.
func buildMono(reel *capture.Reel, w register.Window, kind SeriesKind, band capture.Span) (rate int, mono []int16, err error) {
	if !ValidBand(reel, band) {
		return 0, nil, ErrInvalidBand
	}
	values, segments, err := BuildSeries(reel, w, kind, band)
	if err != nil {
		return 0, nil, err
	}
	rate = register.Rate(reel.Header)
	mono = make([]int16, len(values))
	if len(segments) == 0 {
		return rate, mono, nil
	}
	first := segments[0].StartRow - w.Start*reel.Rows
	minimum, maximum := values[first], values[first]
	for _, segment := range segments[1:] {
		for row := segment.StartRow; row < segment.EndRow; row++ {
			value := values[row-w.Start*reel.Rows]
			minimum = math.Min(minimum, value)
			maximum = math.Max(maximum, value)
		}
	}
	span := maximum - minimum
	for _, segment := range segments {
		for row := segment.StartRow; row < segment.EndRow; row++ {
			index := row - w.Start*reel.Rows
			normalized := 0.0
			if span > 0 {
				normalized = (values[index] - minimum) / span
			}
			mono[index] = int16(math.Round((2*normalized - 1) * 32767))
		}
	}
	return rate, mono, nil
}

// Audio returns a stereo WAV payload with the same mono evidence in both
// channels, matching the station's fixed stereo interchange.
func Audio(reel *capture.Reel, w register.Window, kind SeriesKind, band capture.Span) (rate int, interleaved []int16, err error) {
	rate, mono, err := buildMono(reel, w, kind, band)
	if err != nil {
		return 0, nil, err
	}
	interleaved = make([]int16, 2*len(mono))
	for index, value := range mono {
		interleaved[2*index] = value
		interleaved[2*index+1] = value
	}
	return rate, interleaved, nil
}

// ComparisonAudio puts the edge band in the left channel and one sound lane in
// the right, so both evidence streams can be re-listened on the same clock.
func ComparisonAudio(reel *capture.Reel, w register.Window, lane SeriesKind, band capture.Span) (rate int, interleaved []int16, err error) {
	if lane != Left && lane != Right {
		return 0, nil, errors.New("comparison needs a sound lane")
	}
	edgeRate, edge, err := buildMono(reel, w, Edge, band)
	if err != nil {
		return 0, nil, err
	}
	laneRate, lanePCM, err := buildMono(reel, w, lane, band)
	if err != nil {
		return 0, nil, err
	}
	if edgeRate != laneRate || len(edge) != len(lanePCM) {
		return 0, nil, errors.New("evidence clocks disagree")
	}
	interleaved = make([]int16, 2*len(edge))
	for index := range edge {
		interleaved[2*index] = edge[index]
		interleaved[2*index+1] = lanePCM[index]
	}
	return edgeRate, interleaved, nil
}
