package auditcard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func validCard() Card {
	return Card{
		Version:      Version,
		Reel:         "cinder-a",
		SourceDigest: digestA,
		Profile:      "bench",
		Window:       register.Window{Start: 1, End: 4},
		Cursor:       Cursor{Frame: 2, Row: 100},
		ParcelID:     digestA,
		AudioDigest:  digestB,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

type fakeEvidence struct {
	reelPresent       bool
	reelName          string
	reelDigest        string
	parcelVerified    bool
	parcelCapture     string
	parcelSource      string
	parcelAudioDigest string
}

func (f fakeEvidence) ReelPresent() bool          { return f.reelPresent }
func (f fakeEvidence) ReelName() string           { return f.reelName }
func (f fakeEvidence) ReelDigest() string         { return f.reelDigest }
func (f fakeEvidence) ParcelVerified() bool       { return f.parcelVerified }
func (f fakeEvidence) ParcelCapture() string      { return f.parcelCapture }
func (f fakeEvidence) ParcelSourceDigest() string { return f.parcelSource }
func (f fakeEvidence) ParcelAudioDigest() string  { return f.parcelAudioDigest }

func matchingParcel(f *fakeEvidence) {
	f.parcelVerified = true
	f.parcelCapture = "cinder-a"
	f.parcelSource = digestA
	f.parcelAudioDigest = digestB
}

func TestAssessThreeStates(t *testing.T) {
	card := validCard()

	// Same-name reel whose source digest matches: original pixels stay provable.
	ready := Assess(card, fakeEvidence{reelPresent: true, reelName: "cinder-a", reelDigest: digestA})
	if ready.Status != PixelReady || !ready.ReelDigestMatch {
		t.Fatalf("expected pixel_ready, got %+v", ready)
	}

	// Same reel name, rescanned into different bytes: name and window length
	// match but the digest does not, so only the frozen parcel can be trusted.
	rescanned := fakeEvidence{reelPresent: true, reelName: "cinder-a", reelDigest: digestB}
	matchingParcel(&rescanned)
	frozen := Assess(card, rescanned)
	if frozen.Status != FrozenAudio || frozen.ReelDigestMatch || !frozen.ParcelMatch {
		t.Fatalf("expected frozen_audio for rescanned reel, got %+v", frozen)
	}

	// Reel gone entirely, only the verified frozen package remains.
	onlyParcel := fakeEvidence{}
	matchingParcel(&onlyParcel)
	if got := Assess(card, onlyParcel); got.Status != FrozenAudio || got.ReelPresent {
		t.Fatalf("expected frozen_audio without reel, got %+v", got)
	}

	// Reel present with a different digest and the parcel damaged: no proof.
	noproof := Assess(card, fakeEvidence{reelPresent: true, reelName: "cinder-a", reelDigest: digestB})
	if noproof.Status != Unverified || noproof.ParcelMatch || noproof.ReelDigestMatch {
		t.Fatalf("expected unverified, got %+v", noproof)
	}

	// A parcel whose audio digest disagrees with the card cannot substantiate it.
	badAudio := fakeEvidence{parcelVerified: true, parcelCapture: "cinder-a", parcelSource: digestA, parcelAudioDigest: digestA}
	if got := Assess(card, badAudio); got.Status != Unverified {
		t.Fatalf("audio digest mismatch must be unverified, got %+v", got)
	}
}

func TestParseRejectsTamperedAndShapedCards(t *testing.T) {
	card := validCard()
	cases := map[string]func(*Card){
		"wrong version":        func(c *Card) { c.Version = 2 },
		"bad reel name":        func(c *Card) { c.Reel = "Cinder A" },
		"bad source digest":    func(c *Card) { c.SourceDigest = "short" },
		"window too long":      func(c *Card) { c.Window = register.Window{Start: 0, End: 49} },
		"cursor outside frame": func(c *Card) { c.Cursor.Frame = 6 },
		"cursor outside row":   func(c *Card) { c.Cursor.Row = 4096 },
		"bad timestamp":        func(c *Card) { c.CreatedAt = "yesterday" },
		"oversized note":       func(c *Card) { c.Note = strings.Repeat("字", MaxNoteRunes+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			broken := card
			mutate(&broken)
			if _, err := Parse(mustJSON(t, broken)); err == nil {
				t.Fatalf("%s: expected validation error", name)
			}
		})
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	raw := append(mustJSON(t, validCard())[:len(mustJSON(t, validCard()))-1], []byte(`,"claimed_pixels":true}`)...)
	if _, err := Parse(raw); err == nil {
		t.Fatal("card with unknown field must be rejected")
	}
}

func mustJSON(t *testing.T, card Card) []byte {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return data
}
