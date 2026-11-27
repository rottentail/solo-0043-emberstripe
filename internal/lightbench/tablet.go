package lightbench

import (
	"errors"
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
	"github.com/rottentail/solo-0043-emberstripe/internal/tablet"
)

type tabletPageData struct {
	Names        []string
	Reel         *capture.Reel
	ReelName     string
	Profiles     []profileOption
	Active       string
	Report       *tablet.Report
	NotFound     bool
	Undefined    bool
	ProfileError string
	JSONPath     string
	TabletActive bool
}

func (b *bench) tabletPage(w http.ResponseWriter, r *http.Request) {
	names := b.station.Names()
	reelName := r.PathValue("reel")
	reel, ok := b.station.Reel(reelName)
	if !ok {
		renderTablet(w, http.StatusNotFound, tabletPageData{Names: names, ReelName: reelName, NotFound: true, TabletActive: true})
		return
	}
	data := tabletPageData{
		Names: names, Reel: reel, ReelName: reel.Name, TabletActive: true,
		JSONPath: "/lightbench/" + reel.Name + "/tablet/evaluate",
	}
	if reel.Tablet == nil {
		data.Undefined = true
		renderTablet(w, http.StatusNotFound, data)
		return
	}
	requested := r.URL.Query().Get("profile")
	active := requested
	if active == "" {
		active = reel.Profiles[0].ID
	}
	for _, profile := range reel.Profiles {
		_, err := gauge.Measure(reel, profile.ID)
		usable := err == nil
		data.Profiles = append(data.Profiles, profileOption{ID: profile.ID, Usable: usable, Selected: profile.ID == active})
	}
	report, err := station.Tablet(reel, requested)
	if err != nil {
		// A bad requested calibration falls back to the first usable one so the
		// page still presents a concrete evaluation with a visible warning.
		data.ProfileError = "所选标定缺少参考页或灰阶对比不足，已改用首个可用标定。"
		for _, profile := range reel.Profiles {
			if _, measureErr := gauge.Measure(reel, profile.ID); measureErr == nil {
				active = profile.ID
				break
			}
		}
		for i := range data.Profiles {
			data.Profiles[i].Selected = data.Profiles[i].ID == active
		}
		report, err = station.Tablet(reel, active)
	}
	data.Active = active
	if err == nil {
		data.Report = report
	} else {
		data.ProfileError = "此卷没有可用的光学标定，无法按当前标定评估试条。"
	}
	renderTablet(w, http.StatusOK, data)
}

func renderTablet(w http.ResponseWriter, status int, data tabletPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pages.ExecuteTemplate(w, "tablet", data)
}

func (b *bench) tabletData(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	report, err := station.Tablet(reel, r.URL.Query().Get("profile"))
	switch {
	case errors.Is(err, tablet.ErrUndefined):
		problem(w, http.StatusNotFound, "该卷没有试条阶档定义。")
	case err != nil:
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
	default:
		writeJSON(w, http.StatusOK, report)
	}
}
