// Package view generates aligned diagnostic images from picture and sound rows.
package view

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const laneWidth = 48

var missing = color.RGBA{R: 119, G: 46, B: 56, A: 255}
var grid = color.RGBA{R: 43, G: 48, B: 52, A: 255}
var trace = color.RGBA{R: 241, G: 175, B: 79, A: 255}
var clip = color.RGBA{R: 255, G: 71, B: 87, A: 255}

// Overlay pairs picture pixels from one frame with calibrated optical readouts
// on the picture clock. Missing picture is independent of missing sound.
func Overlay(reel *capture.Reel, g gauge.Gauge, frame int) ([]byte, error) {
	if frame < 0 || frame >= reel.Frames {
		return nil, errors.New("picture frame outside capture")
	}
	width := reel.Picture.Width()
	dst := image.NewRGBA(image.Rect(0, 0, width+2*laneWidth, reel.Rows))
	sheet, hasPicture := reel.Sheets[frame]
	for row := 0; row < reel.Rows; row++ {
		for x := 0; x < width; x++ {
			if hasPicture {
				v := uint8(sheet.Gray(reel.Picture.X0+x, row) >> 8)
				dst.SetRGBA(x, row, color.RGBA{v, v, v, 255})
			} else {
				dst.SetRGBA(x, row, missing)
			}
		}
		v := readout.Trace(reel, g, frame*reel.Rows+row)
		for lane, sample := range v.Sample {
			base := width + lane*laneWidth
			for x := 0; x < laneWidth; x++ {
				shade := grid
				if v.Status != register.Readable {
					shade = missing
				}
				dst.SetRGBA(base+x, row, shade)
			}
			if v.Status == register.Readable {
				x := (int(sample) + 32767) * (laneWidth - 1) / 65534
				paint := trace
				if v.Clipped[lane] {
					paint = clip
				}
				dst.SetRGBA(base+x, row, paint)
			}
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, dst)
	return b.Bytes(), err
}
