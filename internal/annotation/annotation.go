// Package annotation owns the opinion-marker layer exchanged with the film
// archive. Restoration notes anchor to the picture clock while equipment
// notes anchor to source scan rows; both keep their original positions
// through import, projection and write-back.
package annotation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Limit bounds one annotation document in bytes.
const Limit = 256 << 10

const maxMarkers = 512
const maxNote = 560
const maxOrigin = 48

// Anchor kinds recorded by the archive.
const (
	AnchorFrame  = "frame"  // restoration opinion pinned to a picture frame
	AnchorSource = "source" // equipment opinion pinned to a source scan row
)

// Dispositions of a marker under an offset hypothesis.
const (
	Stays = "stays" // hypothesis keeps the marker on its registered picture frame
	Moves = "moves" // hypothesis shifts the marker to another picture frame
)

// Reel-side verifiability of a projected marker.
const (
	VerifyContent  = "content"       // readable: position and samples can be checked
	VerifyPosition = "position_only" // missing sheet: only the gap itself can be checked
	VerifyAnchor   = "anchor_only"   // outside the reel: only the original anchor survives
)

// Parcel-side verifiability against a frozen handoff.
const (
	ParcelContent = "content" // inside a readable run: delivered PCM can be checked
	ParcelSilence = "silence" // inside a silent run: delivered zero samples can be checked
	ParcelNone    = "none"    // outside the frozen window or a different capture
)

var (
	idPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Invalid marks a client-supplied document or hypothesis that cannot be used.
type Invalid struct{ Cause error }

func (e *Invalid) Error() string { return e.Cause.Error() }
func (e *Invalid) Unwrap() error { return e.Cause }

// Corrupt marks a stored document that no longer passes validation.
type Corrupt struct{ Cause error }

func (e *Corrupt) Error() string { return e.Cause.Error() }
func (e *Corrupt) Unwrap() error { return e.Cause }

// Marker is one archived opinion. Frame and Row are coordinates in the
// anchor's own space: the picture clock for restoration notes, the source
// scan rows for equipment notes.
type Marker struct {
	ID     string `json:"id"`
	Anchor string `json:"anchor"`
	Frame  int    `json:"frame"`
	Row    int    `json:"row"`
	Origin string `json:"origin,omitempty"`
	Note   string `json:"note"`
}

// Document is the unit exchanged with the archive. Provenance fields record
// the capture bytes and offset the set was last reconciled against.
type Document struct {
	Version int      `json:"version"`
	Capture string   `json:"capture"`
	Source  string   `json:"source_sha256,omitempty"`
	Lead    *int     `json:"lead_rows,omitempty"`
	Markers []Marker `json:"markers"`
}

func invalid(format string, args ...any) *Invalid {
	return &Invalid{Cause: fmt.Errorf(format, args...)}
}

// Parse validates the structure of an exchanged document. Coordinates are
// checked against the global format envelope here; Reconcile applies the
// geometry of one registered capture.
func Parse(data []byte) (*Document, error) {
	if len(data) > Limit {
		return nil, invalid("意见文档超过 %d 字节上限。", Limit)
	}
	var doc Document
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, &Invalid{Cause: fmt.Errorf("意见文档无法解析：%v", err)}
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, invalid("意见文档后有多余数据。")
	}
	if doc.Version != 1 {
		return nil, invalid("意见文档版本须为 1。")
	}
	if !idPattern.MatchString(doc.Capture) {
		return nil, invalid("意见文档卷名无效。")
	}
	if doc.Source != "" && !digestPattern.MatchString(doc.Source) {
		return nil, invalid("意见文档来源摘要无效。")
	}
	if len(doc.Markers) > maxMarkers {
		return nil, invalid("意见标记数量超过 %d 条上限。", maxMarkers)
	}
	seen := make(map[string]bool, len(doc.Markers))
	for _, m := range doc.Markers {
		if !idPattern.MatchString(m.ID) {
			return nil, invalid("标记标识 %q 无效。", m.ID)
		}
		if seen[m.ID] {
			return nil, invalid("标记 %s 重复。", m.ID)
		}
		seen[m.ID] = true
		if m.Anchor != AnchorFrame && m.Anchor != AnchorSource {
			return nil, invalid("标记 %s 的锚点须为 frame 或 source。", m.ID)
		}
		if m.Frame < 0 || m.Frame >= 4096 || m.Row < 0 || m.Row >= 4096 {
			return nil, invalid("标记 %s 的锚点坐标超出格式范围。", m.ID)
		}
		if n := utf8.RuneCountInString(m.Note); n == 0 || n > maxNote || controlled(m.Note) {
			return nil, invalid("标记 %s 的备注须为 1–%d 个可打印字符。", m.ID, maxNote)
		}
		if utf8.RuneCountInString(m.Origin) > maxOrigin || controlled(m.Origin) {
			return nil, invalid("标记 %s 的来源须不超过 %d 个可打印字符。", m.ID, maxOrigin)
		}
	}
	return &doc, nil
}

