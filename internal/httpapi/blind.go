package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/blind"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// maxBlindBody keeps a sheet creation request bounded to a small JSON document.
const maxBlindBody = 64 << 10

func parseWindowIndex(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("window is required")
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("window must be a non-negative integer")
	}
	return value, nil
}

func (h *apiHandlers) createBlindSheet(w http.ResponseWriter, r *http.Request) {
	reel, ok := h.station.Reel(r.PathValue("name"))
	if !ok {
		write(w, 404, map[string]string{"error": "capture unavailable"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBlindBody+1))
	if err != nil || len(body) > maxBlindBody {
		invalid(w, "blind sheet request is too large or unreadable")
		return
	}
	var req blind.CreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		invalid(w, "blind sheet request must be valid JSON")
		return
	}
	if req.Capture == "" {
		req.Capture = reel.Name
	}
	if req.Capture != reel.Name {
		invalid(w, "capture in body must match the requested capture")
		return
	}
	sheet, err := h.station.CreateBlind(reel, req)
	if err != nil {
		var noCommon *blind.NoCommonError
		switch {
		case errors.As(err, &noCommon):
			write(w, 422, map[string]any{
				"error":  "window has no common audible range",
				"reason": blind.ReasonNoCommon,
				"window": noCommon.Window,
			})
		default:
			invalid(w, err.Error())
		}
		return
	}
	// The public view deliberately omits profile identities and audio hashes.
	write(w, 201, blind.PublicView(sheet))
}

func (h *apiHandlers) listBlindSheets(w http.ResponseWriter, r *http.Request) {
	ids, err := h.station.BlindIDs()
	if err != nil {
		write(w, 500, map[string]string{"error": "blind store unavailable"})
		return
	}
	write(w, 200, map[string]any{"sheets": ids})
}

func (h *apiHandlers) getBlindSheet(w http.ResponseWriter, r *http.Request) {
	sheet, err := h.station.BlindSheet(r.PathValue("id"))
	if err != nil {
		blindStoreError(w, err)
		return
	}
	reviewer := r.URL.Query().Get("reviewer")
	if reviewer != "" {
		if _, err := blind.NormalizeReviewer(reviewer); err != nil {
			invalid(w, err.Error())
			return
		}
	}
	write(w, 200, blind.PublicViewFor(sheet, reviewer))
}

func (h *apiHandlers) blindOption(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	window, err := parseWindowIndex(query.Get("window"))
	if err != nil {
		invalid(w, "window must be a non-negative integer")
		return
	}
	option := query.Get("option")
	if option != "A" && option != "B" {
		invalid(w, `option must be "A" or "B"`)
		return
	}
	audio, _, err := h.station.BlindOptionAudio(r.PathValue("id"), window, option)
	if err != nil {
		blindRenderError(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "blind-option.wav", time.Time{}, bytes.NewReader(audio))
}

func (h *apiHandlers) answerBlindSheet(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBlindBody+1))
	if err != nil || len(body) > maxBlindBody {
		invalid(w, "answer is too large or unreadable")
		return
	}
	var choice blind.Choice
	if err := json.Unmarshal(body, &choice); err != nil {
		invalid(w, "answer must be valid JSON")
		return
	}
	if choice.Option != "A" && choice.Option != "B" {
		invalid(w, `option must be "A" or "B"`)
		return
	}
	sheet, err := h.station.RecordBlindChoice(r.PathValue("id"), choice)
	if err != nil {
		blindStoreError(w, err)
		return
	}
	write(w, 200, blind.PublicView(sheet))
}

func (h *apiHandlers) unblindSheet(w http.ResponseWriter, r *http.Request) {
	result, err := h.station.Unblind(r.PathValue("id"))
	if err != nil {
		blindStoreError(w, err)
		return
	}
	write(w, 200, result)
}

func blindStoreError(w http.ResponseWriter, err error) {
	if blind.IsNotExist(err) {
		write(w, 404, map[string]string{"error": "blind sheet missing"})
		return
	}
	invalid(w, err.Error())
}

func blindRenderError(w http.ResponseWriter, err error) {
	if blind.IsNotExist(err) || errors.Is(err, station.ErrCaptureGone) {
		write(w, 409, map[string]string{"error": "blind sheet material no longer matches the catalog"})
		return
	}
	invalid(w, err.Error())
}
