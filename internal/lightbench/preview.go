package lightbench

import (
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/preview"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

type previewAxis struct {
	Clock    preview.Clock     `json:"clock"`
	Frames   []preview.Picture `json:"frames"`
	Coverage []preview.Run     `json:"coverage"`
	Edges    []preview.Run     `json:"edges"`
	Markers  []previewMarker   `json:"markers"`
}

// previewMarker ties each source frame boundary to both clocks so the browser
// can label the presentation axis with the same frame numbers the picture rail
// uses.
type previewMarker struct {
	Frame        int     `json:"frame"`
	Sample       int     `json:"sample"`
	WindowSample int     `json:"window_sample"`
	TimeMS       float64 `json:"time_ms"`
}

// previewAxisEndpoint describes the screening clock for the applied window
// without rendering audio. Frame boundaries are derived from absolute frame
// numbers, so the axis of two neighbouring windows meets without a gap.
func (b *bench) previewAxis(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	opts, ok := requestedPreview(w, r)
	if !ok {
		return
	}
	result, err := preview.Layout(reel, window, opts)
	if err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	axis := previewAxis{
		Clock: result.Clock, Frames: result.Pictures, Coverage: result.Coverage,
		Markers: make([]previewMarker, 0, len(result.Pictures)+1),
	}
	for _, run := range result.Coverage {
		if run.Status == preview.Edge {
			axis.Edges = append(axis.Edges, run)
		}
	}
	for _, picture := range result.Pictures {
		axis.Markers = append(axis.Markers, previewMarker{
			Frame: picture.Frame, Sample: result.Clock.FirstSample + picture.SampleStart,
			WindowSample: picture.SampleStart, TimeMS: float64(picture.SampleStart) * 1000 / float64(opts.Rate),
		})
	}
	axis.Markers = append(axis.Markers, previewMarker{
		Frame: window.End, Sample: result.Clock.LastSample, WindowSample: result.Clock.Samples,
		TimeMS: float64(result.Clock.Samples) * 1000 / float64(opts.Rate),
	})
	writeJSON(w, http.StatusOK, axis)
}

// previewClock resolves one presentation sample back to its frame, source row
// and sheet, using the same layout the axis and WAV are built from.
func (b *bench) previewClock(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	opts, ok := requestedPreview(w, r)
	if !ok {
		return
	}
	at, err := strconv.Atoi(r.URL.Query().Get("sample"))
	if err != nil {
		problem(w, http.StatusBadRequest, "请提供整数试映样本号。")
		return
	}
	report, err := station.PreviewReport(reel, window, r.URL.Query().Get("profile"), opts, at)
	if err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func requestedPreview(w http.ResponseWriter, r *http.Request) (preview.Options, bool) {
	query := r.URL.Query()
	fps, fpsErr := strconv.Atoi(query.Get("fps"))
	rate, rateErr := strconv.Atoi(query.Get("rate"))
	if fpsErr != nil || rateErr != nil {
		problem(w, http.StatusBadRequest, "请以整数填写放映帧率和输出采样率。")
		return preview.Options{}, false
	}
	opts := preview.Options{FPS: fps, Rate: rate}
	if err := opts.Validate(); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return preview.Options{}, false
	}
	return opts, true
}
