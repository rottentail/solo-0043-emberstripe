// Package capture loads immutable scanner sheets and their geometry.
package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const manifestLimit = 256 << 10
const fileLimit = 12 << 20
const pixelBudget = 16 << 20

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// Span is a half-open strip of columns.
type Span struct {
	X0 int `json:"x0"`
	X1 int `json:"x1"`
}

func (s Span) Width() int            { return s.X1 - s.X0 }
func (s Span) inside(width int) bool { return s.X0 >= 0 && s.X1 > s.X0 && s.X1 <= width }

type Patch struct {
	Frame int  `json:"frame"`
	Rows  Span `json:"rows"`
}
type Profile struct {
	ID    string `json:"id"`
	Dark  Patch  `json:"dark"`
	Clear Patch  `json:"clear"`
}
type Header struct {
	Name     string    `json:"name"`
	Width    int       `json:"width"`
	Rows     int       `json:"rows_per_frame"`
	FPS      int       `json:"fps"`
	Frames   int       `json:"frames"`
	Lead     int       `json:"lead_rows"`
	Picture  Span      `json:"picture"`
	Lanes    [2]Span   `json:"lanes"`
	Profiles []Profile `json:"profiles"`
}

type Sheet struct {
	Bitmap image.Image
	Depth  int    `json:"depth"`
	Digest string `json:"sha256"`
}

func (s Sheet) Gray(x, y int) uint16 {
	return color.Gray16Model.Convert(s.Bitmap.At(x, y)).(color.Gray16).Y
}

type Reel struct {
	Header
	Sheets map[int]Sheet
	Digest string
}

func (r Reel) Available() []int {
	ids := make([]int, 0, len(r.Sheets))
	for id := range r.Sheets {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func LoadCatalog(root string) (map[string]*Reel, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*Reel)
	remaining := pixelBudget
	for _, entry := range entries {
		if !entry.IsDir() || !namePattern.MatchString(entry.Name()) {
			continue
		}
		reel, err := load(filepath.Join(root, entry.Name()), entry.Name(), &remaining)
		if err != nil {
			return nil, fmt.Errorf("capture %s: %w", entry.Name(), err)
		}
		result[entry.Name()] = reel
	}
	if len(result) == 0 {
		return nil, errors.New("empty scanner catalog")
	}
	return result, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	// The catalog is administered locally. Only regular files belong to it.
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid file or size: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file exceeds limit")
	}
	return data, nil
}

func load(dir, key string, remaining *int) (*Reel, error) {
	raw, err := readBounded(filepath.Join(dir, "strip.json"), manifestLimit)
	if err != nil {
		return nil, err
	}
	var h Header
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing manifest data")
	}
	if err := h.validate(key); err != nil {
		return nil, err
	}
	reel := &Reel{Header: h, Sheets: make(map[int]Sheet)}
	hash := sha256.New()
	hash.Write(raw)
	for frame := 0; frame < h.Frames; frame++ {
		path := filepath.Join(dir, fmt.Sprintf("emulsion-%04d.png", frame))
		data, err := readBounded(path, fileLimit)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if h.Width*h.Rows > *remaining {
			return nil, errors.New("catalog pixel budget exceeded")
		}
		sheet, err := decode(data, h.Width, h.Rows)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", frame, err)
		}
		*remaining -= h.Width * h.Rows
		reel.Sheets[frame] = sheet
		fmt.Fprintf(hash, "\n%d %s", frame, sheet.Digest)
	}
	if len(reel.Sheets) == 0 {
		return nil, errors.New("capture has no sheets")
	}
	reel.Digest = fmt.Sprintf("%x", hash.Sum(nil))
	return reel, nil
}

func (h Header) validate(key string) error {
	if h.Name != key || h.Width < 8 || h.Width > 1024 || h.Rows < 8 || h.Rows > 4096 || h.FPS < 16 || h.FPS > 60 || h.Frames < 1 || h.Frames > 256 || h.FPS*h.Rows > 96000 || h.FPS*h.Rows < 8000 {
		return errors.New("invalid capture geometry, cadence, or native PCM rate")
	}
	if h.Lead < -h.Rows*h.Frames || h.Lead > h.Rows*h.Frames {
		return errors.New("lead outside capture envelope")
	}
	if !h.Picture.inside(h.Width) || !h.Lanes[0].inside(h.Width) || !h.Lanes[1].inside(h.Width) || h.Picture.X1 > h.Lanes[0].X0 || h.Lanes[0].X1 > h.Lanes[1].X0 {
		return errors.New("picture, left and right spans must be ordered and disjoint")
	}
	if len(h.Profiles) == 0 || len(h.Profiles) > 12 {
		return errors.New("expected 1–12 gauge profiles")
	}
	seen := make(map[string]bool)
	for _, p := range h.Profiles {
		if !namePattern.MatchString(p.ID) || seen[p.ID] {
			return errors.New("invalid or duplicate gauge profile")
		}
		seen[p.ID] = true
		for _, patch := range []Patch{p.Dark, p.Clear} {
			if patch.Frame < 0 || patch.Frame >= h.Frames || !patch.Rows.inside(h.Rows) {
				return errors.New("gauge patch outside capture")
			}
		}
	}
	return nil
}

func decode(data []byte, width, rows int) (Sheet, error) {
	conf, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Sheet{}, err
	}
	if conf.Width != width || conf.Height != rows || (conf.ColorModel != color.GrayModel && conf.ColorModel != color.Gray16Model) {
		return Sheet{}, errors.New("expected native grayscale PNG at declared dimensions")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Sheet{}, err
	}
	// PNG's config does not distinguish 1/2/4-bit opaque grayscale from 8-bit.
	// DecodeConfig has already validated the fixed PNG header before this read.
	depth := int(data[24])
	switch img.(type) {
	case *image.Gray:
		if depth != 8 {
			return Sheet{}, errors.New("expected 8-bit grayscale PNG")
		}
	case *image.Gray16:
		if depth != 16 {
			return Sheet{}, errors.New("expected 16-bit grayscale PNG")
		}
	default:
		return Sheet{}, errors.New("expected opaque grayscale PNG")
	}
	return Sheet{Bitmap: img, Depth: depth, Digest: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}
