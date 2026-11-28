// Package blind builds and scores blind listening answer sheets.
//
// A sheet pairs two usable gauge profiles over the same picture windows. Each
// reviewer only ever sees neutral A/B options; the option order is a seeded,
// deterministic permutation so every device reproduces the same presentation.
// Sheets and their answer journals are separately persisted so a half-finished
// sheet can be resumed on another device and later unblinded against the
// current catalog. Answers are keyed by reviewer id so several listeners can
// answer the same sheet and disagreement between them can be detected.
package blind

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

// MaxWindows bounds how many listening windows one sheet may carry.
const MaxWindows = 24

const schemaV1 = 1

var (
	idPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	reviewerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)
)

// Lifecycle statuses.
const (
	StatusOpen       = "open"
	StatusUnblinded  = "unblinded"
	StatusQuarantine = "quarantined"
)

// Quarantine reasons explain why a trial or sheet is set aside.
const (
	ReasonNoCommon      = "no_common_audible_range"
	ReasonSourceChanged = "source_material_changed"
	ReasonCoverageDrift = "coverage_no_longer_matches"
	ReasonProfileGone   = "profile_no_longer_valid"
)

// DefaultReviewer is used when no explicit reviewer id is supplied.
const DefaultReviewer = "reviewer"

var now = time.Now

