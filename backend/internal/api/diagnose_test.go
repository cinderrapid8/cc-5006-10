package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
	"highlight-assembly/internal/store"
	"highlight-assembly/internal/trace"
)

// newTraceServer builds a server whose renderer points at nonexistent
// binaries: diagnostic tests never invoke ffmpeg because keyframes and frame
// rate are supplied in the request body.
func newTraceServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rend := &media.Renderer{FFmpeg: "/nonexistent/ffmpeg", FFprobe: "/nonexistent/ffprobe"}
	return New(st, rend)
}

// seedDiagnoseProject writes a project directly to the store so no video probe
// is needed. The fixture contains a contained overlap and a disjoint window.
func seedDiagnoseProject(t *testing.T, srv *Server) *domain.Project {
	t.Helper()
	anchors := []domain.Anchor{
		{ID: "A1", Clock: 0, VideoTime: 0},
		{ID: "A2", Clock: 60, VideoTime: 60},
	}
	cm, err := domain.NewClockMap(anchors)
	if err != nil {
		t.Fatal(err)
	}
	meta := domain.VideoMeta{DurationSec: 60, AvailableStart: 0, AvailableEnd: 60}
	events := []domain.Event{
		{EventID: "E1", Type: "goal", Clock: 10, Occurrence: 0}, // [5,15)
		{EventID: "E2", Type: "shot", Clock: 12, Occurrence: 0}, // [10,14) contained
		{EventID: "E3", Type: "foul", Clock: 20, Occurrence: 0}, // [18,22)
	}
	pads := map[string]domain.Padding{
		"goal": {BeforeSec: 5, AfterSec: 5},
		"shot": {BeforeSec: 2, AfterSec: 2},
		"foul": {BeforeSec: 2, AfterSec: 2},
	}
	segs, _ := domain.BuildSegments(events, cm, meta, pads)
	blocks := domain.BuildBlocks(segs, domain.PolicyMerge)
	order := make([]string, len(blocks))
	for i, b := range blocks {
		order[i] = b.ID
	}
	p := &domain.Project{
		ID: "PTRACE", Name: "diag", VideoPath: "/nonexistent/video.mp4", VideoMeta: meta,
		Anchors: anchors, Events: events, Paddings: pads, OverlapPolicy: domain.PolicyMerge,
		Segments: segs, Blocks: blocks, BlockOrder: order,
	}
	if err := srv.Store.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func diagnoseRequestBody() map[string]any {
	return map[string]any{
		"cutMode":       media.CutKeyframe,
		"frameRate":     30,
		"keyframeTimes": []float64{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22},
	}
}

func TestDiagnoseEndpointAttributesStages(t *testing.T) {
	srv := newTraceServer(t)
	p := seedDiagnoseProject(t, srv)

	code, out := doJSON(t, srv.Mux, http.MethodPost, "/api/projects/"+p.ID+"/diagnose", diagnoseRequestBody())
	if code != 200 {
		t.Fatalf("diagnose status = %d: %v", code, out)
	}
	var resp diagnoseResponse
	raw, _ := json.Marshal(out)
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode diagnosis: %v", err)
	}
	if len(resp.Trace.Stages) == 0 {
		t.Fatal("trace must record pipeline stages")
	}
	// Every reported anomaly must name one of the known processing stages.
	valid := map[string]bool{
		domain.StageInput: true, domain.StageMapping: true, domain.StageSegments: true,
		domain.StageMerge: true, domain.StageTimeline: true, domain.StageCut: true,
	}
	for _, tl := range resp.Diagnosis.TailLosses {
		if !valid[tl.RootStage] {
			t.Errorf("tail loss without valid stage: %+v", tl)
		}
	}
	var sawMergeDupe bool
	for _, dc := range resp.Diagnosis.DuplicateCandidates {
		if !valid[dc.RootStage] {
			t.Errorf("duplicate without valid stage: %+v", dc)
		}
		if dc.RootStage == domain.StageMerge && dc.OverlapSec > 0 {
			sawMergeDupe = true
		}
	}
	if !sawMergeDupe {
		t.Errorf("expected merge-stage overlap duplicate: %+v", resp.Diagnosis.DuplicateCandidates)
	}
	// Cut evidence must carry concrete frame indices (actual frame order).
	if len(resp.Trace.Cuts) == 0 {
		t.Fatal("diagnose must record cut evidence")
	}
	for _, c := range resp.Trace.Cuts {
		if c.StartFrame < 0 || c.EndFrame < 0 {
			t.Errorf("cut evidence missing frame indices: %+v", c)
		}
	}
}

