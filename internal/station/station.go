// Package station coordinates scan, calibration, rendering and publication.
package station

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/silence"
	"github.com/rottentail/solo-0043-emberstripe/internal/view"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

type InvalidRenderingInput struct{ Cause error }

func (e *InvalidRenderingInput) Error() string { return e.Cause.Error() }
func (e *InvalidRenderingInput) Unwrap() error { return e.Cause }

type Station struct {
	catalog  map[string]*capture.Reel
	Store    parcel.Store
	Verdicts silence.Store
}
type CaptureInfo struct {
	capture.Header
	Digest    string      `json:"sha256"`
	Available []int       `json:"available"`
	Depths    map[int]int `json:"depths"`
	Rate      int         `json:"sample_rate"`
}

func Open(scans, releases string) (*Station, error) {
	catalog, err := capture.LoadCatalog(scans)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(releases, 0o750); err != nil {
		return nil, err
	}
	verdicts := filepath.Join(releases, "silence-verdicts")
	if err := os.MkdirAll(verdicts, 0o750); err != nil {
		return nil, err
	}
	return &Station{
		catalog:  catalog,
		Store:    parcel.Store{Root: releases},
		Verdicts: silence.Store{Root: verdicts},
	}, nil
}
func (s *Station) Names() []string {
	names := make([]string, 0, len(s.catalog))
	for name := range s.catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func (s *Station) Reel(name string) (*capture.Reel, bool) {
	reel, ok := s.catalog[name]
	return reel, ok
}
func Info(reel *capture.Reel) CaptureInfo {
	depths := make(map[int]int, len(reel.Sheets))
	for frame, sheet := range reel.Sheets {
		depths[frame] = sheet.Depth
	}
	return CaptureInfo{Header: reel.Header, Digest: reel.Digest, Available: reel.Available(), Depths: depths, Rate: register.Rate(reel.Header)}
}
func Mapping(reel *capture.Reel, frame, row int) (map[string]any, error) {
	if frame < 0 || frame >= reel.Frames || row < 0 || row >= reel.Rows {
		return nil, errors.New("invalid picture coordinates")
	}
	picture := frame*reel.Rows + row
	a, status := register.Locate(reel, picture)
	result := map[string]any{"picture_row": picture, "rate": register.Rate(reel.Header), "lead_rows": reel.Lead, "status": status}
	if status != register.Outside {
		result["source"] = a
	}
	return result, nil
}
func Trace(reel *capture.Reel, profile string, frame, row int) (readout.Value, error) {
	if _, err := Mapping(reel, frame, row); err != nil {
		return readout.Value{}, err
	}
	g, err := gauge.Measure(reel, profile)
	if err != nil {
		return readout.Value{}, err
	}
	return readout.Trace(reel, g, frame*reel.Rows+row), nil
}
func Coverage(reel *capture.Reel, w register.Window) ([]register.Run, error) {
	if err := register.Validate(reel.Header, w); err != nil {
		return nil, err
	}
	return register.Coverage(reel, w), nil
}

// RenderWindow resolves a calibration and extracts a window on the native picture clock.
func RenderWindow(reel *capture.Reel, w register.Window, profile string) (gauge.Gauge, readout.Render, error) {
	if err := register.Validate(reel.Header, w); err != nil {
		return gauge.Gauge{}, readout.Render{}, err
	}
	g, err := gauge.Measure(reel, profile)
	if err != nil {
		return gauge.Gauge{}, readout.Render{}, err
	}
	return g, readout.Extract(reel, g, w), nil
}
func Audition(reel *capture.Reel, w register.Window, profile string) ([]byte, error) {
	_, r, err := RenderWindow(reel, w, profile)
	if err != nil {
		return nil, err
	}
	return wave.Encode(register.Rate(reel.Header), r.Samples), nil
}
func Compare(reel *capture.Reel, w register.Window, a, b string) (map[string]any, error) {
	if b == "" {
		return nil, errors.New("second profile is required")
	}
	first, ar, err := RenderWindow(reel, w, a)
	if err != nil {
		return nil, err
	}
	second, br, err := RenderWindow(reel, w, b)
	if err != nil {
		return nil, err
	}
	if first.Profile == second.Profile {
		return nil, errors.New("two distinct profiles are required")
	}
	return map[string]any{"left": first, "right": second, "left_clipped": ar.Clipped, "right_clipped": br.Clipped, "difference": readout.Compare(ar, br, w.Start*reel.Rows), "coverage": ar.Coverage}, nil
}
func Overlay(reel *capture.Reel, profile string, frame int) ([]byte, error) {
	g, err := gauge.Measure(reel, profile)
	if err != nil {
		return nil, err
	}
	return view.Overlay(reel, g, frame)
}
func (s *Station) Release(reel *capture.Reel, w register.Window, profile string) (parcel.Receipt, error) {
	g, r, err := RenderWindow(reel, w, profile)
	if err != nil {
		return parcel.Receipt{}, &InvalidRenderingInput{Cause: err}
	}
	index := parcel.Index{Capture: reel.Name, Source: reel.Digest, Window: w, Rows: reel.Rows, FPS: reel.FPS, Lead: reel.Lead, Gauge: g, Coverage: r.Coverage}
	for frame := w.Start; frame < w.End; frame++ {
		index.Pictures = append(index.Pictures, parcel.Picture{Frame: frame, Sample: (frame - w.Start) * reel.Rows, Sheet: reel.Sheets[frame].Digest})
	}
	receipt, err := s.Store.Publish(index, wave.Encode(register.Rate(reel.Header), r.Samples))
	if err != nil {
		return parcel.Receipt{}, fmt.Errorf("publish sound parcel: %w", err)
	}
	return receipt, nil
}

// SilenceReview runs the quiet-leader analysis over a validated window. The
// same calibrated render that feeds audition and release drives the review, so
// the rows the foreman sees match the frozen PCM clock exactly.
func SilenceReview(reel *capture.Reel, w register.Window, profile string, t silence.Thresholds) (silence.Analysis, gauge.Gauge, error) {
	if err := silence.ValidateThresholds(t); err != nil {
		return silence.Analysis{}, gauge.Gauge{}, err
	}
	g, render, err := RenderWindow(reel, w, profile)
	if err != nil {
		return silence.Analysis{}, gauge.Gauge{}, err
	}
	return silence.Analyze(reel.Name, reel.Rows, register.Rate(reel.Header), g, render, w, t), g, nil
}

// SaveVerdict records a sign-off against the exact calibration and source
// snapshot used for the analysis, so a later shift can reopen and revalidate it.
func (s *Station) SaveVerdict(reel *capture.Reel, w register.Window, profile string, t silence.Thresholds, signedBy, note string, approved bool) (silence.Verdict, error) {
	analysis, g, err := SilenceReview(reel, w, profile, t)
	if err != nil {
		return silence.Verdict{}, &InvalidRenderingInput{Cause: err}
	}
	verdict := silence.Verdict{
		Capture: reel.Name, Window: w, Profile: profile, Thresholds: t,
		Baseline: silence.Baseline{SourceDigest: reel.Digest, Gauge: g},
		Analysis: analysis, SignedBy: signedBy, Note: note, Approved: approved,
	}
	saved, err := s.Verdicts.Save(verdict)
	if err != nil {
		return silence.Verdict{}, fmt.Errorf("save silence verdict: %w", err)
	}
	return saved, nil
}

// ReopenVerdict loads a saved verdict and checks it against the live catalog.
// Re-registration changes the reel digest; a new calibration changes the gauge.
func (s *Station) ReopenVerdict(reel *capture.Reel, id string) (silence.Verdict, silence.Revalidation, error) {
	verdict, err := s.Verdicts.Read(reel.Name, id)
	if err != nil {
		return silence.Verdict{}, silence.Revalidation{}, err
	}
	g, err := gauge.Measure(reel, verdict.Profile)
	if err != nil {
		return silence.Verdict{}, silence.Revalidation{}, err
	}
	return verdict, silence.Revalidate(verdict, reel.Digest, g), nil
}

// ListVerdicts returns the saved sign-offs for a reel.
func (s *Station) ListVerdicts(reel *capture.Reel) ([]silence.Verdict, error) {
	return s.Verdicts.List(reel.Name)
}
