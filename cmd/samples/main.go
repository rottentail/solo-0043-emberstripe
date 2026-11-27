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
	tabletDir := filepath.Join(*root, "step-tablet")
	if err := os.Mkdir(tabletDir, 0750); err != nil {
		log.Fatal(err)
	}
	if err := generateTablet(tabletDir); err != nil {
		log.Fatal(err)
	}
}

func generate(dir, name string, lead, rows, fps, gap int) error {
	h := capture.Header{Name: name, Width: 112, Rows: rows, FPS: fps, Frames: 7, Lead: lead, Picture: capture.Span{X0: 0, X1: 64}, Lanes: [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}}, Profiles: []capture.Profile{
		{ID: "bench", Dark: patch(0, 0, 36), Clear: patch(0, 36, 72)},
		{ID: "soft", Dark: patch(1, 0, 36), Clear: patch(1, 36, 72)},
		{ID: "flat", Dark: patch(0, 0, 18), Clear: patch(0, 18, 36)},
		{ID: "absent", Dark: patch(gap, 0, 36), Clear: patch(0, 36, 72)},
	}}
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
						mark := int(math.Round(8 + 6*math.Sin(t*2*math.Pi*float64(280+220*lane))))
						intensity = uint16(dark)
						if x-band.X0 < mark {
							intensity = uint16(clear)
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

// tabletStepSpec describes one engineered step-strip interval. The coverage
// cases are carved around missing frame 3 (global rows 1200..1600):
// s07 keeps only 16 valid rows, s08 has none, s09 keeps 24 against s10's 128.
type tabletStepSpec struct {
	id, label string
	intensity float64
	start     int
	end       int
}

var tabletSpecs = []tabletStepSpec{
	{"s01", "1", 0.000, 88, 216},
	{"s02", "2", 0.080, 232, 360},
	{"s03", "3", 0.160, 488, 616},
	{"s04", "4", 0.240, 632, 760},
	{"s05", "5", 0.340, 816, 944},
	{"s06", "6", 0.355, 960, 1088},
	{"s07", "7", 0.460, 1184, 1280},
	{"s08", "8", 0.540, 1296, 1400},
	{"s09", "9", 0.620, 1408, 1624},
	{"s10", "10", 0.700, 1656, 1784},
	{"s11", "11", 0.780, 1800, 1928},
	{"s12", "12", 0.860, 2016, 2144},
	{"s13", "13", 0.940, 2160, 2288},
	{"s14", "14", 1.000, 2304, 2400},
}

func generateTablet(dir string) error {
	const rows, frames, gapFrame = 400, 6, 3
	steps := make([]capture.TabletStep, 0, len(tabletSpecs))
	for _, s := range tabletSpecs {
		steps = append(steps, capture.TabletStep{ID: s.id, Label: s.label, Intensity: s.intensity, StartRow: s.start, EndRow: s.end})
	}
	h := capture.Header{
		Name: "step-tablet", Width: 112, Rows: rows, FPS: 24, Frames: frames, Lead: 0,
		Picture: capture.Span{X0: 0, X1: 64},
		Lanes:   [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}},
		Profiles: []capture.Profile{
			{ID: "bench", Dark: patch(0, 0, 32), Clear: patch(0, 32, 64)},
			{ID: "soft", Dark: patch(1, 0, 32), Clear: patch(1, 32, 64)},
		},
		Tablet: &capture.Tablet{Steps: steps},
	}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "strip.json"), raw, 0640); err != nil {
		return err
	}

	// global row -> step index lookup
	atRow := make([]int, rows*frames)
	for i := range atRow {
		atRow[i] = -1
	}
	for i, s := range tabletSpecs {
		for y := s.start; y < s.end; y++ {
			atRow[y] = i
		}
	}

	for frame := 0; frame < frames; frame++ {
		if frame == gapFrame {
			continue
		}
		bounds := image.Rect(0, 0, h.Width, h.Rows)
		img := image.NewGray16(bounds)
		for y := 0; y < rows; y++ {
			global := frame*rows + y
			step := atRow[global]
			for x := 0; x < h.Width; x++ {
				intensity := uint16(26000 + (x*13+y*7)%5000)
				if x < h.Picture.X1 {
					intensity = uint16(36000 + (x*19+y*11+frame*97)%9000)
				}
				for lane, band := range h.Lanes {
					if x < band.X0 || x >= band.X1 {
						continue
					}
					dark, clear := 6000+lane*2200, 55000-lane*4800
					// Flat calibration reference patches.
					if frame < 2 && y < 64 {
						d, c := dark, clear
						if frame == 1 {
							d += 5000
							c -= 6500
						}
						if y < 32 {
							intensity = uint16(d)
						} else {
							intensity = uint16(c)
						}
						continue
					}
					if step < 0 {
						intensity = uint16(20000 + lane*1500)
						continue
					}
					known := tabletSpecs[step].intensity
					// Lane-specific latitude curves. Left resolves a wider
					// range; right has a darker toe and more row grain.
					var q, rowAmp, pixAmp float64
					if lane == 0 {
						q = (known - 0.15) / 0.75
						rowAmp, pixAmp = 0.022, 0.010
					} else {
						q = (known - 0.23) / 0.67
						rowAmp, pixAmp = 0.038, 0.014
					}
					span := float64(clear - dark)
					rowNoise := jitter(7, global, lane) * rowAmp * span
					pixNoise := jitter(13, global, x, lane) * pixAmp * span
					value := math.Round(float64(dark) + q*span + rowNoise + pixNoise)
					intensity = uint16(max(0, min(65535, int(value))))
				}
				img.Set(x, y, color.Gray16{Y: intensity})
			}
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("emulsion-%04d.png", frame)))
		if err != nil {
			return err
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// jitter is a deterministic centered pseudo-noise value in [-1, 1].
func jitter(salt int, parts ...int) float64 {
	h := uint64(1469598103934665603)
	h ^= uint64(salt)
	h *= 1099511628211
	for _, p := range parts {
		h ^= uint64(p)
		h *= 1099511628211
		h ^= h >> 29
	}
	return float64(h%2001)/1000.0 - 1
}
