package lightbench

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

const (
	defaultFragmentSpan = 64
	maxFragmentSpan     = 256
	fragmentColumnScale = 3
)

// fragmentRow keeps both the scanner-native 16-bit intensity and the value an
// 8-bit display channel can actually represent. Two native levels that quantize
// to the same shade are still distinguishable through quantized_away.
type fragmentRow struct {
	Row       int  `json:"row"`
	Native    int  `json:"native_gray"`
	Displayed int  `json:"displayed_gray"`
	Quantized bool `json:"quantized_away"`
}

type fragmentResponse struct {
	Frame    int           `json:"frame"`
	Depth    int           `json:"depth"`
	RowStart int           `json:"row_start"`
	RowEnd   int           `json:"row_end"`
	Selected int           `json:"selected_row"`
	Columns  int           `json:"image_columns"`
	Rows     []fragmentRow `json:"rows"`
}

// centeredSpan returns at most span rows around the selected row, sliding the
// window inward at the frame edges while keeping every row inside [0, limit).
func centeredSpan(row, span, limit int) (int, int) {
	start := row - span/2
	if start < 0 {
		start = 0
	}
	end := start + span
	if end > limit {
		end = limit
		start = max(0, end-span)
	}
	return start, end
}

// fragmentParams resolves the frame, center row and half-open row span. A
// missing picture sheet is reported as 404 without touching the sound clock,
// so picture gaps never block the waveform or probe workflows.
func (b *bench) fragmentParams(w http.ResponseWriter, r *http.Request) (reel *capture.Reel, sheet capture.Sheet, frame, row, start, end int, ok bool) {
	reel, exists := b.reel(w, r)
	if !exists {
		return nil, capture.Sheet{}, 0, 0, 0, 0, false
	}
	frame, err := strconv.Atoi(r.URL.Query().Get("frame"))
	if err != nil || frame < 0 || frame >= reel.Frames {
		problem(w, http.StatusBadRequest, "画格超出扫描卷范围。")
		return nil, capture.Sheet{}, 0, 0, 0, 0, false
	}
	row, err = strconv.Atoi(r.URL.Query().Get("row"))
	if err != nil || row < 0 || row >= reel.Rows {
		problem(w, http.StatusBadRequest, "请选择该画格内的扫描行。")
		return nil, capture.Sheet{}, 0, 0, 0, 0, false
	}
	span := defaultFragmentSpan
	if raw := r.URL.Query().Get("span"); raw != "" {
		span, err = strconv.Atoi(raw)
		if err != nil || span < 1 || span > maxFragmentSpan {
			problem(w, http.StatusBadRequest, "局部片段行数需在 1 到 256 之间。")
			return nil, capture.Sheet{}, 0, 0, 0, 0, false
		}
	}
	sheet, hasPicture := reel.Sheets[frame]
	if !hasPicture {
		problem(w, http.StatusNotFound, "此画格没有画面扫描页，仍可在波形和探针中继续声音检查。")
		return nil, capture.Sheet{}, 0, 0, 0, 0, false
	}
	start, end = centeredSpan(row, span, reel.Rows)
	return reel, sheet, frame, row, start, end, true
}

// fragment serves the numeric neighborhood so the browser can compare true
// scanner brightness against what an 8-bit display channel can render.
func (b *bench) fragment(w http.ResponseWriter, r *http.Request) {
	reel, sheet, frame, row, start, end, ok := b.fragmentParams(w, r)
	if !ok {
		return
	}
	response := fragmentResponse{
		Frame: frame, Depth: sheet.Depth, RowStart: start, RowEnd: end,
		Selected: row, Columns: min(reel.Picture.Width(), 144) * fragmentColumnScale,
		Rows: make([]fragmentRow, 0, end-start),
	}
	for sourceRow := start; sourceRow < end; sourceRow++ {
		var sum uint64
		for x := reel.Picture.X0; x < reel.Picture.X1; x++ {
			sum += uint64(sheet.Gray(x, sourceRow))
		}
		native := int(sum / uint64(reel.Picture.Width()))
		displayed := native / 257
		response.Rows = append(response.Rows, fragmentRow{
			Row: sourceRow, Native: native, Displayed: displayed,
			Quantized: native != displayed*257,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// fragmentImage draws the same neighborhood magnified horizontally; source
// rows are never averaged together, preserving edge stripes one row tall.
func (b *bench) fragmentImage(w http.ResponseWriter, r *http.Request) {
	reel, sheet, _, _, start, end, ok := b.fragmentParams(w, r)
	if !ok {
		return
	}
	etag := fmt.Sprintf(`"%s-%s-fragment-v1-%d-%d"`, reel.Digest, sheet.Digest, start, end)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	columnCount := min(reel.Picture.Width(), 144)
	imageWidth := columnCount * fragmentColumnScale
	picture := image.NewGray(image.Rect(0, 0, imageWidth, end-start))
	for y := start; y < end; y++ {
		for column := 0; column < columnCount; column++ {
			sourceX := reel.Picture.X0 + column*reel.Picture.Width()/columnCount
			shade := uint8(sheet.Gray(sourceX, y) / 257)
			for dx := 0; dx < fragmentColumnScale; dx++ {
				picture.Pix[(y-start)*picture.Stride+column*fragmentColumnScale+dx] = shade
			}
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		problem(w, http.StatusInternalServerError, "暂时无法绘制局部片段。")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(data.Len()))
	_, _ = w.Write(data.Bytes())
}
