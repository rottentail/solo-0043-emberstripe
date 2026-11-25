package silence

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const verdictLimit = 1 << 20

var (
	idPattern       = regexp.MustCompile(`^[a-f0-9]{16,64}$`)
	namePattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	signedByPattern = regexp.MustCompile(`^[\p{L}\p{N} ._\-]{1,48}$`)
)

// Store keeps signed verdicts as JSON files under a station-managed root.
// Files are written to a temp name and atomically renamed, so a reopening
// shift never observes a half-written judgment.
type Store struct{ Root string }

// fingerprint is the stable identity of a judgment, independent of who signed
// it or when. Repeating the same review on unchanged data yields the same ID.
func fingerprint(v Verdict) string {
	stable := struct {
		Capture    string     `json:"capture"`
		Window     any        `json:"window"`
		Profile    string     `json:"profile"`
		Thresholds Thresholds `json:"thresholds"`
		Baseline   Baseline   `json:"baseline"`
		Analysis   Analysis   `json:"analysis"`
		Approved   bool       `json:"approved"`
	}{v.Capture, v.Window, v.Profile, v.Thresholds, v.Baseline, v.Analysis, v.Approved}
	raw, _ := json.Marshal(stable)
	return fmt.Sprintf("%x", sha256.Sum256(raw))[:24]
}

func (s Store) dir(capture string) (string, error) {
	if !namePattern.MatchString(capture) {
		return "", errors.New("unknown capture directory")
	}
	dir := filepath.Join(s.Root, capture)
	return dir, nil
}

func (s Store) path(capture, id string) (string, error) {
	dir, err := s.dir(capture)
	if err != nil {
		return "", err
	}
	if !idPattern.MatchString(id) {
		return "", os.ErrNotExist
	}
	return filepath.Join(dir, id+".json"), nil
}

// Save validates and persists a verdict, returning the stored copy with its ID
// and creation timestamp. Saving the same review again is idempotent.
func (s Store) Save(v Verdict) (Verdict, error) {
	if _, err := s.dir(v.Capture); err != nil {
		return Verdict{}, err
	}
	if v.SignedBy == "" || !signedByPattern.MatchString(strings.TrimSpace(v.SignedBy)) {
		return Verdict{}, errors.New("signed_by is required and may use at most 48 letters, digits, spaces, dots, underscores or dashes")
	}
	if len(v.Note) > 500 {
		return Verdict{}, errors.New("note must not exceed 500 characters")
	}
	if err := ValidateThresholds(v.Thresholds); err != nil {
		return Verdict{}, err
	}
	if v.Analysis.Capture != v.Capture || v.Baseline.SourceDigest == "" {
		return Verdict{}, errors.New("verdict must carry its analysis and source baseline")
	}
	v.SignedBy = strings.TrimSpace(v.SignedBy)
	v.ID = fingerprint(v)
	dir, _ := s.dir(v.Capture)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Verdict{}, err
	}
	if existing, err := s.Read(v.Capture, v.ID); err == nil {
		return existing, nil
	}
	v.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Verdict{}, err
	}
	temp, err := os.CreateTemp(dir, ".verdict-*")
	if err != nil {
		return Verdict{}, err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return Verdict{}, err
	}
	if err := temp.Close(); err != nil {
		return Verdict{}, err
	}
	if err := os.Rename(temp.Name(), filepath.Join(dir, v.ID+".json")); err != nil {
		return Verdict{}, err
	}
	return v, nil
}

// Read loads one verdict.
func (s Store) Read(capture, id string) (Verdict, error) {
	path, err := s.path(capture, id)
	if err != nil {
		return Verdict{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Verdict{}, err
	}
	if len(raw) > verdictLimit {
		return Verdict{}, errors.New("stored verdict exceeds size limit")
	}
	var v Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return Verdict{}, err
	}
	if v.ID != id || v.Capture != capture || fingerprint(v) != id {
		return Verdict{}, errors.New("stored verdict digest mismatch")
	}
	return v, nil
}

// List returns a reel's verdicts newest first.
func (s Store) List(capture string) ([]Verdict, error) {
	dir, err := s.dir(capture)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Verdict{}, nil
	}
	if err != nil {
		return nil, err
	}
	verdicts := make([]Verdict, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		v, err := s.Read(capture, id)
		if err != nil {
			continue
		}
		verdicts = append(verdicts, v)
	}
	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i].CreatedAt > verdicts[j].CreatedAt })
	return verdicts, nil
}
