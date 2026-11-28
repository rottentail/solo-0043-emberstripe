package lightbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

const (
	// MaxTrail bounds the applied steps kept per reel for shift review.
	MaxTrail     = 48
	historyName  = ".workbench-history.json"
	historyLimit = 4 << 20
)

var (
	errNoStep       = errors.New("no applied step in that direction")
	errUnknownEntry = errors.New("unknown applied step")
)

// Probe is the picture position the operator was inspecting when the window was
// applied. It travels with the step so the lead can revisit the same witness.
type Probe struct {
	Frame int `json:"frame"`
	Row   int `json:"row"`
}

// Clock records the native timing a step was rendered on.
type Clock struct {
	Rows int `json:"rows_per_frame"`
	Rate int `json:"rate"`
	Lead int `json:"lead_rows"`
}

// Source pins the bytes of one scan page that contributes sound to the window.
// An empty Sheet means the page was missing when the step was applied.
type Source struct {
	Frame int    `json:"frame"`
	Sheet string `json:"sheet_sha256"`
}

// FrameState pins what each picture frame of the window looked like when the
// step was applied: depth 0 means the picture page was missing. Record-only
// review shows these values instead of the possibly re-scanned current pages.
type FrameState struct {
	Frame int    `json:"frame"`
	Depth int    `json:"depth"`
	Sheet string `json:"sheet_sha256,omitempty"`
}

// Step is one window and calibration pair that was actually applied. The gauge
// here is the meter measured at that moment, not a pointer to live settings.
type Step struct {
	ID          string          `json:"id"`
	Window      register.Window `json:"window"`
	Profile     string          `json:"profile"`
	Gauge       gauge.Gauge     `json:"gauge"`
	Probe       Probe           `json:"probe"`
	ProbeTrace  *readout.Value  `json:"probe_trace,omitempty"`
	Clock       Clock           `json:"clock"`
	Coverage    []register.Run  `json:"coverage"`
	Envelope    []amplitudeBin  `json:"envelope,omitempty"`
	Render      string          `json:"render_sha256"`
	Sources     []Source        `json:"sources"`
	Pictures    []FrameState    `json:"pictures,omitempty"`
	Environment string          `json:"environment_sha256"`
	Capture     string          `json:"capture_sha256"`
	AppliedAt   time.Time       `json:"applied_at"`
	Parcel      string          `json:"parcel,omitempty"`
	FrozenAt    *time.Time      `json:"frozen_at,omitempty"`
}

type node struct {
	Step
	Parent    string   `json:"parent,omitempty"`
	Children  []string `json:"children,omitempty"`
	Preferred string   `json:"preferred,omitempty"`
}

// ledgerData is the full step tree for one reel. Current is the root-to-step
// path in force; every other child stays reachable as an alternative route.
type ledgerData struct {
	Version       int              `json:"version"`
	Next          int              `json:"next"`
	Roots         []string         `json:"roots,omitempty"`
	PreferredRoot string           `json:"preferred_root,omitempty"`
	Current       []string         `json:"current,omitempty"`
	Nodes         map[string]*node `json:"nodes,omitempty"`
}

type ledgerFile struct {
	Version int                    `json:"version"`
	Reels   map[string]*ledgerData `json:"reels"`
}

func newLedger() *ledgerData {
	return &ledgerData{Version: 1, Nodes: make(map[string]*node)}
}

func (d *ledgerData) currentStep() (Step, bool) {
	if len(d.Current) == 0 {
		return Step{}, false
	}
	n, ok := d.Nodes[d.Current[len(d.Current)-1]]
	if !ok {
		return Step{}, false
	}
	return n.Step, true
}

func (d *ledgerData) pathTo(id string) []string {
	var reversed []string
	for id != "" {
		n, ok := d.Nodes[id]
		if !ok {
			return nil
		}
		reversed = append(reversed, id)
		id = n.Parent
	}
	path := make([]string, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		path = append(path, reversed[i])
	}
	return path
}

func (d *ledgerData) onPath(id string) bool {
	for _, item := range d.Current {
		if item == id {
			return true
		}
	}
	return false
}

