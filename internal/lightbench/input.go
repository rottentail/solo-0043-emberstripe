package lightbench

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

func (b *bench) reel(w http.ResponseWriter, r *http.Request) (*capture.Reel, bool) {
	reel, ok := b.station.Reel(r.PathValue("reel"))
	if !ok {
		problem(w, http.StatusNotFound, "扫描卷不存在。")
	}
	return reel, ok
}

func requestedWindow(r *http.Request, h capture.Header) (register.Window, bool) {
	start, startErr := intQuery(r, "start")
	end, endErr := intQuery(r, "end")
	window := register.Window{Start: start, End: end}
	return window, startErr == nil && endErr == nil && register.Validate(h, window) == nil
}

func intQuery(r *http.Request, key string) (int, error) {
	return strconv.Atoi(r.URL.Query().Get(key))
}

func problem(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
