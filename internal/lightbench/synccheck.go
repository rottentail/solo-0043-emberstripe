package lightbench

import (
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
	"github.com/rottentail/solo-0043-emberstripe/internal/syncheck"
)

type syncResponse struct {
	Window register.Window `json:"window"`
	Rate   int             `json:"rate"`
	Rows   int             `json:"rows_per_frame"`
	Check  syncheck.Result `json:"sync"`
}

func (b *bench) syncCheck(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	requested, _ := strconv.Atoi(r.URL.Query().Get("search_rows"))
	if requested < 0 {
		problem(w, http.StatusBadRequest, "搜索行数必须是非负整数。")
		return
	}
	result, err := station.SyncCheck(reel, window, r.URL.Query().Get("profile"), requested)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足，无法核验时差。")
		return
	}
	writeJSON(w, http.StatusOK, syncResponse{
		Window: window,
		Rate:   register.Rate(reel.Header),
		Rows:   reel.Rows,
		Check:  result,
	})
}
