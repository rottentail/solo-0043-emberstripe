// Package board plans and assembles time-bounded listening review boards.
//
// A review board is an ordered set of required evidence items and optional
// comparison items. The planner resolves each item to a picture-row interval
// carrying the requested lead/tail context, merges items that can share one
// continuous playback, keeps items that must be separated in distinct clips,
// reports coverage limits next to missing pages, and produces a playable WAV
// plus a cue sheet that maps every board sample back to the source reel row.
package board

import (
	"errors"
	"fmt"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

const (
	Required = "required"
	Optional = "optional"

	// MaxBoardSeconds bounds the default QC listening board.
	MaxBoardSeconds = 12
	// clipFrameLimit mirrors the station's per-request 48-frame window limit.
	clipFrameLimit = register.MaxFrames
)

// Outcome markers used in ItemOutcome.Outcome.
const (
	OutcomeRequired = "required"
	OutcomeShared   = "shared"
	OutcomeAdded    = "added"
	OutcomeSplit    = "split"
	OutcomeOmitted  = "omitted"
)

// Target is one marked listening position with its minimum context.
type Target struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Priority     int    `json:"priority,omitempty"`
	Frame        int    `json:"frame"`
	Row          int    `json:"row"`
	LeadRows     int    `json:"lead_rows"`
	TailRows     int    `json:"tail_rows"`
	SeparateFrom string `json:"separate_from,omitempty"`
	Note         string `json:"note,omitempty"`
}

// Request is the full review brief for one reel and calibration.
type Request struct {
	Reel       string   `json:"reel"`
	Profile    string   `json:"profile,omitempty"`
	MaxSeconds float64  `json:"max_seconds,omitempty"`
	Required   []Target `json:"required"`
	Optional   []Target `json:"optional"`
}

// Interval is a half-open span of picture rows.
type Interval struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (i Interval) length() int { return i.End - i.Start }

// Clip is one continuous playback on the board timeline.
type Clip struct {
	ID          string          `json:"id"`
	Picture     Interval        `json:"picture"`
	Window      register.Window `json:"window"`
	ItemIDs     []string        `json:"item_ids"`
	Separates   *[2]string      `json:"separates,omitempty"`
	BoardStart  int             `json:"board_sample_start"`
	BoardEnd    int             `json:"board_sample_end"`
	BoardOffset float64         `json:"board_offset_seconds"`
	BoardLength float64         `json:"board_length_seconds"`
}

// CoverageNote explains why requested context cannot be fully heard.
type CoverageNote struct {
	ItemID  string   `json:"item_id"`
	Side    string   `json:"side"`
	Reason  string   `json:"reason"`
	Limited Interval `json:"limited_to"`
	Wanted  Interval `json:"wanted"`
}

// ItemOutcome records how the planner handled one brief item.
type ItemOutcome struct {
	Target
	Sequence   int            `json:"sequence"`
	Requested  Interval       `json:"requested"`
	Played     Interval       `json:"played"`
	ClipIDs    []string       `json:"clip_ids"`
	Anchor     Interval       `json:"anchor"`
	SharedWith []string       `json:"shared_with,omitempty"`
	Outcome    string         `json:"outcome"`
	Reason     string         `json:"reason,omitempty"`
	Limits     []CoverageNote `json:"limits,omitempty"`
}

// Adjustment is a concrete relaxation needed when the brief cannot fit.
type Adjustment struct {
	Kind        string     `json:"kind"`
	ItemID      string     `json:"item_id,omitempty"`
	Pair        *[2]string `json:"pair,omitempty"`
	Relax       string     `json:"relaxation"`
	NeedSeconds float64    `json:"need_seconds"`
	GainSeconds float64    `json:"gain_seconds,omitempty"`
	Detail      string     `json:"detail"`
}

// Issue is a non-fatal planning observation.
type Issue struct {
	Code    string `json:"code"`
	ItemID  string `json:"item_id,omitempty"`
	Message string `json:"message"`
}

