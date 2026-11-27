package diffreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

func publishSource(t *testing.T, spec FixtureSpec) *Source {
	t.Helper()
	if spec.Dir == "" {
		spec.Dir = t.TempDir()
	}
	path, _, err := PublishFixture(spec)
	if err != nil {
		t.Fatalf("PublishFixture: %v", err)
	}
	s, err := LoadParcel(path)
	if err != nil {
		t.Fatalf("LoadParcel %s: %v", path, err)
	}
	return s
}

func baseSpec(dir string) FixtureSpec {
	return FixtureSpec{Dir: dir, Capture: "fix", Rows: 2000, FPS: 24, Start: 0, End: 3}
}

func frameMark(rep *Report, frame int) *FrameMark {
	for i := range rep.Frames {
		if rep.Frames[i].Frame == frame {
			return &rep.Frames[i]
		}
	}
	return nil
}

// Identical same-grid parcels produce no review regions.
func TestIdenticalParcelsNoRegions(t *testing.T) {
	a := publishSource(t, baseSpec(t.TempDir()))
	b := publishSource(t, baseSpec(t.TempDir()))
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Regions) != 0 {
		t.Fatalf("expected no regions, got %d", len(rep.Regions))
	}
	for _, f := range rep.Frames {
		if f.Mark != MarkSame {
			t.Fatalf("frame %d mark %s, want same", f.Frame, f.Mark)
		}
	}
	if rep.Summary.FramesByMark[MarkSame] != 3 {
		t.Fatalf("same frames = %d, want 3", rep.Summary.FramesByMark[MarkSame])
	}
}

// Same grid + different PCM is exact, conclusive content change.
func TestSameGridContentChange(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	sA := baseSpec(dirA)
	sA.Tone = toneAt(24, 2000, toneSpec{440, 12000})
	a := publishSource(t, sA)
	sB := baseSpec(dirB)
	sB.Tone = toneAt(24, 2000, toneSpec{440, 2000})
	b := publishSource(t, sB)

	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f1 := frameMark(rep, 1)
	if f1 == nil || f1.Mark != MarkContent {
		t.Fatalf("frame1 = %v, want content_change", f1)
	}
	if len(f1.Acoustic) == 0 || f1.Acoustic[0].Basis != "sample_exact" {
		t.Fatalf("expected sample_exact acoustic evidence, got %+v", f1.Acoustic)
	}
	if f1.Acoustic[0].ChangedSamples[0] == 0 {
		t.Fatal("expected changed samples on left lane")
	}
	if len(rep.Regions) == 0 || rep.Regions[0].Mark != MarkContent {
		t.Fatalf("expected content region, got %+v", rep.Regions)
	}
}

// Coverage partition difference is a confirmed readability change and must not
// be treated as an acoustic difference through the gap zeros.
func TestCoverageChangeReadabilityMark(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	sA := baseSpec(dirA)
	sA.Tone = toneAt(24, 2000, toneSpec{330, 9000})
	a := publishSource(t, sA)

	sB := baseSpec(dirB)
	sB.Coverage = []register.Run{
		{Start: 0, End: 2000, Status: register.Readable},
		{Start: 2000, End: 4000, Status: register.Missing},
		{Start: 4000, End: 6000, Status: register.Readable},
	}
	sB.Tone = toneAt(24, 2000, toneSpec{330, 9000})
	b := publishSource(t, sB)

	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f1 := frameMark(rep, 1)
	if f1 == nil || f1.Mark != MarkReadability {
		t.Fatalf("frame1 = %+v, want readability_change", f1)
	}
	if len(f1.CoverageChanges) != 1 {
		t.Fatalf("coverage evidence = %d, want 1", len(f1.CoverageChanges))
	}
	c := f1.CoverageChanges[0]
	if c.StatusA != register.Readable || c.StatusB != register.Missing || !c.AudibleStateChanged {
		t.Fatalf("bad coverage evidence: %+v", c)
	}
	if len(f1.Acoustic) != 0 {
		t.Fatalf("gap zeros must not become acoustic evidence, got %+v", f1.Acoustic)
	}
}

