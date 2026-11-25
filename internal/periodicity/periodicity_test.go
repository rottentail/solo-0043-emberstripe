package periodicity

import (
	"math"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const (
	testRows = 1000
	testFPS  = 24
)

func syntheticReel(t *testing.T, edgeHz, laneHz float64, edgeAmplitude float64, missingFrame int) *capture.Reel {
	t.Helper()
	header := capture.Header{
		Name: "periodic-test", Width: 40, Rows: testRows, FPS: testFPS, Frames: 6, Lead: 0,
		Picture: capture.Span{X0: 0, X1: 10},
		Lanes:   [2]capture.Span{{X0: 16, X1: 22}, {X0: 26, X1: 32}},
		Profiles: []capture.Profile{{
			ID:    "bench",
			Dark:  capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: 10}},
			Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 10, X1: 20}},
		}},
	}
	reel := &capture.Reel{Header: header, Sheets: map[int]capture.Sheet{}}
	rate := float64(register.Rate(header))
	for frame := 0; frame < header.Frames; frame++ {
		if frame == missingFrame {
			continue
		}
		bitmap := newGraySheet(header.Width, header.Rows)
		for row := 0; row < header.Rows; row++ {
			absolute := frame*header.Rows + row
			edgeWave := 30000 + edgeAmplitude*math.Sin(2*math.Pi*edgeHz*float64(absolute)/rate)
			leftWave := 30000 + 4000*math.Sin(2*math.Pi*laneHz*float64(absolute)/rate)
			for x := 0; x < header.Width; x++ {
				intensity := uint16(28000)
				switch {
				case x >= header.Picture.X1 && x < header.Lanes[0].X0:
					intensity = uint16(math.Max(0, math.Min(65535, edgeWave)))
				case x >= header.Lanes[0].X0 && x < header.Lanes[0].X1:
					intensity = uint16(math.Max(0, math.Min(65535, leftWave)))
				case x >= header.Lanes[1].X0 && x < header.Lanes[1].X1:
					intensity = uint16(math.Max(0, math.Min(65535, leftWave)))
				}
				bitmap.set(x, row, intensity)
			}
		}
		reel.Sheets[frame] = capture.Sheet{Bitmap: bitmap, Depth: 16, Digest: "sheet"}
	}
	return reel
}

func TestVerifySharedPeriodSupported(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 5000, -1)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	report, err := Verify(reel, register.Window{Start: 0, End: 6}, band)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Overall != VerdictSupported {
		t.Fatalf("overall = %q, want %q; pairs: %+v", report.Overall, VerdictSupported, report.Pairs)
	}
	for _, pair := range report.Pairs {
		if pair.Verdict != VerdictSupported {
			t.Fatalf("pair %s = %q (%s)", pair.Lane, pair.Verdict, pair.Reason)
		}
		if math.Abs(pair.EdgeFrequency-120) > 6 {
			t.Fatalf("edge frequency %.2f not near 120 Hz", pair.EdgeFrequency)
		}
	}
}

func TestVerifyCloseFrequenciesStayDistinct(t *testing.T) {
	// The lane carries a separate tone: with no component at the edge
	// frequency at all, the edge mechanism must be dismissed rather than
	// having the two programme/edge tones merged into one cause.
	reel := syntheticReel(t, 120, 100, 5000, -1)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	report, err := Verify(reel, register.Window{Start: 0, End: 6}, band)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Overall != VerdictDismissed {
		t.Fatalf("overall = %q, want %q", report.Overall, VerdictDismissed)
	}
	for _, pair := range report.Pairs {
		if pair.Verdict != VerdictDismissed {
			t.Fatalf("pair %s = %q, want dismissed: %s", pair.Lane, pair.Verdict, pair.Reason)
		}
	}
}

// TestVerifyNeighboringBinsNotMerged uses two genuinely close tones that land
// on neighboring spectrum bins. Both are real peaks, but the window cannot
// resolve them, so the verdict must stay inconclusive rather than merge them.
func TestVerifyNeighboringBinsNotMerged(t *testing.T) {
	// At 24 kHz over 6000 rows the bin width is 4 Hz; 120 Hz versus 116 Hz
	// sit one bin apart and must remain an unresolvable, inconclusive pair.
	reel := syntheticReel(t, 120, 116, 5000, -1)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	report, err := Verify(reel, register.Window{Start: 0, End: 6}, band)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, pair := range report.Pairs {
		if pair.Verdict != VerdictInconclusive {
			t.Fatalf("neighboring-bin pair %s = %q, want inconclusive: %s", pair.Lane, pair.Verdict, pair.Reason)
		}
		if pair.FrequencyGapHz > pair.ResolutionLimit {
			t.Fatalf("gap %.3f Hz should stay within resolution limit %.3f Hz", pair.FrequencyGapHz, pair.ResolutionLimit)
		}
	}
}

