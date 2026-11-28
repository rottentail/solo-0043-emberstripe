package view

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// A card keeps the picture projection and both optical lanes on one picture
// row clock, so neighbouring frames can be compared before choosing a frame.
// Every card has the same cell size; the browser crops the contact sheet by
// these dimensions and draws its own labels and borders. Cards are
// deliberately small: the sheet is an overview, not a replacement for the
// full-frame overlay.
const (
	CardWidth   = 48
	CardPicture = 30
	CardLane    = 14
	CardHeight  = CardPicture + 1 + CardLane + 1 + CardLane
	cardColumns = 8
	// A card column spans many picture rows. Sampling a bounded stride keeps a
	// 48-frame sheet cheap enough to share the station with audition and freeze.
	cardSamples  = 12
	cardLaneHalf = CardLane/2 - 1
)

var (
	cardBackground = color.RGBA{R: 34, G: 38, B: 37, A: 255}
	cardReadable   = color.RGBA{R: 27, G: 32, B: 30, A: 255}
	cardMissing    = color.RGBA{R: 88, G: 53, B: 46, A: 255}
	cardOutside    = color.RGBA{R: 72, G: 71, B: 65, A: 255}
	cardAxis       = color.RGBA{R: 58, G: 64, B: 56, A: 255}
	cardLaneLeft   = color.RGBA{R: 239, G: 166, B: 110, A: 255}
	cardLaneRight  = color.RGBA{R: 167, G: 204, B: 170, A: 255}
)

// CardSheet describes the contact sheet geometry. The browser uses the same
// layout to crop one card per frame, so the two sides cannot drift apart.
type CardSheet struct {
	Columns    int `json:"columns"`
	Rows       int `json:"rows"`
	Frames     int `json:"frames"`
	Width      int `json:"width"`
	Height     int `json:"height"`
	CardWidth  int `json:"card_width"`
	CardHeight int `json:"card_height"`
}

// CardLayout is the single source of truth for the sheet and browser grid.
func CardLayout(frames int) CardSheet {
	if frames < 1 {
		frames = 1
	}
	columns := min(frames, cardColumns)
	rows := (frames + columns - 1) / columns
	return CardSheet{
		Columns: columns, Rows: rows, Frames: frames,
		Width: columns * CardWidth, Height: rows * CardHeight,
		CardWidth: CardWidth, CardHeight: CardHeight,
	}
}

// Contact renders one compact card per picture frame of the window. Picture
// and sound inputs are independent: a frame without a scan sheet keeps its
// missing-picture band while its calibrated lanes still show readable samples.
func Contact(reel *capture.Reel, g gauge.Gauge, w register.Window) ([]byte, CardSheet, error) {
	if w.Start < 0 || w.End <= w.Start || w.End > reel.Frames {
		return nil, CardSheet{}, errors.New("picture window outside capture")
	}
	layout := CardLayout(w.End - w.Start)
	dst := image.NewRGBA(image.Rect(0, 0, layout.Width, layout.Height))
	fill(dst, dst.Rect, cardBackground)
	for index := 0; index < layout.Frames; index++ {
		drawCard(dst, reel, g, w.Start+index, (index%layout.Columns)*CardWidth, (index/layout.Columns)*CardHeight)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, dst); err != nil {
		return nil, CardSheet{}, err
	}
	return data.Bytes(), layout, nil
}

// laneColumn is the readable extent of one card column in one lane.
type laneColumn struct {
	Status    string
	Low, High int
	Readable  int
	Saturated bool
}

type cardColumn struct {
	Lanes [2]laneColumn
}

func drawCard(dst *image.RGBA, reel *capture.Reel, g gauge.Gauge, frame, x0, y0 int) {
	drawPicture(dst, reel, frame, x0, y0)
	columns := sampleColumns(reel, g, frame*reel.Rows)
	laneTop := y0 + CardPicture + 1
	for lane := range 2 {
		drawLane(dst, columns, lane, x0, laneTop+lane*CardLane)
	}
}