// Sub-frame coverage boundaries survive with fractional frame coordinates.
func TestSubFrameCoverageBoundary(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	sA := baseSpec(dirA)
	a := publishSource(t, sA)
	sB := baseSpec(dirB)
	sB.Coverage = []register.Run{
		{Start: 0, End: 2100, Status: register.Readable},
		{Start: 2100, End: 3900, Status: register.Missing},
		{Start: 3900, End: 6000, Status: register.Readable},
	}
	b := publishSource(t, sB)
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f1 := frameMark(rep, 1)
	if f1 == nil || f1.Mark != MarkReadability || len(f1.CoverageChanges) != 1 {
		t.Fatalf("frame1 = %+v", f1)
	}
	c := f1.CoverageChanges[0]
	if c.Span.FrameStart < 1.049 || c.Span.FrameStart > 1.051 || c.Span.FrameEnd < 1.949 || c.Span.FrameEnd > 1.951 {
		t.Fatalf("sub-frame span wrong: %+v", c.Span)
	}
}

// Different grids + dramatic acoustic difference is still confirmed content.
func TestCrossGridStrongContent(t *testing.T) {
	a := publishSource(t, FixtureSpec{
		Dir: t.TempDir(), Capture: "fix", Rows: 2000, FPS: 24, Start: 0, End: 2,
		Tone: func(absRow int) (int16, int16) {
			if absRow >= 2000 {
				return toneAt(24, 2000, toneSpec{523, 14000})(absRow)
			}
			return toneAt(24, 2000, toneSpec{440, 9000})(absRow)
		},
	})
	b := publishSource(t, FixtureSpec{
		Dir: t.TempDir(), Capture: "fix2", Rows: 1920, FPS: 24, Start: 0, End: 2,
		Tone: func(absRow int) (int16, int16) {
			if absRow >= 1920 {
				return 0, 0 // readable silence on frame 1
			}
			return toneAt(24, 1920, toneSpec{440, 9000})(absRow)
		},
	})
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f1 := frameMark(rep, 1)
	if f1 == nil || f1.Mark != MarkContent {
		t.Fatalf("frame1 = %+v, want content_change", f1)
	}
	if f1.Acoustic[0].Basis != "resampled_continuous_time" || !f1.Acoustic[0].EvidenceSufficient {
		t.Fatalf("bad acoustic evidence: %+v", f1.Acoustic[0])
	}
}

// Different grids + borderline metrics => unaligned with both parcel cues.
func TestCrossGridInconclusiveUnaligned(t *testing.T) {
	a := publishSource(t, FixtureSpec{
		Dir: t.TempDir(), Capture: "fix", Rows: 2000, FPS: 24, Start: 4, End: 5,
		Tone: toneAt(24, 2000, toneSpec{392, 8000}),
	})
	b := publishSource(t, FixtureSpec{
		Dir: t.TempDir(), Capture: "fix2", Rows: 1920, FPS: 24, Start: 4, End: 5,
		Tone: toneAt(24, 1920, toneSpec{392, 6500}),
	})
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f4 := frameMark(rep, 4)
	if f4 == nil || f4.Mark != MarkUnaligned {
		t.Fatalf("frame4 = %+v, want unaligned", f4)
	}
	if len(f4.Unaligned) == 0 || f4.Unaligned[0].Cues.A.ParcelID == f4.Unaligned[0].Cues.B.ParcelID {
		t.Fatalf("unaligned note must carry distinct dual cues: %+v", f4.Unaligned)
	}
	if f4.Cues.A.SampleRate == f4.Cues.B.SampleRate {
		// rates are 48000 vs 46080
		t.Fatalf("cue rates should differ across grids: %d vs %d", f4.Cues.A.SampleRate, f4.Cues.B.SampleRate)
	}
	if len(rep.Regions) != 1 || rep.Regions[0].Mark != MarkUnaligned {
		t.Fatalf("regions = %+v", rep.Regions)
	}
	r := rep.Regions[0]
	if r.Cues.A.ParcelID == "" || r.Cues.B.ParcelID == "" ||
		r.Cues.A.WAVByteOffset != 44+4*r.Cues.A.PCMFrame ||
		r.Cues.B.WAVByteOffset != 44+4*r.Cues.B.PCMFrame {
		t.Fatalf("bad region cue offsets: %+v", r.Cues)
	}
}

