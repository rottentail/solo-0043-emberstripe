// Package cue synthesizes the audible hearing-audit timing track and its lookup sheet.
//
// The timing track keeps the audition window's length and original samples: cue
// tones are added to both channels on readable rows only. Missing-sheet and
// outside-capture rows stay silent zero fill, so a gap is never painted over.
package cue

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Cue tone constants. The 1 kHz beep sits inside the telephone speech band and
// remains identifiable on handheld recordings of the monitoring output.
const (
	ToneHz       = 1000
	ToneMs       = 60
	TonePeak     = 12000
	FadeMs       = 2
	MinAudibleMs = 5
)

// Marker is one requested cue point addressed in reel picture coordinates.
type Marker struct {
	Frame int `json:"frame"`
	Row   int `json:"row"`
}

// ToneSpec records the exact synthesis contract used for an export.
type ToneSpec struct {
	FrequencyHz   int `json:"frequency_hz"`
	DurationMs    int `json:"duration_ms"`
	PeakAmplitude int `json:"peak_amplitude"`
	FadeMs        int `json:"fade_ms"`
	MinAudibleMs  int `json:"minimum_audible_ms"`
}

// Segment is one contiguous interval where a marker actually sounds. A cue
// crossing a coverage gap is split into several segments while keeping one
// continuous phase, and the gap itself remains silent.
type Segment struct {
	SampleStart int     `json:"sample_start"`
	SampleEnd   int     `json:"sample_end"`
	TimeStartMs float64 `json:"time_start_ms"`
	DurationMs  float64 `json:"duration_ms"`
}

// MarkerRecord binds an exported cue point back to the original reel line.
type MarkerRecord struct {
	Frame           int       `json:"frame"`
	Row             int       `json:"row"`
	PictureRow      int       `json:"picture_row"`
	SampleStart     int       `json:"sample_start"`
	TimeStartMs     float64   `json:"time_start_ms"`
	OnsetStatus     string    `json:"onset_status"`
	Complete        bool      `json:"complete"`
	SoundingSamples int       `json:"sounding_samples"`
	Segments        []Segment `json:"sounding_segments"`
}

// ZeroPad explains an interval where the export carries silent fill rather
// than program material or cue tone.
type ZeroPad struct {
	SampleStart int    `json:"sample_start"`
	SampleEnd   int    `json:"sample_end"`
	Status      string `json:"status"`
}

// Sheet is the machine-readable timing companion of an exported WAV.
type Sheet struct {
	Capture      string          `json:"capture"`
	Profile      string          `json:"profile"`
	Window       register.Window `json:"window"`
	Rate         int             `json:"sample_rate"`
	Rows         int             `json:"rows_per_frame"`
	Lead         int             `json:"lead_rows"`
	OriginRow    int             `json:"origin_picture_row"`
	SampleFrames int             `json:"sample_frames"`
	Tone         ToneSpec        `json:"tone"`
	Markers      []MarkerRecord  `json:"markers"`
	Coverage     []register.Run  `json:"coverage"`
	ZeroPads     []ZeroPad       `json:"zero_pads"`
}

func Spec() ToneSpec {
	return ToneSpec{FrequencyHz: ToneHz, DurationMs: ToneMs, PeakAmplitude: TonePeak, FadeMs: FadeMs, MinAudibleMs: MinAudibleMs}
}

// ParseMarks reads repeated mark=frame:row query values and keeps them in
// picture-clock order. Every marker must fall inside the selected window.
func ParseMarks(values url.Values, h capture.Header, w register.Window) ([]Marker, error) {
	raw := values["mark"]
	if len(raw) == 0 {
		return nil, errors.New("at least one mark=frame:row parameter is required")
	}
	markers := make([]Marker, 0, len(raw))
	seen := make(map[int]bool, len(raw))
	for _, item := range raw {
		parts := strings.Split(item, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("mark %q must use frame:row coordinates", item)
		}
		frame, frameErr := strconv.Atoi(parts[0])
		row, rowErr := strconv.Atoi(parts[1])
		if frameErr != nil || rowErr != nil {
			return nil, fmt.Errorf("mark %q must use integer frame:row coordinates", item)
		}
		if frame < w.Start || frame >= w.End || row < 0 || row >= h.Rows {
			return nil, fmt.Errorf("mark %s is outside the selected picture window", item)
		}
		absolute := frame*h.Rows + row
		if seen[absolute] {
			return nil, fmt.Errorf("duplicate mark at frame %d row %d", frame, row)
		}
		seen[absolute] = true
		markers = append(markers, Marker{Frame: frame, Row: row})
	}
	sort.Slice(markers, func(i, j int) bool {
		return markers[i].Frame*h.Rows+markers[i].Row < markers[j].Frame*h.Rows+markers[j].Row
	})
	return markers, nil
}