// Plan is the cue sheet for a rendered or renderable board.
type Plan struct {
	Reel          string        `json:"reel"`
	Source        string        `json:"source_sha256"`
	Profile       string        `json:"profile"`
	Rate          int           `json:"sample_rate"`
	RowsPerFrame  int           `json:"rows_per_frame"`
	FPS           int           `json:"fps"`
	LeadRows      int           `json:"lead_rows"`
	MaxSeconds    float64       `json:"max_seconds"`
	BudgetSamples int           `json:"budget_samples"`
	UsedSamples   int           `json:"used_samples"`
	UsedSeconds   float64       `json:"used_seconds"`
	Feasible      bool          `json:"feasible"`
	Clips         []Clip        `json:"clips"`
	Items         []ItemOutcome `json:"items"`
	Adjustments   []Adjustment  `json:"adjustments,omitempty"`
	Issues        []Issue       `json:"issues,omitempty"`
}

type resolved struct {
	sequence  int
	target    Target
	anchor    Interval
	requested Interval
	played    Interval
	limits    []CoverageNote
}

func effectiveSeconds(r Request) float64 {
	if r.MaxSeconds > 0 {
		return r.MaxSeconds
	}
	return MaxBoardSeconds
}

// PlanBoard builds a cue sheet without rendering audio.
func PlanBoard(reel *capture.Reel, brief Request) (Plan, error) {
	if err := validateBrief(reel, brief); err != nil {
		return Plan{}, err
	}
	g, err := gauge.Measure(reel, brief.Profile)
	if err != nil {
		return Plan{}, err
	}
	rate := register.Rate(reel.Header)
	seconds := effectiveSeconds(brief)
	budget := int(seconds*float64(rate) + 0.5)

	plan := Plan{
		Reel: reel.Name, Source: reel.Digest, Profile: g.Profile, Rate: rate,
		RowsPerFrame: reel.Rows, FPS: reel.FPS, LeadRows: reel.Lead,
		MaxSeconds: seconds, BudgetSamples: budget,
	}

	required, err := resolveItems(reel, brief.Required, true)
	if err != nil {
		return Plan{}, err
	}
	optional, err := resolveItems(reel, brief.Optional, false)
	if err != nil {
		return Plan{}, err
	}

	clips, outcomes, separation := buildRequiredClips(required)
	used := totalClipRows(clips)
	stampWindows(reel, clips)
	stampTimeline(rate, clips)
	plan.Clips = clips
	plan.Items = outcomes
	plan.UsedSamples = used
	plan.UsedSeconds = float64(used) / float64(rate)

	if over := oversizedClips(reel, plan.Clips); len(over) > 0 {
		plan.Feasible = false
		plan.Adjustments = clipLengthAdjustments(rate, over)
		plan.Issues = append(plan.Issues, Issue{
			Code:    "clip_too_long",
			Message: fmt.Sprintf("有 %d 条播放超过工位每次 %d 格的上限，需要拆成多个画格窗。", len(over), clipFrameLimit),
		})
		return plan, nil
	}

	if used > budget {
		plan.Feasible = false
		plan.Adjustments = requiredAdjustments(rate, seconds, required, clips, separation, used-budget)
		plan.Issues = append(plan.Issues, Issue{
			Code: "budget_exceeded",
			Message: fmt.Sprintf("必听证据最短需要 %.3f 秒（%d 个采样帧），超过 %.3f 秒上限（%d 个采样帧）。",
				plan.UsedSeconds, used, seconds, budget),
		})
		return plan, nil
	}

	plan.Clips, plan.Items = placeOptional(rate, reel, clips, outcomes, optional, budget, used)
	used = totalClipRows(plan.Clips)
	stampWindows(reel, plan.Clips)
	stampTimeline(rate, plan.Clips)
	plan.UsedSamples = used
	plan.UsedSeconds = float64(used) / float64(rate)
	if over := oversizedClips(reel, plan.Clips); len(over) > 0 {
		plan.Feasible = false
		plan.Adjustments = clipLengthAdjustments(rate, over)
		plan.Issues = append(plan.Issues, Issue{
			Code:    "clip_too_long",
			Message: fmt.Sprintf("有 %d 条播放超过工位每次 %d 格的上限，需要拆成多个画格窗。", len(over), clipFrameLimit),
		})
		return plan, nil
	}
	plan.Feasible = true
	return plan, nil
}

