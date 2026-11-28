// Package handoff locates frozen sound parcels by the two addresses operators
// actually carry: picture-grid rows and scanner-reported in-page sound rows.
//
// Every parcel keeps its own frozen clock (rows per frame, lead offset and
// coverage partition), so two parcels with the same capture name must never
// share an interpretation. A location is "audible" only where the parcel's
// own coverage declares readable material; a matching time range that is
// padded with zeros stays "time_range", and an address the parcel cannot
// speak to at all stays "unverifiable".
package handoff

import (
	"errors"
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const (
	// Audible: the parcel's frozen coverage places a real readable source
	// row at this clock position.
	Audible = "audible"
	// TimeRange: the position is inside the parcel window and keeps a valid
	// sample slot, but coverage marks it missing or outside, so the WAV only
	// carries padding zeros there.
	TimeRange = "time_range"
	// Unverifiable: the parcel does not cover this address on its own clock.
	Unverifiable = "unverifiable"
)

const (
	// AddressPicture resolves a picture frame plus an in-frame row.
	AddressPicture = "picture"
	// AddressSource resolves a scanner-reported sound page plus an in-page row.
	AddressSource = "source"
)

// Query is an operator report. Exactly one address mode is set by Mode.
type Query struct {
	Mode  string
	Frame int
	Row   int
}

// Validate enforces the half-open row convention shared with the station.
func (q Query) Validate() error {
	if q.Mode != AddressPicture && q.Mode != AddressSource {
		return errors.New("address mode must be picture or source")
	}
	if q.Frame < 0 {
		return errors.New("frame must be zero-based and non-negative")
	}
	if q.Row < 0 {
		return errors.New("row must be zero-based and non-negative")
	}
	return nil
}

// Finding explains where a reported address lands inside one parcel. Fields
// stay populated even for unverifiable answers so an operator can audit how
// the parcel clock interpreted the report.
type Finding struct {
	ParcelID        string            `json:"parcel_id"`
	Capture         string            `json:"capture"`
	Mode            string            `json:"address_mode"`
	Verdict         string            `json:"verdict"`
	RowsPerFrame    int               `json:"rows_per_frame"`
	LeadRows        int               `json:"lead_rows"`
	Rate            int               `json:"sample_rate"`
	QueryFrame      int               `json:"query_frame"`
	QueryRow        int               `json:"query_row"`
	PictureRow      int               `json:"picture_row"`
	Source          *register.Address `json:"source,omitempty"`
	Window          register.Window   `json:"window"`
	InsideWindow    bool              `json:"inside_window"`
	RowInGrid       bool              `json:"row_in_grid"`
	SampleFrame     *int              `json:"sample_frame,omitempty"`
	CoverageStatus  string            `json:"coverage_status,omitempty"`
	PictureInParcel bool              `json:"picture_in_parcel"`
	HasPictureSheet bool              `json:"has_picture_sheet"`
	NonZeroPCM      *bool             `json:"pcm_nonzero,omitempty"`
	Sample          *[2]int16         `json:"sample,omitempty"`
}

// Locate interprets a query against one frozen parcel index. It performs no
// I/O; callers that need the actual PCM fill Sample via parcel.SampleAt.
func Locate(idx parcel.Index, q Query) Finding {
	f := Finding{
		ParcelID:     "",
		Capture:      idx.Capture,
		Mode:         q.Mode,
		RowsPerFrame: idx.Rows,
		LeadRows:     idx.Lead,
		Rate:         idx.Rows * idx.FPS,
		QueryFrame:   q.Frame,
		QueryRow:     q.Row,
		Window:       idx.Window,
	}
	firstRow := idx.Window.Start * idx.Rows
	lastRow := idx.Window.End * idx.Rows

	var pictureRow int
	switch q.Mode {
	case AddressPicture:
		// A picture report names a frame row: the picture-grid row is direct.
		pictureRow = q.Frame*idx.Rows + q.Row
		f.RowInGrid = q.Row < idx.Rows
	case AddressSource:
		// A scanner report names a sound-slot page and its in-page row. The
		// parcel's own lead offset converts source rows back to picture rows.
		sourceRow := q.Frame*idx.Rows + q.Row
		pictureRow = sourceRow - idx.Lead
		f.RowInGrid = q.Row < idx.Rows
		// Reflect how the parcel clock would decompose that picture row.
		source := decompose(sourceRow, idx.Rows)
		f.Source = &source
	}
	f.PictureRow = pictureRow
	f.InsideWindow = f.RowInGrid && pictureRow >= firstRow && pictureRow < lastRow

	// The reported picture page can be absent from the parcel even when its
	// offset sound row is present, and vice versa. Answer both questions.
	if q.Mode == AddressPicture {
		f.PictureInParcel = q.Frame >= idx.Window.Start && q.Frame < idx.Window.End
		f.HasPictureSheet = hasSheet(idx, q.Frame)
	} else {
		// For source reports the "picture page" is the page the parcel clock
		// derives for that sound slot.
		pictureFrame := floorDiv(pictureRow, idx.Rows)
		f.PictureInParcel = pictureFrame >= idx.Window.Start && pictureFrame < idx.Window.End
		f.HasPictureSheet = hasSheet(idx, pictureFrame)
	}

	if !f.InsideWindow {
		f.Verdict = Unverifiable
		return f
	}

	sampleFrame := pictureRow - firstRow
	f.SampleFrame = &sampleFrame
	if q.Mode == AddressPicture {
		source := decompose(pictureRow+idx.Lead, idx.Rows)
		f.Source = &source
	}
	for _, run := range idx.Coverage {
		if pictureRow >= run.Start && pictureRow < run.End {
			f.CoverageStatus = run.Status
			break
		}
	}
	switch f.CoverageStatus {
	case register.Readable:
		f.Verdict = Audible
	default:
		// missing_sheet and outside_capture keep the clock slot but the WAV
		// contract forces these samples to silence.
		f.Verdict = TimeRange
	}
	return f
}

func hasSheet(idx parcel.Index, frame int) bool {
	for _, p := range idx.Pictures {
		if p.Frame == frame {
			return p.Sheet != ""
		}
	}
	return false
}

func decompose(absolute, rows int) register.Address {
	return register.Address{Frame: floorDiv(absolute, rows), Row: mod(absolute, rows)}
}

// floorDiv and mod follow Euclidean semantics so negative source rows (a
// negative lead can push the first sound slots before page zero) still
// decompose into a non-negative in-page remainder.
func floorDiv(a, b int) int {
	if a >= 0 {
		return a / b
	}
	return -((-a + b - 1) / b)
}

func mod(a, b int) int {
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}

// Describe renders a human-readable explanation used by the demo harness.
func (f Finding) Describe() string {
	switch f.Verdict {
	case Audible:
		return fmt.Sprintf("确有声音：画格行 %d 落在包 %s 的可读覆盖（源 %d 页 %d 行，包内采样帧 %d）",
			f.PictureRow, short(f.ParcelID), f.Source.Frame, f.Source.Row, *f.SampleFrame)
	case TimeRange:
		return fmt.Sprintf("只有时间范围：画格行 %d 在包 %s 窗内但覆盖为 %s，对应采样帧 %d 仅补零",
			f.PictureRow, short(f.ParcelID), f.CoverageStatus, *f.SampleFrame)
	default:
		return fmt.Sprintf("没有足够材料核实：包 %s 的窗为 %d–%d 格，无法覆盖画格行 %d",
			short(f.ParcelID), f.Window.Start, f.Window.End-1, f.PictureRow)
	}
}

func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}
