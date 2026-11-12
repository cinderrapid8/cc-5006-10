package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"highlight-assembly/internal/domain"
)

const (
	CutReencode = "reencode"
	CutKeyframe = "keyframe"
)

// Renderer drives ffmpeg. Paths are injected so tests can point at a static build.
type Renderer struct {
	FFmpeg  string
	FFprobe string
}

// PiecePlan is one renderable interval with the cut mode to apply.
type PiecePlan struct {
	Start float64
	End   float64
}

// Equal reports whether two plans describe the same interval.
func (p PiecePlan) Equal(o PiecePlan) bool {
	return p.Start == o.Start && p.End == o.End
}

// BlockPlan is an ordered timeline block with its pieces and provenance.
type BlockPlan struct {
	BlockID     string
	Refs        []domain.Ref
	Pieces      []PiecePlan
	ShortStart  bool
	ShortEnd    bool
	CrossedGap  bool
	SegmentKeys []string
}

// RenderManifest records exactly what was produced, including the cut method
// and any approximation, so outputs are self-describing.
type RenderManifest struct {
	OutputFile string        `json:"outputFile"`
	CutMode    string        `json:"cutMode"`
	CutNote    string        `json:"cutNote"`
	Blocks     []BlockResult `json:"blocks"`
	Cancelled  bool          `json:"cancelled"`
}

type BlockResult struct {
	BlockID     string       `json:"blockId"`
	SegmentKeys []string     `json:"segmentKeys"`
	Refs        []domain.Ref `json:"refs"`
	Planned     []PiecePlan  `json:"planned"`
	Actual      []PiecePlan  `json:"actual"`
	ShortStart  bool         `json:"shortStart"`
	ShortEnd    bool         `json:"shortEnd"`
	CrossedGap  bool         `json:"crossedGap"`
	Note        string       `json:"note,omitempty"`
}

// ProgressFunc receives 0..1 completion and a human message.
type ProgressFunc func(ratio float64, message string)

// Render executes the full export: cut every piece, concat per block, then
// concat all blocks. Cancellation via ctx kills the ffmpeg process group.
func (r *Renderer) Render(ctx context.Context, src string, hasAudio bool, plans []BlockPlan, cutMode string, outPath string, prog ProgressFunc) (RenderManifest, error) {
	return r.RenderWithTrace(ctx, src, hasAudio, plans, cutMode, outPath, prog, nil)
}

// RenderWithTrace is Render with an optional highlight-chain tracer. The
// tracer only observes planned vs actual cut ranges and frame order; passing
// nil yields byte-identical behavior to Render.
func (r *Renderer) RenderWithTrace(ctx context.Context, src string, hasAudio bool, plans []BlockPlan, cutMode string, outPath string, prog ProgressFunc, tr *domain.Tracer) (RenderManifest, error) {
	tr.Stage(domain.StageCut)
	mf := RenderManifest{OutputFile: outPath, CutMode: cutMode}
	switch cutMode {
	case CutReencode:
		mf.CutNote = "frame-accurate re-encode (libx264); boundaries land on the requested frames"
	case CutKeyframe:
		mf.CutNote = "keyframe approximation via stream copy; boundaries snap to nearest keyframes and actual ranges are recorded below"
	default:
		return mf, fmt.Errorf("unknown cut mode %q", cutMode)
	}

	work, err := os.MkdirTemp(filepath.Dir(outPath), "hlwork-*")
	if err != nil {
		return mf, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(work)
		}
	}()

	total := 0
	for _, p := range plans {
		total += len(p.Pieces)
	}
	if total == 0 {
		return mf, errors.New("nothing to render: no renderable pieces in the selected blocks")
	}

	done := 0
	var blockFiles []string
	for bi, plan := range plans {
		if ctx.Err() != nil {
			mf.Cancelled = true
			return mf, ctx.Err()
		}
		res := BlockResult{
			BlockID: plan.BlockID, SegmentKeys: plan.SegmentKeys,
			Refs: plan.Refs, Planned: plan.Pieces,
			ShortStart: plan.ShortStart, ShortEnd: plan.ShortEnd, CrossedGap: plan.CrossedGap,
		}
		var pieceFiles []string
		for pi, piece := range plan.Pieces {
			if ctx.Err() != nil {
				mf.Cancelled = true
				return mf, ctx.Err()
			}
			pf := filepath.Join(work, fmt.Sprintf("b%03d_p%03d.mp4", bi, pi))
			actual, note, err := r.cutPiece(ctx, src, hasAudio, piece, cutMode, pf, tr, bi, pi, plan)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					mf.Cancelled = true
				}
				return mf, err
			}
			res.Actual = append(res.Actual, actual)
			if note != "" {
				res.Note = note
			}
			pieceFiles = append(pieceFiles, pf)
			done++
			prog(float64(done)/float64(total+1), fmt.Sprintf("cutting block %d piece %d", bi+1, pi+1))
		}
		bf := filepath.Join(work, fmt.Sprintf("block%03d.mp4", bi))
		if len(pieceFiles) == 1 {
			if err := os.Rename(pieceFiles[0], bf); err != nil {
				return mf, err
			}
		} else {
			if err := r.concat(ctx, pieceFiles, bf); err != nil {
				if errors.Is(err, context.Canceled) {
					mf.Cancelled = true
				}
				return mf, err
			}
		}
		blockFiles = append(blockFiles, bf)
		mf.Blocks = append(mf.Blocks, res)
	}

	if len(blockFiles) == 1 {
		if err := moveFile(blockFiles[0], outPath); err != nil {
			return mf, err
		}
	} else {
		if err := r.concat(ctx, blockFiles, outPath); err != nil {
			if errors.Is(err, context.Canceled) {
				mf.Cancelled = true
			}
			return mf, err
		}
	}
	cleanup = false
	_ = os.RemoveAll(work)
	prog(1, "export complete")
	return mf, nil
}