// oversizedClips returns clips whose frame-aligned extraction window exceeds
// the station's 48-frame per-request limit.
func oversizedClips(reel *capture.Reel, clips []Clip) []Clip {
	over := []Clip{}
	for _, clip := range clips {
		if clip.Window.End-clip.Window.Start > clipFrameLimit {
			over = append(over, clip)
		}
	}
	return over
}

func clipLengthAdjustments(rate int, over []Clip) []Adjustment {
	result := make([]Adjustment, 0, len(over))
	for _, clip := range over {
		frames := clip.Window.End - clip.Window.Start
		result = append(result, Adjustment{
			Kind: "split_clip",
			Relax: fmt.Sprintf("把播放 %s（画格 %d–%d，共 %d 格，含项目 %s）拆成至多 %d 格一段的多个画格窗",
				clip.ID, clip.Window.Start, clip.Window.End-1, frames, joinCN(clip.ItemIDs, "、"), clipFrameLimit),
			NeedSeconds: float64(clip.Picture.length()) / float64(rate),
			Detail:      "工位单次试听/发布上限为 48 格；拆分不删证据，只把连续播放切成多段，听审顺序保持不变。",
		})
	}
	return result
}

func validateBrief(reel *capture.Reel, brief Request) error {
	if brief.Reel != "" && brief.Reel != reel.Name {
		return errors.New("brief reel does not match capture")
	}
	if brief.MaxSeconds < 0 {
		return errors.New("max_seconds must be positive")
	}
	seen := map[string]bool{}
	validate := func(items []Target, group string, wantKind string) error {
		for _, item := range items {
			if item.ID == "" {
				return fmt.Errorf("%s item is missing an id", group)
			}
			if seen[item.ID] {
				return fmt.Errorf("duplicate item id %q", item.ID)
			}
			seen[item.ID] = true
			if item.Kind != "" && item.Kind != wantKind {
				return fmt.Errorf("item %s is in the %s list but kind=%q", item.ID, group, item.Kind)
			}
			if item.Frame < 0 || item.Frame >= reel.Frames || item.Row < 0 || item.Row >= reel.Rows {
				return fmt.Errorf("item %s: frame/row outside capture", item.ID)
			}
			if item.LeadRows < 0 || item.TailRows < 0 {
				return fmt.Errorf("item %s: context rows must be non-negative", item.ID)
			}
		}
		return nil
	}
	if err := validate(brief.Required, "required", Required); err != nil {
		return err
	}
	if err := validate(brief.Optional, "optional", Optional); err != nil {
		return err
	}
	if len(brief.Required) == 0 {
		return errors.New("a review board needs at least one required item")
	}
	ids := map[string]bool{}
	for _, item := range brief.Required {
		ids[item.ID] = true
	}
	for _, item := range brief.Required {
		if item.SeparateFrom != "" {
			if item.SeparateFrom == item.ID {
				return fmt.Errorf("item %s cannot be separated from itself", item.ID)
			}
			if !ids[item.SeparateFrom] {
				return fmt.Errorf("item %s: separation partner %q is not a required item", item.ID, item.SeparateFrom)
			}
		}
	}
	return nil
}

// resolveItems expands each target to its wanted and playable row intervals
// and records where missing pages or reel edges truncate the context.
func resolveItems(reel *capture.Reel, items []Target, required bool) ([]resolved, error) {
	out := make([]resolved, 0, len(items))
	for order, item := range items {
		anchorRow := item.Frame*reel.Rows + item.Row
		anchor := Interval{Start: anchorRow, End: anchorRow + 1}
		wanted := Interval{Start: anchorRow - item.LeadRows, End: anchorRow + 1 + item.TailRows}
		played := Interval{Start: max(0, wanted.Start), End: min(reel.Frames*reel.Rows, wanted.End)}
		limits := coverageLimits(reel, item, wanted, played, anchorRow)
		kind := item.Kind
		if kind == "" {
			if required {
				kind = Required
			} else {
				kind = Optional
			}
		}
		item.Kind = kind
		out = append(out, resolved{sequence: order, target: item, anchor: anchor, requested: wanted, played: played, limits: limits})
	}
	// Position order drives clip merging; the original meeting order is kept
	// on each outcome via Sequence.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].played.Start != out[j].played.Start {
			return out[i].played.Start < out[j].played.Start
		}
		return out[i].target.ID < out[j].target.ID
	})
	return out, nil
}

