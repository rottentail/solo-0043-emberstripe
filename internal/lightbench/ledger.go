package lightbench

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// What a recorded step is still good for. Reapplicable means every page that
// feeds sound to the window is byte-identical again and the meter re-measures
// the same; recordonly means the frozen copy can still be read but the step
// must not be applied to the current reel.
const (
	reapplicable = "reapplicable"
	recordonly   = "record_only"
)

type classification struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// recordOrigin resolves a step against the current catalog without trusting the
// stored gauge. Every classification re-measures the reference patches, so a
// stale archive cannot masquerade as live material.
func (b *bench) recordOrigin(reel *capture.Reel, step Step) classification {
	key := reel.Digest
	if cached, ok := b.history.cached(reel.Name, key, step.ID); ok {
		return cached
	}
	result := b.classify(reel, step)
	b.history.remember(reel.Name, key, step.ID, result)
	return result
}

// classify compares the recorded sound footprint with the pages registered now.
// Only pages that actually feed the window count: a supplementary scan that
// adds pages elsewhere in the reel leaves an old window re-applicable, while a
// newly supplied page that projects into the window changes it to record-only.
func (b *bench) classify(reel *capture.Reel, step Step) classification {
	if step.Profile == "" {
		return classification{recordonly, "旧记录没有标定，无法重新应用，只能按旧记录查。"}
	}
	if step.Clock.Rows != reel.Rows || step.Clock.Rate != register.Rate(reel.Header) || step.Clock.Lead != reel.Lead {
		return classification{recordonly, "当前卷声画时钟与当时不同，只能按旧记录查。"}
	}
	if err := register.Validate(reel.Header, step.Window); err != nil {
		return classification{recordonly, "旧窗已不在当前卷范围内，只能按旧记录查。"}
	}
	for _, source := range step.Sources {
		sheet, ok := reel.Sheets[source.Frame]
		switch {
		case !ok && source.Sheet != "":
			return classification{recordonly, "当时供声的扫描页现已缺失，只能按旧记录查。"}
		case ok && source.Sheet == "":
			return classification{recordonly, "旧窗缺口现已补拍，重新应用不再是当时的声轨，只能按旧记录查。"}
		case ok && sheet.Digest != source.Sheet:
			return classification{recordonly, "当时供声的扫描页字节已变，只能按旧记录查。"}
		}
	}
	measured, err := gauge.Measure(reel, step.Profile)
	switch {
	case err != nil:
		return classification{recordonly, "该标定在当前卷已不可用，只能按旧记录查。"}
	case measured != step.Gauge:
		return classification{recordonly, "当前标定端点与当时不同，只能按旧记录查。"}
	}
	return classification{reapplicable, "当前卷的供声页与标定均可重新量测。"}
}

// footprint lists every scan page that can supply sound to a window. Coverage
// runs merge adjacent same-state rows, so a readable run may span a page
// boundary; walking the run row by row through Locate catches every page,
// including one that lies outside the picture window but feeds it through the
// lane lead.
func footprint(reel *capture.Reel, coverage []register.Run) []Source {
	seen := make(map[int]bool)
	sources := make([]Source, 0, len(coverage))
	add := func(frame int) {
		if frame < 0 || frame >= reel.Frames || seen[frame] {
			return
		}
		seen[frame] = true
		digest := ""
		if sheet, ok := reel.Sheets[frame]; ok {
			digest = sheet.Digest
		}
		sources = append(sources, Source{Frame: frame, Sheet: digest})
	}
	for _, run := range coverage {
		next := run.Start
		for next < run.End {
			a, status := register.Locate(reel, next)
			if status == register.Outside {
				next++
				continue
			}
			add(a.Frame)
			// Skip to the end of this source page, or the end of the run.
			next += reel.Rows - a.Row
		}
	}
	return sources
}

// material fingerprints the rendered PCM so a step can prove both that it was
// actually applied and that no other step shares its output.
func material(render readout.Render) string {
	hash := sha256.New()
	var pair [2]byte
	for _, sample := range render.Samples {
		pair[0] = byte(uint16(sample) >> 8)
		pair[1] = byte(uint16(sample))
		hash.Write(pair[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func probeValue(reel *capture.Reel, g gauge.Gauge, probe Probe) *readout.Value {
	if probe.Frame < 0 || probe.Frame >= reel.Frames || probe.Row < 0 || probe.Row >= reel.Rows {
		return nil
	}
	value := readout.Trace(reel, g, probe.Frame*reel.Rows+probe.Row)
	return &value
}

// pictures pins each frame's picture presence at apply time, including the
// missing pages that the frozen record must reproduce.
func pictures(reel *capture.Reel, window register.Window) []FrameState {
	states := make([]FrameState, 0, window.End-window.Start)
	for frame := window.Start; frame < window.End; frame++ {
		state := FrameState{Frame: frame}
		if sheet, ok := reel.Sheets[frame]; ok {
			state.Depth, state.Sheet = sheet.Depth, sheet.Digest
		}
		states = append(states, state)
	}
	return states
}

// apply renders one window with the chosen calibration and commits it to the
// trail as a single successful step. An invalid window or unusable meter
// returns an error before anything is recorded, leaving the previous step in
// effect exactly as the browser shows it.
func (b *bench) apply(reel *capture.Reel, window register.Window, profile string, probe Probe) (Step, readout.Render, bool, error) {
	g, render, err := station.RenderWindow(reel, window, profile)
	if err != nil {
		return Step{}, readout.Render{}, false, err
	}
	step := Step{
		Window: window, Profile: g.Profile, Gauge: g, Probe: probe,
		Clock:      Clock{Rows: reel.Rows, Rate: register.Rate(reel.Header), Lead: reel.Lead},
		Coverage:   render.Coverage,
		Envelope:   summarize(render, window.Start*reel.Rows),
		Render:     material(render),
		Sources:    footprint(reel, render.Coverage),
		Pictures:   pictures(reel, window),
		Capture:    reel.Digest,
		AppliedAt:  time.Now().UTC(),
		ProbeTrace: probeValue(reel, g, probe),
	}
	recorded, created, err := b.history.Record(reel.Name, step)
	if err != nil {
		return Step{}, readout.Render{}, false, err
	}
	return recorded, render, created, nil
}

func defaultProbe(window register.Window) Probe {
	return Probe{Frame: window.Start, Row: 0}
}

func validProbe(reel *capture.Reel, window register.Window, probe Probe) bool {
	return probe.Frame >= window.Start && probe.Frame < window.End && probe.Row >= 0 && probe.Row < reel.Rows
}

// probeFor clamps an explicit picture position to the window, falling back to
// the window origin when the coordinates belong to a different region.
func probeFor(reel *capture.Reel, window register.Window, frame, row int) Probe {
	probe := Probe{Frame: frame, Row: row}
	if validProbe(reel, window, probe) {
		return probe
	}
	return defaultProbe(window)
}
