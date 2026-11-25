package lightbench

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/lineaudit"
)

func (b *bench) pairAnalyze(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	aName := firstQueryValue(q, "a", "left", "first")
	bName := firstQueryValue(q, "b", "against", "right", "second", "other", "with")
	b.serveAlignmentRequest(w, r, aName, bName)
}

func (b *bench) reelAlignment(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bName := firstQueryValue(q, "against", "b", "with", "other", "right")
	b.serveAlignmentRequest(w, r, r.PathValue("reel"), bName)
}

func (b *bench) serveAlignmentRequest(w http.ResponseWriter, r *http.Request, aName, bName string) {
	query := r.URL.Query()
	if aName == "" || bName == "" {
		problem(w, http.StatusBadRequest, "需要两份同规格试扫（a 与 b，或路径卷加 against）。")
		return
	}
	ar, ok := b.station.Reel(aName)
	if !ok {
		problem(w, http.StatusNotFound, "A 试扫不存在。")
		return
	}
	if _, ok := b.station.Reel(bName); !ok {
		problem(w, http.StatusNotFound, "B 试扫不存在。")
		return
	}
	req := lineaudit.Request{
		A:        aName,
		B:        bName,
		Profile:  firstQueryValue(query, "profile_a", "profile"),
		ProfileB: firstQueryValue(query, "profile_b", "against_profile", "profile"),
	}
	if v := query.Get("start_row"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			problem(w, http.StatusBadRequest, "start_row 必须是整数。")
			return
		}
		req.StartRow = n
	}
	if v := query.Get("end_row"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			problem(w, http.StatusBadRequest, "end_row 必须是整数。")
			return
		}
		req.EndRow = n
	}
	if req.StartRow == 0 && req.EndRow == 0 && (query.Has("start") || query.Has("end")) {
		start, err1 := strconv.Atoi(query.Get("start"))
		end, err2 := strconv.Atoi(query.Get("end"))
		if err1 != nil || err2 != nil || start < 0 || end <= start || end > ar.Frames {
			problem(w, http.StatusBadRequest, "请填写卷内合法的起止格。")
			return
		}
		req.StartRow = start * ar.Rows
		req.EndRow = end * ar.Rows
	}
	if v := query.Get("max_drift_rows"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			problem(w, http.StatusBadRequest, "max_drift_rows 必须是整数。")
			return
		}
		req.MaxDrift = n
	}
	report, err := b.station.LineAudit(req)
	if err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func firstQueryValue(q url.Values, keys ...string) string {
	for _, key := range keys {
		if value := q.Get(key); value != "" {
			return value
		}
	}
	return ""
}