// coverageLimits walks coverage inside the wanted interval and reports every
// side of the anchor whose requested context is not fully readable.
func coverageLimits(reel *capture.Reel, item Target, wanted, played Interval, anchorRow int) []CoverageNote {
	notes := []CoverageNote{}
	add := func(side string, span Interval, reason string) {
		notes = append(notes, CoverageNote{ItemID: item.ID, Side: side, Reason: reason, Limited: span, Wanted: wanted})
	}
	if wanted.Start < 0 {
		// Rows before the reel start are simply absent; only report a bounded
		// span when some playable lead context still exists.
		span := Interval{Start: 0, End: min(anchorRow, wanted.End)}
		if span.length() > 0 {
			add("lead", span, "卷首之外没有声槽行，前上下文被卷界截断。")
		}
	}
	if wanted.End > reel.Frames*reel.Rows {
		span := Interval{Start: max(anchorRow+1, wanted.Start), End: reel.Frames * reel.Rows}
		if span.length() > 0 {
			add("tail", span, "卷尾之外没有声槽行，后上下文被卷界截断。")
		}
	}
	runs := register.Coverage(reel, register.Window{Start: played.Start / reel.Rows, End: (played.End + reel.Rows - 1) / reel.Rows})
	for _, run := range runs {
		if run.Status == register.Readable {
			continue
		}
		start := max(run.Start, played.Start)
		end := min(run.End, played.End)
		if start >= end {
			continue
		}
		side := "lead"
		switch {
		case end <= anchorRow:
			side = "lead"
		case start >= anchorRow+1:
			side = "tail"
		default:
			// The unreadable span covers the anchor row itself.
			side = "anchor"
		}
		reason := "该区间对应缺页，PCM 只能补零，名义前后文实际听不全。"
		if run.Status == register.Outside {
			reason = "该区间已移出卷界，没有可听声槽行。"
		}
		add(side, Interval{Start: start, End: end}, reason)
	}
	return notes
}

// buildRequiredClips groups required items into the minimum ordered set of
// continuous clips while honouring every separation pair.
func buildRequiredClips(items []resolved) ([]Clip, []ItemOutcome, map[[2]string]bool) {
	separation := map[[2]string]bool{}
	for _, item := range items {
		if item.target.SeparateFrom == "" {
			continue
		}
		a, b := item.target.ID, item.target.SeparateFrom
		if a > b {
			a, b = b, a
		}
		separation[[2]string{a, b}] = true
	}
	blocked := func(a, b string) bool {
		if a > b {
			a, b = b, a
		}
		return separation[[2]string{a, b}]
	}

	clips := []Clip{}
	clipOf := map[string]int{}
	for _, item := range items {
		placed := false
		// Items are position-sorted, so only the last clip can still overlap.
		if n := len(clips); n > 0 {
			clip := &clips[n-1]
			if clip.Picture.Start < item.played.End && item.played.Start < clip.Picture.End {
				if !hasBlockedMember(clip.ItemIDs, item.target.ID, blocked) {
					clip.Picture.Start = min(clip.Picture.Start, item.played.Start)
					clip.Picture.End = max(clip.Picture.End, item.played.End)
					clip.ItemIDs = append(clip.ItemIDs, item.target.ID)
					clipOf[item.target.ID] = n - 1
					placed = true
				}
			}
		}
		if !placed {
			clips = append(clips, Clip{
				ID:      fmt.Sprintf("clip-%d", len(clips)+1),
				Picture: Interval{Start: item.played.Start, End: item.played.End},
				ItemIDs: []string{item.target.ID},
			})
			clipOf[item.target.ID] = len(clips) - 1
		}
	}
	for pair := range separation {
		if ci, cj := clipOf[pair[0]], clipOf[pair[1]]; ci != cj {
			hi := max(ci, cj)
			pairCopy := pair
			clips[hi].Separates = &pairCopy
		}
	}

	outcomes := make([]ItemOutcome, 0, len(items))
	for _, item := range items {
		idx := clipOf[item.target.ID]
		shared := make([]string, 0, len(clips[idx].ItemIDs)-1)
		for _, id := range clips[idx].ItemIDs {
			if id != item.target.ID {
				shared = append(shared, id)
			}
		}
		sort.Strings(shared)
		outcomes = append(outcomes, ItemOutcome{
			Target: item.target, Sequence: item.sequence, Requested: item.requested,
			Played: item.played, ClipIDs: []string{clips[idx].ID}, Anchor: item.anchor,
			SharedWith: shared, Outcome: OutcomeRequired, Limits: item.limits,
		})
	}
	return clips, outcomes, separation
}

