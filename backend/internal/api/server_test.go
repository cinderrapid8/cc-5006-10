package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
	"highlight-assembly/internal/store"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	ff := envOr("FFMPEG_BIN", "ffmpeg")
	fp := envOr("FFPROBE_BIN", "ffprobe")
	if _, err := os.Stat(ff); err != nil && ff == "ffmpeg" {
		t.Skip("ffmpeg not available")
	}
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rend := &media.Renderer{FFmpeg: ff, FFprobe: fp}
	return New(st, rend), st.ExportDir()
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func fixtureVideo(t *testing.T) string {
	t.Helper()
	p := os.Getenv("TEST_VIDEO")
	if p == "" {
		p = filepath.Join("..", "..", "..", "testdata", "match60.mp4")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("test video missing at %s", p)
	}
	abs, _ := filepath.Abs(p)
	return abs
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func mustGet[T any](t *testing.T, m map[string]any, key string) T {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("missing key %q in %v", key, m)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var typed T
	if err := json.Unmarshal(out, &typed); err != nil {
		t.Fatalf("key %q: %v", key, err)
	}
	return typed
}

// fullScenario builds one project exercising every requirement at once.
func fullScenario(video string) createRequest {
	return createRequest{
		Name:      "e2e",
		VideoPath: video,
		// Record clock 0s -> video 0s; record clock 60s -> video 60s. Mapping
		// still flows through explicit anchors; equal numbers are not assumed.
		RawAnchors: []rawAnchor{
			{ID: "A1", Label: "kickoff", Clock: "0", Video: "0"},
			{ID: "A2", Label: "fulltime", Clock: "60", Video: "60"},
		},
		RawEvents: []rawEvent{
			{EventID: "E1", Type: "goal", Clock: "10", Detail: "opener"},
			{EventID: "E2", Type: "shot", Clock: "14", Detail: "overlaps E1 window"},
			{EventID: "E9", Type: "foul", Clock: "22", Detail: "first E9"},
			{EventID: "E9", Type: "foul", Clock: "24", Detail: "repeated number"},
			{EventID: "E5", Type: "goal", Clock: "58", Detail: "short end"},
			{EventID: "E6", Type: "goal", Clock: "90", Detail: "outside anchors"},
		},
		Paddings: map[string]domain.Padding{
			"goal": {BeforeSec: 5, AfterSec: 5},
			"shot": {BeforeSec: 3, AfterSec: 3},
			"foul": {BeforeSec: 2, AfterSec: 2},
		},
		OverlapPolicy: domain.PolicyMerge,
		// Gap inside the E9 windows.
		Gaps: []domain.Range{{Start: 20, End: 23}},
	}
}

func TestEndToEndMappingOverlapGapsAndPending(t *testing.T) {
	srv, _ := newTestServer(t)
	video := fixtureVideo(t)

	code, body := doJSON(t, srv.Mux, http.MethodPost, "/api/projects", fullScenario(video))
	if code != http.StatusCreated {
		t.Fatalf("create project: %d %v", code, body)
	}
	pid := mustGet[string](t, body, "id")

	// 5 mapped events, 1 unmapped (E6 at 90s beyond anchor coverage).
	unmapped := mustGet[[]map[string]any](t, body, "unmapped")
	if len(unmapped) != 1 {
		t.Fatalf("want 1 unmapped event, got %d", len(unmapped))
	}
	ev := unmapped[0]["event"].(map[string]any)
	if ev["eventId"] != "E6" || unmapped[0]["reason"] == "" {
		t.Errorf("unmapped entry = %v", unmapped[0])
	}

	segments := mustGet[[]map[string]any](t, body, "segments")
	if len(segments) != 5 {
		t.Fatalf("want 5 candidate segments, got %d", len(segments))
	}

	blocks := mustGet[[]map[string]any](t, body, "blocks")
	// E1 [5,15) and E2 [11,17) overlap -> merged; two E9 [20,24)/[22,26) overlap
	// -> merged; E5 alone. Expect 3 blocks.
	if len(blocks) != 3 {
		t.Fatalf("want 3 merged blocks, got %d: %v", len(blocks), blocks)
	}

	// Merged block keeps every event reference (no dedup-by-number).
	var e9block map[string]any
	for _, b := range blocks {
		keys, _ := json.Marshal(b["segmentKeys"])
		if bytes.Contains(keys, []byte("E9")) {
			e9block = b
		}
	}
	if e9block == nil {
		t.Fatal("E9 block not found")
	}
	refs := e9block["refs"].([]any)
	if len(refs) != 2 {
		t.Fatalf("repeated E9 must keep 2 refs, got %d", len(refs))
	}
	if e9block["crossedGap"] != true {
		t.Errorf("E9 block must flag crossed gap: %v", e9block)
	}
	// Gap [20,23) removed from merged window: first piece starts at 23.
	pieces := e9block["pieces"].([]any)
	if len(pieces) != 1 {
		t.Errorf("E9 pieces = %v", pieces)
	}
	first := pieces[0].(map[string]any)
	if first["start"].(float64) != 23 {
		t.Errorf("E9 merged piece should start after gap at 23: %v", first)
	}

	// E5 short end must be flagged and not reported complete.
	var e5block map[string]any
	for _, b := range blocks {
		keys, _ := json.Marshal(b["segmentKeys"])
		if bytes.Contains(keys, []byte("E5")) {
			e5block = b
		}
	}
	if e5block == nil || e5block["shortEnd"] != true {
		t.Fatalf("E5 short-end flag missing: %v", e5block)
	}

	// Project can be reloaded from disk (saveable editing document).
	code, saved := doJSON(t, srv.Mux, http.MethodGet, "/api/projects/"+pid, nil)
	if code != 200 || mustGet[string](t, saved, "id") != pid {
		t.Fatalf("reload project failed: %d", code)
	}
}

func TestSeparatePolicyKeepsOverlapsIndependent(t *testing.T) {
	srv, _ := newTestServer(t)
	req := fullScenario(fixtureVideo(t))
	req.OverlapPolicy = domain.PolicySeparate
	code, body := doJSON(t, srv.Mux, http.MethodPost, "/api/projects", req)
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	blocks := mustGet[[]map[string]any](t, body, "blocks")
	// One block per mapped segment; the gapped E9 stays flagged and excluded
	// from export only when it has no renderable footage at all.
	if len(blocks) != 5 {
		t.Errorf("separate policy should keep 5 blocks, got %d", len(blocks))
	}
	gapCount := 0
	for _, b := range blocks {
		if b["crossedGap"] == true {
			gapCount++
		}
	}
	if gapCount != 2 {
		t.Errorf("want both E9 occurrences flagged as gap-crossing, got %d", gapCount)
	}
}

func TestReorderAndExcludeThenExportManifest(t *testing.T) {
	srv, exportDir := newTestServer(t)
	video := fixtureVideo(t)
	code, body := doJSON(t, srv.Mux, http.MethodPost, "/api/projects", fullScenario(video))
	if code != 201 {
		t.Fatal(body)
	}
	pid := mustGet[string](t, body, "id")
	blocks := mustGet[[]map[string]any](t, body, "blocks")
	order := mustGet[[]string](t, body, "blockOrder")
	if len(order) != len(blocks) {
		t.Fatal("block order length mismatch")
	}
	// Reverse timeline order and exclude the block now last in the order.
	rev := make([]string, len(order))
	for i, id := range order {
		rev[len(order)-1-i] = id
	}
	excluded := rev[len(rev)-1]
	inc := map[string]bool{excluded: false}
	code, body = doJSON(t, srv.Mux, http.MethodPut, "/api/projects/"+pid,
		updateRequest{BlockOrder: rev, Included: inc})
	if code != 200 {
		t.Fatalf("update: %d %v", code, body)
	}
	if got := mustGet[[]string](t, body, "blockOrder"); got[0] != rev[0] {
		t.Errorf("order not persisted: %v", got)
	}

	// Export re-encode and poll to completion.
	code, body = doJSON(t, srv.Mux, http.MethodPost, "/api/exports",
		exportRequest{ProjectID: pid, CutMode: media.CutReencode})
	if code != 202 {
		t.Fatalf("export start: %d %v", code, body)
	}
	jobID := mustGet[string](t, body, "jobId")

	manifest := waitJob(t, srv, jobID, 15*time.Second)
	if manifest["cancelled"] != false {
		t.Errorf("job should not be cancelled: %v", manifest)
	}
	mfBlocks := manifest["blocks"].([]any)
	if len(mfBlocks) != 2 {
		t.Fatalf("2 included blocks expected, manifest has %d", len(mfBlocks))
	}
	// Manifest order must follow the reversed timeline.
	first := mfBlocks[0].(map[string]any)["blockId"].(string)
	if first != rev[0] {
		t.Errorf("export order = %s, want %s", first, rev[0])
	}
	out := filepath.Join(exportDir, jobID+".mp4")
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	info, err := media.Probe(context.Background(), srv.Renderer.FFprobe, out)
	if err != nil || info.DurationSec <= 0 {
		t.Fatalf("probing export: %v", err)
	}
}

func TestExportCancellationMidway(t *testing.T) {
	srv, exportDir := newTestServer(t)
	video := fixtureVideo(t)
	req := fullScenario(video)
	req.OverlapPolicy = domain.PolicySeparate // 5 independent blocks => more ffmpeg calls
	code, body := doJSON(t, srv.Mux, http.MethodPost, "/api/projects", req)
	if code != 201 {
		t.Fatal(body)
	}
	pid := mustGet[string](t, body, "id")

	code, body = doJSON(t, srv.Mux, http.MethodPost, "/api/exports",
		exportRequest{ProjectID: pid, CutMode: media.CutReencode})
	if code != 202 {
		t.Fatal(body)
	}
	jobID := mustGet[string](t, body, "jobId")

	// Cancel immediately, then verify the job reports cancellation and no file remains.
	time.Sleep(100 * time.Millisecond)
	code, _ = doJSON(t, srv.Mux, http.MethodPost, "/api/exports/"+jobID+"/cancel", nil)
	if code != 202 {
		t.Fatalf("cancel status = %d", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	var status map[string]any
	for time.Now().Before(deadline) {
		code, status = doJSON(t, srv.Mux, http.MethodGet, "/api/exports/"+jobID+"/status", nil)
		if status["done"] == true {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status["error"] != "cancelled" {
		t.Fatalf("expected cancelled error, got %v", status)
	}
	out := filepath.Join(exportDir, jobID+".mp4")
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("cancelled export must leave no output, got err=%v", err)
	}
}

func TestProjectWithTooFewAnchorsRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	req := fullScenario(fixtureVideo(t))
	req.RawAnchors = req.RawAnchors[:1]
	code, body := doJSON(t, srv.Mux, http.MethodPost, "/api/projects", req)
	if code != 400 {
		t.Fatalf("want 400 for single anchor, got %d %v", code, body)
	}
}

func waitJob(t *testing.T, srv *Server, jobID string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		code, status := doJSON(t, srv.Mux, http.MethodGet, "/api/exports/"+jobID+"/status", nil)
		if code != 200 {
			t.Fatalf("status poll %d", code)
		}
		if status["done"] == true {
			if e, ok := status["error"]; ok {
				t.Fatalf("job failed: %v", e)
			}
			// Fetch manifest document.
			req := httptest.NewRequest(http.MethodGet, "/api/exports/"+jobID+"/manifest", nil)
			rec := httptest.NewRecorder()
			srv.Mux.ServeHTTP(rec, req)
			var mf map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &mf); err != nil {
				t.Fatal(err)
			}
			return mf
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job did not finish in time")
	return nil
}
