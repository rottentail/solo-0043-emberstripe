// Package httpapi exposes the restoration station's local HTTP interface.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/annotation"
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
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
	mux.HandleFunc("GET /captures/{name}/annotations", func(w http.ResponseWriter, r *http.Request) {
		reel, ok := s.Reel(r.PathValue("name"))
		if !ok {
			write(w, 404, map[string]string{"error": "capture unavailable"})
			return
		}
		data, err := s.Annotations(reel)
		if err != nil {
			annotationError(w, err)
			return
		}
		binary(w, "application/json", data)
	})
	mux.HandleFunc("POST /captures/{name}/annotations", func(w http.ResponseWriter, r *http.Request) {
		reel, ok := s.Reel(r.PathValue("name"))
		if !ok {
			write(w, 404, map[string]string{"error": "capture unavailable"})
			return
		}
		body, err := readBody(w, r)
		if err != nil {
			invalid(w, "意见文档读取失败或超过大小上限。")
			return
		}
		doc, err := s.ImportAnnotations(reel, body)
		if err != nil {
			annotationError(w, err)
			return
		}
		write(w, 200, doc)
	})
	mux.HandleFunc("GET /captures/{name}/annotations/projection", func(w http.ResponseWriter, r *http.Request) {
		reel, ok := s.Reel(r.PathValue("name"))
		if !ok {
			write(w, 404, map[string]string{"error": "capture unavailable"})
			return
		}
		var hypothesis *int
		if raw := r.URL.Query().Get("lead"); raw != "" {
			lead, err := strconv.Atoi(raw)
			if err != nil {
				invalid(w, "偏移假设须为整数行数。")
				return
			}
			hypothesis = &lead
		}
		report, err := s.ProjectAnnotations(reel, hypothesis)
		if err != nil {
			annotationError(w, err)
			return
		}
		write(w, 200, report)
	})
	mux.HandleFunc("POST /parcels/{id}/annotations", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.Store.Read(r.PathValue("id"))
		if err != nil {
			parcelError(w, err)
			return
		}
		receipt, err := parcel.Verify(b)
		if err != nil {
			parcelError(w, err)
			return
		}
		body, err := readBody(w, r)
		if err != nil {
			invalid(w, "意见文档读取失败或超过大小上限。")
			return
		}
		doc, err := annotation.Parse(body)
		if err != nil {
			annotationError(w, err)
			return
		}
		if doc.Capture == receipt.Index.Capture {
			if err := doc.ReconcileRows(receipt.Index.Rows); err != nil {
				annotationError(w, err)
				return
			}
		}
		write(w, 200, annotation.CheckParcel(doc, receipt.ID, receipt.Index))
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

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, annotation.Limit+1))
}

func annotationError(w http.ResponseWriter, err error) {
	var corrupt *annotation.Corrupt
	var invalidInput *annotation.Invalid
	switch {
	case errors.As(err, &corrupt):
		write(w, 409, map[string]string{"error": "已存意见文件未通过校验，请重新导入。"})
	case errors.As(err, &invalidInput):
		invalid(w, err.Error())
	case os.IsNotExist(err):
		write(w, 404, map[string]string{"error": "尚未导入意见文件。"})
	default:
		write(w, 500, map[string]string{"error": "意见文件暂时不可用。"})
	}
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
