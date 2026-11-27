package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/relight"
)

type relightView struct {
	Reel           *capture.Reel
	Names          []string
	Records        []relight.Record
	VerdictLabels  map[string]string
	ZoneLabels     map[string]string
	MaterialLabels map[string]string
	ZoneOrder      []string
}

var verdictText = map[string]string{
	relight.Aligned:      "可沿用",
	relight.Diverged:     "单独核对",
	relight.Insufficient: "材料不足",
}

var zoneText = map[string]string{
	relight.ZonePicture: "画面",
	relight.ZoneLeft:    "左槽",
	relight.ZoneRight:   "右槽",
	relight.ZoneEdge:    "片边",
}

var materialText = map[string]string{
	relight.MaterialBase:    "透明带片基",
	relight.MaterialStrip:   "中间试条",
	relight.MaterialReferer: "标定参考",
}

func (b *bench) relightPage(w http.ResponseWriter, r *http.Request) {
	data := relightView{
		Names:          b.station.Names(),
		Records:        b.station.RelightRecords(),
		VerdictLabels:  verdictText,
		ZoneLabels:     zoneText,
		MaterialLabels: materialText,
		ZoneOrder:      []string{relight.ZonePicture, relight.ZoneLeft, relight.ZoneRight, relight.ZoneEdge},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = pages.ExecuteTemplate(w, "relight", data)
}
