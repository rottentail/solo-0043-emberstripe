package board_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/board"
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

func loadCinderA(t *testing.T) *capture.Reel {
	t.Helper()
	reels, err := capture.LoadCatalog(sampleRoot(t))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	reel := reels["cinder-a"]
	if reel == nil {
		t.Fatal("cinder-a not generated")
	}
	return reel
}

func loadVireoB(t *testing.T) *capture.Reel {
	t.Helper()
	reels, err := capture.LoadCatalog(sampleRoot(t))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	reel := reels["vireo-b"]
	if reel == nil {
		t.Fatal("vireo-b not generated")
	}
	return reel
}

// syntheticReel builds an in-memory reel with the given frame count so tests
// can exercise the 48-frame station limit without scanner files.
func syntheticReel(t *testing.T, frames int) *capture.Reel {
	t.Helper()
	const rows, width, fps = 2000, 112, 24
	h := capture.Header{
		Name: "synthetic-roll", Width: width, Rows: rows, FPS: fps, Frames: frames, Lead: 0,
		Picture: capture.Span{X0: 0, X1: 64},
		Lanes:   [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}},
		Profiles: []capture.Profile{{
			ID:    "bench",
			Dark:  capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: 36}},
			Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 36, X1: 72}},
		}},
	}
	reel := &capture.Reel{Header: h, Sheets: map[int]capture.Sheet{}}
	for frame := 0; frame < frames; frame++ {
		img := image.NewGray16(image.Rect(0, 0, width, rows))
		for y := 0; y < rows; y++ {
			intensity := uint16(30000)
			if y < 36 {
				intensity = 6000
			} else if y < 72 {
				intensity = 55000
			}
			for x := 72; x < 108; x++ {
				img.SetGray16(x, y, color.Gray16{Y: intensity})
			}
		}
		reel.Sheets[frame] = capture.Sheet{Bitmap: img, Depth: 16, Digest: "synthetic"}
	}
	return reel
}

func TestPlanMergesOverlappingRequiredItems(t *testing.T) {
	reel := loadCinderA(t)
	brief := board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 400, LeadRows: 100, TailRows: 100},
			{ID: "r2", Frame: 0, Row: 500, LeadRows: 100, TailRows: 100},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("expected feasible plan: %+v", plan.Adjustments)
	}
	if len(plan.Clips) != 1 {
		t.Fatalf("overlapping items must share one playback, got %d clips", len(plan.Clips))
	}
	outcome := findItem(t, plan, "r2")
	if len(outcome.SharedWith) != 1 || outcome.SharedWith[0] != "r1" {
		t.Fatalf("r2 should share playback with r1, got %v", outcome.SharedWith)
	}
	if plan.UsedSeconds > board.MaxBoardSeconds {
		t.Fatalf("board exceeds cap: %f", plan.UsedSeconds)
	}
}

func TestPlanHonoursSeparation(t *testing.T) {
	reel := loadCinderA(t)
	brief := board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 400, LeadRows: 100, TailRows: 100, SeparateFrom: "r2"},
			{ID: "r2", Frame: 0, Row: 500, LeadRows: 100, TailRows: 100},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Clips) != 2 {
		t.Fatalf("separated items must be distinct clips, got %d", len(plan.Clips))
	}
	separated := false
	for _, clip := range plan.Clips {
		if clip.Separates != nil && clip.Separates[0] != "" {
			separated = true
		}
	}
	if !separated {
		t.Fatal("no clip records the enforced separation pair")
	}
}

func TestPlanReportsCoverageLimitsNearMissingSheet(t *testing.T) {
	reel := loadCinderA(t)
	// cinder-a: lead 230, frame 2 sheet missing. Picture rows 3770..5770
	// map to the missing source page per the reference workflow.
	brief := board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "r1", Frame: 2, Row: 0, LeadRows: 400, TailRows: 400},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	outcome := findItem(t, plan, "r1")
	if len(outcome.Limits) == 0 {
		t.Fatal("anchor next to missing sheet must report coverage limits")
	}
	found := false
	for _, note := range outcome.Limits {
		if note.Reason != "" && note.Limited.End > note.Limited.Start {
			found = true
		}
	}
	if !found {
		t.Fatalf("coverage notes lack bounded intervals: %+v", outcome.Limits)
	}
}