// Partially overlapping windows report overlap plus one-sided exclusives.
func TestPartialOverlapExclusives(t *testing.T) {
	tone := toneAt(24, 2000, toneSpec{440, 9000})
	a := publishSource(t, FixtureSpec{Dir: t.TempDir(), Capture: "f", Rows: 2000, FPS: 24, Start: 1, End: 4, Tone: tone})
	b := publishSource(t, FixtureSpec{Dir: t.TempDir(), Capture: "f", Rows: 2000, FPS: 24, Start: 2, End: 5, Tone: tone})
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overlap == nil || rep.Overlap.Start != 2 || rep.Overlap.End != 4 {
		t.Fatalf("overlap = %+v, want [2,4)", rep.Overlap)
	}
	if rep.Summary.ExclusiveFramesA != 1 || rep.Summary.ExclusiveFramesB != 1 {
		t.Fatalf("exclusives A=%d B=%d, want 1/1", rep.Summary.ExclusiveFramesA, rep.Summary.ExclusiveFramesB)
	}
	sides := map[string][2]int{}
	for _, ex := range rep.Exclusive {
		sides[ex.Side] = [2]int{ex.FrameStart, ex.FrameEnd}
		if ex.Cue.ParcelID == "" {
			t.Fatal("exclusive span missing cue")
		}
	}
	if sides["a"] != [2]int{1, 2} || sides["b"] != [2]int{4, 5} {
		t.Fatalf("exclusive ranges = %+v", sides)
	}
}

// Disjoint windows are a valid report with no frame marks.
func TestDisjointWindows(t *testing.T) {
	a := publishSource(t, FixtureSpec{Dir: t.TempDir(), Capture: "f", Rows: 2000, FPS: 24, Start: 0, End: 2})
	b := publishSource(t, FixtureSpec{Dir: t.TempDir(), Capture: "f", Rows: 2000, FPS: 24, Start: 3, End: 5})
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overlap != nil || len(rep.Frames) != 0 {
		t.Fatalf("expected no overlap/frames, got %+v", rep.Overlap)
	}
	if rep.Summary.ExclusiveFramesA != 2 || rep.Summary.ExclusiveFramesB != 2 {
		t.Fatalf("exclusive = %+v", rep.Summary)
	}
}

// A tampered parcel must fail verification rather than be compared.
func TestTamperedParcelRejected(t *testing.T) {
	path, _, err := PublishFixture(baseSpec(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xFF
	if _, err := LoadParcelBytes(path, data); err == nil {
		t.Fatal("expected verification error for tampered parcel")
	}
}

// IDs resolve through the parcel store layout <dir>/<id>.zip.
func TestResolveParcelRefByID(t *testing.T) {
	dir := t.TempDir()
	path, receipt, err := PublishFixture(baseSpec(dir))
	if err != nil {
		t.Fatal(err)
	}
	gotPath, data, err := ResolveParcelRef(receipt.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path || len(data) == 0 {
		t.Fatalf("resolved %q len %d", gotPath, len(data))
	}
	if _, _, err := ResolveParcelRef("not-a-path.zip", ""); err == nil {
		t.Fatal("missing path should error")
	}
}

// End-to-end demo fixtures produce one region per headline mark and a full
// artifact directory including JSON, Markdown, HTML, WAV and ZIP.
func TestDemoFixturesAndWriteArtifact(t *testing.T) {
	root := t.TempDir()
	pdir := filepath.Join(root, "parcels")
	rdir := filepath.Join(root, "review")
	aPath, bPath, err := BuildDemoParcels(pdir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadParcel(aPath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadParcel(bPath)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Compare(a, b, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{MarkContent: false, MarkReadability: false, MarkUnaligned: false}
	for _, r := range rep.Regions {
		if _, ok := want[r.Mark]; ok {
			want[r.Mark] = true
		}
	}
	for mark, found := range want {
		if !found {
			t.Fatalf("demo missing region mark %s; summary=%+v", mark, rep.Summary)
		}
	}
	files, err := WriteArtifact(rep, rdir)
	if err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	for _, p := range []string{files.ReportJSON, files.Markdown, files.HTML, files.MasterWAV, files.BundleZip} {
		if p == "" {
			t.Fatal("empty artifact path")
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("artifact missing %s: %v", p, err)
		}
	}
	raw, err := os.ReadFile(files.ReportJSON)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("report JSON invalid: %v", err)
	}
	if decoded.Schema != Schema || len(decoded.Regions) != len(rep.Regions) {
		t.Fatalf("decoded report mismatch: schema=%s regions=%d", decoded.Schema, len(decoded.Regions))
	}
	if files.ZipSHA256 == "" {
		t.Fatal("missing bundle sha")
	}

	// Cue coordinates must be internally consistent.
	for _, f := range decoded.Frames {
		for _, c := range []Cue{f.Cues.A, f.Cues.B} {
			if c.ParcelID == "" {
				continue
			}
			if c.PictureRow != c.Frame*c.RowsPerFrame+c.RowInFrame {
				t.Fatalf("cue row inconsistency: %+v", c)
			}
			if c.WAVByteOffset != 44+4*c.PCMFrame {
				t.Fatalf("cue byte offset inconsistency: %+v", c)
			}
		}
	}
}
