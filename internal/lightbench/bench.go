// Package lightbench serves the browser workstation for optical-sound inspection.
package lightbench

import (
	"embed"
	"html/template"
	"io/fs"
	"math"
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

//go:embed templates/*.html assets/styles/*.css assets/scripts/*.js
var files embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"mul100": func(v float64) int { return int(math.Round(v * 100)) },
}).ParseFS(files, "templates/*.html"))

type bench struct {
	station *station.Station
	renders chan struct{}
	evals   chan struct{}
}

func New(s *station.Station) http.Handler {
	b := &bench{station: s, renders: make(chan struct{}, 2), evals: make(chan struct{}, 2)}
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
	mux.HandleFunc("GET /lightbench/{reel}/tablet", b.tabletPage)
	mux.HandleFunc("GET /lightbench/{reel}/tablet/evaluate", b.evaluating(b.tabletData))
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

func (b *bench) evaluating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case b.evals <- struct{}{}:
			defer func() { <-b.evals }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			problem(w, http.StatusServiceUnavailable, "试条评估槽位繁忙，请稍后重试。")
		}
	}
}
