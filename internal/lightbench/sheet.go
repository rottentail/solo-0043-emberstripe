package lightbench

import (
	"bytes"
	"fmt"
	"image/png"
	"net/http"
	"strconv"
)

// sheet renders the registered full emulsion page. The catalog keeps a
// decoded read-only bitmap; this endpoint avoids reaching back into the
// administrator's scan directory while still allowing original-page review.
func (b *bench) sheet(w http.ResponseWriter, r *http.Request) {
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
	etag := fmt.Sprintf(`"%s-sheet-v1"`, sheet.Digest)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, sheet.Bitmap); err != nil {
		problem(w, http.StatusInternalServerError, "暂时无法绘制完整原页。")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}
