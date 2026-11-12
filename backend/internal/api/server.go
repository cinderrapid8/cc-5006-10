package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
	"highlight-assembly/internal/store"
	"highlight-assembly/internal/trace"
)

type Server struct {
	Store    *store.Store
	Renderer *media.Renderer
	Mux      *http.ServeMux
}

func New(st *store.Store, rend *media.Renderer) *Server {
	s := &Server{Store: st, Renderer: rend, Mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.Mux.HandleFunc("GET /api/health", s.health)
	s.Mux.HandleFunc("POST /api/videos", s.uploadVideo)
	s.Mux.HandleFunc("GET /api/videos/{name}", s.serveVideo)
	s.Mux.HandleFunc("POST /api/projects", s.createProject)
	s.Mux.HandleFunc("GET /api/projects", s.listProjects)
	s.Mux.HandleFunc("GET /api/projects/{id}", s.getProject)
	s.Mux.HandleFunc("PUT /api/projects/{id}", s.updateProject)
	s.Mux.HandleFunc("POST /api/projects/{id}/rebuild", s.rebuild)
	s.Mux.HandleFunc("POST /api/projects/{id}/diagnose", s.diagnose)
	s.Mux.HandleFunc("GET /api/diagnostics/{name}", s.getDiagnostic)
	s.Mux.HandleFunc("POST /api/exports", s.startExport)
	s.Mux.HandleFunc("GET /api/exports/{id}/status", s.exportStatus)
	s.Mux.HandleFunc("POST /api/exports/{id}/cancel", s.exportCancel)
	s.Mux.HandleFunc("GET /api/exports/{id}/file", s.exportFile)
	s.Mux.HandleFunc("GET /api/exports/{id}/manifest", s.exportManifest)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// uploadVideo accepts a multipart file and probes it.
func (s *Server) uploadVideo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(2 << 30); err != nil {
		writeErr(w, 400, "bad multipart form: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("video")
	if err != nil {
		writeErr(w, 400, "missing 'video' field")
		return
	}
	defer file.Close()
	name := fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(hdr.Filename))
	dst := filepath.Join(s.Store.VideoDir(), name)
	out, err := os.Create(dst)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		writeErr(w, 500, err.Error())
		return
	}
	out.Close()

	info, err := media.Probe(r.Context(), s.Renderer.FFprobe, dst)
	if err != nil {
		_ = os.Remove(dst)
		writeErr(w, 400, "ffprobe failed: "+err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"path":        dst,
		"name":        hdr.Filename,
		"durationSec": info.DurationSec,
		"width":       info.Width,
		"height":      info.Height,
		"codec":       info.Codec,
		"hasAudio":    info.HasAudio,
		"frameRate":   info.FrameRate,
	})
}

func (s *Server) serveVideo(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	http.ServeFile(w, r, filepath.Join(s.Store.VideoDir(), name))
}

// createRequest is the full project payload from the UI or a test harness.
type createRequest struct {
	Name          string                    `json:"name"`
	VideoPath     string                    `json:"videoPath"`
	RawAnchors    []rawAnchor               `json:"anchors"`
	RawEvents     []rawEvent                `json:"events"`
	Paddings      map[string]domain.Padding `json:"paddings"`
	OverlapPolicy string                    `json:"overlapPolicy"`
	Gaps          []domain.Range            `json:"gaps"`
	// Diagnose is an off-by-default observation switch. When true the request
	// additionally returns a trace report; it never changes segment selection.
	Diagnose bool `json:"diagnose"`
	// DiagnosticCutMode defaults to "reencode"; "keyframe" exercises cut-time
	// snapping evidence without requiring an actual export.
	DiagnosticCutMode string `json:"diagnosticCutMode"`
	// FrameRate is optional; when zero the probed video rate is used.
	FrameRate float64 `json:"frameRate"`
}

type rawAnchor struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Clock string `json:"clock"`
	Video string `json:"video"`
}