func hasBlockedMember(members []string, id string, blocked func(a, b string) bool) bool {
	for _, member := range members {
		if blocked(member, id) {
			return true
		}
	}
	return false
}

// requiredAdjustments enumerates concrete relaxations that could make an
// over-budget required brief deliverable, largest measurable gain first.
func requiredAdjustments(rate int, capSeconds float64, items []resolved, clips []Clip, separation map[[2]string]bool, overflow int) []Adjustment {
	result := []Adjustment{}
	need := func(gain int) float64 {
		return float64(max(0, overflow-gain)) / float64(rate)
	}
	// 1. Drop a separation constraint that currently forces extra clips.
	baseline := totalClipRows(clips)
	for pair := range separation {
		merged := mergedLengthIfJoined(clips, pair)
		if gain := baseline - merged; gain > 0 {
			pairCopy := pair
			result = append(result, Adjustment{
				Kind: "drop_separation", Pair: &pairCopy,
				Relax:       fmt.Sprintf("允许 %s 与 %s 并入同一次连续播放", pair[0], pair[1]),
				NeedSeconds: need(gain), GainSeconds: float64(gain) / float64(rate),
				Detail: "两项区间本可合并为一条播放，隔开要求重复携带了各自的上下文。",
			})
		}
	}
	// 2. Trim requested lead/tail context. Each candidate reports how many
	// board rows would disappear if that item's context were halved.
	for _, item := range items {
		gain := trimmableRows(clips, items, item)
		if gain > 0 {
			leadKeep, tailKeep := item.target.LeadRows-item.target.LeadRows/2, item.target.TailRows-item.target.TailRows/2
			sides := []string{}
			if item.target.LeadRows > 0 {
				sides = append(sides, fmt.Sprintf("前上下文 %d→%d 行", item.target.LeadRows, leadKeep))
			}
			if item.target.TailRows > 0 {
				sides = append(sides, fmt.Sprintf("后上下文 %d→%d 行", item.target.TailRows, tailKeep))
			}
			result = append(result, Adjustment{
				Kind: "trim_context", ItemID: item.target.ID,
				Relax:       fmt.Sprintf("缩短 %s 的%s（锚点行 %d 保留）", item.target.ID, joinCN(sides, "、"), item.anchor.Start),
				NeedSeconds: need(gain), GainSeconds: float64(gain) / float64(rate),
				Detail: "缩短该项目的上下文不会删除锚点本身，只减少重复或可从原卷补听的环境声。",
			})
		}
	}
	// 3. Extend the time cap while keeping every requirement intact.
	requiredSeconds := float64(baseline) / float64(rate)
	result = append(result, Adjustment{
		Kind:        "extend_budget",
		Relax:       fmt.Sprintf("把拼板时长上限从 %.3f 秒放宽到 %.3f 秒", capSeconds, requiredSeconds),
		NeedSeconds: float64(overflow) / float64(rate),
		Detail:      "保持全部必听位置、上下文和隔开要求不变，仅放宽总时长。",
	})
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].GainSeconds != result[j].GainSeconds {
			return result[i].GainSeconds > result[j].GainSeconds
		}
		return result[i].Kind < result[j].Kind
	})
	return result
}

// mergedLengthIfJoined recomputes total clip rows assuming the separation
// pair no longer blocks merging.
func mergedLengthIfJoined(clips []Clip, pair [2]string) int {
	find := func(id string) int {
		for i, clip := range clips {
			for _, member := range clip.ItemIDs {
				if member == id {
					return i
				}
			}
		}
		return -1
	}
	i, j := find(pair[0]), find(pair[1])
	if i < 0 || j < 0 || i == j {
		return totalClipRows(clips)
	}
	lo, hi := min(i, j), max(i, j)
	total := 0
	for k, clip := range clips {
		if k == lo || k == hi {
			continue
		}
		total += clip.Picture.length()
	}
	a, b := clips[lo].Picture, clips[hi].Picture
	if a.End <= b.Start || b.End <= a.Start {
		total += a.length() + b.length()
	} else {
		total += max(a.End, b.End) - min(a.Start, b.Start)
	}
	return total
}

