package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// seams reports every internal picture-page boundary with its exposure verdict
// and the separate location heard on the offset optical-sound clock.
func (b *bench) seams(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, station.Seams(reel))
}
