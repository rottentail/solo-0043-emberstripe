package lightbench

import (
	"net/http"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
)

// profileFingerprint identifies a calibration independently of its current id.
// It binds the reference rectangles to the exact reference sheet bytes, so a
// rename keeps the fingerprint while a same-name reference swap changes it.
type profileFingerprint struct {
	DarkFrame   int    `json:"dark_frame"`
	ClearFrame  int    `json:"clear_frame"`
	DarkRows    [2]int `json:"dark_rows"`
	ClearRows   [2]int `json:"clear_rows"`
	DarkDigest  string `json:"dark_digest,omitempty"`
	ClearDigest string `json:"clear_digest,omitempty"`
}

type profileState struct {
	ID          string             `json:"id"`
	Usable      bool               `json:"usable"`
	Reason      string             `json:"reason,omitempty"`
	Fingerprint profileFingerprint `json:"fingerprint"`
	Gauge       *gauge.Gauge       `json:"gauge,omitempty"`
}

type profilesReply struct {
	ReelDigest string         `json:"reel_digest"`
	Profiles   []profileState `json:"profiles"`
}

func fingerprintOf(reel *capture.Reel, profile capture.Profile) profileFingerprint {
	fp := profileFingerprint{
		DarkFrame:  profile.Dark.Frame,
		ClearFrame: profile.Clear.Frame,
		DarkRows:   [2]int{profile.Dark.Rows.X0, profile.Dark.Rows.X1},
		ClearRows:  [2]int{profile.Clear.Rows.X0, profile.Clear.Rows.X1},
	}
	if sheet, ok := reel.Sheets[profile.Dark.Frame]; ok {
		fp.DarkDigest = sheet.Digest
	}
	if sheet, ok := reel.Sheets[profile.Clear.Frame]; ok {
		fp.ClearDigest = sheet.Digest
	}
	return fp
}

func unusableReason(reel *capture.Reel, profile capture.Profile) string {
	if _, ok := reel.Sheets[profile.Dark.Frame]; !ok {
		return "dark reference sheet missing"
	}
	if _, ok := reel.Sheets[profile.Clear.Frame]; !ok {
		return "clear reference sheet missing"
	}
	return "insufficient lane contrast"
}

// profiles reports each calibration's stable identity and measured endpoints so
// the browser can reconcile a saved pairing after rescans or renames.
func (b *bench) profiles(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	reply := profilesReply{ReelDigest: reel.Digest}
	for _, profile := range reel.Profiles {
		state := profileState{ID: profile.ID, Fingerprint: fingerprintOf(reel, profile)}
		if g, err := gauge.Measure(reel, profile.ID); err == nil {
			state.Usable = true
			state.Gauge = &g
		} else {
			state.Reason = unusableReason(reel, profile)
		}
		reply.Profiles = append(reply.Profiles, state)
	}
	writeJSON(w, http.StatusOK, reply)
}
