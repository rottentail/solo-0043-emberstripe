package handoff

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
)

const (
	// PageSize bounds how many findings one response carries.
	PageSize = 25
	// scanBudget bounds how many on-disk parcels a single request opens so
	// the first screen stays responsive even with thousands of parcels.
	scanBudget = 80
	// snapshotTTL keeps frozen result sets alive long enough for an operator
	// to page through a screen without pinning catalogs forever.
	snapshotTTL = 20 * time.Minute
	// maxSnapshots bounds memory if many operators search concurrently.
	maxSnapshots = 64
)

var (
	// ErrSnapshot is returned when a pagination token is unknown or expired.
	ErrSnapshot = errors.New("handoff snapshot expired, please restart the search")
	// ErrNoParcels means the archive directory currently holds no parcels.
	ErrNoParcels = errors.New("no frozen parcels in the handoff area")
)

// Item is one fully verified parcel on a listing page. Cached rows are
// re-checked against the file's size and modification time before reuse, so
// a replaced file can never present another parcel's verdict.
type Item struct {
	Finding Finding        `json:"finding"`
	Index   parcel.Index   `json:"index"`
	Wave    parcel.Receipt `json:"receipt"`
	Size    int64          `json:"bytes"`
	Mtime   time.Time      `json:"modified_at"`
}

type cached struct {
	index   parcel.Index
	receipt parcel.Receipt
	size    int64
	mtime   time.Time
}

type snapshot struct {
	ids     []string
	cursor  int
	expires time.Time
}

// Service owns the handoff-area read model on top of the content-addressed
// parcel store.
type Service struct {
	Store parcel.Store
	Now   func() time.Time

	mu        sync.Mutex
	snapshots map[string]*snapshot
	cache     map[string]cached
}

// SearchPage returns one stable page. With an empty snapshot token the
// current directory listing is frozen first; subsequent pages replay that
// exact identifier list, so a same-named parcel published mid-paging cannot
// silently replace a row. Every returned item has passed full integrity
// verification against the bytes currently on disk.
type PageRequest struct {
	Query   Query
	Capture string
	Token   string
}

type Page struct {
	Token     string `json:"snapshot_token"`
	NextToken string `json:"next_token,omitempty"`
	Items     []Item `json:"items"`
	Totals    Totals `json:"totals"`
	Exhausted bool   `json:"exhausted"`
	Truncated bool   `json:"truncated"`
}

type Totals struct {
	Audible      int `json:"audible"`
	TimeRange    int `json:"time_range"`
	Unverifiable int `json:"unverifiable"`
	Failed       int `json:"failed_integrity"`
	Scanned      int `json:"scanned"`
	Remaining    int `json:"remaining_in_snapshot"`
}

// SearchPage executes one bounded paging pass.
func (s *Service) SearchPage(req PageRequest) (Page, error) {
	if err := req.Query.Validate(); err != nil {
		return Page{}, err
	}
	now := s.clock()
	s.mu.Lock()
	s.gcSnapshotsLocked(now)

	snap, token, err := s.resolveSnapshotLocked(req.Token, now)
	if err != nil {
		s.mu.Unlock()
		return Page{}, err
	}
	s.mu.Unlock()

	page := Page{Token: token, Items: make([]Item, 0, PageSize)}
	budget := scanBudget
	totals := Totals{}

	// Claim candidate ids in locked batches, then run the (potentially
	// expensive) verification outside the lock. Advancing the cursor only
	// while holding s.mu makes concurrent paging on the same token safe and
	// guarantees every snapshot id is evaluated exactly once.
	for len(page.Items) < PageSize && budget > 0 {
		s.mu.Lock()
		// Never claim more ids than this request can still evaluate, so a
		// batch tail can never be consumed by the cursor and then skipped.
		batch := claimLocked(snap, min(budget, PageSize-len(page.Items)))
		s.mu.Unlock()
		if len(batch) == 0 {
			break
		}
		for _, id := range batch {
			budget--
			totals.Scanned++
			item, ok := s.evaluate(id, req)
			if !ok {
				totals.Failed++
				continue
			}
			switch item.Finding.Verdict {
			case Audible:
				totals.Audible++
			case TimeRange:
				totals.TimeRange++
			default:
				totals.Unverifiable++
			}
			page.Items = append(page.Items, item)
		}
	}

	s.mu.Lock()
	snap.expires = s.clock().Add(snapshotTTL)
	totals.Remaining = len(snap.ids) - snap.cursor
	page.Exhausted = snap.cursor >= len(snap.ids)
	page.Truncated = budget == 0 && !page.Exhausted
	// Exhausted snapshots stay until their TTL so a client re-requesting the
	// last page gets a stable empty answer instead of an expiry error.
	if !page.Exhausted {
		page.NextToken = token
	}
	s.mu.Unlock()

	page.Totals = totals
	// Verified findings lead the screen: audible material first, then padded
	// time ranges, then addresses the parcels cannot substantiate.
	sort.SliceStable(page.Items, func(i, j int) bool {
		ri, rj := rank(page.Items[i].Finding.Verdict), rank(page.Items[j].Finding.Verdict)
		if ri != rj {
			return ri < rj
		}
		if page.Items[i].Index.Capture != page.Items[j].Index.Capture {
			return page.Items[i].Index.Capture < page.Items[j].Index.Capture
		}
		return page.Items[i].Finding.ParcelID < page.Items[j].Finding.ParcelID
	})
	return page, nil
}