type rawEvent struct {
	EventID string `json:"eventId"`
	Type    string `json:"type"`
	Clock   string `json:"clock"`
	Team    string `json:"team"`
	Detail  string `json:"detail"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if req.OverlapPolicy == "" {
		req.OverlapPolicy = domain.PolicyMerge
	}
	if req.OverlapPolicy != domain.PolicyMerge && req.OverlapPolicy != domain.PolicySeparate {
		writeErr(w, 400, "overlapPolicy must be merge or separate")
		return
	}

	info, err := media.Probe(r.Context(), s.Renderer.FFprobe, req.VideoPath)
	if err != nil {
		writeErr(w, 400, "cannot probe video: "+err.Error())
		return
	}
	meta := domain.VideoMeta{
		DurationSec:    info.DurationSec,
		AvailableStart: 0,
		AvailableEnd:   info.DurationSec,
		Gaps:           req.Gaps,
	}

	anchors := make([]domain.Anchor, 0, len(req.RawAnchors))
	for _, ra := range req.RawAnchors {
		c, err := domain.ParseClock(ra.Clock)
		if err != nil {
			writeErr(w, 400, "anchor clock: "+err.Error())
			return
		}
		v, err := domain.ParseClock(ra.Video)
		if err != nil {
			writeErr(w, 400, "anchor video time: "+err.Error())
			return
		}
		id := ra.ID
		if id == "" {
			id = fmt.Sprintf("A%d", len(anchors)+1)
		}
		anchors = append(anchors, domain.Anchor{ID: id, Label: ra.Label, Clock: c, VideoTime: v})
	}
	cm, err := domain.NewClockMap(anchors)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	seen := map[string]int{}
	events := make([]domain.Event, 0, len(req.RawEvents))
	for _, re := range req.RawEvents {
		c, err := domain.ParseClock(re.Clock)
		if err != nil {
			writeErr(w, 400, "event clock: "+err.Error())
			return
		}
		occ := seen[re.EventID]
		seen[re.EventID]++
		events = append(events, domain.Event{
			EventID: re.EventID, Type: re.Type, Clock: c, ClockText: re.Clock,
			Team: re.Team, Detail: re.Detail, Occurrence: occ,
		})
	}

	segments, unmapped := domain.BuildSegments(events, cm, meta, req.Paddings)
	blocks := domain.BuildBlocks(segments, req.OverlapPolicy)
	order := make([]string, len(blocks))
	for i, b := range blocks {
		order[i] = b.ID
	}
	p := &domain.Project{
		ID: fmt.Sprintf("P%d", time.Now().UnixNano()), Name: req.Name,
		VideoPath: req.VideoPath, VideoMeta: meta, Anchors: anchors,
		Events: events, Paddings: req.Paddings, OverlapPolicy: req.OverlapPolicy,
		Segments: segments, Unmapped: unmapped, Blocks: blocks, BlockOrder: order,
	}
	if err := s.Store.SaveProject(p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !req.Diagnose {
		writeJSON(w, 201, p)
		return
	}
	// Diagnostic mode: re-run the same frozen inputs through the observable
	// pipeline and return evidence alongside the unchanged project.
	cutMode := req.DiagnosticCutMode
	if cutMode == "" {
		cutMode = media.CutReencode
	}
	frameRate := req.FrameRate
	if frameRate == 0 {
		frameRate = info.FrameRate
	}
	report, diagnosis := s.runDiagnosis(r, p, cutMode, frameRate)
	writeJSON(w, 201, map[string]any{
		"project":   p,
		"trace":     report,
		"diagnosis": diagnosis,
	})
}

// runDiagnosis rebuilds evidence from a saved project. It never writes the
// project back, so the diagnostic switch cannot alter stored selection.
func (s *Server) runDiagnosis(r *http.Request, p *domain.Project, cutMode string, frameRate float64) (domain.TraceReport, trace.Diagnosis) {
	var keyframes []float64
	if cutMode == media.CutKeyframe {
		if kf, err := s.Renderer.KeyframeTimes(r.Context(), p.VideoPath); err == nil {
			keyframes = kf
		}
	}
	snap := trace.SnapshotFromProject(p, cutMode, frameRate, keyframes)
	tr, _ := trace.Replay(snap, trace.ReplayOptions{
		CutMode: cutMode, FrameRate: frameRate, KeyframeTimes: keyframes,
	})
	tr.Finish()
	return tr.Report(), trace.Diagnose(tr.Report())
}

func (s *Server) saveDiagnostic(prefix string, report domain.TraceReport, diagnosis trace.Diagnosis) (string, error) {
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	path := filepath.Join(s.Store.DiagnosticDir(), name+".json")
	doc := map[string]any{"trace": report, "diagnosis": diagnosis}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ids, err := s.Store.ListProjects()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"projectIds": ids})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.LoadProject(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found: "+r.PathValue("id"))
		return
	}
	writeJSON(w, 200, p)
}

type updateRequest struct {
	OverlapPolicy *string                   `json:"overlapPolicy"`
	Paddings      map[string]domain.Padding `json:"paddings"`
	BlockOrder    []string                  `json:"blockOrder"`
	Included      map[string]bool           `json:"included"` // blockID -> included
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.LoadProject(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if req.OverlapPolicy != nil {
		if *req.OverlapPolicy != domain.PolicyMerge && *req.OverlapPolicy != domain.PolicySeparate {
			writeErr(w, 400, "overlapPolicy must be merge or separate")
			return
		}
		p.OverlapPolicy = *req.OverlapPolicy
	}
	if req.Paddings != nil {
		p.Paddings = req.Paddings
	}
	if req.Included != nil {
		for _, b := range p.Blocks {
			if v, ok := req.Included[b.ID]; ok {
				b.Included = v
			}
		}
	}
	if req.BlockOrder != nil {
		p.BlockOrder = req.BlockOrder
	}
	if err := s.Store.SaveProject(p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

// rebuild regenerates segments and blocks after anchor/padding/policy edits.
func (s *Server) rebuild(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.LoadProject(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found")
		return
	}
	cm, err := domain.NewClockMap(p.Anchors)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	p.Segments, p.Unmapped = domain.BuildSegments(p.Events, cm, p.VideoMeta, p.Paddings)
	p.Blocks = domain.BuildBlocks(p.Segments, p.OverlapPolicy)
	p.BlockOrder = make([]string, len(p.Blocks))
	for i, b := range p.Blocks {
		p.BlockOrder[i] = b.ID
	}
	if err := s.Store.SaveProject(p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

type diagnoseRequest struct {
	CutMode       string    `json:"cutMode"`
	FrameRate     float64   `json:"frameRate"`
	KeyframeTimes []float64 `json:"keyframeTimes"`
	// Save persists the snapshot/report under diagnostics and returns its name.
	Save bool `json:"save"`
}

type diagnoseResponse struct {
	Trace        domain.TraceReport `json:"trace"`
	Diagnosis    trace.Diagnosis    `json:"diagnosis"`
	DiagnosticID string             `json:"diagnosticId,omitempty"`
}

// diagnose replays a stored project's full chain and returns attributed
// evidence. It is read-only against the project: rebuild/export selection is
// untouched, and supplying keyframeTimes/frameRate makes it deterministic in
// environments without ffprobe.
func (s *Server) diagnose(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.LoadProject(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "project not found: "+r.PathValue("id"))
		return
	}
	req := diagnoseRequest{CutMode: media.CutReencode}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "invalid JSON: "+err.Error())
			return
		}
	}
	if req.CutMode != media.CutReencode && req.CutMode != media.CutKeyframe {
		writeErr(w, 400, "cutMode must be reencode or keyframe")
		return
	}
	frameRate := req.FrameRate
	var keyframes []float64
	if req.KeyframeTimes != nil {
		keyframes = req.KeyframeTimes
	} else if req.CutMode == media.CutKeyframe {
		kf, err := s.Renderer.KeyframeTimes(r.Context(), p.VideoPath)
		if err != nil {
			writeErr(w, 400, "cannot read keyframes: "+err.Error())
			return
		}
		keyframes = kf
	}
	snap := trace.SnapshotFromProject(p, req.CutMode, frameRate, keyframes)
	tr, _ := trace.Replay(snap, trace.ReplayOptions{
		CutMode: req.CutMode, FrameRate: frameRate, KeyframeTimes: keyframes,
	})
	tr.Finish()
	report, diagnosis := tr.Report(), trace.Diagnose(tr.Report())
	resp := diagnoseResponse{Trace: report, Diagnosis: diagnosis}
	if req.Save {
		name, err := s.saveDiagnostic(p.ID, report, diagnosis)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		resp.DiagnosticID = name
	}
	writeJSON(w, 200, resp)
}

func (s *Server) getDiagnostic(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	http.ServeFile(w, r, filepath.Join(s.Store.DiagnosticDir(), name+".json"))
}

type exportRequest struct {
	ProjectID string `json:"projectId"`
	CutMode   string `json:"cutMode"`
}

func (s *Server) startExport(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if req.CutMode != media.CutReencode && req.CutMode != media.CutKeyframe {
		writeErr(w, 400, "cutMode must be reencode or keyframe")
		return
	}
	p, err := s.Store.LoadProject(req.ProjectID)
	if err != nil {
		writeErr(w, 404, "project not found: "+req.ProjectID)
		return
	}

	ordered := domain.ReorderBlocks(p.Blocks, p.BlockOrder)
	var plans []media.BlockPlan
	for _, b := range ordered {
		if !b.Included {
			continue
		}
		plan := media.BlockPlan{
			BlockID: b.ID, Refs: b.Refs, ShortStart: b.ShortStart,
			ShortEnd: b.ShortEnd, CrossedGap: b.CrossedGap, SegmentKeys: b.Segments,
		}
		for _, pc := range b.Pieces {
			plan.Pieces = append(plan.Pieces, media.PiecePlan{Start: pc.Start, End: pc.End})
		}
		plans = append(plans, plan)
	}
	if len(plans) == 0 {
		writeErr(w, 400, "no included blocks to export")
		return
	}

	jobID := fmt.Sprintf("J%d", time.Now().UnixNano())
	outPath := filepath.Join(s.Store.ExportDir(), jobID+".mp4")
	info, err := media.Probe(r.Context(), s.Renderer.FFprobe, p.VideoPath)
	if err != nil {
		writeErr(w, 400, "probe failed: "+err.Error())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &media.Job{}
	job.BindCancel(cancel)
	s.Store.RegisterJob(jobID, job)

	// Trace the real export so its evidence chain matches a later replay of the
	// same project. Rendering inputs and outputs are not modified by the tracer.
	tr := domain.NewTracer(domain.TraceConfig{FrameRate: info.FrameRate, CutMode: req.CutMode})
	tr.Stage(domain.StageInput)

	go func() {
		mf, err := s.Renderer.RenderWithTrace(ctx, p.VideoPath, info.HasAudio, plans, req.CutMode, outPath, job.Set, tr)
		tr.Finish()
		job.Lock()
		job.Manifest = mf
		report := tr.Report()
		job.TraceReport = &report
		job.Done = true
		if err != nil {
			if errors.Is(err, context.Canceled) {
				job.Err = "cancelled"
				_ = os.Remove(outPath)
			} else {
				job.Err = err.Error()
			}
		} else {
			_ = s.writeManifest(jobID, mf)
		}
		job.Unlock()
	}()

	writeJSON(w, 202, map[string]string{"jobId": jobID})
}

func (s *Server) writeManifest(jobID string, mf media.RenderManifest) error {
	path := filepath.Join(s.Store.ExportDir(), jobID+".manifest.json")
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *Server) exportStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.Store.GetJob(id)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	ratio, msg, done, errMsg := job.Snapshot()
	resp := map[string]any{"ratio": ratio, "message": msg, "done": done}
	if errMsg != "" {
		resp["error"] = errMsg
	}
	if done && errMsg == "" {
		resp["downloadUrl"] = "/api/exports/" + id + "/file"
		resp["manifestUrl"] = "/api/exports/" + id + "/manifest"
	}
	writeJSON(w, 200, resp)
}

func (s *Server) exportCancel(w http.ResponseWriter, r *http.Request) {
	job, err := s.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	job.Cancel()
	writeJSON(w, 202, map[string]string{"status": "cancel requested"})
}

func (s *Server) exportFile(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(s.Store.ExportDir(), r.PathValue("id")+".mp4"))
}

func (s *Server) exportManifest(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(s.Store.ExportDir(), r.PathValue("id")+".manifest.json"))
}
