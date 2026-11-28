package lightbench

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"

	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

const cardCacheEntries = 24

type cardSheet struct {
	png  []byte
	etag string
}

// cardCache keeps recent sheets. A sheet is pure for one reel digest, profile
// and window, so neighbouring frames can be revisited without re-rendering.
type cardCache struct {
	mu      sync.Mutex
	entries map[string]cardSheet
	order   []string
}

func (c *cardCache) get(key string) (cardSheet, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sheet, ok := c.entries[key]
	return sheet, ok
}

func (c *cardCache) put(key string, sheet cardSheet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		return
	}
	for len(c.order) >= cardCacheEntries {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.entries[key] = sheet
	c.order = append(c.order, key)
}

// contactSheet draws the window's frame cards. It runs outside the interactive
// drawing slots and refuses immediately when its single slot is busy, so a
// near-48-frame window opening cannot stall audition or release.
func (b *bench) contactSheet(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	profile := r.URL.Query().Get("profile")
	key := fmt.Sprintf("%s|%s|%d-%d", reel.Digest, profile, window.Start, window.End)
	if sheet, ok := b.cards.get(key); ok {
		writeSheet(w, r, sheet)
		return
	}
	select {
	case b.sheets <- struct{}{}:
		defer func() { <-b.sheets }()
	default:
		w.Header().Set("Retry-After", "1")
		problem(w, http.StatusServiceUnavailable, "诊断画格正在绘制，请稍后重试。")
		return
	}
	data, _, calibration, err := station.Contact(reel, window, profile)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	sum := sha256.Sum256(data)
	sheet := cardSheet{png: data, etag: fmt.Sprintf(`"%x-%s-%d-%d"`, sum[:8], calibration.Profile, window.Start, window.End)}
	b.cards.put(key, sheet)
	writeSheet(w, r, sheet)
}

func writeSheet(w http.ResponseWriter, r *http.Request, sheet cardSheet) {
	w.Header().Set("ETag", sheet.etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == sheet.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", fmt.Sprint(len(sheet.png)))
	_, _ = w.Write(sheet.png)
}