func TestOptionalItemsShareSplitOrOmit(t *testing.T) {
	reel := loadCinderA(t)
	brief := board.Request{
		Profile:    "bench",
		MaxSeconds: 0.02, // tight budget: 960 sample frames at 48 kHz
		Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 400, LeadRows: 50, TailRows: 50},
		},
		Optional: []board.Target{
			{ID: "o1", Priority: 1, Frame: 0, Row: 420, LeadRows: 20, TailRows: 20},
			{ID: "o2", Priority: 2, Frame: 5, Row: 100, LeadRows: 2000, TailRows: 2000},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Feasible {
		t.Fatal("required anchor fits, plan must stay feasible with optional degradation")
	}
	o1 := findItem(t, plan, "o1")
	if o1.Outcome != board.OutcomeShared {
		t.Fatalf("o1 sits inside r1 playback and should share, got %s", o1.Outcome)
	}
	o2 := findItem(t, plan, "o2")
	switch o2.Outcome {
	case board.OutcomeSplit, board.OutcomeOmitted:
	default:
		t.Fatalf("o2 must split or omit under the tight budget, got %s", o2.Outcome)
	}
}

func TestInfeasibleBriefReturnsAdjustments(t *testing.T) {
	reel := loadCinderA(t)
	// Two separated wide contexts cannot fit into a deliberately tiny cap.
	brief := board.Request{
		Profile:    "bench",
		MaxSeconds: 0.01,
		Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 1000, LeadRows: 800, TailRows: 800, SeparateFrom: "r2"},
			{ID: "r2", Frame: 4, Row: 1000, LeadRows: 800, TailRows: 800},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Feasible || len(plan.Adjustments) == 0 {
		t.Fatalf("expected infeasible plan with adjustments, got feasible=%v", plan.Feasible)
	}
	kinds := map[string]bool{}
	for _, adjustment := range plan.Adjustments {
		kinds[adjustment.Kind] = true
		if adjustment.Relax == "" || adjustment.Detail == "" {
			t.Fatalf("adjustment must name the relaxation and detail: %+v", adjustment)
		}
	}
	if !kinds["extend_budget"] {
		t.Fatal("adjustment list must always offer a time-cap extension")
	}
}

func TestRenderProducesPlayableWAVWithTraceableRows(t *testing.T) {
	reel := loadCinderA(t)
	brief := board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "r1", Frame: 1, Row: 500, LeadRows: 100, TailRows: 100},
			{ID: "r2", Frame: 5, Row: 300, LeadRows: 80, TailRows: 80},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	audio, stamped, err := board.Render(reel, plan)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	header, err := wave.Inspect(audio)
	if err != nil {
		t.Fatalf("wav inspect: %v", err)
	}
	if header.Frames != stamped.UsedSamples {
		t.Fatalf("wav frames %d != plan used %d", header.Frames, stamped.UsedSamples)
	}
	if float64(header.Frames)/float64(header.Rate) > board.MaxBoardSeconds+1e-9 {
		t.Fatal("rendered board exceeds 12 seconds")
	}
	// Every clip must carry window and board-relative timing traceability.
	for _, clip := range stamped.Clips {
		if clip.Window.Start > clip.Picture.Start/reel.Rows || clip.Window.End < (clip.Picture.End+reel.Rows-1)/reel.Rows {
			t.Fatalf("clip %s window does not cover its picture rows", clip.ID)
		}
		if clip.BoardLength <= 0 || clip.BoardEnd-clip.BoardStart != clip.Picture.End-clip.Picture.Start {
			t.Fatalf("clip %s board timing inconsistent", clip.ID)
		}
	}
}

func TestRenderRejectsInfeasiblePlan(t *testing.T) {
	reel := loadCinderA(t)
	plan, err := board.PlanBoard(reel, board.Request{
		Profile:    "bench",
		MaxSeconds: 0.001,
		Required:   []board.Target{{ID: "r1", Frame: 0, Row: 100, LeadRows: 500, TailRows: 500}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, _, err := board.Render(reel, plan); err == nil {
		t.Fatal("infeasible plan must not render")
	}
}

func TestInvalidBriefs(t *testing.T) {
	reel := loadCinderA(t)
	cases := map[string]board.Request{
		"no required": {Profile: "bench"},
		"bad frame": {Profile: "bench", Required: []board.Target{
			{ID: "r1", Frame: 99, Row: 0},
		}},
		"unknown partner": {Profile: "bench", Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 0, SeparateFrom: "ghost"},
		}},
		"duplicate ids": {Profile: "bench", Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 0}, {ID: "r1", Frame: 0, Row: 1},
		}},
		"bad gauge": {Profile: "absent", Required: []board.Target{
			{ID: "r1", Frame: 0, Row: 0},
		}},
	}
	for name, brief := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := board.PlanBoard(reel, brief); err == nil {
				t.Fatalf("%s: expected error", name)
			}
		})
	}
}

