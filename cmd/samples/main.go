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
		sync                 bool
	}{{"cinder-a", 230, 2000, 24, 2, false}, {"vireo-b", -135, 1920, 25, 3, false}, {"sync-d", 0, 500, 24, 10, true}} {
		dir := filepath.Join(*root, fixture.name)
		if err := os.Mkdir(dir, 0750); err != nil {
			log.Fatal(err)
		}
		if fixture.sync {
			if err := generateSync(dir, fixture.name, fixture.rows, fixture.fps, fixture.gap); err != nil {
				log.Fatal(err)
			}
			continue
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

// generateSync builds a dual-lane calibration reel. Both lanes carry the same
// short check sequence; the right lane is printed seven picture rows late.
// The sequence repeats every syncPeriod rows, except for one distinctive long
// block that lets a wider window resolve the lag instead of a period shift.
func generateSync(dir, name string, rows, fps, gap int) error {
	const (
		syncPeriod = 100
		syncLag    = 7
		markerRow  = 3000
		markerSpan = 150
		clipStart  = 6000
		clipSpan   = 100
	)
	h := capture.Header{Name: name, Width: 112, Rows: rows, FPS: fps, Frames: 14, Lead: 0,
		Picture: capture.Span{X0: 0, X1: 64}, Lanes: [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}},
		Profiles: []capture.Profile{
			{ID: "bench", Dark: patch(0, 0, 36), Clear: patch(0, 40, 76)},
			{ID: "soft", Dark: patch(1, 0, 36), Clear: patch(1, 40, 76)},
			{ID: "flat", Dark: patch(0, 0, 18), Clear: patch(0, 18, 36)},
			{ID: "absent", Dark: patch(gap, 0, 36), Clear: patch(0, 40, 76)},
		}}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "strip.json"), raw, 0640); err != nil {
		return err
	}
	// level is the shared check sequence on the source-row clock. The body is
	// a short clear block repeating every period. One marker span uses an
	// irregular pulse rhythm, so no whole-period shift can line up with it.
	markerWidths := []int{13, 29, 7, 41, 17, 23, 11, 37, 19, 31, 9, 27, 15, 21}
	markerLevel := func(offset int) int {
		for _, width := range markerWidths {
			if offset < width {
				return 1
			}
			offset -= width
			if offset < width {
				return 0
			}
			offset -= width
		}
		return 0
	}
	level := func(sourceRow int) int {
		if sourceRow >= markerRow && sourceRow < markerRow+2*markerSpan {
			return markerLevel(sourceRow - markerRow)
		}
		if sourceRow%syncPeriod < 28 {
			return 1
		}
		return 0
	}
	for frame := 0; frame < h.Frames; frame++ {
		if frame == gap {
			continue
		}
		bounds := image.Rect(0, 0, h.Width, h.Rows)
		img := image.NewGray16(bounds)
		for y := 0; y < h.Rows; y++ {
			pictureRow := frame*rows + y
			for x := 0; x < h.Width; x++ {
				intensity := uint16((x*271+y*59+frame*1079)%48300 + 8000)
				if x < h.Picture.X1 && ((x+frame*5)%25 < 4 || y%500 < 16) {
					intensity = 57000
				}
				for lane, band := range h.Lanes {
					if x < band.X0 || x >= band.X1 {
						continue
					}
					dark, clear := uint16(6000+lane*2200), uint16(55000-lane*4800)
					switch {
					case frame < 2 && y < 36:
						intensity = dark
						if frame == 1 {
							intensity += 1500
						}
					case frame < 2 && y >= 40 && y < 76:
						intensity = clear
						if frame == 1 {
							intensity -= 2000
						}
					default:
						sourceRow := pictureRow
						if lane == 1 {
							sourceRow -= syncLag
						}
						if sourceRow >= 0 && level(sourceRow) == 1 {
							intensity = clear
						} else {
							intensity = dark
						}
						// One-sided overload: the right lane saturates past its
						// own clear endpoint, so these rows cannot be evidence.
						if lane == 1 && pictureRow >= clipStart && pictureRow < clipStart+clipSpan {
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