// trimmableRows estimates board rows saved by halving this item's context.
// Only rows that no other member of the same merged clip still needs count.
func trimmableRows(clips []Clip, all []resolved, item resolved) int {
	anchorRow := item.anchor.Start
	byID := map[string]resolved{}
	for _, peer := range all {
		byID[peer.target.ID] = peer
	}
	clipIdx := -1
	for i, clip := range clips {
		for _, id := range clip.ItemIDs {
			if id == item.target.ID {
				clipIdx = i
			}
		}
	}
	if clipIdx < 0 {
		return 0
	}
	clip := clips[clipIdx]
	// Spans of every other member sharing this playback.
	others := []Interval{}
	for _, member := range clip.ItemIDs {
		if member == item.target.ID {
			continue
		}
		others = append(others, byID[member].played)
	}
	savings := 0
	for _, side := range []string{"lead", "tail"} {
		var span Interval
		if side == "lead" {
			span = Interval{Start: max(item.played.Start, clip.Picture.Start), End: min(anchorRow, item.played.End)}
		} else {
			span = Interval{Start: max(anchorRow+1, item.played.Start), End: min(item.played.End, clip.Picture.End)}
		}
		requested := item.target.LeadRows
		if side == "tail" {
			requested = item.target.TailRows
		}
		cut := requested / 2
		if cut <= 0 || span.length() <= 0 {
			continue
		}
		// The removable part sits next to the anchor; rows still covered by
		// another member are excluded because the clip cannot shrink there.
		for step := 1; step <= cut; step++ {
			var row int
			if side == "lead" {
				row = anchorRow - step
			} else {
				row = anchorRow + step
			}
			if row < span.Start || row >= span.End || coveredByOthers(row, others) {
				break
			}
			savings++
		}
	}
	return savings
}

// coveredByOthers reports whether a picture row falls in any member span.
func coveredByOthers(row int, others []Interval) bool {
	for _, span := range others {
		if row >= span.Start && row < span.End {
			return true
		}
	}
	return false
}

// placeOptional fits optional comparison items by priority using four
// outcomes: share an existing playback, add a new clip, split around the
// anchor, or omit with a recorded reason.
func placeOptional(rate int, reel *capture.Reel, clips []Clip, outcomes []ItemOutcome, items []resolved, budget, used int) ([]Clip, []ItemOutcome) {
	ordered := append([]resolved{}, items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].target.Priority != ordered[j].target.Priority {
			return ordered[i].target.Priority < ordered[j].target.Priority
		}
		return ordered[i].target.ID < ordered[j].target.ID
	})
	for _, item := range ordered {
		outcome := ItemOutcome{
			Target: item.target, Sequence: item.sequence, Requested: item.requested,
			Played: item.played, Anchor: item.anchor, Limits: item.limits,
		}
		// 1. Share a clip that already covers the whole wanted interval.
		if idx := clipCovering(clips, item.played); idx >= 0 {
			clips[idx].ItemIDs = append(clips[idx].ItemIDs, item.target.ID)
			outcome.ClipIDs = []string{clips[idx].ID}
			outcome.Outcome = OutcomeShared
			outcome.SharedWith = membersWithout(clips[idx].ItemIDs, item.target.ID)
			outcomes = append(outcomes, outcome)
			continue
		}
		need := item.played.length()
		// 2. Add a new clip when the whole interval fits the remaining budget.
		if used+need <= budget {
			clip := Clip{ID: nextClipID(clips), Picture: Interval{Start: item.played.Start, End: item.played.End}, ItemIDs: []string{item.target.ID}}
			clips = append(clips, clip)
			used += need
			outcome.ClipIDs = []string{clip.ID}
			outcome.Outcome = OutcomeAdded
			outcomes = append(outcomes, outcome)
			continue
		}
		// 3. Split: keep the anchor, then as much lead/tail context as fits.
		pieces, pieceRows, reason := splitPieces(item, budget, used, clipSpans(clips))
		if len(pieces) > 0 {
			ids := make([]string, 0, len(pieces))
			playedStart, playedEnd := item.anchor.Start, item.anchor.End
			for _, piece := range pieces {
				clip := Clip{ID: nextClipID(clips), Picture: piece, ItemIDs: []string{item.target.ID}}
				clips = append(clips, clip)
				ids = append(ids, clip.ID)
				playedStart = min(playedStart, piece.Start)
				playedEnd = max(playedEnd, piece.End)
			}
			used += pieceRows
			outcome.ClipIDs = ids
			outcome.Played = Interval{Start: playedStart, End: playedEnd}
			outcome.Outcome = OutcomeSplit
			outcome.Reason = reason
			outcomes = append(outcomes, outcome)
			continue
		}
		// 4. No room even for the anchor.
		outcome.Outcome = OutcomeOmitted
		outcome.Reason = fmt.Sprintf("剩余 %.3f 秒不足以容纳锚点（需 %.3f 秒），该选听对照只能省去。",
			float64(budget-used)/float64(rate), float64(item.anchor.length())/float64(rate))
		outcomes = append(outcomes, outcome)
	}
	sort.SliceStable(clips, func(i, j int) bool {
		if clips[i].Picture.Start != clips[j].Picture.Start {
			return clips[i].Picture.Start < clips[j].Picture.Start
		}
		return clips[i].ID < clips[j].ID
	})
	return clips, outcomes
}

