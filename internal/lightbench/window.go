package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
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
	Lead     int             `json:"lead_rows"`
	Profile  gauge.Gauge     `json:"profile"`
	Frames   []frameCoverage `json:"frames"`
	Coverage []register.Run  `json:"coverage"`
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
	calibration, render, err := station.RenderWindow(reel, window, r.URL.Query().Get("profile"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	result := windowReadout{
		Window: window, Rate: register.Rate(reel.Header), Rows: reel.Rows, Lead: reel.Lead, Profile: calibration,
		Coverage: render.Coverage, Envelope: summarize(render, window.Start*reel.Rows),
	}
	for frame := window.Start; frame < window.End; frame++ {
		sheet := reel.Sheets[frame]
		coverage := frameCoverage{Frame: frame, Depth: sheet.Depth, Source: sheet.Digest}
		first, last := frame*reel.Rows, (frame+1)*reel.Rows
		for _, run := range render.Coverage {
			count := max(0, min(run.End, last)-max(run.Start, first))
			switch run.Status {
			case register.Readable:
				coverage.Readable += count
			case register.Missing:
				coverage.Missing += count
			case register.Outside:
				coverage.Outside += count
			}
		}
		result.Frames = append(result.Frames, coverage)
	}
	writeJSON(w, http.StatusOK, result)
}
