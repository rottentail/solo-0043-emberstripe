// Package relight compares light readings of the same physical reference
// material between an original scan and a rescan. It keeps every raw range on
// the common 16-bit grayscale scale so an operator can recompute the verdict,
// and it never turns a partial match into a whole-reel release.
package relight

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

const (
	// Aligned means both scans carry the reference and every observed shift
	// stays inside the declared exposure tolerance.
	Aligned = "aligned"
	// Diverged means both scans carry the reference but at least one range
	// endpoint or the mean shifted beyond tolerance.
	Diverged = "diverged"
	// Insufficient means one side is missing material, so no comparison can
	// settle whether the exposure line still applies.
	Insufficient = "insufficient"
)

const (
	MaterialBase    = "clear-base"
	MaterialStrip   = "middle-strip"
	MaterialReferer = "reference"
)

const (
	ZonePicture = "picture"
	ZoneLeft    = "left-lane"
	ZoneRight   = "right-lane"
	ZoneEdge    = "film-edge"
)

// zoneOrder fixes the matrix columns: picture, both sound lanes, then edge.
var zoneOrder = []string{ZonePicture, ZoneLeft, ZoneRight, ZoneEdge}

// materialOrder fixes the matrix rows: shared clear base, independent middle
// test strip, then the gauge reference.
var materialOrder = []string{MaterialBase, MaterialStrip, MaterialReferer}

// Placement is the operator's registration of one material instance inside one
// scan. Edge columns are not part of the reel header and are declared here.
// DamagedZones lists viewing areas whose corner is missing on this material;
// those cells are recorded as insufficient while the other zones stay readable.
type Placement struct {
	Frame        int          `json:"frame"`
	Rows         capture.Span `json:"rows"`
	Edge         capture.Span `json:"edge_columns"`
	DamagedZones []string     `json:"damaged_zones,omitempty"`
}

type materialInput struct {
	ID         string               `json:"material"`
	Placements map[string]Placement `json:"placements"`
}

// Manifest is the cross-scan record the duty operator files.
type Manifest struct {
	ID        string          `json:"id"`
	Original  string          `json:"original_scan"`
	Rescan    string          `json:"rescan"`
	Tolerance int             `json:"tolerance_gray16"`
	Materials []materialInput `json:"materials"`
}

// Reading retains the raw intensity range in both the native bit depth and the
// common 16-bit scale, together with the exact rectangle and pixel count so a
// reviewer can recompute the mean independently.
type Reading struct {
	Available  bool         `json:"available"`
	Reason     string       `json:"reason,omitempty"`
	Frame      int          `json:"frame,omitempty"`
	Rows       capture.Span `json:"rows,omitempty"`
	Columns    capture.Span `json:"columns,omitempty"`
	Depth      int          `json:"depth,omitempty"`
	Samples    int          `json:"samples,omitempty"`
	NativeMin  int          `json:"native_min,omitempty"`
	NativeMax  int          `json:"native_max,omitempty"`
	Scale16Min int          `json:"scale16_min,omitempty"`
	Scale16Max int          `json:"scale16_max,omitempty"`
	Mean16     float64      `json:"mean_gray16,omitempty"`
	Sheet      string       `json:"sheet_sha256,omitempty"`
}

// Cell pairs the two scans of one material in one zone. Signed deltas are
// rescan minus original on the 16-bit scale.
type Cell struct {
	Zone      string   `json:"zone"`
	Verdict   string   `json:"verdict"`
	Reasons   []string `json:"reasons,omitempty"`
	Original  Reading  `json:"original"`
	Rescan    Reading  `json:"rescan"`
	MinDelta  int      `json:"min_delta_gray16,omitempty"`
	MaxDelta  int      `json:"max_delta_gray16,omitempty"`
	MeanDelta int      `json:"mean_delta_gray16,omitempty"`
}

type MaterialResult struct {
	Material string `json:"material"`
	Verdict  string `json:"verdict"`
	Cells    []Cell `json:"cells"`
}

type ZoneVerdict struct {
	Zone    string `json:"zone"`
	Verdict string `json:"verdict"`
}

type ScanSummary struct {
	Scan   string      `json:"scan"`
	Digest string      `json:"source_sha256"`
	Depth  map[int]int `json:"depths"`
}

// Record is the computed, fully reproducible cross-scan light-reading record.
type Record struct {
	Manifest
	ScanSummaries [2]ScanSummary   `json:"scans"`
	GeometryMatch bool             `json:"geometry_match"`
	Verdict       string           `json:"verdict"`
	Release       bool             `json:"whole_reel_release_allowed"`
	ZoneVerdicts  []ZoneVerdict    `json:"zone_verdicts"`
	Materials     []MaterialResult `json:"materials"`
}

