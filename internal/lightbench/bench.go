package lightbench

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"

	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

//go:embed templates/*.html assets/styles/*.css assets/scripts/*.js
var files embed.FS

var pages = template.Must(template.ParseFS(files, "templates/*.html"))

var entryPattern = regexp.MustCompile(`^e[0-9]{1,9}$`)

type bench struct {
	station *station.Station
	history *historyStore
	renders chan struct{}
}

func New(s *station.Station, releases string) http.Handler {
	b := &bench{station: s, history: newHistory(releases), renders: make(chan struct{}, 2)}
	mux := http.NewServeMux()
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /light-assets/", http.StripPrefix("/light-assets/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /{$}", b.home)
	mux.HandleFunc("GET /lightbench/{reel}", b.reelPage)
	mux.HandleFunc("GET /lightbench/{reel}/window", b.drawing(b.window))
	mux.HandleFunc("GET /lightbench/{reel}/strip", b.drawing(b.strip))
	mux.HandleFunc("GET /lightbench/{reel}/history", b.drawing(b.historyView))
	mux.HandleFunc("GET /lightbench/{reel}/applied", b.drawing(b.applied))
	mux.HandleFunc("POST /lightbench/{reel}/undo", b.drawing(b.undo))
	mux.HandleFunc("POST /lightbench/{reel}/redo", b.drawing(b.redo))
	mux.HandleFunc("POST /lightbench/{reel}/focus", b.drawing(b.focus))
	mux.HandleFunc("POST /lightbench/{reel}/freeze", b.drawing(b.freeze))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}

func (b *bench) drawing(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case b.renders <- struct{}{}:
			defer func() { <-b.renders }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			problem(w, http.StatusServiceUnavailable, "工位正在绘制其他画格，请稍后重试。")
		}
	}
}
