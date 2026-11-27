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
	if err := generateTransferPair(*root); err != nil {
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

// transferGeometry is shared by the two devices so frame rows and lane columns
// address the same physical strip locations on both scans.
type transferGeometry struct {
	width, rows, fps, frames int
	picture                  capture.Span
	lanes                    [2]capture.Span
}

func transferPairGeometry() transferGeometry {
	return transferGeometry{
		width: 112, rows: 1600, fps: 30, frames: 6,
		picture: capture.Span{X0: 0, X1: 64},
		lanes:   [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}},
	}
}

// transferProfiles declares the full calibration on the reference device, the
// intermediate strips both devices can read, and the target's native profiles
// whose dark/clear boards have moved outside the new scanner's frame.
func transferProfiles() []capture.Profile {
	return []capture.Profile{
		{ID: "bench", Dark: patch(0, 0, 40), Clear: patch(0, 40, 80)},
		{ID: "mid-a", Dark: patch(1, 0, 40), Clear: patch(1, 40, 80)},
		{ID: "mid-b", Dark: patch(2, 0, 40), Clear: patch(2, 40, 80)},
		{ID: "mid-c", Dark: patch(3, 0, 40), Clear: patch(3, 40, 80)},
		// mid-d survives on both scanners; mid-e's board sits on the frame the
		// new scanner never captured, so only the old device can read it.
		{ID: "mid-d", Dark: patch(4, 0, 40), Clear: patch(4, 40, 80)},
		{ID: "mid-e", Dark: patch(5, 0, 40), Clear: patch(5, 40, 80)},
		{ID: "broken", Dark: patch(5, 0, 40), Clear: patch(0, 40, 80)},
	}
}

// scannerResponse maps a physical optical level to the gray value one device
// records. Both devices are linear but differ in gain and black level.
func scannerResponse(level, slope, intercept float64) uint16 {
	v := math.Round(slope*level + intercept)
	if v < 0 {
		v = 0
	}
	if v > 65535 {
		v = 65535
	}
	return uint16(v)
}

// generateTransferPair writes two scans of one calibration board set:
// beacon-ref keeps every dark/clear reference, while beacon-new's own
// reference boards are clipped by its frame and only the intermediate strips
// stay readable on both machines.
func generateTransferPair(root string) error {
	geometry := transferPairGeometry()
	names := []string{"beacon-ref", "beacon-new"}
	for index, name := range names {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0750); err != nil {
			return err
		}
		header := capture.Header{
			Name: name, Width: geometry.width, Rows: geometry.rows, FPS: geometry.fps,
			Frames: geometry.frames, Lead: 0, Picture: geometry.picture,
			Lanes: geometry.lanes, Profiles: transferProfiles(),
		}
		raw, err := json.MarshalIndent(header, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "strip.json"), raw, 0640); err != nil {
			return err
		}
		target := index == 1
		for frame := 0; frame < geometry.frames; frame++ {
			// The new scanner never captured the last sheet: its native dark
			// reference board now sits beyond the reel boundary.
			if target && frame == 5 {
				continue
			}
			if err := writeTransferSheet(dir, geometry, frame, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeTransferSheet(dir string, g transferGeometry, frame int, target bool) error {
	bounds := image.Rect(0, 0, g.width, g.rows)
	img := image.NewGray16(bounds)
	for y := 0; y < g.rows; y++ {
		for x := 0; x < g.width; x++ {
			intensity := uint16((x*271+y*59+frame*1079)%48300 + 8000)
			if x < g.picture.X1 && ((x+frame*5)%25 < 4 || y%500 < 16) {
				intensity = 57000
			}
			for lane, band := range g.lanes {
				if x < band.X0 || x >= band.X1 {
					continue
				}
				intensity = transferLanePixel(g, frame, y, x, lane, target)
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
		return fmt.Errorf("write transfer sheet: %v %v", encodeErr, closeErr)
	}
	return nil
}

// transferLanePixel renders reference blocks, shared intermediate strips and the
// variable-area signal for one lane at one source pixel.
func transferLanePixel(g transferGeometry, frame, y, x, lane int, target bool) uint16 {
	dark, clear := transferOpticalEndpoints(lane)
	switch {
	case frame == 0 && y < 80:
		level := dark
		if y >= 40 {
			level = clear
		}
		return transferRecord(level, lane, 0, target)
	case frame >= 1 && frame <= 4 && y < 80:
		// Each shared strip carries one fixed intermediate optical level.
		return transferRecord(transferMidLevel(frame, lane), lane, frame, target)
	default:
		t := float64(frame*g.rows+y) / float64(g.fps*g.rows)
		mark := int(math.Round(8 + 6*math.Sin(t*2*math.Pi*float64(280+220*lane))))
		level := dark
		if x-g.lanes[lane].X0 < mark {
			level = clear
		}
		return transferRecord(level, lane, 0, target)
	}
}

// transferOpticalEndpoints are the physical levels the board set presents.
func transferOpticalEndpoints(lane int) (dark, clear float64) {
	return 6000 + float64(lane)*2200, 55000 - float64(lane)*4800
}

// transferMidLevel gives every shared strip a distinct, strictly ordered
// intermediate level per lane.
func transferMidLevel(frame, lane int) float64 {
	dark, clear := transferOpticalEndpoints(lane)
	fraction := []float64{0, 0.32, 0.55, 0.78, 0.9}[frame]
	return dark + (clear-dark)*fraction
}

// transferRecord applies one device's response curve. The new scanner has a
// gentler gain overall; its right-lane response inverts the optical ordering
// on the shared intermediate strips (frames 1–4), so that lane's transfer
// check must fail independently while its own reference blocks stay ordered.
func transferRecord(level float64, lane, frame int, target bool) uint16 {
	if !target {
		return scannerResponse(level, 1.0, 0)
	}
	slope, intercept := 0.9, 2600.0
	if lane == 1 && frame >= 1 && frame <= 4 {
		// Reversed response only on the intermediate strips both devices share.
		slope, intercept = -0.35, 60000
	} else if lane == 1 {
		slope, intercept = 0.85, 3400.0
	}
	return scannerResponse(level, slope, intercept)
}
