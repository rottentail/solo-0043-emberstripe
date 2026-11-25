// Package auditcard carries a shift handoff listening card and decides, when
// the card is reopened, whether the current station can still prove the
// original pixels, only replay the frozen sound, or prove nothing at all.
//
// Provenance is established by content digests. A reel that merely shares the
// card's reel name and window length is never silently treated as the old
// evidence: the source digest must match before pixel inspection is offered.
package auditcard

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const (
	// Version is the only accepted card schema.
	Version = 1
	// MaxNoteRunes bounds the free-text handoff note.
	MaxNoteRunes = 500
	// MaxCardBytes bounds the card document accepted by the station.
	MaxCardBytes = 1 << 14

	// PixelReady means the currently registered reel shares the card's source
	// digest: original pixels can still be inspected.
	PixelReady = "pixel_ready"
	// FrozenAudio means no matching live reel exists, but the frozen parcel is
	// present and verified, so only the sound captured at freeze time can be
	// replayed.
	FrozenAudio = "frozen_audio"
	// Unverified means neither a matching reel nor a usable frozen parcel can
	// prove where the card's material came from.
	Unverified = "unverified"
)

var (
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Cursor is the picture frame row that was being listened to when the card
// was written. Frame is zero-based inside the capture, row is zero-based
// inside the frame.
type Cursor struct {
	Frame int `json:"frame"`
	Row   int `json:"row"`
}

// Card is the portable, machine-readable handoff document.
type Card struct {
	Version      int             `json:"version"`
	Reel         string          `json:"reel"`
	SourceDigest string          `json:"source_sha256"`
	Profile      string          `json:"profile"`
	Window       register.Window `json:"window"`
	Cursor       Cursor          `json:"cursor"`
	ParcelID     string          `json:"parcel_id"`
	AudioDigest  string          `json:"audio_sha256"`
	CreatedAt    string          `json:"created_at"`
	Note         string          `json:"note,omitempty"`
}

// New builds a validated card from a live reel, the applied window and the
// receipt of the parcel frozen for this handoff.
func New(reel *capture.Reel, w register.Window, frame, row int, receipt parcel.Receipt, note string) (Card, error) {
	if len([]rune(note)) > MaxNoteRunes {
		return Card{}, errors.New("handoff note is too long")
	}
	if register.Validate(reel.Header, w) != nil || frame < w.Start || frame >= w.End || row < 0 || row >= reel.Rows {
		return Card{}, errors.New("card cursor or window is outside the reel")
	}
	if receipt.Index.Source != reel.Digest || receipt.Index.Window != w || receipt.ID == "" || receipt.Index.Audio == "" {
		return Card{}, errors.New("frozen receipt does not match the reel and window")
	}
	card := Card{
		Version:      Version,
		Reel:         reel.Name,
		SourceDigest: reel.Digest,
		Profile:      receipt.Index.Gauge.Profile,
		Window:       w,
		Cursor:       Cursor{Frame: frame, Row: row},
		ParcelID:     receipt.ID,
		AudioDigest:  receipt.Index.Audio,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		Note:         note,
	}
	if err := card.validate(); err != nil {
		return Card{}, err
	}
	return card, nil
}

// Parse decodes a card document and rejects unknown fields or any value that
// cannot satisfy the card contract.
func Parse(data []byte) (Card, error) {
	var card Card
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&card); err != nil {
		return Card{}, err
	}
	if err := card.validate(); err != nil {
		return Card{}, err
	}
	return card, nil
}

func (c Card) validate() error {
	if c.Version != Version {
		return errors.New("unsupported audit card version")
	}
	if !namePattern.MatchString(c.Reel) || c.Profile == "" || len(c.Profile) > 48 {
		return errors.New("invalid reel or calibration identity")
	}
	if !digestPattern.MatchString(c.SourceDigest) || !digestPattern.MatchString(c.ParcelID) || !digestPattern.MatchString(c.AudioDigest) {
		return errors.New("card must carry source, parcel and audio digests")
	}
	w := c.Window
	if w.Start < 0 || w.End <= w.Start || w.End > 256 || w.End-w.Start > register.MaxFrames {
		return errors.New("card window is outside capture limits")
	}
	if c.Cursor.Frame < w.Start || c.Cursor.Frame >= w.End || c.Cursor.Row < 0 || c.Cursor.Row >= 4096 {
		return errors.New("card cursor is outside its frame window")
	}
	if _, err := time.Parse(time.RFC3339, c.CreatedAt); err != nil {
		return errors.New("card creation time is not RFC3339")
	}
	if len([]rune(c.Note)) > MaxNoteRunes {
		return errors.New("handoff note is too long")
	}
	return nil
}

// Evidence is the station-side material that can substantiate a card.
// Implementations report the currently registered reel and the frozen parcel
// addressed by the card, after integrity verification.
type Evidence interface {
	ReelPresent() bool
	ReelName() string
	ReelDigest() string
	ParcelVerified() bool
	ParcelCapture() string
	ParcelSourceDigest() string
	ParcelAudioDigest() string
}

// Assessment explains exactly which kind of follow-up the card supports.
type Assessment struct {
	Status          string `json:"status"`
	Reason          string `json:"reason"`
	Card            Card   `json:"card"`
	ReelPresent     bool   `json:"reel_present"`
	ReelDigestMatch bool   `json:"reel_digest_match"`
	ParcelVerified  bool   `json:"parcel_verified"`
	ParcelMatch     bool   `json:"parcel_match"`
}

// Assess compares the card against current station material. Digest equality
// is required for every positive claim; matching names or windows alone never
// upgrade the result.
func Assess(card Card, evidence Evidence) Assessment {
	result := Assessment{Card: card, ReelPresent: evidence.ReelPresent(), ParcelVerified: evidence.ParcelVerified()}
	result.ReelDigestMatch = result.ReelPresent &&
		evidence.ReelName() == card.Reel && evidence.ReelDigest() == card.SourceDigest
	result.ParcelMatch = result.ParcelVerified &&
		evidence.ParcelCapture() == card.Reel &&
		evidence.ParcelSourceDigest() == card.SourceDigest &&
		evidence.ParcelAudioDigest() == card.AudioDigest

	switch {
	case result.ReelDigestMatch:
		result.Status = PixelReady
		result.Reason = "当前登记的同名卷与卡内源摘要一致，仍是当时扫描的同一份像素，可继续查原像素、覆盖和标定。"
	case result.ParcelMatch:
		result.Status = FrozenAudio
		result.Reason = "同名卷已重新扫描或源摘要不一致，新条带不会被当作旧证据；冻结包通过完整性校验，仅可回听当时冻结的声音。"
	default:
		result.Status = Unverified
		switch {
		case result.ReelPresent:
			result.Reason = "登记卷与卡内卷名或源摘要不一致，且冻结包无法对应，手头材料无法证明这张卡的出处。"
		case !result.ParcelVerified:
			result.Reason = "工位没有登记该卷，卡内冻结包缺失或未通过完整性校验，手头材料无法证明这张卡的出处。"
		default:
			result.Reason = "冻结包虽可读取，但与卡内记录的卷、源摘要或音频摘要不一致，手头材料无法证明这张卡的出处。"
		}
	}
	return result
}
