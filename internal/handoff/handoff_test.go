package handoff

import (
	"strings"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// indexBuilder mirrors the parcel.Station layout without rendering PCM.
type indexBuilder struct {
	capture string
	rows    int
	fps     int
	lead    int
	window  register.Window
	status  map[int]string // picture row -> status for non-readable rows
	sheets  map[int]bool   // picture frames carrying a picture sheet digest
}

func (b indexBuilder) build() parcel.Index {
	idx := parcel.Index{
		Version: 1, Capture: b.capture, Source: strings.Repeat("a", 64), Window: b.window,
		Rows: b.rows, FPS: b.fps, Lead: b.lead,
		Gauge: gauge.Gauge{Profile: "bench"},
	}
	first, last := b.window.Start*b.rows, b.window.End*b.rows
	for row := first; row < last; {
		status := register.Readable
		if s, ok := b.status[row]; ok {
			status = s
		}
		end := row + 1
		// Merge adjacent equal-status rows into one run like register.Coverage.
		for end < last {
			nextStatus := register.Readable
			if s, ok := b.status[end]; ok {
				nextStatus = s
			}
			if nextStatus != status {
				break
			}
			end++
		}
		idx.Coverage = append(idx.Coverage, register.Run{Start: row, End: end, Status: status})
		row = end
	}
	for frame := b.window.Start; frame < b.window.End; frame++ {
		pic := parcel.Picture{Frame: frame, Sample: (frame - b.window.Start) * b.rows}
		if b.sheets[frame] {
			pic.Sheet = strings.Repeat("b", 64)
		}
		idx.Pictures = append(idx.Pictures, pic)
	}
	return idx
}

// cinderLike reproduces the cinder-a geometry: 2000 rows/frame, lead +230,
// window frames 1..3 with frame 2's sound sheet missing (rows 3770..5769).
func cinderLike() parcel.Index {
	b := indexBuilder{
		capture: "cinder-a", rows: 2000, fps: 24, lead: 230,
		window: register.Window{Start: 1, End: 4},
		sheets: map[int]bool{1: true, 3: true},
	}
	idx := b.build()
	// Replace coverage with an explicit three-way partition, the exact shape
	// produced by register.Coverage for this fixture.
	idx.Coverage = []register.Run{
		{Start: 2000, End: 3770, Status: register.Readable},
		{Start: 3770, End: 5770, Status: register.Missing},
		{Start: 5770, End: 8000, Status: register.Readable},
	}
	return idx
}

func TestLocatePictureAddress(t *testing.T) {
	idx := cinderLike()

	// Picture frame 1 row 1770 -> picture row 3770, source page 2 row 0,
	// which is the missing sheet: a clock slot with padding zeros only.
	f := Locate(idx, Query{Mode: AddressPicture, Frame: 1, Row: 1770})
	if f.Verdict != TimeRange {
		t.Fatalf("verdict = %s, want time_range", f.Verdict)
	}
	if f.Source == nil || *f.Source != (register.Address{Frame: 2, Row: 0}) {
		t.Fatalf("source = %+v, want page 2 row 0", f.Source)
	}
	if f.SampleFrame == nil || *f.SampleFrame != 1770 {
		t.Fatalf("sample frame = %v, want 1770", f.SampleFrame)
	}
	if f.CoverageStatus != register.Missing {
		t.Fatalf("coverage = %q, want missing_sheet", f.CoverageStatus)
	}
	if !f.PictureInParcel || !f.HasPictureSheet {
		t.Fatal("picture frame 1 is inside the parcel and carries a sheet")
	}

	// One row earlier lands on readable material even though it reads the
	// same source page boundary: frame 1 row 1769 -> source page 1 row 1999.
	ok := Locate(idx, Query{Mode: AddressPicture, Frame: 1, Row: 1769})
	if ok.Verdict != Audible || ok.Source.Frame != 1 || ok.Source.Row != 1999 {
		t.Fatalf("adjacent row verdict = %s source = %+v", ok.Verdict, ok.Source)
	}
}

func TestLocateSourceAddressUsesParcelLead(t *testing.T) {
	idx := cinderLike()

	// Scanner reports sound page 2 row 0 directly. On this parcel clock
	// (lead +230) that is picture row 3770, the padded gap.
	f := Locate(idx, Query{Mode: AddressSource, Frame: 2, Row: 0})
	if f.Verdict != TimeRange {
		t.Fatalf("verdict = %s, want time_range", f.Verdict)
	}
	if f.PictureRow != 3770 {
		t.Fatalf("picture row = %d, want 3770", f.PictureRow)
	}
	if f.SampleFrame == nil || *f.SampleFrame != 1770 {
		t.Fatalf("sample frame = %v, want 1770", f.SampleFrame)
	}

	// Sound page 1 row 1999 -> source row 3999 -> picture row 3769, readable.
	audible := Locate(idx, Query{Mode: AddressSource, Frame: 1, Row: 1999})
	if audible.Verdict != Audible || audible.PictureRow != 3769 {
		t.Fatalf("verdict = %s picture row = %d", audible.Verdict, audible.PictureRow)
	}
}

func TestTwoOffsetsResolveSameReportDifferently(t *testing.T) {
	// A rescan parcel with the same capture name but a different clock:
	// 1920 rows/frame and a negative lead (-135).
	alt := parcel.Index{
		Version: 1, Capture: "cinder-a", Source: strings.Repeat("c", 64),
		Rows: 1920, FPS: 25, Lead: -135,
		Window:   register.Window{Start: 0, End: 4},
		Gauge:    gauge.Gauge{Profile: "bench"},
		Coverage: []register.Run{{Start: 0, End: 4 * 1920, Status: register.Readable}},
	}
	for frame := 0; frame < 4; frame++ {
		alt.Pictures = append(alt.Pictures, parcel.Picture{Frame: frame, Sample: frame * 1920, Sheet: strings.Repeat("d", 64)})
	}

	// Same scanner report "sound page 2 row 0" lands at two different
	// picture rows and sample frames on the two parcel clocks.
	first := Locate(cinderLike(), Query{Mode: AddressSource, Frame: 2, Row: 0})
	second := Locate(alt, Query{Mode: AddressSource, Frame: 2, Row: 0})

	if first.PictureRow != 3770 || second.PictureRow != 3840+135 {
		t.Fatalf("picture rows = %d / %d, want 3770 / %d", first.PictureRow, second.PictureRow, 3840+135)
	}
	if *first.SampleFrame != 1770 || *second.SampleFrame != 3840+135 {
		t.Fatalf("sample frames = %v / %v", first.SampleFrame, second.SampleFrame)
	}
	if first.Verdict != TimeRange || second.Verdict != Audible {
		t.Fatalf("verdicts = %s / %s, want time_range / audible", first.Verdict, second.Verdict)
	}
}

func TestOutsideWindowIsUnverifiable(t *testing.T) {
	idx := cinderLike()

	// Frame 0 exists on the reel but the parcel window starts at frame 1.
	f := Locate(idx, Query{Mode: AddressPicture, Frame: 0, Row: 10})
	if f.Verdict != Unverifiable || f.InsideWindow {
		t.Fatalf("verdict = %s inside = %v", f.Verdict, f.InsideWindow)
	}
	if f.SampleFrame != nil {
		t.Fatal("unverifiable findings must not claim a sample frame")
	}

	// A source report whose converted picture row leaves the parcel clock.
	outside := Locate(idx, Query{Mode: AddressSource, Frame: 9, Row: 5})
	if outside.Verdict != Unverifiable {
		t.Fatalf("verdict = %s, want unverifiable", outside.Verdict)
	}
}

func TestRowOutsideGridIsUnverifiable(t *testing.T) {
	idx := cinderLike()
	f := Locate(idx, Query{Mode: AddressPicture, Frame: 2, Row: 2000})
	if f.RowInGrid || f.Verdict != Unverifiable {
		t.Fatalf("row in grid = %v verdict = %s", f.RowInGrid, f.Verdict)
	}
}

func TestPictureAbsentButSoundPresent(t *testing.T) {
	idx := cinderLike()
	// Frame 2 has no picture sheet, yet its tail rows land on source page 3:
	// picture frame 2 row 1770 is picture row 5770, source page 3 row 0,
	// which the fixture marks readable even though the picture page is absent.
	f := Locate(idx, Query{Mode: AddressPicture, Frame: 2, Row: 1770})
	if f.Verdict != Audible {
		t.Fatalf("verdict = %s, want audible", f.Verdict)
	}
	if f.HasPictureSheet {
		t.Fatal("frame 2 in cinderLike carries no picture sheet digest")
	}
	if f.PictureInParcel != true {
		t.Fatal("frame 2 is inside the parcel window even without a sheet")
	}
	if f.Source == nil || f.Source.Frame != 3 || f.Source.Row != 0 {
		t.Fatalf("source = %+v, want page 3 row 0", f.Source)
	}
}

func TestNegativeLeadBoundary(t *testing.T) {
	// vireo-b style: lead -135, rows 1920. Picture frame 0 row 0 maps to a
	// negative source row and must decompose without a negative remainder.
	idx := parcel.Index{
		Version: 1, Capture: "vireo-b", Source: strings.Repeat("e", 64),
		Rows: 1920, FPS: 25, Lead: -135,
		Window:   register.Window{Start: 0, End: 2},
		Gauge:    gauge.Gauge{Profile: "bench"},
		Coverage: []register.Run{{Start: 0, End: 135, Status: register.Outside}, {Start: 135, End: 3840, Status: register.Readable}},
	}
	for frame := 0; frame < 2; frame++ {
		idx.Pictures = append(idx.Pictures, parcel.Picture{Frame: frame, Sample: frame * 1920, Sheet: strings.Repeat("f", 64)})
	}

	f := Locate(idx, Query{Mode: AddressPicture, Frame: 0, Row: 0})
	if f.Verdict != TimeRange || f.CoverageStatus != register.Outside {
		t.Fatalf("verdict = %s coverage = %q", f.Verdict, f.CoverageStatus)
	}
	if f.Source == nil || f.Source.Row != 1920-135 || f.Source.Frame != -1 {
		t.Fatalf("negative source decomposition = %+v", f.Source)
	}

	// A scanner-side source frame cannot be negative; that is rejected at
	// the query boundary instead of being silently decomposed.
	if err := (Query{Mode: AddressSource, Frame: -1, Row: 0}).Validate(); err == nil {
		t.Fatal("negative source frame must be rejected at the query boundary")
	}
}

func TestQueryValidation(t *testing.T) {
	cases := []Query{
		{Mode: "bogus", Frame: 0, Row: 0},
		{Mode: AddressPicture, Frame: -1, Row: 0},
		{Mode: AddressSource, Frame: 0, Row: -1},
	}
	for i, q := range cases {
		if err := q.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}
