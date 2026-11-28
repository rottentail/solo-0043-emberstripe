package lightbench

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/blind"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
)

// reelProfiles summarizes the usable calibrations offered in the build form.
type reelProfiles struct {
	Name     string   `json:"name"`
	Profiles []string `json:"profiles"`
}

// sheetSummary is one row in the administrator's blind sheet index.
type sheetSummary struct {
	ID        string   `json:"id"`
	Capture   string   `json:"capture"`
	Status    string   `json:"status"`
	Windows   int      `json:"windows"`
	Reviewers []string `json:"reviewers"`
}

type blindLandingData struct {
	Names       []string
	Reels       []reelProfiles
	ReelOptions template.JS
	Sheets      []sheetSummary
}

type blindSheetData struct {
	ID      string
	Capture string
}

func (b *bench) blindHome(w http.ResponseWriter, r *http.Request) {
	names := b.station.Names()
	data := blindLandingData{Names: names, Reels: []reelProfiles{}, Sheets: []sheetSummary{}}
	for _, name := range names {
		reel, ok := b.station.Reel(name)
		if !ok {
			continue
		}
		summary := reelProfiles{Name: name, Profiles: []string{}}
		for _, profile := range reel.Profiles {
			if _, err := gauge.Measure(reel, profile.ID); err == nil {
				summary.Profiles = append(summary.Profiles, profile.ID)
			}
		}
		data.Reels = append(data.Reels, summary)
	}
	rawOptions, err := json.Marshal(data.Reels)
	if err != nil {
		problem(w, http.StatusInternalServerError, "无法整理卷标定信息。")
		return
	}
	data.ReelOptions = template.JS(rawOptions)
	ids, err := b.station.BlindIDs()
	if err != nil {
		problem(w, http.StatusInternalServerError, "无法读取盲听答卷目录。")
		return
	}
	for _, id := range ids {
		sheet, err := b.station.BlindSheet(id)
		if err != nil {
			problem(w, http.StatusInternalServerError, "盲听答卷已损坏，请检查存档目录。")
			return
		}
		data.Sheets = append(data.Sheets, sheetSummary{
			ID: sheet.ID, Capture: sheet.Capture, Status: sheet.Status,
			Windows: len(sheet.Windows), Reviewers: blind.PublicView(sheet).Reviewers,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = pages.ExecuteTemplate(w, "blind", data)
}

func (b *bench) blindSheetPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sheet, err := b.station.BlindSheet(id)
	if err != nil {
		if blind.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_ = pages.ExecuteTemplate(w, "blind", blindLandingData{Names: b.station.Names(), Reels: []reelProfiles{}, Sheets: []sheetSummary{}})
			return
		}
		problem(w, http.StatusInternalServerError, "盲听答卷无法读取。")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = pages.ExecuteTemplate(w, "blind-sheet", blindSheetData{ID: sheet.ID, Capture: sheet.Capture})
}
