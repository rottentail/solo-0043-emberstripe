package blind

import (
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

// syntheticReel builds a minimal in-memory reel where every available sheet is
// readable and the two profiles only differ by which dark/clear row bands they
// reference.
func syntheticReel(t *testing.T, missingFrames ...int) *capture.Reel {
	t.Helper()
	const width, rows, fps, frames = 64, 16, 16, 6
	header := capture.Header{
		Name: "test-reel", Width: width, Rows: rows, FPS: fps, Frames: frames,
		Picture: capture.Span{X0: 0, X1: 16},
		Lanes:   [2]capture.Span{{X0: 24, X1: 32}, {X0: 40, X1: 48}},
		Profiles: []capture.Profile{
			{ID: "one", Dark: capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: 2}}, Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 2, X1: 4}}},
			{ID: "two", Dark: capture.Patch{Frame: 0, Rows: capture.Span{X0: 4, X1: 6}}, Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 6, X1: 8}}},
		},
	}
	reel := &capture.Reel{Header: header, Sheets: map[int]capture.Sheet{}, Digest: "test-digest"}
	missing := map[int]bool{}
	for _, frame := range missingFrames {
		missing[frame] = true
	}
	// Bands give both profiles a low dark reference and a high clear reference.
	darkRows := map[int]bool{0: true, 1: true, 4: true, 5: true}
	clearRows := map[int]bool{2: true, 3: true, 6: true, 7: true}
	for frame := 0; frame < frames; frame++ {
		if missing[frame] {
			continue
		}
		img := image.NewGray16(image.Rect(0, 0, width, rows))
		for y := 0; y < rows; y++ {
			value := uint16(24000)
			switch {
			case darkRows[y]:
				value = 7000
			case clearRows[y]:
				value = 52000
			}
			for x := 0; x < width; x++ {
				img.SetGray16(x, y, color.Gray16{Y: value})
			}
		}
		reel.Sheets[frame] = capture.Sheet{Bitmap: img, Depth: 16, Digest: "sheet"}
	}
	return reel
}

func TestBuildRejectsWindowWithoutCommonAudibleRange(t *testing.T) {
	// Every sheet that window 3..5 maps to is missing, so neither profile has
	// any readable row and no common audible range exists.
	reel := syntheticReel(t, 3, 4, 5)
	req := CreateRequest{Capture: reel.Name, Windows: []WindowRequest{{Start: 3, End: 6, Profile: "one", Against: "two"}}}
	_, err := Build(reel, req)
	var noCommon *NoCommonError
	if err == nil {
		t.Fatal("expected a no-common-audible-range error")
	}
	if !errors.As(err, &noCommon) {
		t.Fatalf("expected NoCommonError, got %v", err)
	}
	if noCommon.Window != 0 {
		t.Fatalf("expected window 0 flagged, got %d", noCommon.Window)
	}
}

func TestOrderingIsDeterministicAndContentAddressed(t *testing.T) {
	reel := syntheticReel(t)
	req := CreateRequest{
		Capture: reel.Name, Seed: "fixed-seed",
		Windows: []WindowRequest{
			{Start: 0, End: 2, Profile: "one", Against: "two"},
			{Start: 2, End: 4, Profile: "one", Against: "two"},
		},
	}
	first, err := Build(reel, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	second, err := Build(reel, req)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("identical requests must share an id: %s vs %s", first.ID, second.ID)
	}
	for i := range first.Windows {
		if first.Windows[i].Order != second.Windows[i].Order {
			t.Fatalf("window %d order changed between builds", i)
		}
	}
	// A different seed is allowed to change the presentation order, and must
	// therefore change the content-addressed id.
	req.Seed = "other-seed"
	other, err := Build(reel, req)
	if err != nil {
		t.Fatalf("reseed build: %v", err)
	}
	if other.ID == first.ID {
		t.Fatal("changing the seed must change the sheet id")
	}
}

