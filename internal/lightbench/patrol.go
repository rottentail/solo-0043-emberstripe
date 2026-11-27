package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// patrol runs the weak-change inspection over the reel's marked segments. It
// is independent of the applied frame window so a station can re-check the
// whole scan after applying a window, and every finding carries its own
// bounded review window back into the workbench.
func (b *bench) patrol(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	report, err := station.Patrol(reel, r.URL.Query().Get("profile"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定无法完成巡检，或卷内没有标记巡检片段。")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
