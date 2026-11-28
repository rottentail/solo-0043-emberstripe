package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

// makeParcel writes a fully valid parcel: a one-frame window with a coverage
// partition and matching PCM (zeros where coverage is unreadable).
func makeParcel(t *testing.T, store parcel.Store, captureName string, rows, fps, lead, start int, coverage []register.Run, nonzeroSamples map[int][2]int16) string {
	t.Helper()
	end := start + 1
	idx := parcel.Index{
		Version: 1, Capture: captureName,
		Source: strings.Repeat("a", 64),
		Window: register.Window{Start: start, End: end},
		Rows:   rows, FPS: fps, Lead: lead,
		Coverage: coverage,
	}
	idx.Pictures = []parcel.Picture{{Frame: start, Sample: 0, Sheet: strings.Repeat("b", 64)}}
	samples := make([]int16, 2*rows)
	for frame, stereo := range nonzeroSamples {
		samples[frame*2] = stereo[0]
		samples[frame*2+1] = stereo[1]
	}
	receipt, err := store.Publish(idx, wave.Encode(rows*fps, samples))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return receipt.ID
}

func readableOnly(start, rows int) []register.Run {
	base := start * rows
	return []register.Run{{Start: base, End: base + rows, Status: register.Readable}}
}

func newService(t *testing.T) (*Service, parcel.Store) {
	t.Helper()
	dir := t.TempDir()
	store := parcel.Store{Root: dir}
	svc := &Service{Store: store}
	return svc, store
}

func TestSearchRanksAudibleFirstAndPaginatesStably(t *testing.T) {
	svc, store := newService(t)
	rows := 2000
	// Three parcels: one audible for picture frame 0 row 10, one whose
	// window does not cover it, one that only offers a padded time range.
	audibleID := makeParcel(t, store, "reel-a", rows, 24, 0, 0, readableOnly(0, rows),
		map[int][2]int16{500: {1200, -900}})
	makeParcel(t, store, "reel-b", rows, 24, 0, 5, readableOnly(5, rows), nil)
	paddedID := makeParcel(t, store, "reel-c", rows, 24, 0, 0,
		[]register.Run{
			{Start: 0, End: 1000, Status: register.Missing},
			{Start: 1000, End: rows, Status: register.Readable},
		}, nil)

	q := Query{Mode: AddressPicture, Frame: 0, Row: 500}
	page, err := svc.SearchPage(PageRequest{Query: q})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(page.Items))
	}
	if page.Items[0].Finding.ParcelID != audibleID || page.Items[0].Finding.Verdict != Audible {
		t.Fatalf("first item = %s %s, want audible %s", page.Items[0].Finding.ParcelID, page.Items[0].Finding.Verdict, audibleID)
	}
	if page.Items[1].Finding.ParcelID != paddedID || page.Items[1].Finding.Verdict != TimeRange {
		t.Fatalf("second item verdict = %s, want time_range", page.Items[1].Finding.Verdict)
	}
	if page.Items[2].Finding.Verdict != Unverifiable {
		t.Fatalf("third item verdict = %s, want unverifiable", page.Items[2].Finding.Verdict)
	}
	if page.Totals.Audible != 1 || page.Totals.TimeRange != 1 || page.Totals.Unverifiable != 1 {
		t.Fatalf("totals = %+v", page.Totals)
	}
}

func TestSnapshotFreezesListingAgainstMidPagingPublish(t *testing.T) {
	svc, store := newService(t)
	rows := 2000
	// Create one more parcel than a page holds so the snapshot spans pages.
	frozen := make([]string, 0, PageSize+1)
	for i := 0; i <= PageSize; i++ {
		frozen = append(frozen, makeParcel(t, store, "reel-a", rows, 24, 0, i, readableOnly(i, rows), nil))
	}

	q := Query{Mode: AddressPicture, Frame: 0, Row: 0}
	first, err := svc.SearchPage(PageRequest{Query: q})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != PageSize || first.NextToken == "" {
		t.Fatalf("first page items = %d next = %q", len(first.Items), first.NextToken)
	}

	// A same-named parcel published mid-paging must not enter the snapshot.
	intruder := makeParcel(t, store, "reel-a", rows, 24, 0, PageSize+1, readableOnly(PageSize+1, rows), nil)
	second, err := svc.SearchPage(PageRequest{Query: q, Token: first.Token})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 {
		t.Fatalf("second page = %d items, want 1", len(second.Items))
	}
	tailID := second.Items[0].Finding.ParcelID
	if !contains(frozen, tailID) || tailID == intruder {
		t.Fatalf("second page tail %s is not part of the frozen snapshot", tailID)
	}
	if !second.Exhausted {
		t.Fatal("snapshot should be exhausted after its frozen tail is served")
	}

	// A fresh search sees the parcel published after the snapshot.
	var freshIDs []string
	token := ""
	for {
		fresh, err := svc.SearchPage(PageRequest{Query: q, Token: token})
		if err != nil {
			t.Fatalf("fresh search: %v", err)
		}
		freshIDs = append(freshIDs, collectIDs(fresh)...)
		if fresh.Exhausted {
			break
		}
		token = fresh.NextToken
	}
	if len(freshIDs) != PageSize+2 {
		t.Fatalf("fresh snapshot should hold %d parcels, got %d", PageSize+2, len(freshIDs))
	}
	if !contains(freshIDs, intruder) {
		t.Fatal("fresh search must admit the parcel published after the snapshot")
	}
}