func TestOptionalSplitReusesCoveredRows(t *testing.T) {
	reel := syntheticReel(t, 10)
	// Required anchor occupies rows 4000..4101. The optional item spans that
	// anchor plus a large tail; under a tight budget the covered anchor rows
	// must cost nothing extra, and only genuinely new tail rows are added.
	brief := board.Request{
		Profile:    "bench",
		MaxSeconds: 0.05, // 2400 sample frames at 48 kHz
		Required: []board.Target{
			{ID: "r1", Frame: 2, Row: 50, LeadRows: 50, TailRows: 50},
		},
		Optional: []board.Target{
			{ID: "o1", Priority: 1, Frame: 2, Row: 50, LeadRows: 50, TailRows: 3000},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("plan should stay feasible by splitting: %+v", plan.Adjustments)
	}
	if plan.UsedSamples > plan.BudgetSamples {
		t.Fatalf("board %d samples exceeds budget %d", plan.UsedSamples, plan.BudgetSamples)
	}
	o1 := findItem(t, plan, "o1")
	if o1.Outcome != board.OutcomeSplit {
		t.Fatalf("o1 must be split, got %s", o1.Outcome)
	}
}

func TestOutsideReelContextIsClamped(t *testing.T) {
	// Anchor at row 300 with 500 lead rows reaches before the reel start;
	// the playable lead is clamped to rows 0..300 and reported as lead-side.
	reel := loadCinderA(t)
	plan, err := board.PlanBoard(reel, board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "head", Frame: 0, Row: 300, LeadRows: 500, TailRows: 10},
		},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	item := findItem(t, plan, "head")
	if item.Played.Start != 0 {
		t.Fatalf("lead context must clamp to reel start, got %d", item.Played.Start)
	}
	if !hasNote(item, "lead") {
		t.Fatal("expected a lead-side coverage note for reel edge")
	}
	for _, note := range item.Limits {
		if note.Limited.Start >= note.Limited.End {
			t.Fatalf("coverage notes must not contain empty intervals: %+v", note)
		}
	}
}

func TestNegativeLeadReportsOutsideCapture(t *testing.T) {
	// vireo-b shifts sound back by 135 rows; the first 135 picture rows have
	// no source sound. The anchor row itself sits inside that outside run.
	reel := loadVireoB(t)
	plan, err := board.PlanBoard(reel, board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "start", Frame: 0, Row: 0, LeadRows: 100, TailRows: 200},
		},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	item := findItem(t, plan, "start")
	found := false
	for _, note := range item.Limits {
		if note.Side == "anchor" && note.Limited.Start == 0 && note.Limited.End == 135 &&
			note.Reason == "该区间已移出卷界，没有可听声槽行。" {
			found = true
		}
		if note.Limited.Start >= note.Limited.End {
			t.Fatalf("coverage notes must not contain empty intervals: %+v", note)
		}
	}
	if !found {
		t.Fatalf("expected an outside-capture anchor note for rows 0..135, got %+v", item.Limits)
	}
}

func TestClipExceedingFrameLimitIsBlockedAtPlanning(t *testing.T) {
	reel := syntheticReel(t, 60)
	plan, err := board.PlanBoard(reel, board.Request{
		Profile:    "bench",
		MaxSeconds: 30,
		Required: []board.Target{
			{ID: "wide", Frame: 30, Row: 0, LeadRows: 25 * 2000, TailRows: 25 * 2000},
		},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Feasible {
		t.Fatal("a 50+ frame clip must not be marked feasible against the 48-frame limit")
	}
	kinds := map[string]bool{}
	for _, adjustment := range plan.Adjustments {
		kinds[adjustment.Kind] = true
	}
	if !kinds["split_clip"] {
		t.Fatalf("expected a split_clip adjustment, got %+v", plan.Adjustments)
	}
	if _, _, err := board.Render(reel, plan); err == nil {
		t.Fatal("infeasible over-length plan must not render")
	}
}

func findItem(t *testing.T, plan board.Plan, id string) board.ItemOutcome {
	t.Helper()
	for _, item := range plan.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("item %s missing from plan", id)
	return board.ItemOutcome{}
}

func hasNote(item board.ItemOutcome, side string) bool {
	for _, note := range item.Limits {
		if note.Side == side {
			return true
		}
	}
	return false
}

func TestCoverageNoteSides(t *testing.T) {
	reel := loadCinderA(t)
	// Anchor at picture row 3700 (frame 1 row 1700) is readable; the missing
	// sheet run starts at 3770, so only the tail is unhearable.
	plan, err := board.PlanBoard(reel, board.Request{
		Profile: "bench",
		Required: []board.Target{
			{ID: "edge", Frame: 1, Row: 1700, LeadRows: 100, TailRows: 200},
		},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	item := findItem(t, plan, "edge")
	sides := map[string]bool{}
	for _, note := range item.Limits {
		sides[note.Side] = true
	}
	if !sides["tail"] {
		t.Fatalf("expected a tail-side coverage note, got %+v", item.Limits)
	}
	if sides["anchor"] {
		t.Fatalf("anchor row is readable and must not be flagged: %+v", item.Limits)
	}
}