// splitPieces returns the playable pieces of an optional item once the full
// interval no longer fits. Rows already covered by an existing clip cost no
// extra budget; only genuinely new clips are appended to the board.
func splitPieces(item resolved, budget, used int, covered []Interval) ([]Interval, int, string) {
	anchor := item.anchor
	if used+uncoveredLength(anchor, covered) > budget {
		return nil, 0, ""
	}
	pieces := []Interval{}
	rows := 0
	appendPiece := func(span Interval) {
		for _, piece := range subtractCoverage(span, covered) {
			pieces = append(pieces, piece)
			rows += piece.length()
		}
	}
	appendPiece(anchor)

	lead := Interval{Start: item.played.Start, End: anchor.Start}
	tail := Interval{Start: anchor.End, End: item.played.End}
	grow := func(span Interval, side string) bool {
		if span.length() == 0 {
			return true
		}
		affordable := budget - used - rows
		if affordable <= 0 {
			return false
		}
		// Extend from the anchor outward, shrinking until the still-uncovered
		// portion fits the remaining budget.
		take := span.length()
		for take > 0 && uncoveredLength(outerSpan(span, take, side), covered) > affordable {
			take--
		}
		if take == 0 {
			return false
		}
		appendPiece(outerSpan(span, take, side))
		return take == span.length()
	}
	leadFull := grow(lead, "lead")
	tailFull := grow(tail, "tail")
	if len(pieces) == 0 {
		return nil, 0, ""
	}
	dropped := []string{}
	if !leadFull {
		dropped = append(dropped, "部分前上下文")
	}
	if !tailFull {
		dropped = append(dropped, "部分后上下文")
	}
	reason := "完整前后文超出剩余时长，选听片段已拆开：锚点单独成段"
	if len(dropped) > 0 {
		reason += "，并省去" + joinCN(dropped, "、")
	}
	reason += "。"
	return pieces, rows, reason
}

func outerSpan(span Interval, take int, side string) Interval {
	if side == "lead" {
		return Interval{Start: span.End - take, End: span.End}
	}
	return Interval{Start: span.Start, End: span.Start + take}
}

func clipSpans(clips []Clip) []Interval {
	spans := make([]Interval, 0, len(clips))
	for _, clip := range clips {
		spans = append(spans, clip.Picture)
	}
	return spans
}

// uncoveredLength counts rows of span that no covered interval contains.
func uncoveredLength(span Interval, covered []Interval) int {
	return sumLengths(subtractCoverage(span, covered))
}

func sumLengths(spans []Interval) int {
	total := 0
	for _, span := range spans {
		total += span.length()
	}
	return total
}

