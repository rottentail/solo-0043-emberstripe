// Package httpapi exposes the restoration station's local HTTP interface.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/handoff"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

func New(s *station.Station) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /captures", func(w http.ResponseWriter, r *http.Request) { write(w, 200, s.Names()) })
	for action, method := range map[string]string{"header": "GET", "calibration": "GET", "register": "GET", "coverage": "GET", "trace": "GET", "audition": "GET", "compare": "GET", "overlay": "GET", "release": "POST"} {
		mux.HandleFunc(method+" /captures/{name}/"+action, func(w http.ResponseWriter, r *http.Request) {
			reel, ok := s.Reel(r.PathValue("name"))
			if !ok {
				write(w, 404, map[string]string{"error": "capture unavailable"})
				return
			}
			serveCapture(w, r, s, reel, action)
		})
	}
	mux.HandleFunc("GET /parcels/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.Store.Read(r.PathValue("id"))
		if err != nil {
			parcelError(w, err)
			return
		}
		binary(w, "application/zip", b)
	})
	mux.HandleFunc("GET /parcels/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.Store.Read(r.PathValue("id"))
		if err != nil {
			parcelError(w, err)
			return
		}
		v, err := parcel.Verify(b)
		if err != nil {
			parcelError(w, err)
			return
		}
		write(w, 200, v)
	})
	mux.HandleFunc("GET /handoff/parcels", func(w http.ResponseWriter, r *http.Request) {
		serveHandoffSearch(w, r, s)
	})
	mux.HandleFunc("GET /handoff/parcels/{id}/locate", func(w http.ResponseWriter, r *http.Request) {
		serveHandoffLocate(w, r, s)
	})
	mux.HandleFunc("GET /handoff/parcels/{id}/audition", func(w http.ResponseWriter, r *http.Request) {
		serveHandoffAudition(w, r, s)
	})
	// Bound concurrent readers so bursts cannot multiply large image renders.
	slots := make(chan struct{}, 3)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			mux.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			write(w, 503, map[string]string{"error": "restoration station busy"})
		}
	})
}

