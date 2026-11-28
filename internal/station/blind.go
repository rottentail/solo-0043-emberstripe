package station

import (
	"errors"

	"github.com/rottentail/solo-0043-emberstripe/internal/blind"
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

// ErrCaptureGone means the catalog no longer holds the reel a sheet targets.
var ErrCaptureGone = errors.New("capture referenced by blind sheet is no longer registered")

// CreateBlind builds a sheet against the live reel snapshot and persists it.
// Rebuilding an identical request resolves to the same content-addressed id.
func (s *Station) CreateBlind(reel *capture.Reel, req blind.CreateRequest) (*blind.Sheet, error) {
	sheet, err := blind.Build(reel, req)
	if err != nil {
		return nil, err
	}
	if err := s.Blind.Save(sheet); err != nil {
		return nil, err
	}
	return sheet, nil
}

// BlindSheet loads a stored sheet regardless of which capture it belongs to.
func (s *Station) BlindSheet(id string) (*blind.Sheet, error) {
	return s.Blind.Load(id)
}

// BlindIDs lists every stored sheet id in stable order.
func (s *Station) BlindIDs() ([]string, error) {
	return s.Blind.List()
}

// RecordBlindChoice stores one reviewer answer and returns the updated sheet.
func (s *Station) RecordBlindChoice(id string, choice blind.Choice) (*blind.Sheet, error) {
	return s.Blind.RecordAnswer(id, choice)
}

// sheetReel resolves the live reel a stored sheet was built for.
func (s *Station) sheetReel(sheet *blind.Sheet) (*capture.Reel, error) {
	reel, ok := s.Reel(sheet.Capture)
	if !ok {
		return nil, ErrCaptureGone
	}
	return reel, nil
}

// BlindOptionAudio renders the WAV behind one neutral option. The profile
// identity is never exposed; only the sheet id, window and A/B are needed.
func (s *Station) BlindOptionAudio(id string, window int, option string) ([]byte, *blind.Sheet, error) {
	sheet, err := s.Blind.Load(id)
	if err != nil {
		return nil, nil, err
	}
	reel, err := s.sheetReel(sheet)
	if err != nil {
		return nil, nil, err
	}
	audio, err := blind.RenderOption(reel, sheet, window, option)
	if err != nil {
		return nil, nil, err
	}
	return audio, sheet, nil
}

// Unblind decodes a sheet against the current catalog. A missing capture,
// changed source material or drifted coverage quarantines the sheet instead
// of silently treating stale preferences as valid.
func (s *Station) Unblind(id string) (*blind.Unblinded, error) {
	sheet, err := s.Blind.Load(id)
	if err != nil {
		return nil, err
	}
	reel, ok := s.Reel(sheet.Capture)
	if !ok {
		return s.quarantineGone(sheet)
	}
	return s.Blind.Unblind(reel, id)
}

// quarantineGone marks a sheet whose capture has left the catalog and returns
// a decoded view explaining why every window is held back.
func (s *Station) quarantineGone(sheet *blind.Sheet) (*blind.Unblinded, error) {
	result := &blind.Unblinded{
		ID: sheet.ID, Capture: sheet.Capture, Source: sheet.Source,
		Status: blind.StatusQuarantine, Seed: sheet.Seed,
		Windows:     []blind.UnblindedWindow{},
		Quarantined: []string{blind.ReasonSourceChanged},
	}
	public := blind.PublicView(sheet)
	for _, win := range sheet.Windows {
		optionA, optionB := win.OptionProfiles()
		entry := blind.UnblindedWindow{
			Index: win.Index, Window: win.Window, OptionOrder: win.Order,
			OptionA: optionA, OptionB: optionB, CommonRows: win.CommonRows,
			Coverage:    win.Coverage,
			Preferences: []blind.ReviewerPreference{},
			Quarantine:  blind.ReasonSourceChanged,
		}
		// The sheet stores its own A/B order, so stored choices can still be
		// decoded even though the live material needed to re-check them is gone.
		for _, answer := range public.Windows[win.Index].Answers {
			profile, err := win.Resolve(answer.Option)
			if err != nil {
				return nil, err
			}
			entry.Preferences = append(entry.Preferences, blind.ReviewerPreference{
				Reviewer: answer.Reviewer, Option: answer.Option, Profile: profile,
			})
		}
		entry.Disagreement = len(preferenceProfiles(entry.Preferences)) > 1
		result.Windows = append(result.Windows, entry)
	}
	sheet.Status = blind.StatusQuarantine
	if err := s.Blind.Save(sheet); err != nil {
		return nil, err
	}
	return result, nil
}

func preferenceProfiles(preferences []blind.ReviewerPreference) map[string]struct{} {
	seen := make(map[string]struct{})
	for _, preference := range preferences {
		seen[preference.Profile] = struct{}{}
	}
	return seen
}