// leaves enumerates every root-to-leaf route so both the trial line and the
// original continuation stay visible to the lead at the end of the shift.
func (d *ledgerData) leaves() [][]string {
	var out [][]string
	var walk func(id string, path []string)
	walk = func(id string, path []string) {
		n, ok := d.Nodes[id]
		if !ok {
			return
		}
		path = append(append([]string{}, path...), id)
		if len(n.Children) == 0 {
			out = append(out, path)
			return
		}
		for _, child := range n.Children {
			walk(child, path)
		}
	}
	for _, root := range d.Roots {
		walk(root, nil)
	}
	sort.SliceStable(out, func(i, j int) bool { return entryNumber(out[i][0]) < entryNumber(out[j][0]) })
	return out
}

// clone copies a ledger so a failed disk write can leave the live state intact.
func (d *ledgerData) clone() *ledgerData {
	raw, err := json.Marshal(d)
	if err != nil {
		return newLedger()
	}
	var out ledgerData
	if json.Unmarshal(raw, &out) != nil {
		return newLedger()
	}
	if out.Nodes == nil {
		out.Nodes = make(map[string]*node)
	}
	return &out
}

// historyStore persists the workbench trail beside the frozen parcels. A
// station restart, including after a supplementary scan, reloads the same
// trail, so closing and reopening the browser loses neither route.
type historyStore struct {
	mu     sync.Mutex
	path   string
	data   ledgerFile
	loaded bool
	cache  map[string]classification
}

func newHistory(root string) *historyStore {
	return &historyStore{path: filepath.Join(root, historyName), cache: make(map[string]classification)}
}

func (h *historyStore) loadLocked() {
	if h.loaded {
		return
	}
	h.loaded = true
	h.data = ledgerFile{Version: 1, Reels: make(map[string]*ledgerData)}
	f, err := os.Open(h.path)
	if err != nil {
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, historyLimit+1))
	if err != nil || len(raw) > historyLimit {
		return
	}
	var parsed ledgerFile
	if json.Unmarshal(raw, &parsed) != nil || parsed.Version != 1 {
		// Preserve the unreadable trail instead of overwriting it, so a later
		// look can recover the shift history after a format or crash problem.
		_ = os.Rename(h.path, h.path+".corrupt")
		return
	}
	if parsed.Reels == nil {
		parsed.Reels = make(map[string]*ledgerData)
	}
	for _, ledger := range parsed.Reels {
		if ledger.Nodes == nil {
			ledger.Nodes = make(map[string]*node)
		}
	}
	h.data = parsed
}

func (h *historyStore) saveLocked() error {
	raw, err := json.Marshal(h.data)
	if err != nil {
		return err
	}
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".workbench-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(raw)
	var syncErr error
	if writeErr == nil {
		syncErr = temp.Sync()
	}
	if err := errors.Join(writeErr, syncErr, temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), h.path)
}

func (h *historyStore) reelLocked(name string) *ledgerData {
	ledger, ok := h.data.Reels[name]
	if !ok {
		ledger = newLedger()
		h.data.Reels[name] = ledger
	}
	if ledger.Nodes == nil {
		ledger.Nodes = make(map[string]*node)
	}
	return ledger
}

// mutate applies fn to a copy of one reel's trail and publishes it only after
// the copy is safely on disk.
func (h *historyStore) mutate(name string, fn func(*ledgerData) error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadLocked()
	previous := h.reelLocked(name)
	working := previous.clone()
	if err := fn(working); err != nil {
		return err
	}
	h.data.Reels[name] = working
	if err := h.saveLocked(); err != nil {
		h.data.Reels[name] = previous
		return err
	}
	return nil
}

func (h *historyStore) current(name string) (Step, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadLocked()
	return h.reelLocked(name).currentStep()
}

func (h *historyStore) find(name, id string) (Step, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadLocked()
	n, ok := h.reelLocked(name).Nodes[id]
	if !ok {
		return Step{}, false
	}
	return n.Step, true
}

