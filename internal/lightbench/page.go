package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

type profileOption struct {
	ID       string
	Usable   bool
	Selected bool
}

type pageData struct {
	Names       []string
	Reel        *capture.Reel
	Profiles    []profileOption
	Active      string
	Alternative bool
	InitialEnd  int
	SampleRate  int
	Available   int
}

func (b *bench) home(w http.ResponseWriter, r *http.Request) {
	names := b.station.Names()
	if len(names) == 0 {
		renderPage(w, http.StatusOK, pageData{})
		return
	}
	http.Redirect(w, r, "/lightbench/"+names[0], http.StatusSeeOther)
}

func (b *bench) reelPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Names: b.station.Names()}
	reel, ok := b.station.Reel(r.PathValue("reel"))
	if !ok {
		renderPage(w, http.StatusNotFound, data)
		return
	}
	data.Reel = reel
	data.InitialEnd = min(reel.Frames, 8)
	data.SampleRate = register.Rate(reel.Header)
	data.Available = len(reel.Sheets)
	for _, profile := range reel.Profiles {
		_, err := gauge.Measure(reel, profile.ID)
		if err == nil {
			if data.Active == "" {
				data.Active = profile.ID
			} else {
				data.Alternative = true
			}
		}
		data.Profiles = append(data.Profiles, profileOption{ID: profile.ID, Usable: err == nil, Selected: profile.ID == data.Active})
	}
	renderPage(w, http.StatusOK, data)
}

func renderPage(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pages.ExecuteTemplate(w, "page", data)
}