func controlled(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// Reconcile binds a document to one registered capture. Rows must fit the
// capture geometry; frames may legitimately point beyond the scanned extent
// and are reported as outside the reel when projected.
func (d *Document) Reconcile(h capture.Header) error {
	if d.Capture != h.Name {
		return invalid("意见文档属于卷 %q，与当前卷不符。", d.Capture)
	}
	return d.ReconcileRows(h.Rows)
}

// ReconcileRows checks every marker row against one frame height.
func (d *Document) ReconcileRows(rows int) error {
	for _, m := range d.Markers {
		if m.Row >= rows {
			return invalid("标记 %s 的第 %d 行超出每格 %d 行。", m.ID, m.Row, rows)
		}
	}
	return nil
}

// Bind fills missing provenance from the capture the document is imported
// against. Declared provenance is kept so stale references stay visible.
func (d *Document) Bind(h capture.Header, digest string) {
	if d.Source == "" {
		d.Source = digest
	}
	if d.Lead == nil {
		lead := h.Lead
		d.Lead = &lead
	}
}

// Place locates a row on one clock as a frame and an in-frame row. Frames
// outside the scanned extent keep their signed distance from the reel.
type Place struct {
	Frame int `json:"frame"`
	Row   int `json:"row"`
}

func place(row, rows int) Place {
	frame, r := row/rows, row%rows
	if r < 0 {
		frame--
		r += rows
	}
	return Place{Frame: frame, Row: r}
}

// Projection is one marker re-resolved under an offset hypothesis. The
// original anchor is echoed unchanged.
type Projection struct {
	Marker
	Registered  Place  `json:"registered"`
	Picture     Place  `json:"picture"`
	PictureRow  int    `json:"picture_row"`
	Source      Place  `json:"source"`
	SourceRow   int    `json:"source_row"`
	Disposition string `json:"disposition"`
	Status      string `json:"status"`
	Verifiable  string `json:"verifiable"`
}

// Report projects every stored marker under one offset hypothesis.
type Report struct {
	Capture         string       `json:"capture"`
	Source          string       `json:"source_sha256"`
	RegisteredLead  int          `json:"registered_lead_rows"`
	HypothesisLead  int          `json:"hypothesis_lead_rows"`
	DocumentLead    *int         `json:"document_lead_rows,omitempty"`
	DocumentSource  string       `json:"document_source_sha256,omitempty"`
	CaptureMismatch bool         `json:"capture_mismatch"`
	Markers         []Projection `json:"markers"`
}

// Project resolves each marker under the hypothesis lead. Frame-anchored
// notes stay on their picture frame; source-anchored notes move with the
// hypothesis. Both original positions are echoed untouched.
func Project(reel *capture.Reel, doc *Document, hypothesis int) (*Report, error) {
	if total := reel.Rows * reel.Frames; hypothesis < -total || hypothesis > total {
		return nil, invalid("偏移假设超出卷内容量。")
	}
	report := &Report{
		Capture:         reel.Name,
		Source:          reel.Digest,
		RegisteredLead:  reel.Lead,
		HypothesisLead:  hypothesis,
		DocumentLead:    doc.Lead,
		DocumentSource:  doc.Source,
		CaptureMismatch: doc.Source != "" && doc.Source != reel.Digest,
	}
	for _, m := range doc.Markers {
		var pictureRow, sourceRow int
		if m.Anchor == AnchorFrame {
			pictureRow = m.Frame*reel.Rows + m.Row
			sourceRow = pictureRow + hypothesis
		} else {
			sourceRow = m.Frame*reel.Rows + m.Row
			pictureRow = sourceRow - hypothesis
		}
		registered := pictureRow
		if m.Anchor == AnchorSource {
			registered = sourceRow - reel.Lead
		}
		p := Projection{
			Marker:      m,
			Registered:  place(registered, reel.Rows),
			Picture:     place(pictureRow, reel.Rows),
			PictureRow:  pictureRow,
			Source:      place(sourceRow, reel.Rows),
			SourceRow:   sourceRow,
			Disposition: Stays,
		}
		if p.Registered.Frame != p.Picture.Frame {
			p.Disposition = Moves
		}
		_, p.Status = register.LocateWith(reel, hypothesis, pictureRow)
		switch p.Status {
		case register.Readable:
			p.Verifiable = VerifyContent
		case register.Missing:
			p.Verifiable = VerifyPosition
		default:
			p.Verifiable = VerifyAnchor
		}
		report.Markers = append(report.Markers, p)
	}
	return report, nil
}

// ParcelMarker is one marker checked against a frozen handoff parcel.
type ParcelMarker struct {
	Marker
	PictureRow *int   `json:"picture_row,omitempty"`
	Picture    *Place `json:"picture,omitempty"`
	Sample     *int   `json:"sample,omitempty"`
	Status     string `json:"status"`
	Verifiable string `json:"verifiable"`
}

// ParcelReport states what a frozen parcel can verify about each marker.
type ParcelReport struct {
	Parcel       string          `json:"parcel"`
	Capture      string          `json:"capture"`
	Source       string          `json:"source_sha256"`
	Window       register.Window `json:"window"`
	Lead         int             `json:"lead_rows"`
	CaptureMatch bool            `json:"capture_match"`
	Markers      []ParcelMarker  `json:"markers"`
}

// CheckParcel resolves markers using only the frozen parcel index. Nothing
// is recomputed against the live capture, so the report stays meaningful
// when the reel itself is no longer registered.
func CheckParcel(doc *Document, id string, idx parcel.Index) *ParcelReport {
	report := &ParcelReport{
		Parcel:       id,
		Capture:      idx.Capture,
		Source:       idx.Source,
		Window:       idx.Window,
		Lead:         idx.Lead,
		CaptureMatch: doc.Capture == idx.Capture && (doc.Source == "" || doc.Source == idx.Source),
	}
	first, last := idx.Window.Start*idx.Rows, idx.Window.End*idx.Rows
	for _, m := range doc.Markers {
		pm := ParcelMarker{Marker: m, Status: "capture_mismatch", Verifiable: ParcelNone}
		if report.CaptureMatch {
			var pictureRow int
			if m.Anchor == AnchorFrame {
				pictureRow = m.Frame*idx.Rows + m.Row
			} else {
				pictureRow = m.Frame*idx.Rows + m.Row - idx.Lead
			}
			pm.PictureRow = &pictureRow
			picture := place(pictureRow, idx.Rows)
			pm.Picture = &picture
			if pictureRow < first || pictureRow >= last {
				pm.Status = "outside_window"
				pm.Verifiable = ParcelNone
			} else {
				sample := pictureRow - first
				pm.Sample = &sample
				pm.Status = runStatus(idx.Coverage, pictureRow)
				if pm.Status == register.Readable {
					pm.Verifiable = ParcelContent
				} else {
					pm.Verifiable = ParcelSilence
				}
			}
		}
		report.Markers = append(report.Markers, pm)
	}
	return report
}

func runStatus(coverage []register.Run, row int) string {
	for _, run := range coverage {
		if row >= run.Start && row < run.End {
			return run.Status
		}
	}
	return register.Outside
}

// Store keeps one write-back document per capture on local disk.
type Store struct{ Root string }

func (s Store) path(name string) string { return filepath.Join(s.Root, name+".json") }

// Write replaces the stored document atomically.
func (s Store) Write(name string, doc *Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.Root, ".annotations-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	var syncErr error
	if writeErr == nil {
		syncErr = temp.Sync()
	}
	if err = errors.Join(writeErr, syncErr, temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), s.path(name))
}

// Read returns the stored document bytes exactly as written.
func (s Store) Read(name string) ([]byte, error) {
	f, err := os.Open(s.path(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, Limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > Limit {
		return nil, errors.New("stored annotation document exceeds limit")
	}
	return data, nil
}
