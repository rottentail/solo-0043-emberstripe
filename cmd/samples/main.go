// Command samples creates reusable synthetic scanner inputs plus the
// cross-scan light-reading records that compare an original scan with a
// rescan of the same physical strip.
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

// reelSpec parametrizes one generated scan. The rescan keeps the original
// geometry but moves its missing page and shifts one middle-strip lane.
type reelSpec struct {
	name            string
	lead, rows, fps int
	gap             int
	shiftFrame      int
	shiftRowStart   int
	shiftRowEnd     int
	shiftLane       int
	shiftAmount     int
}

type placement struct {
	Frame        int          `json:"frame"`
	Rows         capture.Span `json:"rows"`
	Edge         capture.Span `json:"edge_columns"`
	DamagedZones []string     `json:"damaged_zones,omitempty"`
}

type material struct {
	ID         string               `json:"material"`
	Placements map[string]placement `json:"placements"`
}

type relightManifest struct {
	ID        string     `json:"id"`
	Original  string     `json:"original_scan"`
	Rescan    string     `json:"rescan"`
	Tolerance int        `json:"tolerance_gray16"`
	Materials []material `json:"materials"`
}

func main() {
	root := flag.String("out", "var/scans", "parent for new sample captures")
	recordsDir := flag.String("records", "var/relight", "directory for cross-scan light-reading records")
	flag.Parse()
	if err := os.MkdirAll(*root, 0750); err != nil {
		log.Fatal(err)
	}
	specs := []reelSpec{
		{name: "cinder-a", lead: 230, rows: 2000, fps: 24, gap: 2, shiftFrame: -1},
		{name: "vireo-b", lead: -135, rows: 1920, fps: 25, gap: 3, shiftFrame: -1},
		{name: "cinder-a-rescan", lead: 230, rows: 2000, fps: 24, gap: 5, shiftFrame: 6, shiftRowStart: 200, shiftRowEnd: 400, shiftLane: 1, shiftAmount: 4000},
	}
	for _, spec := range specs {
		dir := filepath.Join(*root, spec.name)
		if err := os.Mkdir(dir, 0750); err != nil {
			log.Fatal(err)
		}
		if err := generate(dir, spec); err != nil {
			log.Fatal(err)
		}
	}
	if err := os.MkdirAll(*recordsDir, 0750); err != nil {
		log.Fatal(err)
	}
	for _, manifest := range sampleRecords() {
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*recordsDir, manifest.ID+".json"), raw, 0640); err != nil {
			log.Fatal(err)
		}
	}
}

// sampleRecords exercises every verdict branch against cinder-a and its rescan:
// one lane of the middle strip diverges, a missing page blocks judgment, a
// damaged corner leaves only one zone unreadable, and a fully comparable
// placement confirms the reuse path.
func sampleRecords() []relightManifest {
	const original, rescan = "cinder-a", "cinder-a-rescan"
	edge := capture.Span{X0: 108, X1: 112}
	build := func(id string, stripFrame, stripStart, stripEnd int) relightManifest {
		return relightManifest{
			ID: id, Original: original, Rescan: rescan, Tolerance: 256,
			Materials: []material{
				{ID: "clear-base", Placements: map[string]placement{
					original: {Frame: 4, Rows: capture.Span{X0: 1200, X1: 1260}, Edge: edge},
					rescan:   {Frame: 4, Rows: capture.Span{X0: 1200, X1: 1260}, Edge: edge},
				}},
				{ID: "middle-strip", Placements: map[string]placement{
					original: {Frame: stripFrame, Rows: capture.Span{X0: stripStart, X1: stripEnd}, Edge: edge},
					rescan:   {Frame: stripFrame, Rows: capture.Span{X0: stripStart, X1: stripEnd}, Edge: edge},
				}},
				{ID: "reference", Placements: map[string]placement{
					original: {Frame: 0, Rows: capture.Span{X0: 36, X1: 72}, Edge: edge},
					rescan:   {Frame: 0, Rows: capture.Span{X0: 36, X1: 72}, Edge: edge},
				}},
			},
		}
	}
	corner := build("relight-corner", 3, 300, 360)
	for index, entry := range corner.Materials {
		if entry.ID != "reference" {
			continue
		}
		// The original reference lost its left-lane corner, but the right lane
		// and every other zone remain fully readable.
		entry.Placements[original] = placement{
			Frame: 0, Rows: capture.Span{X0: 36, X1: 72}, Edge: edge,
			DamagedZones: []string{"left-lane"},
		}
		corner.Materials[index] = entry
	}
	return []relightManifest{
		build("relight-diverged", 6, 200, 400),
		build("relight-gap", 2, 300, 360),
		corner,
		build("relight-aligned", 3, 300, 360),
	}
}

func generate(dir string, spec reelSpec) error {
	h := capture.Header{Name: spec.name, Width: 112, Rows: spec.rows, FPS: spec.fps, Frames: 7, Lead: spec.lead, Picture: capture.Span{X0: 0, X1: 64}, Lanes: [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}}, Profiles: []capture.Profile{
		{ID: "bench", Dark: patch(0, 0, 36), Clear: patch(0, 36, 72)},
		{ID: "soft", Dark: patch(1, 0, 36), Clear: patch(1, 36, 72)},
		{ID: "flat", Dark: patch(0, 0, 18), Clear: patch(0, 18, 36)},
		{ID: "absent", Dark: patch(spec.gap, 0, 36), Clear: patch(0, 36, 72)},
	}}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "strip.json"), raw, 0640); err != nil {
		return err
	}
	for frame := 0; frame < h.Frames; frame++ {
		if frame == spec.gap {
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
						// The rescan exposes the failure mode the scanning room
						// reported: one middle-strip lane cannot keep the line.
						if frame == spec.shiftFrame && lane == spec.shiftLane &&
							y >= spec.shiftRowStart && y < spec.shiftRowEnd {
							intensity = uint16(min(65535, int(intensity)+spec.shiftAmount))
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