// Record appends a step under the step in force. Re-applying the identical
// audio that is already in force reuses the existing step so a reload does not
// fabricate history. Every other application becomes a new node, so earlier
// provenance is never rewritten.
func (h *historyStore) Record(name string, step Step) (Step, bool, error) {
	var recorded Step
	created := false
	err := h.mutate(name, func(d *ledgerData) error {
		if current, ok := d.currentStep(); ok {
			if current.Window == step.Window && current.Profile == step.Profile && current.Render == step.Render {
				// Same window, calibration and audio: reuse the step. Only its
				// reviewed probe may move, and only forward to a new position.
				if step.Probe != current.Probe {
					n := d.Nodes[current.ID]
					n.Probe, n.ProbeTrace = step.Probe, step.ProbeTrace
					current.Probe, current.ProbeTrace = step.Probe, step.ProbeTrace
				}
				recorded = current
				return nil
			}
		}
		parent := ""
		parentPath := []string(nil)
		if len(d.Current) > 0 {
			parent = d.Current[len(d.Current)-1]
			parentPath = append(parentPath, d.Current...)
		}
		d.Next++
		step.ID = fmt.Sprintf("e%d", d.Next)
		d.Nodes[step.ID] = &node{Step: step, Parent: parent}
		if parent == "" {
			d.Roots = append(d.Roots, step.ID)
			d.PreferredRoot = step.ID
		} else {
			entry := d.Nodes[parent]
			// A branch that was withdrawn leaves its continuation preferred, so a
			// trial recorded from the old window does not hijack redo away from
			// the original follow-up the operator wants to recover.
			if entry.Preferred == "" || d.Nodes[entry.Preferred] == nil {
				entry.Preferred = step.ID
			}
			entry.Children = append(entry.Children, step.ID)
		}
		d.Current = append(parentPath, step.ID)
		recorded, created = step, true
		d.prune()
		return nil
	})
	if err != nil {
		return Step{}, false, err
	}
	return recorded, created, nil
}

// prune drops the oldest route once the trail exceeds MaxTrail. The route that
// holds the current step is never dropped.
func (d *ledgerData) prune() {
	for len(d.Nodes) > MaxTrail && len(d.Roots) > 0 {
		victim := ""
		for _, root := range d.Roots {
			if d.onPath(root) {
				continue
			}
			victim = root
			break
		}
		if victim == "" {
			return
		}
		d.drop(victim)
	}
}

func (d *ledgerData) drop(id string) {
	n, ok := d.Nodes[id]
	if !ok {
		return
	}
	for _, child := range n.Children {
		d.drop(child)
	}
	delete(d.Nodes, id)
	for i, root := range d.Roots {
		if root == id {
			d.Roots = append(d.Roots[:i], d.Roots[i+1:]...)
			break
		}
	}
	if d.PreferredRoot == id {
		d.PreferredRoot = ""
		if len(d.Roots) > 0 {
			d.PreferredRoot = d.Roots[0]
		}
	}
}

// Undo steps back one applied window. The abandoned continuation is kept as a
// redo route, so a failed trial can still return to the original follow-up.
// Stepping back to the empty state succeeds and returns ok=false with a nil
// error; only an undo on an already-empty trail reports errNoStep.
func (h *historyStore) Undo(name string) (Step, bool, error) {
	var restored Step
	found := false
	err := h.mutate(name, func(d *ledgerData) error {
		if len(d.Current) == 0 {
			return errNoStep
		}
		leaving := d.Current[len(d.Current)-1]
		if len(d.Current) == 1 {
			// Withdrawing the only step makes it the preferred root so a redo
			// re-enters the line the operator was on.
			d.PreferredRoot = leaving
			d.Current = nil
			return nil
		}
		parent := d.Nodes[d.Current[len(d.Current)-2]]
		// Keep an existing continuation preferred. A trial recorded from an old
		// window never overwrites it, so retreating off the trial leaves the
		// original follow-up reachable by redo.
		if parent.Preferred == "" || d.Nodes[parent.Preferred] == nil || !containsString(parent.Children, parent.Preferred) {
			parent.Preferred = leaving
		}
		d.Current = d.Current[:len(d.Current)-1]
		restored, found = parent.Step, true
		return nil
	})
	return restored, found, err
}

func (h *historyStore) Redo(name string) (Step, bool, error) {
	var restored Step
	found := false
	err := h.mutate(name, func(d *ledgerData) error {
		if len(d.Current) == 0 {
			id := d.PreferredRoot
			if id == "" || d.Nodes[id] == nil {
				if len(d.Roots) == 0 {
					return errNoStep
				}
				id = d.Roots[0]
			}
			d.Current = []string{id}
			restored, found = d.Nodes[id].Step, true
			return nil
		}
		parent := d.Nodes[d.Current[len(d.Current)-1]]
		if parent == nil || len(parent.Children) == 0 {
			return errNoStep
		}
		child := parent.Preferred
		if child == "" || d.Nodes[child] == nil || !containsString(parent.Children, child) {
			child = parent.Children[0]
		}
		d.Current = append(d.Current, child)
		restored, found = d.Nodes[child].Step, true
		return nil
	})
	return restored, found, err
}

