package periodicity

import (
	"image"
	"image/color"
)

// graySheet is an in-memory 16-bit grayscale bitmap for package tests.
type graySheet struct {
	*image.Gray16
}

func newGraySheet(width, height int) graySheet {
	return graySheet{Gray16: image.NewGray16(image.Rect(0, 0, width, height))}
}

func (g graySheet) set(x, y int, value uint16) {
	g.SetGray16(x, y, color.Gray16{Y: value})
}
