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

// Build assembles the frozen parcel bytes and verifies them before any write.
// The bytes depend only on the index and audio, so identical approved material
// always yields the same receipt ID.
func Build(index Index, audio []byte) ([]byte, Receipt, error) {
	index.Version = 1
	index.Audio = digest(audio)
	table, err := json.Marshal(index)
	if err != nil {
		return nil, Receipt{}, err
	}
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"register.json", table}, {"sound.wav", audio}} {
		w, err := archive.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			return nil, Receipt{}, err
		}
		if _, err = w.Write(entry.data); err != nil {
			return nil, Receipt{}, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, Receipt{}, err
	}
	data := out.Bytes()
	receipt, err := Verify(data)
	if err != nil {
		return nil, Receipt{}, err
	}
	return data, receipt, nil
}

func (s Store) Publish(index Index, audio []byte) (Receipt, error) {
	data, receipt, err := Build(index, audio)
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

// Inspect reads a stored parcel and verifies its integrity.
func (s Store) Inspect(id string) (Receipt, error) {
	data, err := s.Read(id)
	if err != nil {
		return Receipt{}, err
	}
	return Verify(data)
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
	files := make(map[string][]byte, 2)
	for _, f := range r.File {
		if (f.Name != "register.json" && f.Name != "sound.wav") || files[f.Name] != nil || f.UncompressedSize64 > maxBytes {
			return Receipt{}, errors.New("invalid parcel entry")
		}
		src, err := f.Open()
		if err != nil {
			return Receipt{}, err
		}
		content, readErr := io.ReadAll(io.LimitReader(src, maxBytes+1))
		if err = errors.Join(readErr, src.Close()); err != nil {
			return Receipt{}, err
		}
		if len(content) > maxBytes {
			return Receipt{}, errors.New("oversized parcel entry")
		}
		files[f.Name] = content
	}
	var idx Index
	if err := json.Unmarshal(files["register.json"], &idx); err != nil {
		return Receipt{}, err
	}
	pcm := files["sound.wav"]
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
