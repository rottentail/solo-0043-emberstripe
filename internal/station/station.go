// Package station coordinates scan, calibration, rendering and publication.
package station

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/view"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

type InvalidRenderingInput struct{ Cause error }

func (e *InvalidRenderingInput) Error() string { return e.Cause.Error() }
func (e *InvalidRenderingInput) Unwrap() error { return e.Cause }

// StoredIntegrity reports a stored parcel that fails verification.
type StoredIntegrity struct{ Cause error }

func (e *StoredIntegrity) Error() string { return e.Cause.Error() }
func (e *StoredIntegrity) Unwrap() error { return e.Cause }

type Station struct {
	catalog map[string]*capture.Reel
	Store   parcel.Store
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
	if err := os.MkdirAll(releases, 0750); err != nil {
		return nil, err
	}
	return &Station{catalog: catalog, Store: parcel.Store{Root: releases}}, nil
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

// Prepare renders a window and assembles the handoff index and audio without storing them.
func Prepare(reel *capture.Reel, w register.Window, profile string) (parcel.Index, []byte, error) {
	g, r, err := RenderWindow(reel, w, profile)
	if err != nil {
		return parcel.Index{}, nil, err
	}
	index := parcel.Index{Capture: reel.Name, Source: reel.Digest, Window: w, Rows: reel.Rows, FPS: reel.FPS, Lead: reel.Lead, Gauge: g, Coverage: r.Coverage}
	for frame := w.Start; frame < w.End; frame++ {
		index.Pictures = append(index.Pictures, parcel.Picture{Frame: frame, Sample: (frame - w.Start) * reel.Rows, Sheet: reel.Sheets[frame].Digest})
	}
	return index, wave.Encode(register.Rate(reel.Header), r.Samples), nil
}
func (s *Station) Release(reel *capture.Reel, w register.Window, profile string) (parcel.Receipt, error) {
	index, audio, err := Prepare(reel, w, profile)
	if err != nil {
		return parcel.Receipt{}, &InvalidRenderingInput{Cause: err}
	}
	receipt, err := s.Store.Publish(index, audio)
	if err != nil {
		return parcel.Receipt{}, fmt.Errorf("publish sound parcel: %w", err)
	}
	return receipt, nil
}

// Confirm recomputes the parcel the same release would publish and reports
// whether that exact parcel is already stored and intact. It never writes,
// so an interrupted or concurrent release can be confirmed before retrying.
func (s *Station) Confirm(reel *capture.Reel, w register.Window, profile string) (parcel.Receipt, bool, error) {
	index, audio, err := Prepare(reel, w, profile)
	if err != nil {
		return parcel.Receipt{}, false, &InvalidRenderingInput{Cause: err}
	}
	_, receipt, err := parcel.Build(index, audio)
	if err != nil {
		return parcel.Receipt{}, false, fmt.Errorf("build sound parcel: %w", err)
	}
	if _, err := s.Store.Inspect(receipt.ID); err != nil {
		if os.IsNotExist(err) {
			return receipt, false, nil
		}
		return parcel.Receipt{}, false, &StoredIntegrity{Cause: err}
	}
	return receipt, true, nil
}
