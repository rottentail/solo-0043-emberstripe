package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// cutpoints analyses the requested window for frame-aligned edit candidates.
// It shares the drawing concurrency slots because it re-renders PCM the same
// way the waveform and strip projection do.
func (b *bench) cutpoints(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	report, err := station.CutPoints(reel, window, r.URL.Query().Get("profile"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