func worse(a, b string) string {
	rank := map[string]int{Aligned: 0, Diverged: 1, Insufficient: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// compatibleGeometry decides whether two reels describe the same physical
// strip: width, frame cadence, offsets and every measured column must match.
func compatibleGeometry(a, b capture.Header) bool {
	return a.Width == b.Width && a.Rows == b.Rows && a.FPS == b.FPS && a.Frames == b.Frames &&
		a.Lead == b.Lead && a.Picture == b.Picture && a.Lanes == b.Lanes
}

func zoneColumns(header capture.Header, zone string, edge capture.Span) (capture.Span, error) {
	switch zone {
	case ZonePicture:
		return header.Picture, nil
	case ZoneLeft:
		return header.Lanes[0], nil
	case ZoneRight:
		return header.Lanes[1], nil
	case ZoneEdge:
		return edge, nil
	default:
		return capture.Span{}, fmt.Errorf("unknown zone %q", zone)
	}
}

// measure integrates one declared placement rectangle. Missing pages are a
// recorded "insufficient" condition rather than an error so the other cells of
// the matrix stay decidable.
func measure(reel *capture.Reel, p Placement, zone string) Reading {
	if slices.Contains(p.DamagedZones, zone) {
		return Reading{Available: false, Reason: "damaged_corner", Frame: p.Frame, Rows: p.Rows}
	}
	columns, err := zoneColumns(reel.Header, zone, p.Edge)
	if err != nil || !columns.Inside(reel.Width) || !p.Rows.Inside(reel.Rows) {
		return Reading{Available: false, Reason: "placement_outside_capture"}
	}
	sheet, ok := reel.Sheets[p.Frame]
	if !ok {
		return Reading{Available: false, Reason: "missing_sheet", Frame: p.Frame, Rows: p.Rows, Columns: columns}
	}
	// Gray16Model expands 8-bit samples by 257; 16-bit sheets stay native.
	nativeMax, scale := 255, 257
	if sheet.Depth == 16 {
		nativeMax, scale = 65535, 1
	}
	r := Reading{Available: true, Frame: p.Frame, Rows: p.Rows, Columns: columns, Depth: sheet.Depth, Sheet: sheet.Digest}
	r.NativeMin, r.NativeMax = nativeMax, 0
	r.Scale16Min, r.Scale16Max = 65535, 0
	var total uint64
	for y := p.Rows.X0; y < p.Rows.X1; y++ {
		for x := columns.X0; x < columns.X1; x++ {
			value16 := int(sheet.Gray(x, y))
			native := value16 / scale
			r.NativeMin = min(r.NativeMin, native)
			r.NativeMax = max(r.NativeMax, native)
			r.Scale16Min = min(r.Scale16Min, value16)
			r.Scale16Max = max(r.Scale16Max, value16)
			total += uint64(value16)
			r.Samples++
		}
	}
	r.Mean16 = float64(total) / float64(r.Samples)
	return r
}

func compareCell(zone string, a, b Reading, tolerance int) Cell {
	cell := Cell{Zone: zone, Original: a, Rescan: b}
	if !a.Available || !b.Available {
		cell.Verdict = Insufficient
		if !a.Available {
			cell.Reasons = append(cell.Reasons, "original:"+missingReason(a))
		}
		if !b.Available {
			cell.Reasons = append(cell.Reasons, "rescan:"+missingReason(b))
		}
		return cell
	}
	cell.MinDelta = b.Scale16Min - a.Scale16Min
	cell.MaxDelta = b.Scale16Max - a.Scale16Max
	cell.MeanDelta = int(math.Round(b.Mean16 - a.Mean16))
	beyond := func(delta int) bool { return delta < -tolerance || delta > tolerance }
	if beyond(cell.MinDelta) || beyond(cell.MaxDelta) || beyond(cell.MeanDelta) {
		cell.Verdict = Diverged
		cell.Reasons = append(cell.Reasons, "gray_shift_exceeds_tolerance")
		return cell
	}
	cell.Verdict = Aligned
	return cell
}

func missingReason(r Reading) string {
	if r.Reason == "" {
		return "unavailable"
	}
	return r.Reason
}

// evaluate turns a validated manifest into the full verdict matrix.
func evaluate(m Manifest, catalog map[string]*capture.Reel) Record {
	original, rescan := catalog[m.Original], catalog[m.Rescan]
	record := Record{
		Manifest:      m,
		GeometryMatch: compatibleGeometry(original.Header, rescan.Header),
	}
	record.ScanSummaries = [2]ScanSummary{
		{Scan: original.Name, Digest: original.Digest, Depth: depths(original)},
		{Scan: rescan.Name, Digest: rescan.Digest, Depth: depths(rescan)},
	}
	inputs := make(map[string]materialInput, len(m.Materials))
	for _, material := range m.Materials {
		inputs[material.ID] = material
	}
	record.Verdict = Aligned
	zoneWorst := map[string]string{}
	for _, zone := range zoneOrder {
		zoneWorst[zone] = Aligned
	}
	for _, materialID := range materialOrder {
		material := inputs[materialID]
		result := MaterialResult{Material: materialID, Verdict: Aligned}
		first := material.Placements[m.Original]
		second := material.Placements[m.Rescan]
		for _, zone := range zoneOrder {
			cell := compareCell(zone, measure(original, first, zone), measure(rescan, second, zone), m.Tolerance)
			result.Cells = append(result.Cells, cell)
			result.Verdict = worse(result.Verdict, cell.Verdict)
			zoneWorst[zone] = worse(zoneWorst[zone], cell.Verdict)
		}
		record.Materials = append(record.Materials, result)
		record.Verdict = worse(record.Verdict, result.Verdict)
	}
	for _, zone := range zoneOrder {
		record.ZoneVerdicts = append(record.ZoneVerdicts, ZoneVerdict{Zone: zone, Verdict: zoneWorst[zone]})
	}
	// Only an all-cells match permits reusing the previous acceptance line.
	record.Release = record.Verdict == Aligned
	return record
}

func depths(reel *capture.Reel) map[int]int {
	out := make(map[int]int, len(reel.Sheets))
	for frame, sheet := range reel.Sheets {
		out[frame] = sheet.Depth
	}
	return out
}

func validate(m Manifest, filename string, catalog map[string]*capture.Reel) error {
	if m.ID == "" || m.ID != filename {
		return errors.New("record id must match its file name")
	}
	if !capture.ValidName(m.Original) || !capture.ValidName(m.Rescan) || m.Original == m.Rescan {
		return errors.New("record must name two distinct registered scans")
	}
	original, ok := catalog[m.Original]
	if !ok {
		return fmt.Errorf("original scan %q is not registered", m.Original)
	}
	rescan, ok := catalog[m.Rescan]
	if !ok {
		return fmt.Errorf("rescan %q is not registered", m.Rescan)
	}
	if m.Tolerance < 1 || m.Tolerance > 65535 {
		return errors.New("tolerance must be a positive 16-bit gray difference")
	}
	if !compatibleGeometry(original.Header, rescan.Header) {
		return errors.New("the two scans must share strip geometry to be compared")
	}
	if len(m.Materials) != len(materialOrder) {
		return fmt.Errorf("record must register exactly %d materials", len(materialOrder))
	}
	seen := make(map[string]bool, len(m.Materials))
	for _, material := range m.Materials {
		if !slices.Contains(materialOrder, material.ID) || seen[material.ID] {
			return fmt.Errorf("invalid or duplicate material %q", material.ID)
		}
		seen[material.ID] = true
		if len(material.Placements) != 2 {
			return fmt.Errorf("material %q needs one placement per scan", material.ID)
		}
		for scan, reel := range map[string]*capture.Reel{m.Original: original, m.Rescan: rescan} {
			placement, ok := material.Placements[scan]
			if !ok {
				return fmt.Errorf("material %q is not registered for scan %q", material.ID, scan)
			}
			if placement.Frame < 0 || placement.Frame >= reel.Frames ||
				!placement.Rows.Inside(reel.Rows) || !placement.Edge.Inside(reel.Width) {
				return fmt.Errorf("material %q placement in %q is outside the capture", material.ID, scan)
			}
			for _, zone := range placement.DamagedZones {
				if !slices.Contains(zoneOrder, zone) {
					return fmt.Errorf("material %q in %q marks an unknown damaged zone %q", material.ID, scan, zone)
				}
			}
		}
	}
	return nil
}

// Load reads every *.json record in root, rejects manifests that cannot bind to
// the registered catalog, and returns the evaluated records sorted by id.
func Load(root string, catalog map[string]*capture.Reel) ([]Record, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Record{}, nil
		}
		return nil, err
	}
	records := make([]Record, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var m Manifest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("relight record %s: %w", entry.Name(), err)
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("relight record %s: trailing data", entry.Name())
		}
		name := entry.Name()[:len(entry.Name())-len(".json")]
		if err := validate(m, name, catalog); err != nil {
			return nil, fmt.Errorf("relight record %s: %w", entry.Name(), err)
		}
		records = append(records, evaluate(m, catalog))
	}
	slices.SortFunc(records, func(a, b Record) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return records, nil
}
