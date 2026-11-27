// Package preview renders the optical track on a presentation clock that can
// differ from the native scan cadence.
//
// The native picture clock places one sample on every picture row. A preview
// instead presents picture frame f during the half-open sample interval
// [ceil(f*rate/fps), ceil((f+1)*rate/fps)) of the requested playback clock.
// The boundary is a pure function of the absolute frame number, so adjacent
// windows tile exactly: no frame is dropped or repeated when the rate ratio is
// not integral. Within a frame, rows are resampled to fill the interval, so the
// picture clock never drifts and the preview duration is frames / fps.
//
// An output sample whose source row interval straddles two coverage states is
// reported as conversion_edge and carries silence. That keeps a missing sheet
// audible as a gap instead of letting resampling smear readable audio into it,
// while the edge itself stays visible in the coverage report.
package preview

import (
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Edge marks an output sample whose source interval touches more than one
// coverage state. It is never audible; it exists so the transition between a
// readable groove and a missing sheet stays visible after resampling.
const Edge = "conversion_edge"

const (
	MinFPS  = 16
	MaxFPS  = 60
	MinRate = 8000
	MaxRate = 96000
)

// Options selects the presentation clock. FPS is the projection cadence and
// Rate is the output sample rate.
type Options struct {
	FPS  int
	Rate int
}

func (o Options) Validate() error {
	if o.FPS < MinFPS || o.FPS > MaxFPS {
		return fmt.Errorf("presentation fps must be an integer from %d to %d", MinFPS, MaxFPS)
	}
	if o.Rate < MinRate || o.Rate > MaxRate {
		return fmt.Errorf("output rate must be an integer from %d to %d Hz", MinRate, MaxRate)
	}
	return nil
}

// Run is a contiguous interval of output samples with one coverage state. For
// an Edge run the source interval straddles From and To, so its picture rows
// overlap the neighbouring runs by the straddling sample.
type Run struct {
	SampleStart     int    `json:"sample_start"`
	SampleEnd       int    `json:"sample_end"`
	PictureRowStart int    `json:"picture_row_start"`
	PictureRowEnd   int    `json:"picture_row_end"`
	Status          string `json:"status"`
	From            string `json:"from,omitempty"`
	To              string `json:"to,omitempty"`
}

// Picture places one source picture frame inside the output clock.
type Picture struct {
	Frame           int `json:"frame"`
	SampleStart     int `json:"sample_start"`
	SampleEnd       int `json:"sample_end"`
	PictureRowStart int `json:"picture_row_start"`
	PictureRowEnd   int `json:"picture_row_end"`
	Readable        int `json:"readable"`
	Missing         int `json:"missing_sheet"`
	Outside         int `json:"outside_capture"`
	Edges           int `json:"conversion_edge"`
}

// Clock describes both the source cadence and the presentation cadence so a
// preview sample range can be re-derived from absolute frame numbers alone.
type Clock struct {
	SourceFPS          int     `json:"source_fps"`
	SourceRate         int     `json:"source_rate"`
	PresentationFPS    int     `json:"presentation_fps"`
	Rate               int     `json:"rate"`
	RowsPerFrame       int     `json:"rows_per_frame"`
	Lead               int     `json:"lead_rows"`
	FirstSample        int     `json:"first_sample"`
	LastSample         int     `json:"last_sample"`
	Samples            int     `json:"samples"`
	DurationMS         float64 `json:"duration_ms"`
	Identity           bool    `json:"identity"`
	ReadableSamples    int     `json:"readable_samples"`
	ConversionEdgeSamp int     `json:"conversion_edge_samples"`
	MissingSamples     int     `json:"missing_samples"`
	OutsideSamples     int     `json:"outside_samples"`
}

type Result struct {
	Samples  []int16   `json:"-"`
	Clock    Clock     `json:"clock"`
	Pictures []Picture `json:"pictures"`
	Coverage []Run     `json:"coverage"`
}

// Boundary is the first preview sample of picture frame f on the presentation
// clock. It is defined on absolute frames, which is what keeps neighbouring
// windows from accumulating a rounding error.
func Boundary(f int, o Options) int {
	num := int64(f) * int64(o.Rate)
	den := int64(o.FPS)
	return int((num + den - 1) / den)
}

// Layout computes the preview clock, picture placement and coverage without
// touching pixels. It is cheap enough to answer a cursor query, and Render
// builds on it so the two never describe different clocks.
func Layout(reel *capture.Reel, w register.Window, opts Options) (Result, error) {
	if err := register.Validate(reel.Header, w); err != nil {
		return Result{}, err
	}
	if err := opts.Validate(); err != nil {
		return Result{}, err
	}
	first, last := Boundary(w.Start, opts), Boundary(w.End, opts)
	result := Result{Clock: Clock{
		SourceFPS: reel.FPS, SourceRate: register.Rate(reel.Header),
		PresentationFPS: opts.FPS, Rate: opts.Rate,
		RowsPerFrame: reel.Rows, Lead: reel.Lead,
		FirstSample: first, LastSample: last, Samples: last - first,
		DurationMS: float64((w.End-w.Start)*1000) / float64(opts.FPS),
		Identity:   opts.FPS == reel.FPS && opts.Rate == register.Rate(reel.Header),
	}}
	for frame := w.Start; frame < w.End; frame++ {
		start := Boundary(frame, opts) - first
		span := Boundary(frame+1, opts) - first - start
		if span <= 0 {
			return Result{}, fmt.Errorf("preview clock collapsed frame %d", frame)
		}
		picture := Picture{
			Frame: frame, SampleStart: start, SampleEnd: start + span,
			PictureRowStart: frame * reel.Rows, PictureRowEnd: (frame + 1) * reel.Rows,
		}
		for local := 0; local < span; local++ {
			index := start + local
			lo, hi := sampleSpan(frame, local, span, reel.Rows)
			status, from, to := classify(statesBetween(reel, lo, hi))
			switch status {
			case register.Readable:
				picture.Readable++
				result.Clock.ReadableSamples++
			case register.Missing:
				picture.Missing++
				result.Clock.MissingSamples++
			case register.Outside:
				picture.Outside++
				result.Clock.OutsideSamples++
			default:
				picture.Edges++
				result.Clock.ConversionEdgeSamp++
			}
			appendRun(&result.Coverage, Run{
				SampleStart: index, SampleEnd: index + 1,
				PictureRowStart: lo, PictureRowEnd: hi,
				Status: status, From: from, To: to,
			})
		}
		result.Pictures = append(result.Pictures, picture)
	}
	return result, nil
}

// Render resamples an already extracted native window. Native samples are
// reused, so an identity preview (source frame rate and native sample rate)
// is byte-for-byte the same PCM as the native audition.
func Render(reel *capture.Reel, w register.Window, native readout.Render, opts Options) (Result, error) {
	result, err := Layout(reel, w, opts)
	if err != nil {
		return Result{}, err
	}
	origin := w.Start * reel.Rows
	result.Samples = make([]int16, 2*result.Clock.Samples)
	run := 0
	for _, picture := range result.Pictures {
		span := picture.SampleEnd - picture.SampleStart
		for local := 0; local < span; local++ {
			index := picture.SampleStart + local
			for run < len(result.Coverage) && index >= result.Coverage[run].SampleEnd {
				run++
			}
			if run >= len(result.Coverage) || result.Coverage[run].Status != register.Readable {
				continue
			}
			row := picture.Frame*reel.Rows + nearestRow(local, span, reel.Rows)
			offset := (row - origin) * 2
			result.Samples[2*index] = native.Samples[offset]
			result.Samples[2*index+1] = native.Samples[offset+1]
		}
	}
	return result, nil
}

// Report explains one output sample: the source picture row interval it
// covers, the row actually heard, and the source sheet address that row maps
// to. PreviewPCM is copied from the same rendered result as the WAV and
// NativePCM is the row-centered value before resampling, so an operator can
// hear exactly what the conversion changed at this instant.
type Report struct {
	Sample          int               `json:"sample"`
	WindowSample    int               `json:"window_sample"`
	WindowTimeMS    float64           `json:"window_time_ms"`
	Status          string            `json:"status"`
	From            string            `json:"from,omitempty"`
	To              string            `json:"to,omitempty"`
	Frame           int               `json:"frame"`
	LocalFrameRow   int               `json:"local_frame_row"`
	PictureRowStart int               `json:"picture_row_start"`
	PictureRowEnd   int               `json:"picture_row_end"`
	Source          *register.Address `json:"source,omitempty"`
	SourceStatus    string            `json:"source_status"`
	PreviewPCM      [2]int16          `json:"preview_sample"`
	NativePCM       [2]int16          `json:"native_sample"`
}

// InspectRow reports the sample at absolute output position at. It works from
// the cheap Layout plus the single source row the preview carries, and
// reproduces exactly the bytes Render would place there: the native sample for
// a readable interval and silence otherwise.
func InspectRow(reel *capture.Reel, w register.Window, g gauge.Gauge, opts Options, at int, result Result) (Report, error) {
	first := Boundary(w.Start, opts)
	if at < first || at >= Boundary(w.End, opts) {
		return Report{}, fmt.Errorf("sample must be in [%d,%d)", first, Boundary(w.End, opts))
	}
	local := at - first
	var picture *Picture
	for i := range result.Pictures {
		if local >= result.Pictures[i].SampleStart && local < result.Pictures[i].SampleEnd {
			picture = &result.Pictures[i]
			break
		}
	}
	if picture == nil {
		return Report{}, fmt.Errorf("sample %d is outside the preview window", at)
	}
	span := picture.SampleEnd - picture.SampleStart
	within := local - picture.SampleStart
	lo, hi := sampleSpan(picture.Frame, within, span, reel.Rows)
	report := Report{
		Sample: at, WindowSample: local,
		WindowTimeMS:    float64(local) * 1000 / float64(opts.Rate),
		Frame:           picture.Frame,
		PictureRowStart: lo, PictureRowEnd: hi,
	}
	for _, run := range result.Coverage {
		if local >= run.SampleStart && local < run.SampleEnd {
			report.Status, report.From, report.To = run.Status, run.From, run.To
			break
		}
	}
	// Resolve the row whose audio this sample would carry, whether or not the
	// interval is fully readable, so the correspondence can be traced.
	row := picture.Frame*reel.Rows + nearestRow(within, span, reel.Rows)
	report.LocalFrameRow = row - picture.Frame*reel.Rows
	address, status := register.Locate(reel, row)
	report.SourceStatus = status
	if status != register.Outside {
		report.Source = &address
	}
	if status == register.Readable {
		native := readout.Trace(reel, g, row)
		report.NativePCM = native.Sample
		if report.Status == register.Readable {
			report.PreviewPCM = native.Sample
		}
	}
	return report, nil
}

// sampleSpan is the half-open source picture row interval covered by one
// output sample. It lies inside the owning picture frame, so a frame boundary
// is never crossed.
func sampleSpan(frame, local, span, rows int) (int, int) {
	return frame*rows + local*rows/span, frame*rows + ceilDiv((local+1)*rows, span)
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// nearestRow is the centre row of the output sample's local interval, clamped
// to the frame. Upsampling repeats a row; downsampling skips rows evenly.
func nearestRow(local, span, rows int) int {
	row := int(int64(2*local+1) * int64(rows) / int64(2*span))
	if row >= rows {
		return rows - 1
	}
	return row
}

// statesBetween lists the coverage states touched by [lo,hi), dropping repeats.
func statesBetween(reel *capture.Reel, lo, hi int) []string {
	states := make([]string, 0, 2)
	for row := lo; row < hi; row++ {
		_, status := register.Locate(reel, row)
		if len(states) == 0 || states[len(states)-1] != status {
			states = append(states, status)
		}
	}
	if len(states) == 0 {
		return []string{register.Outside}
	}
	return states
}

func classify(states []string) (status, from, to string) {
	if len(states) == 1 {
		return states[0], "", ""
	}
	return Edge, states[0], states[len(states)-1]
}

func appendRun(runs *[]Run, run Run) {
	if n := len(*runs); n > 0 && (*runs)[n-1].Status == run.Status && (*runs)[n-1].From == run.From && (*runs)[n-1].To == run.To {
		(*runs)[n-1].SampleEnd = run.SampleEnd
		(*runs)[n-1].PictureRowEnd = run.PictureRowEnd
		return
	}
	*runs = append(*runs, run)
}