func TestDetailRejectsReplacedFileInsteadOfInheritingPass(t *testing.T) {
	svc, store := newService(t)
	rows := 2000
	id := makeParcel(t, store, "reel-a", rows, 24, 0, 0, readableOnly(0, rows),
		map[int][2]int16{500: {700, 700}})
	q := Query{Mode: AddressPicture, Frame: 0, Row: 500}

	item, pcm, err := svc.Detail(id, q)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if item.Finding.Verdict != Audible || item.Finding.NonZeroPCM == nil || !*item.Finding.NonZeroPCM {
		t.Fatalf("finding = %+v", item.Finding)
	}
	if sample, ok := parcel.SampleAt(pcm, *item.Finding.SampleFrame); !ok || sample != ([2]int16{700, 700}) {
		t.Fatalf("pcm frame = %v", sample)
	}

	// Swap the file's bytes while keeping the same content-addressed name.
	// Read must reject it on digest mismatch, so no stale pass survives.
	path := filepath.Join(store.Root, id+".zip")
	tampered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered = append(tampered, 0)
	if err := os.WriteFile(path, tampered, 0640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Detail(id, q); err == nil {
		t.Fatal("replaced parcel must fail re-verification, not inherit the old pass")
	}
	// The listing keeps the verified first screen and counts the failure
	// instead of presenting the swapped file with another parcel's verdict.
	page, err := svc.SearchPage(PageRequest{Query: q})
	if err != nil {
		t.Fatalf("listing should degrade with a failure count, got err %v", err)
	}
	for _, listed := range page.Items {
		if listed.Finding.ParcelID == id {
			t.Fatal("tampered parcel must not appear among verified items")
		}
	}
	if page.Totals.Failed != 1 {
		t.Fatalf("failed integrity count = %d, want 1", page.Totals.Failed)
	}
}

func TestDetailPaddedFrameIsZeroAndMarkedTimeRange(t *testing.T) {
	svc, store := newService(t)
	rows := 2000
	id := makeParcel(t, store, "reel-a", rows, 24, 0, 0,
		[]register.Run{
			{Start: 0, End: 1000, Status: register.Missing},
			{Start: 1000, End: rows, Status: register.Readable},
		}, map[int][2]int16{1500: {300, 300}})

	padded, _, err := svc.Detail(id, Query{Mode: AddressPicture, Frame: 0, Row: 500})
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if padded.Finding.Verdict != TimeRange || padded.Finding.NonZeroPCM == nil || *padded.Finding.NonZeroPCM {
		t.Fatalf("padded finding = %+v", padded.Finding)
	}
	audible, _, err := svc.Detail(id, Query{Mode: AddressPicture, Frame: 0, Row: 1500})
	if err != nil || audible.Finding.Verdict != Audible || !*audible.Finding.NonZeroPCM {
		t.Fatalf("audible finding = %+v err = %v", audible.Finding, err)
	}
}

func TestConcurrentPagingClaimsEachParcelOnce(t *testing.T) {
	svc, store := newService(t)
	rows := 2000
	const total = 40
	for i := 0; i < total; i++ {
		makeParcel(t, store, "reel-a", rows, 24, 0, i, readableOnly(i, rows), nil)
	}
	q := Query{Mode: AddressPicture, Frame: 0, Row: 0}
	first, err := svc.SearchPage(PageRequest{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	token := first.Token

	// Seed the union with the first screen, which was claimed before the
	// concurrent workers start paging with the same frozen token.
	var mu sync.Mutex
	seen := map[string]int{}
	for _, item := range first.Items {
		seen[item.Finding.ParcelID]++
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A page can return before the snapshot is exhausted when the
			// per-request scan budget is spent, so keep paging until
			// Exhausted is actually reported.
			for round := 0; round < total; round++ {
				page, err := svc.SearchPage(PageRequest{Query: q, Token: token})
				if err != nil {
					t.Errorf("paging: %v", err)
					return
				}
				mu.Lock()
				for _, item := range page.Items {
					seen[item.Finding.ParcelID]++
				}
				mu.Unlock()
				if page.Exhausted {
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(seen) != total {
		t.Fatalf("concurrent paging evaluated %d unique parcels, want %d", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("parcel %s evaluated %d times, want exactly once across all requests", id, count)
		}
	}
}

func TestSnapshotExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	svc, store := newService(t)
	svc.Now = func() time.Time { return now }
	makeParcel(t, store, "reel-a", 2000, 24, 0, 0, readableOnly(0, 2000), nil)

	page, err := svc.SearchPage(PageRequest{Query: Query{Mode: AddressPicture, Frame: 0, Row: 0}})
	if err != nil {
		t.Fatal(err)
	}
	svc.Now = func() time.Time { return now.Add(snapshotTTL + time.Second) }
	if _, err := svc.SearchPage(PageRequest{
		Query: Query{Mode: AddressPicture, Frame: 0, Row: 0}, Token: page.Token,
	}); err != ErrSnapshot {
		t.Fatalf("expired snapshot err = %v, want ErrSnapshot", err)
	}
}

func collectIDs(page Page) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Finding.ParcelID)
	}
	return ids
}

func contains(set []string, target string) bool {
	for _, s := range set {
		if s == target {
			return true
		}
	}
	return false
}
