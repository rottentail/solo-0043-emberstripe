package httpapi

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/lineaudit"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

func serveTopLevelLineAudit(w http.ResponseWriter, r *http.Request, s *station.Station) {
	serveLineAudit(w, r, s, "")
}

func serveReelLineAudit(w http.ResponseWriter, r *http.Request, s *station.Station, reel *capture.Reel) {
	serveLineAudit(w, r, s, reel.Name)
}

func serveLineAudit(w http.ResponseWriter, r *http.Request, s *station.Station, pathA string) {
	q := r.URL.Query()
	a := firstNonEmpty(q.Get("a"), q.Get("left"), q.Get("first"), pathA)
	b := firstNonEmpty(q.Get("b"), q.Get("against"), q.Get("with"), q.Get("right"), q.Get("second"))
	if a == "" || b == "" {
		invalid(w, "需要 a 和 b（或路径卷加 against）指定两份试扫。")
		return
	}
	ar, ok := s.Reel(a)
	if !ok {
		write(w, http.StatusNotFound, map[string]string{"error": "A scan unavailable"})
		return
	}
	if _, ok := s.Reel(b); !ok {
		write(w, http.StatusNotFound, map[string]string{"error": "B scan unavailable"})
		return
	}
	req := lineaudit.Request{
		A:        a,
		B:        b,
		Profile:  firstNonEmpty(q.Get("profile_a"), q.Get("profile")),
		ProfileB: firstNonEmpty(q.Get("profile_b"), q.Get("against_profile"), q.Get("profile")),
	}
	if v := q.Get("start_row"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			invalid(w, "start_row must be an integer")
			return
		}
		req.StartRow = n
	}
	if v := q.Get("end_row"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			invalid(w, "end_row must be an integer")
			return
		}
		req.EndRow = n
	}
	if req.StartRow == 0 && req.EndRow == 0 && (q.Has("start") || q.Has("end")) {
		start, err1 := strconv.Atoi(q.Get("start"))
		end, err2 := strconv.Atoi(q.Get("end"))
		if err1 != nil || err2 != nil {
			invalid(w, "start and end must be picture/source frame integers")
			return
		}
		if start < 0 || end <= start || end > ar.Frames {
			invalid(w, "frame window is outside the scan")
			return
		}
		req.StartRow = start * ar.Rows
		req.EndRow = end * ar.Rows
	}
	if v := q.Get("max_drift_rows"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			invalid(w, "max_drift_rows must be an integer")
			return
		}
		req.MaxDrift = n
	}
	report, err := s.LineAudit(req)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	write(w, http.StatusOK, report)
}

func serveRowAudition(w http.ResponseWriter, r *http.Request, reel *capture.Reel) {
	q := r.URL.Query()
	start, err1 := strconv.Atoi(q.Get("start_row"))
	end, err2 := strconv.Atoi(q.Get("end_row"))
	if err1 != nil || err2 != nil {
		invalid(w, "start_row and end_row must be integer scanner rows")
		return
	}
	space := firstNonEmpty(q.Get("space"), "source")
	sourceClock := space != "picture" && space != "picture_clock"
	data, _, err := station.RowAudition(reel, q.Get("profile"), start, end, sourceClock)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	http.ServeContent(w, r, "row-audition.wav", time.Time{}, bytes.NewReader(data))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