func TestRecordAnswerAndResumeAcrossStore(t *testing.T) {
	reel := syntheticReel(t)
	req := CreateRequest{Capture: reel.Name, Windows: []WindowRequest{{Start: 0, End: 2, Profile: "one", Against: "two"}}}
	sheet, err := Build(reel, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	store := &Store{Root: t.TempDir()}
	if err := store.Save(sheet); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := store.RecordAnswer(sheet.ID, Choice{Window: 0, Option: "A", Reviewer: "listener-01"}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	// Re-opening through Load simulates continuing on another device: the
	// stored option order and the prior pick must both come back intact.
	resumed, err := store.Load(sheet.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	win := resumed.Windows[0]
	if len(win.Answers) != 1 {
		t.Fatalf("expected one resumed answer, got %d", len(win.Answers))
	}
	position, ok := win.Answers["listener-01"]
	if !ok {
		t.Fatal("reviewer answer missing after resume")
	}
	if position != win.Order[0] {
		t.Fatal("resumed answer does not map back to option A's position")
	}
	// Re-posting the same pick is idempotent; switching option overwrites it.
	if _, err := store.RecordAnswer(sheet.ID, Choice{Window: 0, Option: "A", Reviewer: "listener-01"}); err != nil {
		t.Fatalf("idempotent answer: %v", err)
	}
	if _, err := store.RecordAnswer(sheet.ID, Choice{Window: 0, Option: "B", Reviewer: "listener-01"}); err != nil {
		t.Fatalf("changed answer: %v", err)
	}
	resumed, _ = store.Load(sheet.ID)
	if len(resumed.Windows[0].Answers) != 1 || resumed.Windows[0].Answers["listener-01"] != win.Order[1] {
		t.Fatal("changed pick did not replace the earlier answer")
	}
}

func TestUnblindFlagsDisagreementAndQuarantine(t *testing.T) {
	reel := syntheticReel(t)
	req := CreateRequest{Capture: reel.Name, Windows: []WindowRequest{{Start: 0, End: 2, Profile: "one", Against: "two"}}}
	sheet, err := Build(reel, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	store := &Store{Root: t.TempDir()}
	if err := store.Save(sheet); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := store.RecordAnswer(sheet.ID, Choice{Window: 0, Option: "A", Reviewer: "listener-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordAnswer(sheet.ID, Choice{Window: 0, Option: "B", Reviewer: "listener-02"}); err != nil {
		t.Fatal(err)
	}
	result, err := store.Unblind(reel, sheet.ID)
	if err != nil {
		t.Fatalf("unblind: %v", err)
	}
	if result.Status != StatusUnblinded || len(result.Quarantined) != 0 {
		t.Fatalf("healthy sheet should unblind cleanly, got %s %v", result.Status, result.Quarantined)
	}
	if !result.Windows[0].Disagreement {
		t.Fatal("two reviewers picking different profiles must be a disagreement")
	}

	// Simulate material drift by changing the live snapshot digest.
	drifted := *reel
	drifted.Digest = "changed-digest"
	driftedResult, err := store.Unblind(&drifted, sheet.ID)
	if err != nil {
		t.Fatalf("unblind drifted: %v", err)
	}
	if driftedResult.Status != StatusQuarantine {
		t.Fatalf("expected quarantine, got %s", driftedResult.Status)
	}
	if driftedResult.Windows[0].Quarantine != ReasonSourceChanged {
		t.Fatalf("expected source change reason, got %q", driftedResult.Windows[0].Quarantine)
	}
	// Preferences remain decodable from the sheet's own ordering even when the
	// material needed to re-check them has moved.
	if len(driftedResult.Windows[0].Preferences) != 2 {
		t.Fatal("quarantined sheet should still expose decoded preferences")
	}
}

func TestCommonReadableIntersection(t *testing.T) {
	reel := syntheticReel(t)
	// Both profiles render the same geometry, so coverage is fully shared.
	req := CreateRequest{Capture: reel.Name, Windows: []WindowRequest{{Start: 0, End: 1, Profile: "one", Against: "two"}}}
	sheet, err := Build(reel, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if sheet.Windows[0].CommonRows != reel.Rows {
		t.Fatalf("expected %d common rows, got %d", reel.Rows, sheet.Windows[0].CommonRows)
	}
}
