package lightbench

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"strconv"
)

// strip projects a scan horizontally so the browser can map pointer position
// to picture rows. Each output column averages only its own source interval.
func (b *bench) strip(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	frame, err := strconv.Atoi(r.URL.Query().Get("frame"))
	if err != nil || frame < 0 || frame >= reel.Frames {
		problem(w, http.StatusBadRequest, "画格超出扫描卷范围。")
		return
	}
	sheet, ok := reel.Sheets[frame]
	if !ok {
		problem(w, http.StatusNotFound, "此画格没有扫描页。")
		return
	}
	etag := fmt.Sprintf(`"%s-%s-strip-v1"`, reel.Digest, sheet.Digest)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	width, height := min(reel.Rows, 768), min(reel.Picture.Width(), 144)
	strip := image.NewGray(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		start, end := x*reel.Rows/width, (x+1)*reel.Rows/width
		for y := 0; y < height; y++ {
			column := reel.Picture.X0 + y*reel.Picture.Width()/height
			var sum uint64
			for row := start; row < end; row++ {
				sum += uint64(sheet.Gray(column, row))
			}
			strip.Pix[y*strip.Stride+x] = uint8(sum / uint64(end-start) / 257)
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, strip); err != nil {
		problem(w, http.StatusInternalServerError, "暂时无法绘制画格。")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(data.Len()))
	_, _ = w.Write(data.Bytes())
}