func TestDiagnoseDoesNotMutateSavedProject(t *testing.T) {
	srv := newTraceServer(t)
	p := seedDiagnoseProject(t, srv)
	before, err := srv.Store.LoadProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}

	code, _ := doJSON(t, srv.Mux, http.MethodPost, "/api/projects/"+p.ID+"/diagnose",
		map[string]any{"cutMode": media.CutReencode, "frameRate": 30})
	if code != 200 {
		t.Fatalf("diagnose status = %d", code)
	}
	after, err := srv.Store.LoadProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version {
		t.Errorf("diagnose changed project version: before=%d after=%d", before.Version, after.Version)
	}
	if len(after.Segments) != len(before.Segments) || len(after.Blocks) != len(before.Blocks) {
		t.Fatal("diagnose altered the saved selection")
	}
	for i := range before.Blocks {
		b1, b2 := before.Blocks[i], after.Blocks[i]
		if b1.Duration != b2.Duration || len(b1.Pieces) != len(b2.Pieces) {
			t.Fatal("diagnose changed block geometry")
		}
	}
}

func TestDiagnoseReplayIsReplayableWithSameEvidence(t *testing.T) {
	srv := newTraceServer(t)
	p := seedDiagnoseProject(t, srv)
	req := diagnoseRequestBody()
	req["save"] = true

	code, out := doJSON(t, srv.Mux, http.MethodPost, "/api/projects/"+p.ID+"/diagnose", req)
	if code != 200 {
		t.Fatalf("diagnose status = %d: %v", code, out)
	}
	id := mustGet[string](t, out, "diagnosticId")
	if id == "" {
		t.Fatal("saved diagnostic must return an id")
	}
	httpReq := httptest.NewRequest(http.MethodGet, "/api/diagnostics/"+id, nil)
	rec := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rec, httpReq)
	if rec.Code != 200 {
		t.Fatalf("get diagnostic = %d", rec.Code)
	}

	// Re-running with identical inputs yields identical cut evidence.
	code2, out2 := doJSON(t, srv.Mux, http.MethodPost, "/api/projects/"+p.ID+"/diagnose", req)
	if code2 != 200 {
		t.Fatalf("second diagnose status = %d", code2)
	}
	decode := func(m map[string]any) diagnoseResponse {
		raw, _ := json.Marshal(m)
		var r diagnoseResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return r
	}
	r1, r2 := decode(out), decode(out2)
	if len(r1.Trace.Cuts) != len(r2.Trace.Cuts) {
		t.Fatal("replay cut evidence length differs")
	}
	for i := range r1.Trace.Cuts {
		if !reflect.DeepEqual(r1.Trace.Cuts[i], r2.Trace.Cuts[i]) {
			t.Fatalf("replay cut %d differs:\n%+v\n%+v", i, r1.Trace.Cuts[i], r2.Trace.Cuts[i])
		}
	}
}

func TestDiagnoseRejectsUnknownCutMode(t *testing.T) {
	srv := newTraceServer(t)
	p := seedDiagnoseProject(t, srv)
	code, _ := doJSON(t, srv.Mux, http.MethodPost, "/api/projects/"+p.ID+"/diagnose",
		map[string]any{"cutMode": "bogus"})
	if code != 400 {
		t.Fatalf("want 400 for unknown cut mode, got %d", code)
	}
}

func TestSnapshotFromProjectIsReadOnly(t *testing.T) {
	srv := newTraceServer(t)
	p := seedDiagnoseProject(t, srv)
	snap := trace.SnapshotFromProject(p, media.CutReencode, 30, nil)
	snap.Events[0].Clock = 999
	if p.Events[0].Clock == 999 {
		t.Fatal("snapshot must be a deep copy, not an alias into the project")
	}
}
