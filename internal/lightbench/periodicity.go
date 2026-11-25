package lightbench

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/periodicity"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// requestedBand reads optional edge-band columns. Missing values select the
// default gap; malformed or overlapping columns are rejected as bad requests.
func requestedBand(r *http.Request, reel *capture.Reel) (capture.Span, bool) {
	query := r.URL.Query()
	_, hasX0 := query["x0"]
	_, hasX1 := query["x1"]
	if !hasX0 && !hasX1 {
		band, err := periodicity.DefaultBand(reel)
		return band, err == nil
	}
	x0, err0 := strconv.Atoi(query.Get("x0"))
	x1, err1 := strconv.Atoi(query.Get("x1"))
	if err0 != nil || err1 != nil {
		return capture.Span{}, false
	}
	band := capture.Span{X0: x0, X1: x1}
	return band, periodicity.ValidBand(reel, band)
}

func (b *bench) periodicity(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	band, ok := requestedBand(r, reel)
	if !ok {
		problem(w, http.StatusBadRequest, "参照带必须落在画面与左右声槽之外的列区。")
		return
	}
	report, err := station.PeriodicReport(reel, window, band)
	if err != nil {
		if errors.Is(err, periodicity.ErrInvalidBand) {
			problem(w, http.StatusBadRequest, "参照带必须落在画面与左右声槽之外的列区。")
			return
		}
		problem(w, http.StatusUnprocessableEntity, "暂时无法完成周期干扰核验。")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (b *bench) periodicAudio(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	band, ok := requestedBand(r, reel)
	if !ok {
		problem(w, http.StatusBadRequest, "参照带必须落在画面与左右声槽之外的列区。")
		return
	}
	mode := r.URL.Query().Get("mode")
	data, err := station.PeriodicAudio(reel, window, band, mode)
	if err != nil {
		if errors.Is(err, periodicity.ErrInvalidBand) {
			problem(w, http.StatusBadRequest, "参照带必须落在画面与左右声槽之外的列区。")
			return
		}
		problem(w, http.StatusBadRequest, "试听模式无效，可选 edge、left、right。")
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	http.ServeContent(w, r, "periodic-reference.wav", time.Time{}, bytes.NewReader(data))
}
