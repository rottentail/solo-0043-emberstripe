package lineaudit

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

func auditTestSignal(lane, row int) float64 {
	freq := []float64{220, 415}[lane]
	t := float64(row) / 48000
	env := 0.72 + 0.28*math.Sin(2*math.Pi*float64(row)/1750+float64(lane))
	v := env * math.Sin(2*math.Pi*freq*t+0.3*float64(lane))
	v += 0.22 * math.Sin(2*math.Pi*float64(row)/930+1.7*float64(lane))
	return math.Tanh(v * 1.4)
}

func buildAuditTestReel(t *testing.T, name string, rows, frames int, value func(lane, row int) float64, omit map[int]bool) *capture.Reel {
	t.Helper()
	h := capture.Header{
		Name: name, Width: 112, Rows: rows, FPS: 24, Frames: frames, Lead: 120,
		Picture: capture.Span{X0: 0, X1: 64},
		Lanes:   [2]capture.Span{{X0: 72, X1: 88}, {X0: 92, X1: 108}},
		Profiles: []capture.Profile{{
			ID:    "bench",
			Dark:  capture.Patch{Frame: 0, Rows: capture.Span{X0: 0, X1: 36}},
			Clear: capture.Patch{Frame: 0, Rows: capture.Span{X0: 36, X1: 72}},
		}},
	}
	r := &capture.Reel{Header: h, Sheets: map[int]capture.Sheet{}}
	dark := []float64{6000, 8200}
	clear := []float64{55000, 50200}
	for frame := 0; frame < frames; frame++ {
		if omit[frame] {
			continue
		}
		img := image.NewGray16(image.Rect(0, 0, h.Width, h.Rows))
		for y := 0; y < rows; y++ {
			global := frame*rows + y
			for x := 0; x < h.Width; x++ {
				val := uint16(20000)
				for lane, band := range h.Lanes {
					if x < band.X0 || x >= band.X1 {
						continue
					}
					g, c := dark[lane], clear[lane]
					val = uint16(g)
					if y < 36 {
						val = uint16(g)
					} else if y < 72 {
						val = uint16(c)
					} else {
						v := value(lane, global)
						mark := int(math.Round(9 + 5*v))
						if x-band.X0 < mark {
							val = uint16(c)
						}
					}
				}
				img.SetGray16(x, y, color.Gray16{Y: val})
			}
		}
		r.Sheets[frame] = capture.Sheet{Bitmap: img, Depth: 16}
	}
	return r
}

func findSuspect(rep *Report, kind string, delta int) *Finding {
	for i := range rep.Suspects {
		f := &rep.Suspects[i]
		if f.Kind == kind && absInt(f.DeltaRows-delta) <= max(3, absInt(delta)/5) {
			return f
		}
	}
	return nil
}

func intervalNear(i Interval, lo, hi, tolerance int) bool {
	return overlapRows(i, Interval{Start: lo - tolerance, End: hi + tolerance}) > 0
}

func TestCompareDetectsSkippedRows(t *testing.T) {
	const rows, frames = 1000, 6
	const p, k = 3200, 28
	a := buildAuditTestReel(t, "trial-a", rows, frames, auditTestSignal, nil)
	b := buildAuditTestReel(t, "trial-b", rows, frames, func(lane, j int) float64 {
		if j >= p {
			j += k
		}
		return auditTestSignal(lane, j)
	}, nil)
	rep, err := Compare(a, b, Request{})
	if err != nil {
		t.Fatal(err)
	}
	f := findSuspect(rep, KindDeletion, -k)
	if f == nil {
		t.Fatalf("expected deletion Δ=%d, got %+v", -k, rep.Suspects)
	}
	if f.Confidence < 0.7 {
		t.Fatalf("confidence too low: %.2f", f.Confidence)
	}
	check := f.AInterval
	if f.FocusA != nil {
		check = *f.FocusA
	}
	if !intervalNear(check, p, p+k, 48) {
		t.Fatalf("deletion focus %+v is not near %d..%d (broad %+v)", check, p, p+k, f.AInterval)
	}
	if len(f.Evidence) == 0 || f.LinksA.AuditionURL == "" || f.AddressA == nil || !f.AddressA.PictureValid {
		t.Fatalf("finding lacks evidence/links/address: %+v", f)
	}
}

func TestCompareDetectsRepeatedRows(t *testing.T) {
	const rows, frames = 1000, 6
	const p, k = 4200, 34
	a := buildAuditTestReel(t, "trial-a", rows, frames, auditTestSignal, nil)
	b := buildAuditTestReel(t, "trial-b", rows, frames, func(lane, j int) float64 {
		switch {
		case j < p:
			return auditTestSignal(lane, j)
		case j < p+2*k:
			return auditTestSignal(lane, p+(j-p)%k)
		default:
			return auditTestSignal(lane, j-k)
		}
	}, nil)
	rep, err := Compare(a, b, Request{})
	if err != nil {
		t.Fatal(err)
	}
	f := findSuspect(rep, KindInsertion, k)
	if f == nil {
		t.Fatalf("expected insertion Δ=%d, got %+v", k, rep.Suspects)
	}
	check := f.AInterval
	if f.FocusA != nil {
		check = *f.FocusA
	}
	if !intervalNear(check, p, p+2*k, 70) {
		t.Fatalf("insertion focus %+v is not near repeated block %d..%d (broad %+v)", check, p, p+2*k, f.AInterval)
	}
}

func TestCompareReportsHeadOffsetAndMissingPage(t *testing.T) {
	const rows, frames = 1000, 6
	const head = 42
	a := buildAuditTestReel(t, "trial-a", rows, frames, auditTestSignal, nil)
	b := buildAuditTestReel(t, "trial-b", rows, frames, func(lane, j int) float64 {
		return auditTestSignal(lane, j-head)
	}, map[int]bool{3: true})
	rep, err := Compare(a, b, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if absInt(rep.GlobalShiftRows-head) > 8 {
		t.Fatalf("head shift = %d, want near %d", rep.GlobalShiftRows, head)
	}
	if len(rep.MissingPages) == 0 {
		t.Fatal("expected physical missing-page finding")
	}
	var hasHead bool
	for _, f := range rep.Findings {
		if f.Kind == KindHeadOffset {
			hasHead = true
		}
		if f.Kind != KindMissingSheet && overlapRows(f.AInterval, Interval{3 * rows, 4 * rows}) > 4 {
			t.Fatalf("line fault %s overlaps physical missing page", f.ID)
		}
	}
	if !hasHead {
		t.Fatal("expected head-offset conclusion")
	}
}

func TestCompareRejectsDifferentGeometry(t *testing.T) {
	a := buildAuditTestReel(t, "a", 1000, 4, auditTestSignal, nil)
	b := buildAuditTestReel(t, "b", 900, 4, auditTestSignal, nil)
	if _, err := Compare(a, b, Request{}); err == nil {
		t.Fatal("expected geometry mismatch error")
	}
}