// drawPicture projects the frame's picture columns down to the card width on
// the same row clock used by the lanes. A missing sheet keeps its own band.
func drawPicture(dst *image.RGBA, reel *capture.Reel, frame, x0, y0 int) {
	sheet, hasPicture := reel.Sheets[frame]
	if !hasPicture {
		fill(dst, image.Rect(x0, y0, x0+CardWidth, y0+CardPicture), missing)
		return
	}
	picture := reel.Picture
	for x := range CardWidth {
		// The sheet holds only this frame, so columns map to its own rows.
		start, end, stride := sampleBand(0, reel.Rows, x)
		for y := range CardPicture {
			first := picture.X0 + y*picture.Width()/CardPicture
			last := picture.X0 + (y+1)*picture.Width()/CardPicture
			if last <= first {
				last = first + 1
			}
			var total, count uint64
			for row := start; row < end; row += stride {
				for column := first; column < last; column++ {
					total += uint64(sheet.Gray(column, row))
					count++
				}
			}
			shade := uint8(0)
			if count > 0 {
				shade = uint8(total / count / 257)
			}
			dst.SetRGBA(x0+x, y0+y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
}

// drawLane keeps the extrema of readable rows only. Missing and outside rows
// keep their own background so a zero-filled gap never reads as silence.
func drawLane(dst *image.RGBA, columns [CardWidth]cardColumn, lane, x0, top int) {
	middle := top + CardLane/2
	paint := [2]color.RGBA{cardLaneLeft, cardLaneRight}[lane]
	for x := range columns {
		extent := columns[x].Lanes[lane]
		shade := cardReadable
		if extent.Readable == 0 {
			shade = cardOutside
			if extent.Status == register.Missing {
				shade = cardMissing
			}
		}
		fill(dst, image.Rect(x0+x, top, x0+x+1, top+CardLane), shade)
		dst.SetRGBA(x0+x, middle, cardAxis)
		if extent.Readable == 0 {
			continue
		}
		from := clampSpan(middle-level(extent.High), top, top+CardLane)
		to := clampSpan(middle-level(extent.Low)+1, top, top+CardLane)
		if to <= from {
			to = min(top+CardLane, from+1)
		}
		shade = paint
		if extent.Saturated {
			shade = clip
		}
		fill(dst, image.Rect(x0+x, from, x0+x+1, to), shade)
	}
}

// sampleColumns walks the rows behind each card column once for both lanes.
func sampleColumns(reel *capture.Reel, g gauge.Gauge, origin int) [CardWidth]cardColumn {
	var columns [CardWidth]cardColumn
	for x := range CardWidth {
		start, end, stride := sampleBand(origin, reel.Rows, x)
		for row := start; row < end; row += stride {
			value := readout.Trace(reel, g, row)
			for lane := range 2 {
				extent := &columns[x].Lanes[lane]
				if value.Status != register.Readable {
					if extent.Readable == 0 && extent.Status == "" {
						extent.Status = value.Status
					}
					continue
				}
				extent.Status = register.Readable
				sample := int(value.Sample[lane])
				if extent.Readable == 0 {
					extent.Low, extent.High = sample, sample
				} else {
					extent.Low, extent.High = min(extent.Low, sample), max(extent.High, sample)
				}
				extent.Readable++
				extent.Saturated = extent.Saturated || value.Clipped[lane]
			}
		}
	}
	return columns
}

func level(sample int) int {
	return int(math.Round(float64(sample) / 32767 * cardLaneHalf))
}

// sampleBand maps a card column to the picture rows of one frame, with a
// bounded stride. Narrow frames repeat a row instead of leaving an empty
// column.
func sampleBand(origin, rows, x int) (int, int, int) {
	start := origin + x*rows/CardWidth
	end := origin + (x+1)*rows/CardWidth
	if end <= start {
		end = start + 1
	}
	stride := max(1, (end-start)/cardSamples)
	return start, end, stride
}

func clampSpan(value, low, high int) int { return max(low, min(high, value)) }

func fill(dst *image.RGBA, area image.Rectangle, shade color.RGBA) {
	area = area.Intersect(dst.Rect)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			dst.SetRGBA(x, y, shade)
		}
	}
}
