package lightbench

import (
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

type frameCoverage struct {
	Frame    int    `json:"frame"`
	Depth    int    `json:"depth"`
	Readable int    `json:"readable"`
	Missing  int    `json:"missing"`
	Outside  int    `json:"outside"`
	Source   string `json:"source_sha256,omitempty"`
}

type windowReadout struct {
	Window   register.Window `json:"window"`
	Rate     int             `json:"rate"`
	Rows     int             `json:"rows_per_frame"`
	Profile  gauge.Gauge     `json:"profile"`
	Frames   []frameCoverage `json:"frames"`
	Coverage []register.Run  `json:"coverage"`
	Detail   register.Window `json:"detail"`
	Envelope []amplitudeBin  `json:"envelope"`
}

func (b *bench) window(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	detail, ok := detailWindow(r, reel, window)
	if !ok {
		problem(w, http.StatusBadRequest, "局部行区间必须落在已应用的画格窗内。")
		return
	}
	calibration, err := gauge.Measure(reel, r.URL.Query().Get("profile"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	coverage := register.Coverage(reel, window)
	result := windowReadout{
		Window: window, Rate: register.Rate(reel.Header), Rows: reel.Rows, Profile: calibration,
		Coverage: coverage, Detail: detail,
		Envelope: summarize(reel, calibration, coverage, detail.Start, detail.End),
	}
	for frame := window.Start; frame < window.End; frame++ {
		sheet := reel.Sheets[frame]
		entry := frameCoverage{Frame: frame, Depth: sheet.Depth, Source: sheet.Digest}
		first, last := frame*reel.Rows, (frame+1)*reel.Rows
		for _, run := range coverage {
			count := max(0, min(run.End, last)-max(run.Start, first))
			switch run.Status {
			case register.Readable:
				entry.Readable += count
			case register.Missing:
				entry.Missing += count
			case register.Outside:
				entry.Outside += count
			}
		}
		result.Frames = append(result.Frames, entry)
	}
	writeJSON(w, http.StatusOK, result)
}

// detailWindow reads the optional absolute picture-row interval that the scope
// currently draws. Omitting it covers the whole applied window. Supplying it
// only narrows the bin interval: the sample count stays bounded while zooming,
// and the browser never receives raw PCM for a wide window. The interval is a
// half-open row range inside the applied window, not a new frame window.
func detailWindow(r *http.Request, reel *capture.Reel, window register.Window) (register.Window, bool) {
	first, last := window.Start*reel.Rows, window.End*reel.Rows
	query := r.URL.Query()
	detail := register.Window{Start: first, End: last}
	if raw := query.Get("start_row"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < first || value >= last {
			return register.Window{}, false
		}
		detail.Start = value
	}
	if raw := query.Get("end_row"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= detail.Start || value > last {
			return register.Window{}, false
		}
		detail.End = value
	}
	return detail, true
}
