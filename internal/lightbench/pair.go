package lightbench

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/lineaudit"
)

type pairPageData struct {
	Names      []string
	Reel       *capture.Reel
	A          string
	B          string
	Report     *lineaudit.Report
	ReportJSON template.JS
}

func (b *bench) pairPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := pairPageData{
		Names: b.station.Names(),
		A:     firstQueryValue(q, "a", "left", "first"),
		B:     firstQueryValue(q, "b", "right", "against", "second"),
	}
	if data.A == "" && len(data.Names) > 0 {
		data.A = data.Names[0]
	}
	if data.B == "" && len(data.Names) > 1 {
		data.B = data.Names[1]
	}
	if aReel, ok := b.station.Reel(data.A); ok {
		if _, ok := b.station.Reel(data.B); ok {
			req := lineaudit.Request{
				A:        data.A,
				B:        data.B,
				Profile:  firstQueryValue(q, "profile_a", "profile"),
				ProfileB: firstQueryValue(q, "profile_b", "against_profile", "profile"),
			}
			req.StartRow, _ = strconv.Atoi(q.Get("start_row"))
			req.EndRow, _ = strconv.Atoi(q.Get("end_row"))
			if req.StartRow == 0 && req.EndRow == 0 && q.Get("start") != "" && q.Get("end") != "" {
				start, e1 := strconv.Atoi(q.Get("start"))
				end, e2 := strconv.Atoi(q.Get("end"))
				if e1 == nil && e2 == nil && start >= 0 && end > start && end <= aReel.Frames {
					req.StartRow = start * aReel.Rows
					req.EndRow = end * aReel.Rows
				}
			}
			if raw := q.Get("max_drift_rows"); raw != "" {
				req.MaxDrift, _ = strconv.Atoi(raw)
			}
			if report, err := b.station.LineAudit(req); err == nil {
				if blob, err := json.Marshal(report); err == nil {
					data.Report = report
					data.ReportJSON = template.JS(blob)
				}
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = pages.ExecuteTemplate(w, "pairpage", data)
}