// subtractCoverage removes covered rows from span, returning the disjoint
// remnants in picture-row order.
func subtractCoverage(span Interval, covered []Interval) []Interval {
	remnants := []Interval{span}
	for _, block := range covered {
		next := make([]Interval, 0, len(remnants))
		for _, current := range remnants {
			if block.End <= current.Start || block.Start >= current.End {
				next = append(next, current)
				continue
			}
			if block.Start > current.Start {
				next = append(next, Interval{Start: current.Start, End: min(current.End, block.Start)})
			}
			if block.End < current.End {
				next = append(next, Interval{Start: max(current.Start, block.End), End: current.End})
			}
		}
		remnants = next
	}
	return remnants
}

func joinCN(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func clipCovering(clips []Clip, span Interval) int {
	for idx, clip := range clips {
		if clip.Picture.Start <= span.Start && clip.Picture.End >= span.End {
			return idx
		}
	}
	return -1
}

func membersWithout(members []string, drop string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range members {
		if id == drop || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func nextClipID(clips []Clip) string {
	return fmt.Sprintf("clip-%d", len(clips)+1)
}

func totalClipRows(clips []Clip) int {
	total := 0
	for _, clip := range clips {
		total += clip.Picture.length()
	}
	return total
}

// stampWindows records the frame-aligned extraction window for each clip.
func stampWindows(reel *capture.Reel, clips []Clip) {
	for idx := range clips {
		clip := &clips[idx]
		clip.Window = register.Window{
			Start: clip.Picture.Start / reel.Rows,
			End:   (clip.Picture.End + reel.Rows - 1) / reel.Rows,
		}
	}
}

// stampTimeline fills board-relative sample positions once clips are final
// and ordered by picture position.
func stampTimeline(rate int, clips []Clip) {
	cursor := 0
	for idx := range clips {
		clip := &clips[idx]
		clip.BoardStart = cursor
		clip.BoardEnd = cursor + clip.Picture.length()
		clip.BoardOffset = float64(cursor) / float64(rate)
		clip.BoardLength = float64(clip.Picture.length()) / float64(rate)
		cursor = clip.BoardEnd
	}
}

// Render validates the plan against the 48-frame per-window station limit,
// extracts each clip's PCM and concatenates them into one playable WAV.
func Render(reel *capture.Reel, plan Plan) ([]byte, Plan, error) {
	if !plan.Feasible {
		return nil, plan, errors.New("plan is not feasible; apply the listed adjustments first")
	}
	if plan.Reel != "" && plan.Reel != reel.Name {
		return nil, plan, errors.New("plan belongs to a different capture")
	}
	if plan.Rate != 0 && plan.Rate != register.Rate(reel.Header) {
		return nil, plan, errors.New("plan sample rate does not match the capture")
	}
	if len(plan.Clips) == 0 {
		return nil, plan, errors.New("plan has no clips to render")
	}
	g, err := gauge.Measure(reel, plan.Profile)
	if err != nil {
		return nil, plan, err
	}
	rate := register.Rate(reel.Header)
	stampWindows(reel, plan.Clips)
	stampTimeline(rate, plan.Clips)
	combined := make([]int16, 0, plan.UsedSamples*2)
	for idx := range plan.Clips {
		clip := &plan.Clips[idx]
		if clip.Picture.Start < 0 || clip.Picture.End <= clip.Picture.Start || clip.Picture.End > reel.Frames*reel.Rows {
			return nil, plan, fmt.Errorf("clip %s lies outside the capture", clip.ID)
		}
		if span := clip.Window.End - clip.Window.Start; span <= 0 || span > clipFrameLimit {
			return nil, plan, fmt.Errorf("clip %s spans %d frames; the station limit is %d frames per playback", clip.ID, span, clipFrameLimit)
		}
		render := readout.Extract(reel, g, clip.Window)
		offset := (clip.Picture.Start - clip.Window.Start*reel.Rows) * 2
		length := clip.Picture.length() * 2
		combined = append(combined, render.Samples[offset:offset+length]...)
	}
	plan.UsedSamples = len(combined) / 2
	plan.UsedSeconds = float64(plan.UsedSamples) / float64(rate)
	return wave.Encode(rate, combined), plan, nil
}
