package diffreview

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

// FixtureSpec builds a verified synthetic parcel for tests and the offline demo.
type FixtureSpec struct {
	Dir     string
	Capture string
	Rows    int
	FPS     int
	Start   int
	End     int
	// Coverage partitions the window in absolute picture rows. Nil means all readable.
	Coverage []register.Run
	// Tone returns the stereo sample for an absolute picture row when readable.
	Tone func(absRow int) (int16, int16)
}

// PublishFixture writes a fully verifiable parcel and returns its path/receipt.
func PublishFixture(spec FixtureSpec) (string, parcel.Receipt, error) {
	frames := spec.End - spec.Start
	n := frames * spec.Rows
	samples := make([]int16, 2*n)
	coverage := spec.Coverage
	if coverage == nil {
		coverage = []register.Run{{Start: spec.Start * spec.Rows, End: spec.End * spec.Rows, Status: register.Readable}}
	}
	readable := map[int]bool{}
	for _, run := range coverage {
		if run.Status == register.Readable {
			for p := run.Start; p < run.End; p++ {
				readable[p] = true
			}
		}
	}
	for i := 0; i < n; i++ {
		absRow := spec.Start*spec.Rows + i
		if readable[absRow] && spec.Tone != nil {
			l, r := spec.Tone(absRow)
			samples[2*i], samples[2*i+1] = l, r
		}
	}
	pics := make([]parcel.Picture, 0, frames)
	for f := spec.Start; f < spec.End; f++ {
		pics = append(pics, parcel.Picture{Frame: f, Sample: (f - spec.Start) * spec.Rows})
	}
	sum := sha256.Sum256([]byte("fixture-source-" + spec.Capture))
	idx := parcel.Index{
		Capture:  spec.Capture,
		Source:   fmt.Sprintf("%x", sum),
		Window:   register.Window{Start: spec.Start, End: spec.End},
		Rows:     spec.Rows,
		FPS:      spec.FPS,
		Lead:     0,
		Coverage: coverage,
		Pictures: pics,
	}
	store := parcel.Store{Root: spec.Dir}
	receipt, err := store.Publish(idx, wave.Encode(spec.FPS*spec.Rows, samples))
	if err != nil {
		return "", parcel.Receipt{}, err
	}
	return filepath.Join(spec.Dir, receipt.ID+".zip"), receipt, nil
}

type toneSpec struct {
	freq, amp float64
}

func toneAt(fps, rows int, t toneSpec) func(int) (int16, int16) {
	return func(absRow int) (int16, int16) {
		secs := (float64(absRow) + 0.5) / float64(fps*rows)
		v := int16(math.Round(t.amp * math.Sin(2*math.Pi*t.freq*secs)))
		return v, v
	}
}

// BuildDemoParcels creates the two handoff parcels described in the task:
// same fps (24), different grids (2000 vs 1920 rows/frame), partially
// overlapping windows ([1,10) vs [2,11)), with one confirmed coverage change,
// one strong cross-grid acoustic content change, one inconclusive cross-grid
// position and several agreeing separator frames.
func BuildDemoParcels(dir string) (aPath, bPath string, err error) {
	if err = os.MkdirAll(dir, 0o750); err != nil {
		return "", "", err
	}
	const fps = 24

	// A: fine grid, window [1,10), everything readable.
	aRows := 2000
	aTone := func(absRow int) (int16, int16) {
		f := absRow / aRows
		switch f {
		case 2:
			return toneAt(fps, aRows, toneSpec{330, 9000})(absRow)
		case 4:
			return toneAt(fps, aRows, toneSpec{523.25, 14000})(absRow)
		case 6:
			return toneAt(fps, aRows, toneSpec{392, 8000})(absRow)
		case 1, 10:
			return toneAt(fps, aRows, toneSpec{261.6, 5000})(absRow)
		default: // separator / agreement frames 3,5,7,8,9
			return toneAt(fps, aRows, toneSpec{440, 9000})(absRow)
		}
	}
	aPath, _, err = PublishFixture(FixtureSpec{
		Dir: dir, Capture: "ember-demo-reel", Rows: aRows, FPS: fps, Start: 1, End: 10, Tone: aTone,
	})
	if err != nil {
		return "", "", fmt.Errorf("publish A: %w", err)
	}

	// B: coarser grid, window [2,11), frame 2 missing_sheet.
	bRows := 1920
	bCoverage := []register.Run{
		{Start: 2 * bRows, End: 3 * bRows, Status: register.Missing},
		{Start: 3 * bRows, End: 11 * bRows, Status: register.Readable},
	}
	bTone := func(absRow int) (int16, int16) {
		f := absRow / bRows
		switch f {
		case 4:
			return 0, 0 // readable digital silence vs A's loud tone
		case 6:
			return toneAt(fps, bRows, toneSpec{392, 6500})(absRow) // level gap in the inconclusive band
		default: // same continuous-time 440 Hz program on agreement frames
			return toneAt(fps, bRows, toneSpec{440, 9000})(absRow)
		}
	}
	bPath, _, err = PublishFixture(FixtureSpec{
		Dir: dir, Capture: "ember-demo-reel-rescan", Rows: bRows, FPS: fps, Start: 2, End: 11,
		Coverage: bCoverage, Tone: bTone,
	})
	if err != nil {
		return "", "", fmt.Errorf("publish B: %w", err)
	}

	// Friendly names for operators browsing the fixtures directory.
	friendlyA := filepath.Join(dir, "handoff-a.zip")
	friendlyB := filepath.Join(dir, "handoff-b.zip")
	if err = copyFile(aPath, friendlyA); err != nil {
		return "", "", err
	}
	if err = copyFile(bPath, friendlyB); err != nil {
		return "", "", err
	}
	return friendlyA, friendlyB, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o640)
}
