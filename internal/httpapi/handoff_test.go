package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rottentail/solo-0043-emberstripe/internal/handoff"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

func publishHTTPParcel(t *testing.T, store parcel.Store, name string, start, rows int) string {
	t.Helper()
	idx := parcel.Index{
		Version: 1, Capture: name, Source: strings.Repeat("a", 64),
		Window: register.Window{Start: start, End: start + 1},
		Rows:   rows, FPS: 24, Lead: 230,
		Coverage: []register.Run{{Start: start * rows, End: (start + 1) * rows, Status: register.Readable}},
	}
	idx.Pictures = []parcel.Picture{{Frame: start, Sample: 0, Sheet: strings.Repeat("b", 64)}}
	receipt, err := store.Publish(idx, wave.Encode(rows*24, make([]int16, 2*rows)))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return receipt.ID
}

func newHandoffServer(t *testing.T) (http.Handler, parcel.Store, *handoff.Service) {
	t.Helper()
	store := parcel.Store{Root: t.TempDir()}
	svc := &handoff.Service{Store: store}
	s := &station.Station{Store: store, Handoff: svc}
	return New(s), store, svc
}

func decodePage(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode page: %v body=%s", err, body)
	}
	return p
}

func TestHTTPHandoffSearchLocateAudition(t *testing.T) {
	handler, store, _ := newHandoffServer(t)
	const rows = 2000
	id := publishHTTPParcel(t, store, "reel-a", 0, rows)

	// Search by picture address.
	req := httptest.NewRequest(http.MethodGet, "/handoff/parcels?mode=picture&frame=0&row=10", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", rec.Code, rec.Body.String())
	}
	page := decodePage(t, rec.Body.Bytes())
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	finding := items[0].(map[string]any)["finding"].(map[string]any)
	if finding["parcel_id"] != id || finding["verdict"] != "audible" {
		t.Fatalf("finding = %+v", finding)
	}

	// Locate re-verifies and attaches the PCM frame.
	req = httptest.NewRequest(http.MethodGet, "/handoff/parcels/"+id+"/locate?mode=picture&frame=0&row=10", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("locate status = %d", rec.Code)
	}
	detail := decodePage(t, rec.Body.Bytes())
	if detail["finding"].(map[string]any)["pcm_nonzero"] != false {
		t.Fatalf("silent readable frame should report pcm_nonzero=false: %+v", detail["finding"])
	}

	// Audition streams the frozen WAV with locating headers.
	req = httptest.NewRequest(http.MethodGet, "/handoff/parcels/"+id+"/audition?mode=picture&frame=0&row=10", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("audition status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Parcel-Verdict") != "audible" || rec.Header().Get("X-Parcel-Sample-Frame") != "10" {
		t.Fatalf("audition headers = %+v", rec.Header())
	}
	if !strings.HasPrefix(rec.Body.String(), "RIFF") {
		t.Fatal("audition body is not a WAV container")
	}
}

func TestHTTPHandoffRejectsBadInputAndUnknownParcel(t *testing.T) {
	handler, _, _ := newHandoffServer(t)

	for _, path := range []string{
		"/handoff/parcels?mode=bogus&frame=0&row=1",
		"/handoff/parcels?mode=picture&frame=-1&row=1",
		"/handoff/parcels?mode=picture&frame=0",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s -> %d, want 400", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/handoff/parcels/"+strings.Repeat("f", 64)+"/locate?mode=picture&frame=0&row=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown parcel locate -> %d, want 404", rec.Code)
	}
}

func TestHTTPHandoffSnapshotTokenKeepsMidPagingPublishOut(t *testing.T) {
	handler, store, _ := newHandoffServer(t)
	const rows = 2000
	// Two parcels are enough to obtain a token; the snapshot must freeze both.
	first := publishHTTPParcel(t, store, "reel-a", 0, rows)
	second := publishHTTPParcel(t, store, "reel-a", 1, rows)

	req := httptest.NewRequest(http.MethodGet, "/handoff/parcels?mode=picture&frame=0&row=0", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	page := decodePage(t, rec.Body.Bytes())
	token := page["snapshot_token"].(string)
	ids := map[string]bool{}
	for _, item := range page["items"].([]any) {
		ids[item.(map[string]any)["finding"].(map[string]any)["parcel_id"].(string)] = true
	}
	if !ids[first] || !ids[second] {
		t.Fatalf("initial snapshot missing parcels: %v", ids)
	}

	// A same-named parcel published after the snapshot is taken.
	intruder := publishHTTPParcel(t, store, "reel-a", 2, rows)

	// Replaying the frozen token must still only see the original two ids.
	req = httptest.NewRequest(http.MethodGet, "/handoff/parcels?mode=picture&frame=0&row=0&snapshot="+token, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot replay status = %d", rec.Code)
	}
	frozen := decodePage(t, rec.Body.Bytes())
	for _, item := range frozen["items"].([]any) {
		if id := item.(map[string]any)["finding"].(map[string]any)["parcel_id"].(string); id == intruder {
			t.Fatal("mid-paging parcel entered the frozen snapshot")
		}
	}

	// A fresh search (no token) admits the new parcel.
	req = httptest.NewRequest(http.MethodGet, "/handoff/parcels?mode=picture&frame=0&row=0", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	fresh := decodePage(t, rec.Body.Bytes())
	sawIntruder := false
	for _, item := range fresh["items"].([]any) {
		if item.(map[string]any)["finding"].(map[string]any)["parcel_id"].(string) == intruder {
			sawIntruder = true
		}
	}
	if !sawIntruder {
		t.Fatal("fresh search must admit the parcel published after the snapshot")
	}
}
