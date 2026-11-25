// Command samples creates two reusable synthetic scanner inputs.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

func main() {
	root := flag.String("out", "var/scans", "parent for new sample captures")
	flag.Parse()
	if err := os.MkdirAll(*root, 0750); err != nil {
		log.Fatal(err)
	}
	for _, fixture := range []struct {
		name                 string
		lead, rows, fps, gap int
	}{{"cinder-a", 230, 2000, 24, 2}, {"vireo-b", -135, 1920, 25, 3}} {
		dir := filepath.Join(*root, fixture.name)
		if err := os.Mkdir(dir, 0750); err != nil {
			log.Fatal(err)
		}
		if err := generate(dir, fixture.name, fixture.lead, fixture.rows, fixture.fps, fixture.gap); err != nil {
			log.Fatal(err)
		}
	}
}

func generate(dir, name string, lead, rows, fps, gap int) error {
	h := capture.Header{Name: name, Width: 112, Rows: rows, FPS: fps, Frames: 7, Lead: lead, Picture: capture.Span{X0: 0, X1: 64}, Lanes: [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}}, Profiles: []capture.Profile{
		{ID: "bench", Dark: patch(0, 0, 36), Clear: patch(0, 36, 72)},
		{ID: "soft", Dark: patch(1, 0, 36), Clear: patch(1, 36, 72)},
		{ID: "flat", Dark: patch(0, 0, 18), Clear: patch(0, 18, 36)},
		{ID: "absent", Dark: patch(gap, 0, 36), Clear: patch(0, 36, 72)},
	}}
	// Edge marks live only in the gaps outside picture and both sound lanes.
	// cinder-a shares one transport cadence between edge and lanes; vireo-b
	// carries a deliberately close but distinct lane frequency, so the
	// verification must keep the two causes separate.
	edgeHz := 165.0
	leftHz, rightHz := 165.0, 165.0
	if name == "vireo-b" {
		leftHz, rightHz = 142.0, 142.0
	}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "strip.json"), raw, 0640); err != nil {
		return err
	}
	for frame := 0; frame < h.Frames; frame++ {
		if frame == gap {
			continue
		}
		bounds := image.Rect(0, 0, h.Width, h.Rows)
		var img draw.Image = image.NewGray16(bounds)
		if frame == 4 {
			img = image.NewGray(bounds)
		}
		for y := 0; y < h.Rows; y++ {
			for x := 0; x < h.Width; x++ {
				intensity := uint16((x*271+y*59+frame*1079)%48300 + 8000)
				if x < h.Picture.X1 && ((x+frame*5)%25 < 4 || y%500 < 16) {
					intensity = 57000
				}
				// Transport periodicity is painted into the edge gaps only,
				// never into the picture columns or the optical sound lanes.
				if (x >= h.Picture.X1 && x < h.Lanes[0].X0) || (x >= h.Lanes[1].X1 && x < h.Width) {
					if frame >= 2 {
						edgeTime := float64(frame*h.Rows+y) / float64(fps*h.Rows)
						intensity = uint16(30000 + 4200*math.Sin(edgeTime*2*math.Pi*edgeHz))
					}
				}
				for lane, band := range h.Lanes {
					if x < band.X0 || x >= band.X1 {
						continue
					}
					dark, clear := 6000+lane*2200, 55000-lane*4800
					if frame < 2 && y < 72 {
						if frame == 1 {
							dark += 5000
							clear -= 6500
						}
						if y < 36 {
							intensity = uint16(dark)
						} else {
							intensity = uint16(clear)
						}
					} else {
						t := float64(frame*h.Rows+y) / float64(h.FPS*h.Rows)
						laneHz := leftHz
						if lane == 1 {
							laneHz = rightHz
						}
						mark := int(math.Round(8 + 6*math.Sin(t*2*math.Pi*float64(280+220*lane))))
						intensity = uint16(dark)
						if x-band.X0 < mark {
							intensity = uint16(clear)
						}
						// A faint transport-synchronized modulation rides on
						// the programme waveform in the lane columns.
						if frame >= 2 {
							ride := 3200 * math.Sin(t*2*math.Pi*laneHz)
							intensity = uint16(math.Max(0, math.Min(65535, float64(intensity)+ride)))
						}
						if frame == 5 && y >= 120 && y < 155 && lane == 1 {
							intensity = 64000
						}
					}
				}
				img.Set(x, y, color.Gray16{Y: intensity})
			}
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("emulsion-%04d.png", frame)))
		if err != nil {
			return err
		}
		encodeErr := png.Encode(f, img)
		if closeErr := f.Close(); encodeErr != nil || closeErr != nil {
			return fmt.Errorf("write sheet: %v %v", encodeErr, closeErr)
		}
	}
	return nil
}

func patch(frame, start, end int) capture.Patch {
	return capture.Patch{Frame: frame, Rows: capture.Span{X0: start, X1: end}}
}