func (r *Renderer) cutPiece(ctx context.Context, src string, hasAudio bool, p PiecePlan, mode, out string, tr *domain.Tracer, blockIdx, pieceIdx int, plan BlockPlan) (PiecePlan, string, error) {
	switch mode {
	case CutReencode:
		args := []string{"-y", "-ss", f(p.Start), "-to", f(p.End), "-i", src,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-avoid_negative_ts", "make_zero"}
		args = appendAudioArgs(args, hasAudio)
		if err := r.run(ctx, args, out); err != nil {
			return p, "", err
		}
		recordCutEvidence(tr, plan, blockIdx, pieceIdx, p, p, false)
		return p, "", nil
	default:
		keyTimes, err := r.keyframeTimes(ctx, src)
		if err != nil {
			return p, "", err
		}
		actual := SnapToKeyframes(p, keyTimes)
		note := fmt.Sprintf("requested %.3f-%.3f; keyframe snap %.3f-%.3f", p.Start, p.End, actual.Start, actual.End)
		args := []string{"-y", "-ss", f(actual.Start), "-to", f(actual.End), "-i", src,
			"-c", "copy", "-avoid_negative_ts", "make_zero"}
		args = appendAudioArgs(args, hasAudio)
		if err := r.run(ctx, args, out); err != nil {
			return actual, note, err
		}
		recordCutEvidence(tr, plan, blockIdx, pieceIdx, p, actual, true)
		return actual, note, nil
	}
}

// recordCutEvidence captures planned vs actual ranges plus the concrete frame
// indices, so a replay can show the actual frame order without re-probing.
func recordCutEvidence(tr *domain.Tracer, plan BlockPlan, blockIdx, pieceIdx int, planned, actual PiecePlan, snapped bool) {
	if tr == nil {
		return
	}
	cfg := tr.Config()
	cut := domain.CutTrace{
		BlockID: plan.BlockID, SegmentKeys: append([]string(nil), plan.SegmentKeys...),
		Order:        pieceIdx,
		PlannedStart: planned.Start, PlannedEnd: planned.End,
		ActualStart: actual.Start, ActualEnd: actual.End,
		StartFrame:  domain.FrameIndexOf(actual.Start, cfg.FrameRate),
		EndFrame:    domain.FrameIndexOf(actual.End, cfg.FrameRate),
		Snapped:     snapped,
		StartOnGrid: domain.OnFrameGrid(planned.Start, cfg.FrameRate),
		EndOnGrid:   domain.OnFrameGrid(planned.End, cfg.FrameRate),
	}
	if snapped && len(cfg.KeyframeTimes) > 0 {
		cut.NearestStartKf, cut.NearestEndKf = nearestKeyframes(planned, cfg.KeyframeTimes)
	}
	tr.RecordCut(cut)
}

func nearestKeyframes(p PiecePlan, keys []float64) (startKf, endKf float64) {
	startKf, endKf = keys[0], 0
	for _, k := range keys {
		if k <= p.Start {
			startKf = k
		}
		if k >= p.End && endKf == 0 {
			endKf = k
		}
	}
	if endKf == 0 {
		endKf = keys[len(keys)-1]
	}
	return startKf, endKf
}

func appendAudioArgs(args []string, hasAudio bool) []string {
	if hasAudio {
		return append(args, "-c:a", "aac", "-b:a", "128k")
	}
	return append(args, "-an")
}

func (r *Renderer) concat(ctx context.Context, inputs []string, out string) error {
	listPath := out + ".txt"
	var b strings.Builder
	for _, in := range inputs {
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(in, "'", "'\\''"))
		b.WriteString("'\n")
	}
	if err := os.WriteFile(listPath, []byte(b.String()), 0o600); err != nil {
		return err
	}
	defer os.Remove(listPath)
	return r.run(ctx, []string{"-y", "-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy", "-movflags", "+faststart"}, out)
}

