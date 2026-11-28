package lightbench

import (
	"errors"
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// historyView serves the applied-window trail without changing it.
func (b *bench) historyView(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, b.trail(reel))
}

func (b *bench) trail(reel *capture.Reel) trailView {
	return b.history.view(reel.Name, func(s Step) classification { return b.recordOrigin(reel, s) }, b.parcelOnDisk)
}

// focus resumes from a step the lead picked out of the history. Resuming never
// rewrites the ancestors that led there: a later application records a new
// child and the earlier provenance stays exactly as it was.
func (b *bench) focus(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("entry")
	if !entryPattern.MatchString(id) {
		problem(w, http.StatusBadRequest, "请指定历史步骤。")
		return
	}
	step, found := b.history.find(reel.Name, id)
	if !found {
		problem(w, http.StatusNotFound, "历史步骤不存在。")
		return
	}
	origin := b.recordOrigin(reel, step)
	if origin.Status != reapplicable {
		problem(w, http.StatusConflict, origin.Reason)
		return
	}
	restored, _, err := b.history.Focus(reel.Name, id)
	if err != nil {
		b.historyProblem(w, err)
		return
	}
	b.restore(w, reel, restored)
}

// freeze publishes the window currently in force and pins the parcel to that
// exact step. Choosing another window afterwards leaves the step and its parcel
// untouched, so the lead can still download the artifact it froze.
func (b *bench) freeze(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	step, found := b.history.current(reel.Name)
	if !found {
		problem(w, http.StatusConflict, "尚未应用画格窗。")
		return
	}
	if origin := b.recordOrigin(reel, step); origin.Status != reapplicable {
		problem(w, http.StatusConflict, origin.Reason)
		return
	}
	receipt, err := b.station.Release(reel, step.Window, step.Profile)
	if err != nil {
		var input *station.InvalidRenderingInput
		if errors.As(err, &input) {
			problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
			return
		}
		problem(w, http.StatusInternalServerError, "冻结交接包失败。")
		return
	}
	updated, _, err := b.history.SetParcel(reel.Name, step.ID, receipt.ID)
	if err != nil {
		b.historyProblem(w, err)
		return
	}
	w.Header().Set("Location", "/parcels/"+receipt.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"receipt": receipt, "step": updated, "trail": b.trail(reel)})
}

func (b *bench) parcelOnDisk(id string) bool {
	if id == "" {
		return false
	}
	_, err := b.station.Store.Read(id)
	return err == nil
}

func (b *bench) historyProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoStep), errors.Is(err, errUnknownEntry):
		problem(w, http.StatusConflict, "历史步骤不可用。")
	default:
		problem(w, http.StatusInternalServerError, "无法保存工作台历史。")
	}
}