// TestLaneProgrammeToneDoesNotMaskEdgeAnswer proves a loud programme tone at
// another frequency cannot hide the transport response aligned with the edge.
func TestLaneProgrammeToneDoesNotMaskEdgeAnswer(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 5000, -1)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	report, err := Verify(reel, register.Window{Start: 0, End: 6}, band)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, spectrum := range report.Spectra {
		if spectrum.Kind == Edge {
			continue
		}
		if spectrum.AlignedPeak == nil {
			t.Fatalf("%s must retain an edge-aligned peak", spectrum.Kind)
		}
		if math.Abs(spectrum.AlignedPeak.FrequencyHz-120) > 10 {
			t.Fatalf("%s aligned peak %.2f Hz not near edge 120 Hz", spectrum.Kind, spectrum.AlignedPeak.FrequencyHz)
		}
	}
}

func TestVerifySparseWindowInconclusive(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 5000, -1)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	// A two-row window contains far fewer than three 120 Hz cycles at 24 kHz.
	tiny := register.Window{Start: 0, End: 1}
	values, _, err := BuildSeries(reel, tiny, Edge, band)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	rate := register.Rate(reel.Header)
	shortSegments := []Segment{{StartRow: 0, EndRow: 40, Frame: 0, Row: 0, Samples: 40}}
	shortValues := values[:40]
	edge := Analyze(Edge, shortValues, shortSegments, rate, 0)
	lane := Analyze(Left, shortValues, shortSegments, rate, 0)
	lane.AlignWith(&edge)
	pair := comparePair(edge, lane)
	if pair.Verdict != VerdictInconclusive || pair.EdgeCycles >= minimumCycles {
		t.Fatalf("sparse window must stay inconclusive, got %+v", pair)
	}
}

func TestVerifyMissingMiddlePagePreserved(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 5000, 3)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	report, err := Verify(reel, register.Window{Start: 0, End: 6}, band)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Missing != testRows {
		t.Fatalf("missing rows = %d, want %d", report.Missing, testRows)
	}
	found := false
	for _, block := range report.Unreadable {
		if block.Status == register.Missing && block.Frame == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing middle page position not retained: %+v", report.Unreadable)
	}
	if report.Overall != VerdictSupported {
		t.Fatalf("remaining pages still support the shared period, got %q", report.Overall)
	}
}

func TestWeakEdgePeakInconclusive(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 40, -1)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	report, err := Verify(reel, register.Window{Start: 0, End: 6}, band)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, pair := range report.Pairs {
		if pair.Verdict != VerdictInconclusive {
			t.Fatalf("weak edge evidence must stay inconclusive, got %q", pair.Verdict)
		}
	}
}

func TestValidAndDefaultBand(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 5000, -1)
	band, err := DefaultBand(reel)
	// The right-side gap (32..40) is wider than the left gap (10..16).
	if err != nil || band.X0 != 32 || band.X1 != 40 {
		t.Fatalf("default band = %+v, %v", band, err)
	}
	if !ValidBand(reel, capture.Span{X0: 10, X1: 16}) {
		t.Fatal("left-side gap must also be a valid reference band")
	}
	for _, invalid := range []capture.Span{
		{X0: 0, X1: 10},  // picture
		{X0: 16, X1: 22}, // left lane
		{X0: 20, X1: 28}, // crosses a lane
		{X0: 35, X1: 35}, // empty
		{X0: -1, X1: 5},  // outside strip
		{X0: 30, X1: 45}, // past strip width
	} {
		if ValidBand(reel, invalid) {
			t.Fatalf("band %+v must be rejected", invalid)
		}
	}
}

func TestAudioKeepsWindowClockAndSilencesGaps(t *testing.T) {
	reel := syntheticReel(t, 120, 120, 5000, 2)
	band := capture.Span{X0: reel.Picture.X1, X1: reel.Lanes[0].X0}
	rate, samples, err := Audio(reel, register.Window{Start: 0, End: 4}, Edge, band)
	if err != nil {
		t.Fatalf("audio: %v", err)
	}
	if rate != testRows*testFPS {
		t.Fatalf("rate = %d", rate)
	}
	wantFrames := 4 * testRows
	if len(samples) != 2*wantFrames {
		t.Fatalf("sample count = %d, want %d", len(samples), 2*wantFrames)
	}
	// Frame 2 is the missing sheet: both stereo slots of its thousand rows
	// must stay silent while readable frames carry actual modulation.
	for pictureRow := 2 * testRows; pictureRow < 3*testRows; pictureRow++ {
		for _, channel := range []int{0, 1} {
			if samples[2*pictureRow+channel] != 0 {
				t.Fatalf("missing sheet must be silent at row %d, got %d", pictureRow, samples[2*pictureRow+channel])
			}
		}
	}
	nonZero := 0
	for pictureRow := 0; pictureRow < 2*testRows; pictureRow++ {
		if samples[2*pictureRow] != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("readable edge material must produce audible samples")
	}
	_, comparison, err := ComparisonAudio(reel, register.Window{Start: 0, End: 4}, Left, band)
	if err != nil || len(comparison) != len(samples) {
		t.Fatalf("comparison audio: %v len=%d", err, len(comparison))
	}
}
