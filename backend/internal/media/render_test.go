package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"highlight-assembly/internal/domain"
)

// testRenderer resolves ffmpeg/ffprobe from env or PATH.
func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	ff := os.Getenv("FFMPEG_BIN")
	if ff == "" {
		ff = "ffmpeg"
	}
	fp := os.Getenv("FFPROBE_BIN")
	if fp == "" {
		fp = "ffprobe"
	}
	if _, err := os.Stat(ff); err != nil && ff == "ffmpeg" {
		t.Skip("ffmpeg not available")
	}
	return &Renderer{FFmpeg: ff, FFprobe: fp}
}

func fixturePath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("TEST_VIDEO")
	if p == "" {
		p = filepath.Join("..", "..", "..", "testdata", "match60.mp4")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("test video missing at %s; run scripts/make_test_video.sh", p)
	}
	abs, _ := filepath.Abs(p)
	return abs
}

func probeDuration(t *testing.T, r *Renderer, path string) float64 {
	t.Helper()
	info, err := Probe(context.Background(), r.FFprobe, path)
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	return info.DurationSec
}

func plansForMarginCheck() []BlockPlan {
	// One block, one piece [10,20): 10s of footage, one event at 15.
	return []BlockPlan{{
		BlockID: "B0",
		Refs:    []domain.Ref{{EventID: "E1", Type: "goal", MappedVideo: 15}},
		Pieces:  []PiecePlan{{Start: 10, End: 20}},
	}}
}

func TestRenderReencodeIsFrameAccurate(t *testing.T) {
	r := testRenderer(t)
	src := fixturePath(t)
	out := filepath.Join(t.TempDir(), "reenc.mp4")

	mf, err := r.Render(context.Background(), src, true, plansForMarginCheck(), CutReencode, out, noopProgress)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if mf.CutMode != CutReencode || mf.Cancelled {
		t.Fatalf("manifest = %+v", mf)
	}
	dur := probeDuration(t, r, out)
	// Re-encode with explicit -ss/-to should land within ~2 frames (0.067s @30fps).
	if dur < 9.9 || dur > 10.1 {
		t.Errorf("reencode duration %v outside frame-accurate tolerance for 10s piece", dur)
	}
	if mf.CutNote == "" {
		t.Error("manifest must explain the cut method")
	}
	br := mf.Blocks[0]
	if len(br.Actual) != 1 || br.Actual[0] != (PiecePlan{10, 20}) {
		t.Errorf("reencode actual range must equal requested: %+v", br.Actual)
	}
}

func TestRenderKeyframeSnapsAndRecordsActual(t *testing.T) {
	r := testRenderer(t)
	src := fixturePath(t)
	out := filepath.Join(t.TempDir(), "kf.mp4")

	// Request non-keyframe-aligned 10.5..20.5; GOP is 2s so keyframes at even seconds.
	plans := []BlockPlan{{
		BlockID: "B0",
		Pieces:  []PiecePlan{{Start: 10.5, End: 20.5}},
	}}
	mf, err := r.Render(context.Background(), src, true, plans, CutKeyframe, out, noopProgress)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	br := mf.Blocks[0]
	actual := br.Actual[0]
	if actual == (PiecePlan{10.5, 20.5}) {
		t.Fatal("keyframe mode must snap away from non-keyframe boundaries")
	}
	// Expected snap: latest keyframe <= 10.5 is 10; earliest >= 20.5 is 22.
	if actual.Start != 10 || actual.End != 22 {
		t.Errorf("snap = %+v, want {10 22}", actual)
	}
	if br.Note == "" {
		t.Error("keyframe block must carry a note describing the approximation")
	}
	dur := probeDuration(t, r, out)
	if dur < 11.9 || dur > 12.1 {
		t.Errorf("keyframe output duration %v, want ~12s", dur)
	}
}

func TestRenderCrossGapProducesTwoPiecesInOrder(t *testing.T) {
	r := testRenderer(t)
	src := fixturePath(t)
	out := filepath.Join(t.TempDir(), "gap.mp4")

	// A declared gap [20,23) leaves pieces [15,20) and [23,28): 10s total.
	plans := []BlockPlan{{
		BlockID: "B0",
		Pieces:  []PiecePlan{{Start: 15, End: 20}, {Start: 23, End: 28}},
		CrossedGap: true,
	}}
	mf, err := r.Render(context.Background(), src, true, plans, CutReencode, out, noopProgress)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(mf.Blocks[0].Actual) != 2 {
		t.Fatalf("cross-gap block must keep two actual pieces: %+v", mf.Blocks[0])
	}
	dur := probeDuration(t, r, out)
	if dur < 9.9 || dur > 10.1 {
		t.Errorf("cross-gap output %v, want ~10s (gap footage absent)", dur)
	}
}

func TestRenderMultipleBlocksFollowTimelineOrder(t *testing.T) {
	r := testRenderer(t)
	src := fixturePath(t)
	out := filepath.Join(t.TempDir(), "order.mp4")

	// Deliberately non-chronological: block from late footage first.
	plans := []BlockPlan{
		{BlockID: "B1", Pieces: []PiecePlan{{Start: 40, End: 45}}},
		{BlockID: "B0", Pieces: []PiecePlan{{Start: 10, End: 15}}},
	}
	mf, err := r.Render(context.Background(), src, true, plans, CutReencode, out, noopProgress)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if mf.Blocks[0].BlockID != "B1" || mf.Blocks[1].BlockID != "B0" {
		t.Errorf("manifest order lost: %s then %s", mf.Blocks[0].BlockID, mf.Blocks[1].BlockID)
	}
	dur := probeDuration(t, r, out)
	if dur < 9.9 || dur > 10.1 {
		t.Errorf("combined output %v, want ~10s", dur)
	}
}

func TestRenderCancellationRemovesPartialOutput(t *testing.T) {
	r := testRenderer(t)
	src := fixturePath(t)
	out := filepath.Join(t.TempDir(), "cancel.mp4")

	// Many tiny pieces so the job spans multiple ffmpeg invocations.
	var plans []BlockPlan
	for i := 0; i < 12; i++ {
		plans = append(plans, BlockPlan{
			BlockID: "B" + string(rune('0'+i)),
			Pieces:  []PiecePlan{{Start: float64(i * 2), End: float64(i*2 + 1)}},
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := r.Render(ctx, src, true, plans, CutReencode, out, noopProgress)
		errCh <- err
	}()
	time.Sleep(250 * time.Millisecond)
	cancel()
	err := <-errCh
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("partial output should be removed, stat err=%v", err)
	}
}

func noopProgress(float64, string) {}