func stamp() string { return now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

// Window is one paired listening trial on the native picture clock.
type Window struct {
	Index  int             `json:"index"`
	Window register.Window `json:"window"`
	// Profiles holds the two candidates in request order.
	Profiles [2]string `json:"profiles"`
	// Order maps the neutral options A and B onto profile positions:
	// Order[0] is the position presented as option A, Order[1] as option B.
	Order      [2]int         `json:"option_order"`
	Coverage   []register.Run `json:"coverage"`
	CommonRows int            `json:"common_readable_rows"`
	// OptionDigests fingerprints the rendered WAV of each profile position.
	OptionDigests [2]string `json:"option_audio_sha256"`
	// Answers maps each reviewer to the profile position they preferred.
	Answers map[string]int `json:"answers,omitempty"`
}

// Sheet is a blind listening answer sheet. Profile identifiers stay on the
// server side until unblinding; reviewer-facing payloads hide them.
type Sheet struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Capture   string    `json:"capture"`
	Source    string    `json:"source_sha256"`
	Seed      string    `json:"seed"`
	Rows      int       `json:"rows_per_frame"`
	FPS       int       `json:"fps"`
	Lead      int       `json:"lead_rows"`
	Rate      int       `json:"sample_rate"`
	Status    string    `json:"status"`
	Windows   []*Window `json:"windows"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

// WindowRequest is one requested trial when a sheet is created.
type WindowRequest struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Profile string `json:"profile"`
	Against string `json:"against"`
}

// CreateRequest builds a sheet for one capture from validated window trials.
type CreateRequest struct {
	Capture string          `json:"capture"`
	Seed    string          `json:"seed,omitempty"`
	Windows []WindowRequest `json:"windows"`
}

// Choice is one reviewer's pick. Option is "A" or "B".
type Choice struct {
	Window   int    `json:"window"`
	Option   string `json:"option"`
	Reviewer string `json:"reviewer,omitempty"`
}

// MissingError reports that no stored sheet matches the requested id.
type MissingError struct{ msg string }

func (e *MissingError) Error() string { return e.msg }

// IsNotExist reports whether an error means the sheet is unknown.
func IsNotExist(err error) bool {
	var missing *MissingError
	return errors.As(err, &missing)
}

// NoCommonError marks a requested trial with no shared audible range.
type NoCommonError struct{ Window int }

func (e *NoCommonError) Error() string {
	return fmt.Sprintf("window %d: %s", e.Window, ReasonNoCommon)
}

// NormalizeReviewer defaults and validates a reviewer identifier.
func NormalizeReviewer(id string) (string, error) {
	if id == "" {
		return DefaultReviewer, nil
	}
	if !reviewerPattern.MatchString(id) {
		return "", errors.New("reviewer must be 2–48 lowercase letters, digits or dashes")
	}
	return id, nil
}

// ordering derives a deterministic A/B permutation for one window from the
// sheet seed and window position, so any device reconstructs the same order.
func ordering(seed string, index int, first, second string) [2]int {
	mac := hmac.New(sha256.New, []byte(seed))
	fmt.Fprintf(mac, "window:%d:%s|%s", index, first, second)
	// Bit 0 of the digest decides whether the profiles swap presentation slots.
	if mac.Sum(nil)[0]&1 == 1 {
		return [2]int{1, 0}
	}
	return [2]int{0, 1}
}

type windowIdentity struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	First  string `json:"first"`
	Second string `json:"second"`
	Order  [2]int `json:"order"`
}

// contentDigest addresses a sheet by its trial content, so rebuilding the same
// request always reaches the same id and never duplicates stored answers.
func contentDigest(s *Sheet) string {
	core := struct {
		Version int              `json:"version"`
		Capture string           `json:"capture"`
		Source  string           `json:"source"`
		Seed    string           `json:"seed"`
		Windows []windowIdentity `json:"windows"`
	}{
		Version: s.Version, Capture: s.Capture, Source: s.Source, Seed: s.Seed,
		Windows: make([]windowIdentity, len(s.Windows)),
	}
	for i, w := range s.Windows {
		core.Windows[i] = windowIdentity{
			Start: w.Window.Start, End: w.Window.End,
			First: w.Profiles[0], Second: w.Profiles[1], Order: w.Order,
		}
	}
	raw, _ := json.Marshal(core)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Build validates every trial against the current capture snapshot and
// assembles a sheet. A window without a shared readable range is rejected.
func Build(reel *capture.Reel, req CreateRequest) (*Sheet, error) {
	if strings.TrimSpace(req.Capture) == "" {
		return nil, errors.New("capture is required")
	}
	if len(req.Windows) == 0 {
		return nil, errors.New("at least one listening window is required")
	}
	if len(req.Windows) > MaxWindows {
		return nil, fmt.Errorf("a sheet holds at most %d windows", MaxWindows)
	}
	seed := req.Seed
	if seed == "" {
		seed = reel.Digest
	}
	sheet := &Sheet{
		Version: schemaV1, Capture: reel.Name, Source: reel.Digest, Seed: seed,
		Rows: reel.Rows, FPS: reel.FPS, Lead: reel.Lead, Rate: register.Rate(reel.Header),
		Status: StatusOpen, CreatedAt: stamp(), UpdatedAt: stamp(),
	}
	for i, trial := range req.Windows {
		w := register.Window{Start: trial.Start, End: trial.End}
		if err := register.Validate(reel.Header, w); err != nil {
			return nil, fmt.Errorf("window %d: %w", i, err)
		}
		if strings.TrimSpace(trial.Profile) == "" || strings.TrimSpace(trial.Against) == "" {
			return nil, fmt.Errorf("window %d: two profiles are required", i)
		}
		if trial.Profile == trial.Against {
			return nil, fmt.Errorf("window %d: two distinct profiles are required", i)
		}
		profiles := [2]string{trial.Profile, trial.Against}
		renders := [2]readout.Render{}
		audioDigests := [2]string{}
		for pos, id := range profiles {
			g, err := gauge.Measure(reel, id)
			if err != nil {
				return nil, fmt.Errorf("window %d profile %q: %w", i, id, err)
			}
			render := readout.Extract(reel, g, w)
			renders[pos] = render
			sum := sha256.Sum256(wave.Encode(register.Rate(reel.Header), render.Samples))
			audioDigests[pos] = hex.EncodeToString(sum[:])
		}
		common := commonReadable(renders[0].Coverage, renders[1].Coverage, w, reel.Rows)
		if common == 0 {
			return nil, &NoCommonError{Window: i}
		}
		sheet.Windows = append(sheet.Windows, &Window{
			Index: i, Window: w, Profiles: profiles,
			Order:    ordering(seed, i, profiles[0], profiles[1]),
			Coverage: renders[0].Coverage, CommonRows: common,
			OptionDigests: audioDigests, Answers: map[string]int{},
		})
	}
	sheet.ID = contentDigest(sheet)
	return sheet, nil
}

// commonReadable counts picture rows readable under both renders inside w.
func commonReadable(a, b []register.Run, w register.Window, rows int) int {
	first, last := w.Start*rows, w.End*rows
	readable := func(runs []register.Run) map[int]struct{} {
		set := make(map[int]struct{})
		for _, run := range runs {
			if run.Status != register.Readable {
				continue
			}
			for row := max(run.Start, first); row < min(run.End, last); row++ {
				set[row] = struct{}{}
			}
		}
		return set
	}
	left, right := readable(a), readable(b)
	total := 0
	for row := range left {
		if _, ok := right[row]; ok {
			total++
		}
	}
	return total
}

// OptionProfiles returns the profile presented as each blind option.
func (win *Window) OptionProfiles() (optionA, optionB string) {
	return win.Profiles[win.Order[0]], win.Profiles[win.Order[1]]
}

// Resolve maps a neutral option letter to the underlying profile id.
func (win *Window) Resolve(option string) (string, error) {
	switch option {
	case "A":
		return win.Profiles[win.Order[0]], nil
	case "B":
		return win.Profiles[win.Order[1]], nil
	default:
		return "", errors.New(`option must be "A" or "B"`)
	}
}

func optionPosition(win *Window, option string) (int, error) {
	switch option {
	case "A":
		return win.Order[0], nil
	case "B":
		return win.Order[1], nil
	default:
		return 0, errors.New(`option must be "A" or "B"`)
	}
}

// RenderOption produces the WAV bytes behind one blind option of one window.
func RenderOption(reel *capture.Reel, sheet *Sheet, windowIndex int, option string) ([]byte, error) {
	if windowIndex < 0 || windowIndex >= len(sheet.Windows) {
		return nil, fmt.Errorf("window must be within 0..%d", len(sheet.Windows)-1)
	}
	win := sheet.Windows[windowIndex]
	profile, err := win.Resolve(option)
	if err != nil {
		return nil, err
	}
	g, err := gauge.Measure(reel, profile)
	if err != nil {
		return nil, err
	}
	render := readout.Extract(reel, g, win.Window)
	return wave.Encode(register.Rate(reel.Header), render.Samples), nil
}

// --------------------------------------------------------------------- views

// ReviewerAnswer is one listener's decoded pick for a public window.
type ReviewerAnswer struct {
	Reviewer string `json:"reviewer"`
	Option   string `json:"option"`
}

// PublicWindow is what a reviewer sees: neutral options, ranges and coverage,
// never the profile identities or their audio fingerprints.
type PublicWindow struct {
	Index      int              `json:"index"`
	Window     register.Window  `json:"window"`
	Options    [2]string        `json:"options"`
	CommonRows int              `json:"common_readable_rows"`
	Coverage   []register.Run   `json:"coverage"`
	Answers    []ReviewerAnswer `json:"answers"`
}

// PublicSheet is the reviewer-facing sheet used on any device.
type PublicSheet struct {
	ID        string         `json:"id"`
	Capture   string         `json:"capture"`
	Status    string         `json:"status"`
	Rows      int            `json:"rows_per_frame"`
	FPS       int            `json:"fps"`
	Rate      int            `json:"sample_rate"`
	Reviewers []string       `json:"reviewers"`
	Windows   []PublicWindow `json:"windows"`
}

// PublicView strips everything that would break the blind presentation.
func PublicView(sheet *Sheet) PublicSheet {
	return PublicViewFor(sheet, "")
}

// PublicViewFor returns the blind view for one reviewer. With an empty
// reviewer every answer is shown; otherwise only that reviewer's picks are
// included, so listeners cannot see each other's A/B choices.
func PublicViewFor(sheet *Sheet, reviewer string) PublicSheet {
	view := PublicSheet{
		ID: sheet.ID, Capture: sheet.Capture, Status: sheet.Status,
		Rows: sheet.Rows, FPS: sheet.FPS, Rate: sheet.Rate,
		Reviewers: sheetReviewers(sheet), Windows: make([]PublicWindow, 0, len(sheet.Windows)),
	}
	for _, win := range sheet.Windows {
		public := PublicWindow{
			Index: win.Index, Window: win.Window, Options: [2]string{"A", "B"},
			CommonRows: win.CommonRows, Coverage: win.Coverage,
			Answers: []ReviewerAnswer{},
		}
		for _, answer := range windowAnswers(win) {
			if reviewer == "" || answer.Reviewer == reviewer {
				public.Answers = append(public.Answers, answer)
			}
		}
		view.Windows = append(view.Windows, public)
	}
	return view
}

func sheetReviewers(sheet *Sheet) []string {
	seen := map[string]struct{}{}
	for _, win := range sheet.Windows {
		for reviewer := range win.Answers {
			seen[reviewer] = struct{}{}
		}
	}
	reviewers := make([]string, 0, len(seen))
	for reviewer := range seen {
		reviewers = append(reviewers, reviewer)
	}
	sort.Strings(reviewers)
	return reviewers
}

// windowAnswers restores the neutral option letter for every reviewer pick.
func windowAnswers(win *Window) []ReviewerAnswer {
	answers := make([]ReviewerAnswer, 0, len(win.Answers))
	for reviewer, position := range win.Answers {
		letter := "A"
		if win.Order[1] == position {
			letter = "B"
		}
		answers = append(answers, ReviewerAnswer{Reviewer: reviewer, Option: letter})
	}
	sort.Slice(answers, func(i, j int) bool { return answers[i].Reviewer < answers[j].Reviewer })
	return answers
}

// ---------------------------------------------------------------- persistence

// Store persists sheets and their answer journals under a root directory.
type Store struct {
	Root string
	mu   sync.Mutex
}

type answerFile struct {
	Version int      `json:"version"`
	Sheet   string   `json:"sheet"`
	Choices []Choice `json:"choices"`
}

func (s *Store) dir(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", &MissingError{msg: "unknown blind sheet"}
	}
	return filepath.Join(s.Root, id), nil
}

// Save writes a sheet and its answer journal atomically. The sheet content
// drives the id, so rebuilding the same trial always hits the same directory.
func (s *Store) Save(sheet *Sheet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.dir(sheet.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "sheet.json"), sheet); err != nil {
		return err
	}
	journal := answerFile{Version: schemaV1, Sheet: sheet.ID, Choices: collectChoices(sheet)}
	return atomicJSON(filepath.Join(dir, "answers.json"), journal)
}

func collectChoices(sheet *Sheet) []Choice {
	choices := make([]Choice, 0)
	for _, win := range sheet.Windows {
		for reviewer, position := range win.Answers {
			letter := "A"
			if win.Order[1] == position {
				letter = "B"
			}
			choices = append(choices, Choice{Window: win.Index, Option: letter, Reviewer: reviewer})
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Window != choices[j].Window {
			return choices[i].Window < choices[j].Window
		}
		return choices[i].Reviewer < choices[j].Reviewer
	})
	return choices
}

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a sheet and re-applies its answer journal so a reviewer can
// continue on another device with the exact prior presentation.
func (s *Store) Load(id string) (*Sheet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.dir(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sheet.json"))
	if os.IsNotExist(err) {
		return nil, &MissingError{msg: "unknown blind sheet"}
	}
	if err != nil {
		return nil, err
	}
	var sheet Sheet
	if err := json.Unmarshal(raw, &sheet); err != nil {
		return nil, err
	}
	// Empty answer maps are omitted when persisted, so restore them before
	// any write to avoid assigning into a nil map.
	for _, win := range sheet.Windows {
		if win.Answers == nil {
			win.Answers = map[string]int{}
		}
	}
	journal, err := os.ReadFile(filepath.Join(dir, "answers.json"))
	switch {
	case err == nil:
		var answers answerFile
		if err := json.Unmarshal(journal, &answers); err != nil {
			return nil, err
		}
		for _, choice := range answers.Choices {
			if choice.Window < 0 || choice.Window >= len(sheet.Windows) {
				return nil, errors.New("answer journal references an unknown window")
			}
			reviewer, err := NormalizeReviewer(choice.Reviewer)
			if err != nil {
				return nil, err
			}
			win := sheet.Windows[choice.Window]
			position, err := optionPosition(win, choice.Option)
			if err != nil {
				return nil, err
			}
			win.Answers[reviewer] = position
		}
	case os.IsNotExist(err):
	default:
		return nil, err
	}
	return &sheet, nil
}

// List returns the ids of every stored sheet, sorted for stable display.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && idPattern.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// RecordAnswer stores one reviewer's choice. Re-posting the same window by the
// same reviewer is idempotent; changing the option overwrites the prior pick.
func (s *Store) RecordAnswer(id string, choice Choice) (*Sheet, error) {
	reviewer, err := NormalizeReviewer(choice.Reviewer)
	if err != nil {
		return nil, err
	}
	sheet, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	if sheet.Status != StatusOpen {
		return nil, errors.New("sheet is no longer accepting answers")
	}
	if choice.Window < 0 || choice.Window >= len(sheet.Windows) {
		return nil, fmt.Errorf("window must be within 0..%d", len(sheet.Windows)-1)
	}
	win := sheet.Windows[choice.Window]
	position, err := optionPosition(win, choice.Option)
	if err != nil {
		return nil, err
	}
	win.Answers[reviewer] = position
	sheet.UpdatedAt = stamp()
	if err := s.Save(sheet); err != nil {
		return nil, err
	}
	return sheet, nil
}

// ---------------------------------------------------------------- unblinding

// ReviewerPreference is one decoded listener choice after unblinding.
type ReviewerPreference struct {
	Reviewer string `json:"reviewer"`
	Option   string `json:"option"`
	Profile  string `json:"profile"`
}

// UnblindedWindow is the decoded result for one trial after unblinding.
type UnblindedWindow struct {
	Index        int                  `json:"index"`
	Window       register.Window      `json:"window"`
	OptionA      string               `json:"option_a_profile"`
	OptionB      string               `json:"option_b_profile"`
	OptionOrder  [2]int               `json:"option_order"`
	CommonRows   int                  `json:"common_readable_rows"`
	Coverage     []register.Run       `json:"coverage"`
	Preferences  []ReviewerPreference `json:"preferences"`
	Disagreement bool                 `json:"disagreement"`
	Quarantine   string               `json:"quarantine_reason,omitempty"`
	Workbench    string               `json:"workbench_url,omitempty"`
}

// Unblinded is the full decoded sheet returned to the administrator.
type Unblinded struct {
	ID          string            `json:"id"`
	Capture     string            `json:"capture"`
	Source      string            `json:"source_sha256"`
	Status      string            `json:"status"`
	Seed        string            `json:"seed"`
	Windows     []UnblindedWindow `json:"windows"`
	Quarantined []string          `json:"quarantine_reasons,omitempty"`
}

// Unblind decodes the stored choices against the current catalog. If the
// underlying material has drifted so a trial can no longer be matched, the
// sheet is quarantined and the affected windows carry explicit reasons.
func (s *Store) Unblind(reel *capture.Reel, id string) (*Unblinded, error) {
	sheet, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	result := &Unblinded{
		ID: sheet.ID, Capture: sheet.Capture, Source: sheet.Source,
		Status: sheet.Status, Seed: sheet.Seed, Windows: []UnblindedWindow{},
	}
	sourceChanged := reel.Digest != sheet.Source
	if sourceChanged {
		result.Quarantined = append(result.Quarantined, ReasonSourceChanged)
	}
	for _, win := range sheet.Windows {
		optionA, optionB := win.OptionProfiles()
		entry := UnblindedWindow{
			Index: win.Index, Window: win.Window, OptionOrder: win.Order,
			OptionA: optionA, OptionB: optionB, CommonRows: win.CommonRows,
			Coverage: win.Coverage, Preferences: []ReviewerPreference{},
			Workbench: fmt.Sprintf("/lightbench/%s?start=%d&end=%d&profile=%s",
				sheet.Capture, win.Window.Start, win.Window.End, optionA),
		}
		for _, answer := range windowAnswers(win) {
			profile, err := win.Resolve(answer.Option)
			if err != nil {
				return nil, err
			}
			entry.Preferences = append(entry.Preferences, ReviewerPreference{
				Reviewer: answer.Reviewer, Option: answer.Option, Profile: profile,
			})
		}
		entry.Disagreement = len(distinctProfiles(entry.Preferences)) > 1
		if reason := driftReason(reel, win, sourceChanged); reason != "" {
			entry.Quarantine = reason
			if !contains(result.Quarantined, reason) {
				result.Quarantined = append(result.Quarantined, reason)
			}
		}
		result.Windows = append(result.Windows, entry)
	}
	if len(result.Quarantined) > 0 {
		result.Status = StatusQuarantine
		sheet.Status = StatusQuarantine
	} else {
		result.Status = StatusUnblinded
		sheet.Status = StatusUnblinded
	}
	sheet.UpdatedAt = stamp()
	if err := s.Save(sheet); err != nil {
		return nil, err
	}
	return result, nil
}

func distinctProfiles(preferences []ReviewerPreference) map[string]struct{} {
	seen := make(map[string]struct{})
	for _, preference := range preferences {
		seen[preference.Profile] = struct{}{}
	}
	return seen
}

// driftReason re-checks one trial against the live catalog snapshot.
func driftReason(reel *capture.Reel, win *Window, sourceChanged bool) string {
	if sourceChanged {
		return ReasonSourceChanged
	}
	for _, id := range win.Profiles {
		if _, err := gauge.Measure(reel, id); err != nil {
			return ReasonProfileGone
		}
	}
	if !sameCoverage(register.Coverage(reel, win.Window), win.Coverage) {
		return ReasonCoverageDrift
	}
	return ""
}

func sameCoverage(a, b []register.Run) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
