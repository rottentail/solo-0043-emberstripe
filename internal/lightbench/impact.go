package lightbench

import (
	"net/http"
)

func (b *bench) impactPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = pages.ExecuteTemplate(w, "impact", map[string]any{
		"Names": b.station.Names(),
	})
}