// Focus moves the review cursor onto an explicit step without discarding any
// other route. It is how the operator resumes from a step that is still
// applicable on the current scan bytes.
func (h *historyStore) Focus(name, id string) (Step, bool, error) {
	var restored Step
	err := h.mutate(name, func(d *ledgerData) error {
		path := d.pathTo(id)
		if len(path) == 0 {
			return errUnknownEntry
		}
		for i := 1; i < len(path); i++ {
			d.Nodes[path[i-1]].Preferred = path[i]
		}
		if len(path) == 1 {
			d.PreferredRoot = path[0]
		}
		d.Current = path
		restored = d.Nodes[id].Step
		return nil
	})
	if err != nil {
		return Step{}, false, err
	}
	return restored, true, nil
}

func (h *historyStore) SetParcel(name, id, parcelID string) (Step, bool, error) {
	var updated Step
	found := false
	err := h.mutate(name, func(d *ledgerData) error {
		n, ok := d.Nodes[id]
		if !ok {
			return errUnknownEntry
		}
		now := time.Now().UTC()
		n.Parcel, n.FrozenAt = parcelID, &now
		updated, found = n.Step, true
		return nil
	})
	return updated, found, err
}

// trailEntry is one recorded step as shown to the lead. Origin says whether it
// can be applied to the current scan bytes or only read back from the record.
type trailEntry struct {
	Step
	Parent       string         `json:"parent,omitempty"`
	Children     []string       `json:"children,omitempty"`
	Preferred    string         `json:"preferred,omitempty"`
	OnPath       bool           `json:"on_current_route"`
	Origin       classification `json:"origin"`
	ParcelOnDisk bool           `json:"parcel_on_disk"`
}

type trailView struct {
	Entries []trailEntry `json:"entries"`
	Routes  [][]string   `json:"routes"`
	Current string       `json:"current"`
	CanUndo bool         `json:"can_undo"`
	CanRedo bool         `json:"can_redo"`
}

func entryNumber(id string) int {
	var n int
	fmt.Sscanf(id, "e%d", &n)
	return n
}

// view builds the read-only trail: every route, each step classified against
// the current scan bytes, and which step is in force. The store lock is
// released before classify and frozen run, because those consult the catalog
// and disk and must never nest inside the history lock.
func (h *historyStore) view(name string, classify func(Step) classification, frozen func(id string) bool) trailView {
	h.mu.Lock()
	h.loadLocked()
	d := h.reelLocked(name)
	view := trailView{Entries: make([]trailEntry, 0, len(d.Nodes)), Routes: [][]string{}}
	if current, ok := d.currentStep(); ok {
		view.Current = current.ID
	}
	for id, n := range d.Nodes {
		view.Entries = append(view.Entries, trailEntry{Step: n.Step, Parent: n.Parent, Children: n.Children, Preferred: n.Preferred, OnPath: d.onPath(id)})
	}
	view.Routes = d.leaves()
	view.CanUndo = len(d.Current) > 0
	if len(d.Current) == 0 {
		view.CanRedo = len(d.Roots) > 0
	} else if parent := d.Nodes[d.Current[len(d.Current)-1]]; parent != nil {
		view.CanRedo = len(parent.Children) > 0
	}
	h.mu.Unlock()
	for i := range view.Entries {
		view.Entries[i].Origin = classify(view.Entries[i].Step)
		if view.Entries[i].Parcel != "" && frozen != nil {
			view.Entries[i].ParcelOnDisk = frozen(view.Entries[i].Parcel)
		}
	}
	sort.Slice(view.Entries, func(i, j int) bool { return entryNumber(view.Entries[i].ID) < entryNumber(view.Entries[j].ID) })
	return view
}

func (h *historyStore) cached(name, environment, id string) (classification, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.cache[name+"|"+environment+"|"+id]
	return found, ok
}

func (h *historyStore) remember(name, environment, id string, found classification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[name+"|"+environment+"|"+id] = found
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
