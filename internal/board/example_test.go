package board_test

import (
	"encoding/json"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/board"
)

// TestExampleBoardJSON documents the cue-sheet shape QC reviewers receive.
func TestExampleBoardJSON(t *testing.T) {
	reel := loadCinderA(t)
	brief := board.Request{
		Profile:    "bench",
		MaxSeconds: 12,
		Required: []board.Target{
			{ID: "must-1", Frame: 0, Row: 400, LeadRows: 240, TailRows: 240, Note: "卷首异常底噪"},
			{ID: "must-2", Frame: 2, Row: 0, LeadRows: 300, TailRows: 300, Note: "缺页前后衔接", SeparateFrom: "must-3"},
			{ID: "must-3", Frame: 4, Row: 500, LeadRows: 200, TailRows: 200, Note: "8 位页声槽对比"},
		},
		Optional: []board.Target{
			{ID: "opt-1", Priority: 1, Frame: 5, Row: 130, LeadRows: 100, TailRows: 100, Note: "右声道越界标定对照"},
			{ID: "opt-2", Priority: 2, Frame: 0, Row: 410, LeadRows: 60, TailRows: 60, Note: "与 must-1 共原声复核"},
		},
	}
	plan, err := board.PlanBoard(reel, brief)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("expected feasible: %+v", plan.Adjustments)
	}
	audio, stamped, err := board.Render(reel, plan)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(audio) == 0 {
		t.Fatal("empty audio")
	}
	raw, err := json.MarshalIndent(stamped, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("cue sheet is not valid JSON")
	}
	// The optional item sharing must-1's source must be recorded as a shared
	// playback, and the two separated required items must end in distinct clips.
	opt2 := findItem(t, stamped, "opt-2")
	if opt2.Outcome != board.OutcomeShared {
		t.Fatalf("opt-2 should share must-1 playback, got %s", opt2.Outcome)
	}
	must2, must3 := findItem(t, stamped, "must-2"), findItem(t, stamped, "must-3")
	if must2.ClipIDs[0] == must3.ClipIDs[0] {
		t.Fatal("separation pair must not share a clip")
	}
	// Every rendered clip must trace back to reel rows and stay in the cap.
	total := 0
	for _, clip := range stamped.Clips {
		total += clip.Picture.End - clip.Picture.Start
		if clip.BoardStart < 0 || clip.BoardEnd <= clip.BoardStart {
			t.Fatalf("clip %s has invalid board timing", clip.ID)
		}
	}
	if total != stamped.UsedSamples {
		t.Fatalf("clip rows %d != used samples %d", total, stamped.UsedSamples)
	}
}