func rank(verdict string) int {
	switch verdict {
	case Audible:
		return 0
	case TimeRange:
		return 1
	default:
		return 2
	}
}

// evaluate reads and fully verifies one parcel, reusing a cached read only
// when the file's size and mtime still match. It applies the optional capture
// filter and reports ok=false for integrity failures or filter mismatch.
func (s *Service) evaluate(id string, req PageRequest) (Item, bool) {
	size, mtime, ok := s.Store.Stat(id)
	if !ok {
		s.invalidate(id)
		return Item{}, false
	}
	entry, fresh := s.cachedEntry(id, size, mtime)
	if !fresh {
		data, err := s.Store.Read(id)
		if err != nil {
			s.invalidate(id)
			return Item{}, false
		}
		receipt, err := parcel.Verify(data)
		if err != nil {
			s.invalidate(id)
			return Item{}, false
		}
		entry = cached{receipt: receipt, index: receipt.Index, size: size, mtime: mtime}
		s.remember(id, entry)
	}
	if req.Capture != "" && entry.index.Capture != req.Capture {
		return Item{}, false
	}
	finding := Locate(entry.index, req.Query)
	finding.ParcelID = id
	return Item{Finding: finding, Index: entry.index, Wave: entry.receipt, Size: size, Mtime: mtime}, true
}

// Detail always re-verifies the current bytes for one parcel and attaches the
// actual PCM frame plus a nonzero flag. A file swapped after a listing was
// rendered fails its digest check here instead of inheriting a pass result.
func (s *Service) Detail(id string, q Query) (Item, []byte, error) {
	if err := q.Validate(); err != nil {
		return Item{}, nil, err
	}
	data, err := s.Store.Read(id)
	if err != nil {
		if os.IsNotExist(err) {
			return Item{}, nil, err
		}
		return Item{}, nil, fmt.Errorf("parcel integrity: %w", err)
	}
	receipt, err := parcel.Verify(data)
	if err != nil {
		s.invalidate(id)
		return Item{}, nil, fmt.Errorf("parcel integrity: %w", err)
	}
	pcm, err := parcel.SoundWAV(data)
	if err != nil {
		return Item{}, nil, err
	}
	size, mtime, _ := s.Store.Stat(id)
	s.remember(id, cached{receipt: receipt, index: receipt.Index, size: size, mtime: mtime})

	finding := Locate(receipt.Index, q)
	finding.ParcelID = id
	if finding.SampleFrame != nil {
		if sample, ok := parcel.SampleAt(pcm, *finding.SampleFrame); ok {
			nonZero := sample[0] != 0 || sample[1] != 0
			finding.Sample = &sample
			finding.NonZeroPCM = &nonZero
		}
	}
	return Item{Finding: finding, Index: receipt.Index, Wave: receipt, Size: size, Mtime: mtime}, pcm, nil
}

func (s *Service) resolveSnapshotLocked(token string, now time.Time) (*snapshot, string, error) {
	if s.snapshots == nil {
		s.snapshots = make(map[string]*snapshot)
	}
	if token != "" {
		snap := s.snapshots[token]
		if snap == nil || now.After(snap.expires) {
			delete(s.snapshots, token)
			return nil, "", ErrSnapshot
		}
		return snap, token, nil
	}
	ids, err := s.Store.List()
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 {
		return nil, "", ErrNoParcels
	}
	token = newToken()
	snap := &snapshot{ids: ids, expires: now.Add(snapshotTTL)}
	s.snapshots[token] = snap
	if len(s.snapshots) > maxSnapshots {
		s.evictOldestLocked()
	}
	return snap, token, nil
}

func (s *Service) gcSnapshotsLocked(now time.Time) {
	for token, snap := range s.snapshots {
		if now.After(snap.expires) {
			delete(s.snapshots, token)
		}
	}
}

// claimLocked atomically removes up to n not-yet-evaluated ids from a
// snapshot. It must run with s.mu held so concurrent page requests cannot
// claim the same id or observe a torn cursor.
func claimLocked(snap *snapshot, n int) []string {
	if n <= 0 || snap.cursor >= len(snap.ids) {
		return nil
	}
	end := min(snap.cursor+n, len(snap.ids))
	batch := snap.ids[snap.cursor:end]
	snap.cursor = end
	return batch
}

func (s *Service) evictOldestLocked() {
	var oldest string
	var when time.Time
	for token, snap := range s.snapshots {
		if oldest == "" || snap.expires.Before(when) {
			oldest, when = token, snap.expires
		}
	}
	delete(s.snapshots, oldest)
}

func (s *Service) cachedEntry(id string, size int64, mtime time.Time) (cached, bool) {
	if s.cache == nil {
		return cached{}, false
	}
	entry, ok := s.cache[id]
	if !ok || entry.size != size || !entry.mtime.Equal(mtime) {
		return cached{}, false
	}
	return entry, true
}

func (s *Service) remember(id string, entry cached) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = make(map[string]cached)
	}
	s.cache[id] = entry
}

func (s *Service) invalidate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, id)
}

func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing on a modern platform is not recoverable; fall
		// back to time-derived entropy rather than handing out empty tokens.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