// Render mixes the cue tones into a copy of the window PCM and reports every
// sounding segment and zero-padded interval. The input slice is never modified
// and the exported length always equals the audition window length.
func Render(reel *capture.Reel, pcm []int16, coverage []register.Run, w register.Window, rate int, markers []Marker) ([]int16, []MarkerRecord, []ZeroPad, error) {
	origin := w.Start * reel.Rows
	length := len(pcm) / 2
	duration := rate * ToneMs / 1000
	minimum := rate * MinAudibleMs / 1000
	fade := rate * FadeMs / 1000
	mixed := append([]int16(nil), pcm...)
	records := make([]MarkerRecord, 0, len(markers))
	for _, marker := range markers {
		startAbs := marker.Frame*reel.Rows + marker.Row
		_, status := register.Locate(reel, startAbs)
		nominalEnd := min(startAbs+duration, origin+length)
		onset := startAbs - origin
		record := MarkerRecord{
			Frame: marker.Frame, Row: marker.Row, PictureRow: startAbs,
			SampleStart: onset, TimeStartMs: milliseconds(onset, rate), OnsetStatus: status,
			Segments: make([]Segment, 0),
		}
		for _, run := range coverage {
			if run.Status != register.Readable || run.End <= startAbs || run.Start >= nominalEnd {
				continue
			}
			s := max(run.Start, startAbs) - origin
			e := min(run.End, nominalEnd) - origin
			for i := s; i < e; i++ {
				phase := i - onset // phase continues across coverage gaps
				amplitude := float64(TonePeak) * envelope(i-s, e-s, fade) * math.Sin(2*math.Pi*ToneHz*float64(phase)/float64(rate))
				tone := int(math.Round(amplitude))
				for channel := 0; channel < 2; channel++ {
					mixed[2*i+channel] = clamp16(int(mixed[2*i+channel]) + tone)
				}
			}
			record.Segments = append(record.Segments, Segment{
				SampleStart: s, SampleEnd: e,
				TimeStartMs: milliseconds(s, rate), DurationMs: milliseconds(e-s, rate),
			})
			record.SoundingSamples += e - s
		}
		record.Complete = len(record.Segments) == 1 && record.SoundingSamples == duration && startAbs+duration <= origin+length
		if record.SoundingSamples < minimum {
			return nil, nil, nil, fmt.Errorf("mark %d:%d has only %d ms of readable audio in the window, below the %d ms audible minimum",
				marker.Frame, marker.Row, record.SoundingSamples*1000/rate, MinAudibleMs)
		}
		records = append(records, record)
	}
	pads := make([]ZeroPad, 0)
	for _, run := range coverage {
		if run.Status == register.Readable {
			continue
		}
		s := max(run.Start, origin) - origin
		e := min(run.End, origin+length) - origin
		if e > s {
			pads = append(pads, ZeroPad{SampleStart: s, SampleEnd: e, Status: run.Status})
		}
	}
	return mixed, records, pads, nil
}

func milliseconds(samples, rate int) float64 {
	return float64(samples) * 1000 / float64(rate)
}

// envelope raises and lowers each contiguous sounding segment so interruptions
// at a coverage gap do not introduce clicks.
func envelope(position, span, fade int) float64 {
	width := min(fade, span/2)
	if width == 0 {
		return 1
	}
	switch {
	case position < width:
		return 0.5 - 0.5*math.Cos(math.Pi*float64(position)/float64(width))
	case position >= span-width:
		return 0.5 - 0.5*math.Cos(math.Pi*float64(span-position)/float64(width))
	default:
		return 1
	}
}

func clamp16(value int) int16 {
	if value > 32767 {
		return 32767
	}
	if value < -32768 {
		return -32768
	}
	return int16(value)
}
