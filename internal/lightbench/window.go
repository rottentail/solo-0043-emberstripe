package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
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
	Profile  gauge.Gauge     `json:"profile"`
	Frames   []frameCoverage `json:"frames"`
	Coverage []register.Run  `json:"coverage"`
	Envelope []amplitudeBin  `json:"envelope"`
	Step     Step            `json:"step"`
	Created  bool            `json:"created"`
	Live     bool            `json:"live"`
	Trail    trailView       `json:"trail"`
}

// window applies one requested picture window and calibration, then answers
// with the readout for the step now in force and the full trail behind it.
// An invalid window or unusable meter records nothing, so the previously
// applied step and its probe remain exactly as they were.
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
	step, render, created, err := b.apply(reel, window, r.URL.Query().Get("profile"), probeFrom(r, reel, window))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	b.answer(w, reel, step, window, render, created)
}

// applied answers with the step currently in force without applying anything.
// Opening or reloading the workbench must not fabricate a new step, and a
// record-only step is still shown from its frozen snapshot so the lead can
// inspect the original window and probe.
func (b *bench) applied(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	step, found := b.history.current(reel.Name)
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"applied": false, "trail": b.trail(reel)})
		return
	}
	writeJSON(w, http.StatusOK, b.readout(reel, step, step.Window, false))
}

// undo steps back one applied window and answers with the previous step. When
// the step just withdrawn was the first one, the answer reports the empty state
// so the page can show that nothing is applied instead of an error.
func (b *bench) undo(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	step, found, err := b.history.Undo(reel.Name)
	if err != nil {
		b.historyProblem(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"applied": false, "trail": b.trail(reel)})
		return
	}
	b.restore(w, reel, step)
}

// redo returns to a step that was left behind, either through undo or a trial.
func (b *bench) redo(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	step, found, err := b.history.Redo(reel.Name)
	if err != nil || !found {
		b.historyProblem(w, err)
		return
	}
	b.restore(w, reel, step)
}

// restore re-renders a step that is being brought back into force. A step that
// cannot be applied to the current scan bytes is refused so the lead is never
// shown a window whose provenance changed underneath it.
func (b *bench) restore(w http.ResponseWriter, reel *capture.Reel, step Step) {
	origin := b.recordOrigin(reel, step)
	if origin.Status != reapplicable {
		problem(w, http.StatusConflict, origin.Reason)
		return
	}
	_, render, err := station.RenderWindow(reel, step.Window, step.Profile)
	if err != nil {
		problem(w, http.StatusConflict, "该步骤的标定在当前卷已不可用。")
		return
	}
	b.answer(w, reel, step, step.Window, render, false)
}

// answer renders the readout for a freshly applied or restored step.
func (b *bench) answer(w http.ResponseWriter, reel *capture.Reel, step Step, window register.Window, render readout.Render, created bool) {
	writeJSON(w, http.StatusOK, b.readout(reel, step, window, created))
}

// readout builds the page payload. A re-applicable step is rendered from the
// current scan bytes so coverage and PCM stay truthful. A record-only step can
// no longer be rendered against the reel, so it reports the frozen snapshot
// recorded at apply time; the page marks it as record-only rather than live.
func (b *bench) readout(reel *capture.Reel, step Step, window register.Window, created bool) windowReadout {
	origin := b.recordOrigin(reel, step)
	result := windowReadout{
		Window: window, Rate: step.Clock.Rate, Rows: step.Clock.Rows,
		Profile: step.Gauge, Coverage: step.Coverage, Envelope: step.Envelope,
		Step: step, Created: created, Live: origin.Status == reapplicable,
		Trail: b.trail(reel),
	}
	if result.Live {
		if _, render, err := station.RenderWindow(reel, step.Window, step.Profile); err == nil {
			result.Coverage = render.Coverage
			result.Envelope = summarize(render, window.Start*reel.Rows)
		}
	}
	if result.Rate == 0 {
		result.Rate = register.Rate(reel.Header)
	}
	if result.Rows == 0 {
		result.Rows = reel.Rows
	}
	for frame := window.Start; frame < window.End; frame++ {
		coverage := frameCoverage{Frame: frame}
		if result.Live {
			coverage.Depth = reel.Sheets[frame].Depth
			coverage.Source = reel.Sheets[frame].Digest
		}
		first, last := frame*reel.Rows, (frame+1)*reel.Rows
		for _, run := range result.Coverage {
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
	if !result.Live {
		// Record-only frames replay the picture presence recorded at apply time,
		// so a page supplemented after the fact is not shown as part of the old
		// step even though it now exists on the current reel.
		recorded := make(map[int]FrameState, len(step.Pictures))
		for _, state := range step.Pictures {
			recorded[state.Frame] = state
		}
		for index := range result.Frames {
			if state, ok := recorded[result.Frames[index].Frame]; ok {
				result.Frames[index].Depth, result.Frames[index].Source = state.Depth, state.Sheet
			}
		}
	}
	return result
}

func probeFrom(r *http.Request, reel *capture.Reel, window register.Window) Probe {
	frame, frameErr := intQuery(r, "frame")
	row, rowErr := intQuery(r, "row")
	if frameErr != nil || rowErr != nil {
		return defaultProbe(window)
	}
	return probeFor(reel, window, frame, row)
}
