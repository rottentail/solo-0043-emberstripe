package impactmap

import (
	"encoding/json"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

func testReel(name string, lead int, pages map[int]string) *capture.Reel {
	h := capture.Header{
		Name: name, Width: 100, Rows: 100, FPS: 25, Frames: 5, Lead: lead,
		Picture: capture.Span{X0: 0, X1: 60},
		Lanes:   [2]capture.Span{{X0: 70, X1: 80}, {X0: 85, X1: 95}},
	}
	reel := &capture.Reel{Header: h, Sheets: map[int]capture.Sheet{}}
	for page, digest := range pages {
		reel.Sheets[page] = capture.Sheet{Depth: 16, Digest: digest}
	}
	return reel
}

func fullPages(prefix string) map[int]string {
	m := map[int]string{}
	for i := 0; i < 5; i++ {
		m[i] = prefix + "-page-digest"
	}
	return m
}

func TestBuildMapsShiftedPagesAndDifferentOffsets(t *testing.T) {
	orig := testReel("orig", 20, fullPages("orig"))
	resc := testReel("resc", -10, fullPages("resc"))
	catalog := map[string]*capture.Reel{"orig": orig, "resc": resc}

	surveyJSON := []byte(`{
      "original_reel":"orig",
      "rescan_reel":"resc",
      "anchors":[
        {"id":"a","original":[0,90],"rescan":{"page":1,"row":5}},
        {"id":"b","original":{"page":2,"row":10},"rescan":[2,25]}
      ],
      "segments":[
        {"id":"tear","start":{"page":0,"row":95},"end":{"page":1,"row":20},"columns":[74,78]}
      ]
    }`)
	survey, err := ParseSurvey(surveyJSON)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Build(catalog, survey)
	if err != nil {
		t.Fatal(err)
	}
	seg := m.Segments[0]
	if len(seg.Original.LaneHits) != 1 || seg.Original.LaneHits[0].Lane != 0 {
		t.Fatalf("original lane hits = %#v", seg.Original.LaneHits)
	}
	if got := seg.Original.AudibleFrameRuns; len(got) != 1 || got[0] != (AbsRange{75, 100}) {
		t.Fatalf("original audible = %#v", got)
	}
	if seg.Original.AudibleRowCount != 25 {
		t.Fatalf("original audible count = %d", seg.Original.AudibleRowCount)
	}
	if seg.Rescan == nil {
		t.Fatal("missing rescan report")
	}
	if len(seg.Rescan.LaneHits) != 1 || seg.Rescan.LaneHits[0].Lane != 0 {
		t.Fatalf("rescan lane hits = %#v", seg.Rescan.LaneHits)
	}
	if got := seg.Rescan.AudibleFrameRuns; len(got) != 1 || got[0] != (AbsRange{120, 145}) {
		t.Fatalf("rescan audible = %#v", got)
	}
	if got := seg.Rescan.ScanRange; got != (AbsRange{110, 135}) {
		t.Fatalf("rescan scan range = %#v", got)
	}
	if len(seg.CorroboratedRanges) != 1 || seg.CorroboratedRanges[0] != (AbsRange{95, 120}) {
		t.Fatalf("corroborated = %#v", seg.CorroboratedRanges)
	}
	if seg.ConfirmOriginalFrom != nil {
		t.Fatalf("unexpected cutoff: %#v", seg.ConfirmOriginalFrom)
	}

	var foundOrigPage, foundRescanPage bool
	for _, ev := range seg.Original.LaneHits[0].FrameRuns[0].Evidence {
		if ev.Reel == "orig" && ev.SHA256 == "orig-page-digest" && ev.ProvesOriginalScan {
			foundOrigPage = true
		}
	}
	for _, ev := range seg.Rescan.LaneHits[0].FrameRuns[0].Evidence {
		if ev.Reel == "resc" && ev.SHA256 == "resc-page-digest" && !ev.ProvesOriginalScan {
			foundRescanPage = true
		}
	}
	if !foundOrigPage || !foundRescanPage {
		t.Fatalf("missing lineage evidence orig=%v resc=%v", foundOrigPage, foundRescanPage)
	}
}

func TestMissingRescanPageDoesNotBackfillOriginalAndSetsCutoff(t *testing.T) {
	orig := testReel("orig", 20, fullPages("orig"))
	rescanPages := fullPages("resc")
	delete(rescanPages, 1)
	resc := testReel("resc", -10, rescanPages)
	catalog := map[string]*capture.Reel{"orig": orig, "resc": resc}

	s := Survey{
		Original: "orig", Rescan: "resc",
		References: []Reference{
			mustRef(t, "a", 0, 90, 1, 5),
			mustRef(t, "b", 2, 10, 2, 25),
		},
		Marks: []Mark{{
			ID: "tear", Start: Point{Page: 0, Row: 95}, End: Point{Page: 1, Row: 20},
			X0: intPtr(74), X1: intPtr(78),
		}},
	}
	m, err := Build(catalog, s)
	if err != nil {
		t.Fatal(err)
	}
	seg := m.Segments[0]
	if len(seg.Original.AudibleFrameRuns) != 1 {
		t.Fatalf("original should remain audible: %#v", seg.Original.AudibleFrameRuns)
	}
	if seg.Rescan.AudibleRowCount != 0 {
		t.Fatalf("rescan should have no audible rows: %#v", seg.Rescan.AudibleFrameRuns)
	}
	if len(seg.CorroboratedRanges) != 0 {
		t.Fatalf("missing rescan page cannot corroborate: %#v", seg.CorroboratedRanges)
	}
	if seg.ConfirmOriginalFrom == nil || seg.ConfirmOriginalFrom.AbsoluteRow != 95 {
		t.Fatalf("cutoff = %#v", seg.ConfirmOriginalFrom)
	}
	var missingEvidence bool
	for _, run := range seg.Rescan.LaneHits[0].FrameRuns {
		if run.Status == StatusMissing && !run.Audible {
			missingEvidence = true
		}
	}
	if !missingEvidence {
		t.Fatalf("expected missing rescan run: %#v", seg.Rescan.LaneHits[0])
	}
}

func TestReferenceHullGivesConfirmedMiddleAndTailCutoff(t *testing.T) {
	orig := testReel("orig", 0, fullPages("orig"))
	resc := testReel("resc", 0, fullPages("resc"))
	// +10 translation.
	refs := []Reference{mustRef(t, "a", 1, 0, 1, 10), mustRef(t, "b", 2, 0, 2, 10)}
	s := Survey{
		Original: "orig", Rescan: "resc", References: refs,
		Marks: []Mark{{ID: "m", Start: Point{Absolute: intPtr(150)}, End: Point{Absolute: intPtr(250)}, X0: intPtr(74), X1: intPtr(78)}},
	}
	m, err := Build(map[string]*capture.Reel{"orig": orig, "resc": resc}, s)
	if err != nil {
		t.Fatal(err)
	}
	seg := m.Segments[0]
	// Rows 150..200 are bracketed by the 100/200 reference pair; row 200 is
	// the verified endpoint itself. Rows 201..249 require original confirm.
	if got := seg.CorroboratedRanges; len(got) != 1 || got[0] != (AbsRange{150, 201}) {
		t.Fatalf("corroborated anchor interval = %#v", got)
	}
	if seg.ConfirmOriginalFrom == nil || seg.ConfirmOriginalFrom.AbsoluteRow != 201 {
		t.Fatalf("tail cutoff = %#v, want 201", seg.ConfirmOriginalFrom)
	}
}

func TestPictureOnlyRangeAccountsForDifferentLead(t *testing.T) {
	orig := testReel("orig", 20, fullPages("orig"))
	resc := testReel("resc", -10, fullPages("resc"))
	s := Survey{
		Original: "orig", Rescan: "resc",
		References: []Reference{mustRef(t, "a", 0, 0, 0, 0)},
		Marks:      []Mark{{ID: "wide", Start: Point{Absolute: intPtr(100)}, End: Point{Absolute: intPtr(120)}, X0: intPtr(0), X1: intPtr(100)}},
	}
	m, err := Build(map[string]*capture.Reel{"orig": orig, "resc": resc}, s)
	if err != nil {
		t.Fatal(err)
	}
	o := m.Segments[0].Original
	if len(o.LaneHits) != 2 {
		t.Fatalf("expected two lanes: %#v", o.LaneHits)
	}
	if len(o.PictureRuns) == 0 || len(o.PictureOnlyRuns) == 0 {
		t.Fatalf("picture=%#v only=%#v", o.PictureRuns, o.PictureOnlyRuns)
	}
	// Picture occupies 100..120. Sound occupies frame rows 80..100. Their
	// only overlap is boundary 100, so all 20 picture rows are picture-only.
	if got := o.PictureOnlyRuns[0]; got.FrameRowStart != 100 || got.FrameRowEnd != 120 {
		t.Fatalf("picture-only = %#v", o.PictureOnlyRuns)
	}
}

func TestExplicitPartialPageGapIsReportedAndSubtractsEvidence(t *testing.T) {
	orig := testReel("orig", 0, fullPages("orig"))
	resc := testReel("resc", 0, fullPages("resc"))
	s := Survey{
		Original: "orig", Rescan: "resc",
		References: []Reference{mustRef(t, "a", 0, 0, 0, 0), mustRef(t, "b", 4, 0, 4, 0)},
		Marks: []Mark{{
			ID: "gap", Start: Point{Absolute: intPtr(10)}, End: Point{Absolute: intPtr(90)},
			Zone: "picture",
		}},
		Gaps: []Gap{{Side: "original", Start: Point{Absolute: intPtr(40)}, End: Point{Absolute: intPtr(55)}}},
	}
	m, err := Build(map[string]*capture.Reel{"orig": orig, "resc": resc}, s)
	if err != nil {
		t.Fatal(err)
	}
	runs := m.Segments[0].Original.PictureOnlyRuns
	var sawGap bool
	for _, run := range runs {
		if run.Status == StatusUnscanned && run.FrameRowStart == 40 && run.FrameRowEnd == 55 {
			sawGap = true
		}
	}
	if !sawGap {
		t.Fatalf("expected partial gap run: %#v", runs)
	}
	if got := m.Segments[0].CorroboratedRanges; len(got) != 2 || got[0] != (AbsRange{10, 40}) || got[1] != (AbsRange{55, 90}) {
		t.Fatalf("corroborated around gap = %#v", got)
	}
}

func TestParseToleratesAliasesAndCoordinates(t *testing.T) {
	var s Survey
	err := json.Unmarshal([]byte(`{
      "source_reel":"orig","target_reel":"resc",
      "control_points":[{"a":[1,2],"b":{"frame":3,"line":4}}],
      "tears":[{"page":2,"start_row":5,"end_row":9,"x":[70,80],"sound_lanes":["left"]}]
    }`), &s)
	if err != nil {
		t.Fatal(err)
	}
	if s.Original != "orig" || s.Rescan != "resc" {
		t.Fatalf("reels = %q %q", s.Original, s.Rescan)
	}
	if s.References[0].Original.Page != 1 || s.References[0].Rescan.Row != 4 {
		t.Fatalf("refs = %#v", s.References)
	}
	if s.Marks[0].Start.Page != 2 || s.Marks[0].End.Row != 9 || len(s.Marks[0].Lanes) != 1 {
		t.Fatalf("mark = %#v", s.Marks[0])
	}
}

func TestParseFlatCrossPageCoordinates(t *testing.T) {
	var s Survey
	err := json.Unmarshal([]byte(`{
      "original_reel":"o","rescan_reel":"r",
      "damage_ranges":[{"id":"x","start_page":1,"start_row":1900,"end_page":2,"end_row":120,"sound_lanes":[0]}]
    }`), &s)
	if err != nil {
		t.Fatal(err)
	}
	mark := s.Marks[0]
	if mark.Start.Page != 1 || mark.Start.Row != 1900 || mark.End.Page != 2 || mark.End.Row != 120 {
		t.Fatalf("flat cross-page mark = %#v", mark)
	}
}

func mustRef(t *testing.T, id string, op, or, rp, rr int) Reference {
	t.Helper()
	return Reference{ID: id, Original: Point{Page: op, Row: or}, Rescan: Point{Page: rp, Row: rr}}
}

func intPtr(v int) *int { return &v }