func serveCapture(w http.ResponseWriter, r *http.Request, s *station.Station, reel *capture.Reel, action string) {
	query := r.URL.Query()
	profile := query.Get("profile")
	switch action {
	case "header":
		write(w, 200, station.Info(reel))
		return
	case "calibration":
		g, err := gauge.Measure(reel, profile)
		respond(w, g, err)
		return
	case "register", "trace", "overlay":
		frame, err := strconv.Atoi(query.Get("frame"))
		if err != nil {
			invalid(w, "frame must be an integer")
			return
		}
		if action == "overlay" {
			b, err := station.Overlay(reel, profile, frame)
			if err != nil {
				invalid(w, err.Error())
				return
			}
			binary(w, "image/png", b)
			return
		}
		row, err := strconv.Atoi(query.Get("row"))
		if err != nil {
			invalid(w, "row must be an integer")
			return
		}
		if action == "register" {
			v, err := station.Mapping(reel, frame, row)
			respond(w, v, err)
		} else {
			v, err := station.Trace(reel, profile, frame, row)
			respond(w, v, err)
		}
		return
	}
	start, a := strconv.Atoi(query.Get("start"))
	end, b := strconv.Atoi(query.Get("end"))
	if a != nil || b != nil {
		invalid(w, "start and end must be picture frame integers")
		return
	}
	window := register.Window{Start: start, End: end}
	switch action {
	case "coverage":
		v, err := station.Coverage(reel, window)
		respond(w, v, err)
	case "compare":
		v, err := station.Compare(reel, window, profile, query.Get("against"))
		respond(w, v, err)
	case "audition":
		data, err := station.Audition(reel, window, profile)
		if err != nil {
			invalid(w, err.Error())
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeContent(w, r, "audition.wav", time.Time{}, bytes.NewReader(data))
	case "release":
		receipt, err := s.Release(reel, window, profile)
		if err != nil {
			var input *station.InvalidRenderingInput
			if errors.As(err, &input) {
				invalid(w, input.Error())
				return
			}
			write(w, 500, map[string]string{"error": "sound parcel unavailable"})
			return
		}
		w.Header().Set("Location", "/parcels/"+receipt.ID)
		write(w, 201, receipt)
	}
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func binary(w http.ResponseWriter, kind string, b []byte) {
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
func invalid(w http.ResponseWriter, message string) {
	write(w, 400, map[string]string{"error": message})
}
func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		invalid(w, err.Error())
		return
	}
	write(w, 200, value)
}
func parcelError(w http.ResponseWriter, err error) {
	if os.IsNotExist(err) {
		write(w, 404, map[string]string{"error": "sound parcel missing"})
		return
	}
	write(w, 409, map[string]string{"error": "sound parcel failed integrity checks"})
}

// handoffQuery parses the two address dialects. A picture query names a
// picture frame plus an in-frame row; a source query names the scanner's
// sound page plus the reported in-page row.
func handoffQuery(values url.Values) (handoff.Query, error) {
	mode := values.Get("mode")
	if mode == "" {
		mode = handoff.AddressPicture
	}
	frame, err := strconv.Atoi(values.Get("frame"))
	if err != nil {
		return handoff.Query{}, errors.New("frame must be an integer")
	}
	row, err := strconv.Atoi(values.Get("row"))
	if err != nil {
		return handoff.Query{}, errors.New("row must be an integer")
	}
	q := handoff.Query{Mode: mode, Frame: frame, Row: row}
	if err := q.Validate(); err != nil {
		return handoff.Query{}, err
	}
	return q, nil
}

func serveHandoffSearch(w http.ResponseWriter, r *http.Request, s *station.Station) {
	q, err := handoffQuery(r.URL.Query())
	if err != nil {
		invalid(w, err.Error())
		return
	}
	page, err := s.Handoff.SearchPage(handoff.PageRequest{
		Query:   q,
		Capture: r.URL.Query().Get("capture"),
		Token:   r.URL.Query().Get("snapshot"),
	})
	if err != nil {
		switch {
		case errors.Is(err, handoff.ErrSnapshot):
			write(w, 409, map[string]string{"error": err.Error()})
		case errors.Is(err, handoff.ErrNoParcels):
			write(w, 404, map[string]string{"error": err.Error()})
		default:
			invalid(w, err.Error())
		}
		return
	}
	write(w, 200, page)
}

func serveHandoffLocate(w http.ResponseWriter, r *http.Request, s *station.Station) {
	q, err := handoffQuery(r.URL.Query())
	if err != nil {
		invalid(w, err.Error())
		return
	}
	item, _, err := s.Handoff.Detail(r.PathValue("id"), q)
	if err != nil {
		handoffError(w, err)
		return
	}
	write(w, 200, item)
}

func serveHandoffAudition(w http.ResponseWriter, r *http.Request, s *station.Station) {
	q, err := handoffQuery(r.URL.Query())
	if err != nil {
		invalid(w, err.Error())
		return
	}
	item, pcm, err := s.Handoff.Detail(r.PathValue("id"), q)
	if err != nil {
		handoffError(w, err)
		return
	}
	header, err := wave.Inspect(pcm)
	if err != nil {
		handoffError(w, err)
		return
	}
	// Write the frozen bytes directly: the content-addressed parcel is the
	// cache key, and ServeContent's path cleaning would redirect this
	// extension-less sub-path before the operator's player ever sees bytes.
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	if item.Finding.SampleFrame != nil {
		w.Header().Set("X-Parcel-Sample-Frame", strconv.Itoa(*item.Finding.SampleFrame))
		w.Header().Set("X-Parcel-Verdict", item.Finding.Verdict)
	}
	w.Header().Set("X-Parcel-Sample-Rate", strconv.Itoa(header.Rate))
	w.Header().Set("Content-Length", strconv.Itoa(len(pcm)))
	_, _ = w.Write(pcm)
}

func handoffError(w http.ResponseWriter, err error) {
	if os.IsNotExist(err) {
		write(w, 404, map[string]string{"error": "sound parcel missing"})
		return
	}
	if errors.Is(err, handoff.ErrSnapshot) {
		write(w, 409, map[string]string{"error": err.Error()})
		return
	}
	write(w, 409, map[string]string{"error": "sound parcel failed integrity checks"})
}