func (r *Renderer) run(ctx context.Context, args []string, out string) error {
	full := append([]string(nil), args...)
	full = append(full, out)
	cmd := exec.CommandContext(ctx, r.FFmpeg, full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	if ctx.Err() != nil {
		return context.Canceled
	}
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %v: %s", err, tail(stderr.String(), 400))
	}
	return nil
}

// KeyframeTimes lists video keyframe PTS values via ffprobe.
func (r *Renderer) KeyframeTimes(ctx context.Context, src string) ([]float64, error) {
	return r.keyframeTimes(ctx, src)
}

func (r *Renderer) keyframeTimes(ctx context.Context, src string) ([]float64, error) {
	cmd := exec.CommandContext(ctx, r.FFprobe,
		"-v", "error", "-select_streams", "v:0",
		"-skip_frame", "nokey", "-show_frames",
		"-show_entries", "frame=pts_time",
		"-of", "csv=p=0", src)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var times []float64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		times = append(times, atof(line))
	}
	return times, nil
}

// SnapToKeyframes moves start to the latest keyframe <= requested start and
// end to the earliest keyframe >= requested end. When no keyframe reaches the
// requested end, the end clamps to the final keyframe (the last seekable
// point), so the actual range never silently keeps an unseekable boundary.
func SnapToKeyframes(p PiecePlan, keys []float64) PiecePlan {
	if len(keys) == 0 {
		return p
	}
	out := p
	best := keys[0]
	for _, k := range keys {
		if k <= p.Start {
			best = k
		} else {
			break
		}
	}
	out.Start = best
	out.End = keys[len(keys)-1]
	for _, k := range keys {
		if k >= p.End {
			out.End = k
			return out
		}
	}
	return out
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Job tracks an export for cancellation and status polling.
type Job struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	Ratio    float64
	Message  string
	Manifest RenderManifest
	// TraceReport holds the highlight-chain evidence when an export ran with a
	// tracer; nil otherwise. It is observational and never affects rendering.
	TraceReport *domain.TraceReport
	Done        bool
	Err         string
}

// Set updates progress and is safe for concurrent use.
func (j *Job) Set(ratio float64, msg string) {
	j.mu.Lock()
	j.Ratio, j.Message = ratio, msg
	j.mu.Unlock()
}

// Lock/Unlock allow the API layer to finalize job state atomically.
func (j *Job) Lock()   { j.mu.Lock() }
func (j *Job) Unlock() { j.mu.Unlock() }

// Snapshot returns a copy of the job state.
func (j *Job) Snapshot() (ratio float64, msg string, done bool, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Ratio, j.Message, j.Done, j.Err
}

// Cancel requests cancellation.
func (j *Job) Cancel() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
	}
}

// BindCancel attaches the context cancellation function.
func (j *Job) BindCancel(cancel context.CancelFunc) {
	j.mu.Lock()
	j.cancel = cancel
	j.mu.Unlock()
}
