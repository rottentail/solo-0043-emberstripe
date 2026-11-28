// Package parcel publishes and verifies immutable sound handoff bundles.
package parcel

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

const maxBytes = 2 << 20

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Picture struct {
	Frame  int    `json:"frame"`
	Sample int    `json:"sample_start"`
	Sheet  string `json:"sheet_sha256,omitempty"`
}
type Index struct {
	Version  int             `json:"version"`
	Capture  string          `json:"capture"`
	Source   string          `json:"source_sha256"`
	Window   register.Window `json:"window"`
	Rows     int             `json:"rows_per_frame"`
	FPS      int             `json:"fps"`
	Lead     int             `json:"lead_rows"`
	Gauge    gauge.Gauge     `json:"gauge"`
	Coverage []register.Run  `json:"coverage"`
	Pictures []Picture       `json:"pictures"`
	Audio    string          `json:"audio_sha256"`
}
type Receipt struct {
	ID    string      `json:"id"`
	Bytes int         `json:"bytes"`
	Index Index       `json:"index"`
	Wave  wave.Header `json:"wave"`
}
type Store struct{ Root string }

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

var entryPattern = regexp.MustCompile(`^([a-f0-9]{64})\.zip$`)

// List returns the content-addressed parcel identifiers currently on disk,
// sorted lexicographically. Partial deposits and foreign files are ignored.
func (s Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		match := entryPattern.FindStringSubmatch(entry.Name())
		if match != nil && entry.Type().IsRegular() {
			ids = append(ids, match[1])
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Stat reports the current size and modification time of a parcel file.
func (s Store) Stat(id string) (size int64, mtime time.Time, ok bool) {
	if !digestPattern.MatchString(id) {
		return 0, time.Time{}, false
	}
	info, err := os.Stat(filepath.Join(s.Root, id+".zip"))
	if err != nil || !info.Mode().IsRegular() {
		return 0, time.Time{}, false
	}
	return info.Size(), info.ModTime(), true
}

// OpenIndex unzips only the frozen register table. It never assumes the
// parcel geometry of the live reel that produced the parcel.
func OpenIndex(data []byte) (Index, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Index{}, errors.New("invalid parcel archive")
	}
	var idx Index
	found := false
	for _, f := range r.File {
		if f.Name != "register.json" || f.UncompressedSize64 > maxBytes {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return Index{}, err
		}
		content, readErr := io.ReadAll(io.LimitReader(src, maxBytes+1))
		err = errors.Join(readErr, src.Close())
		if err != nil {
			return Index{}, err
		}
		if len(content) > maxBytes {
			return Index{}, errors.New("oversized parcel entry")
		}
		if err := json.Unmarshal(content, &idx); err != nil {
			return Index{}, err
		}
		found = true
	}
	if !found {
		return Index{}, errors.New("parcel register missing")
	}
	return idx, nil
}

// SampleAt reads one interleaved stereo frame directly from a frozen WAV.
// The 44-byte container header and 4-byte stereo blocks are part of the
// station's fixed interchange contract.
func SampleAt(pcm []byte, frame int) ([2]int16, bool) {
	offset := wave.HeaderBytes + frame*wave.BlockBytes
	if frame < 0 || offset+wave.BlockBytes > len(pcm) {
		return [2]int16{}, false
	}
	le := binary.LittleEndian
	return [2]int16{
		int16(le.Uint16(pcm[offset:])),
		int16(le.Uint16(pcm[offset+2:])),
	}, true
}

// SoundWAV extracts the frozen PCM container from published parcel bytes.
func SoundWAV(data []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("invalid parcel archive")
	}
	for _, f := range r.File {
		if f.Name != "sound.wav" || f.UncompressedSize64 > maxBytes {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(io.LimitReader(src, maxBytes+1))
		err = errors.Join(readErr, src.Close())
		if err != nil {
			return nil, err
		}
		if len(content) > maxBytes {
			return nil, errors.New("oversized parcel entry")
		}
		return content, nil
	}
	return nil, errors.New("parcel sound missing")
}

func (s Store) Publish(index Index, audio []byte) (Receipt, error) {
	index.Version = 1
	index.Audio = digest(audio)
	table, err := json.Marshal(index)
	if err != nil {
		return Receipt{}, err
	}
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"register.json", table}, {"sound.wav", audio}} {
		w, err := archive.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			return Receipt{}, err
		}
		if _, err = w.Write(entry.data); err != nil {
			return Receipt{}, err
		}
	}
	if err := archive.Close(); err != nil {
		return Receipt{}, err
	}
	data := out.Bytes()
	receipt, err := Verify(data)
	if err != nil {
		return Receipt{}, err
	}
	// Build fully, then make the digest reachable with a same-filesystem rename.
	temp, err := os.CreateTemp(s.Root, ".deposit-*")
	if err != nil {
		return Receipt{}, err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	var syncErr error
	if writeErr == nil {
		syncErr = temp.Sync()
	}
	if err = errors.Join(writeErr, syncErr, temp.Close()); err != nil {
		return Receipt{}, err
	}
	if err := os.Rename(temp.Name(), filepath.Join(s.Root, receipt.ID+".zip")); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (s Store) Read(id string) ([]byte, error) {
	if !digestPattern.MatchString(id) {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.Root, id+".zip"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes || digest(data) != id {
		return nil, errors.New("stored parcel digest mismatch")
	}
	return data, nil
}

func Verify(data []byte) (Receipt, error) {
	if len(data) > maxBytes {
		return Receipt{}, errors.New("parcel exceeds size limit")
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(r.File) != 2 {
		return Receipt{}, errors.New("expected a two-entry sound parcel")
	}
	names := make(map[string]bool, 2)
	for _, f := range r.File {
		if (f.Name != "register.json" && f.Name != "sound.wav") || names[f.Name] || f.UncompressedSize64 > maxBytes {
			return Receipt{}, errors.New("invalid parcel entry")
		}
		src, err := f.Open()
		if err != nil {
			return Receipt{}, err
		}
		content, readErr := io.ReadAll(io.LimitReader(src, maxBytes+1))
		err = errors.Join(readErr, src.Close())
		if err != nil {
			return Receipt{}, err
		}
		if len(content) > maxBytes {
			return Receipt{}, errors.New("oversized parcel entry")
		}
		names[f.Name] = true
	}
	var idx Index
	if idx, err = OpenIndex(data); err != nil {
		return Receipt{}, err
	}
	pcm, err := SoundWAV(data)
	if err != nil {
		return Receipt{}, err
	}
	header, err := wave.Inspect(pcm)
	if err != nil {
		return Receipt{}, err
	}
	if err := verifyClock(idx, header, pcm); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: digest(data), Bytes: len(data), Index: idx, Wave: header}, nil
}

func verifyClock(idx Index, h wave.Header, pcm []byte) error {
	w := idx.Window
	if idx.Version != 1 || idx.Capture == "" || !digestPattern.MatchString(idx.Source) || idx.Audio != digest(pcm) || idx.Rows < 8 || idx.Rows > 4096 || idx.FPS < 16 || idx.FPS > 60 || w.Start < 0 || w.End <= w.Start || w.End > 256 || w.End-w.Start > register.MaxFrames || h.Rate != idx.Rows*idx.FPS || h.Frames != (w.End-w.Start)*idx.Rows || len(idx.Pictures) != w.End-w.Start {
		return errors.New("audio and register disagree")
	}
	for i, p := range idx.Pictures {
		if p.Frame != w.Start+i || p.Sample != i*idx.Rows || (p.Sheet != "" && !digestPattern.MatchString(p.Sheet)) {
			return errors.New("invalid picture-to-sample index")
		}
	}
	first, end := w.Start*idx.Rows, w.End*idx.Rows
	next := first
	for _, c := range idx.Coverage {
		if c.Start != next || c.End <= c.Start || c.End > end {
			return errors.New("coverage must partition the picture window")
		}
		switch c.Status {
		case register.Readable:
		case register.Outside, register.Missing:
			for sample := c.Start - first; sample < c.End-first; sample++ {
				if binary.LittleEndian.Uint32(pcm[44+4*sample:]) != 0 {
					return errors.New("unreadable coverage contains nonzero audio")
				}
			}
		default:
			return errors.New("unknown coverage status")
		}
		next = c.End
	}
	if next != end {
		return errors.New("incomplete coverage")
	}
	return nil
}
