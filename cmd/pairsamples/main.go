// Command pairsamples creates two same-spec trial scans with known line faults.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

const (
	rows   = 2000
	frames = 7
	head   = 36
	delAt  = 3600
	delLen = 28
	insAt  = 8200
	insLen = 36
)

func main() {
	out := flag.String("out", "var/pair-scans", "catalog directory for trial-a and trial-b")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o750); err != nil {
		log.Fatal(err)
	}
	aDir := filepath.Join(*out, "trial-a")
	bDir := filepath.Join(*out, "trial-b")
	if err := os.Mkdir(aDir, 0o750); err != nil {
		log.Fatal(err)
	}
	if err := os.Mkdir(bDir, 0o750); err != nil {
		log.Fatal(err)
	}
	if err := generate(aDir, "trial-a", false); err != nil {
		log.Fatal(err)
	}
	if err := generate(bDir, "trial-b", true); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pair samples written to %s (head=%d, deletion@%d+%d, insertion@%d+%d)\n", *out, head, delAt, delLen, insAt, insLen)
}

func header(name string) capture.Header {
	return capture.Header{
		Name: name, Width: 112, Rows: rows, FPS: 24, Frames: frames, Lead: 230,
		Picture: capture.Span{X0: 0, X1: 64},
		Lanes:   [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}},
		Profiles: []capture.Profile{
			{ID: "bench", Dark: patch(0, 0, 36), Clear: patch(0, 36, 72)},
			{ID: "soft", Dark: patch(1, 0, 36), Clear: patch(1, 36, 72)},
		},
	}
}

func patch(frame, start, end int) capture.Patch {
	return capture.Patch{Frame: frame, Rows: capture.Span{X0: start, X1: end}}
}

func signal(lane, x int) float64 {
	f := []float64{245, 415}[lane]
	t := float64(x) / 48000
	v := (0.7 + 0.3*math.Sin(2*math.Pi*float64(x)/2100+float64(lane))) * math.Sin(2*math.Pi*f*t)
	v += 0.18 * math.Sin(2*math.Pi*float64(x)/760+1.4*float64(lane))
	// Same phrase is physically repeated in both trials.
	for _, start := range []int{1500, 6900} {
		u := x - start
		if u >= 0 && u < 420 {
			env := math.Sin(math.Pi * float64(u) / 420)
			v += (0.75 + 0.2*float64(lane)) * env * math.Sin(2*math.Pi*(2.0+0.004*float64(u))*float64(u)/48000*12)
		}
	}
	return math.Tanh(v * 1.25)
}

func bCoordinates(n int) []int {
	coords := make([]int, 0, n+200)
	for x := -head; len(coords) < n+80 && x < n+head+500; x++ {
		if x == delAt {
			x += delLen - 1 // skip delLen base rows
			continue
		}
		if x == insAt {
			for k := 0; k < insLen; k++ {
				coords = append(coords, x+k)
			}
			for k := 0; k < insLen; k++ {
				coords = append(coords, x+k) // repeated copy
			}
			x += insLen - 1
			continue
		}
		coords = append(coords, x)
	}
	return coords[:n]
}

func generate(dir, name string, faulty bool) error {
	h := header(name)
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "strip.json"), raw, 0o640); err != nil {
		return err
	}
	n := rows * frames
	coords := make([]int, n)
	for i := range coords {
		coords[i] = i
	}
	if faulty {
		coords = bCoordinates(n)
	}
	dark := []float64{6000, 8200}
	clear := []float64{55000, 50200}
	for frame := 0; frame < frames; frame++ {
		if frame == 5 { // physical missing page in both trials
			continue
		}
		img := image.NewGray16(image.Rect(0, 0, h.Width, h.Rows))
		for y := 0; y < rows; y++ {
			j := frame*rows + y
			x := coords[j]
			for px := 0; px < h.Width; px++ {
				val := uint16(22000)
				for lane, band := range h.Lanes {
					if px < band.X0 || px >= band.X1 {
						continue
					}
					g, c := dark[lane], clear[lane]
					if frame < 2 && y < 72 {
						if y < 36 {
							val = uint16(g)
						} else {
							val = uint16(c)
						}
					} else {
						unit := signal(lane, x)*0.5 + 0.5
						val = uint16(g + unit*(c-g))
					}
				}
				img.SetGray16(px, y, color.Gray16{Y: val})
			}
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("emulsion-%04d.png", frame)))
		if err != nil {
			return err
		}
		err = png.Encode(f, img)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
