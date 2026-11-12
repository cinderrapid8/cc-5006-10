// Package trace turns a saved pipeline snapshot into replayable evidence and
// attributes highlight anomalies (duplicate footage, dropped event tails,
// out-of-order intervals) to the stage that produced them.
package trace

import (
	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
)

// ReplayOptions controls a replay. Zero values fall back to the snapshot's
// stored cut mode/frame data, so re-running the same snapshot twice yields
// identical evidence.
type ReplayOptions struct {
	CutMode       string
	FrameRate     float64
	KeyframeTimes []float64
	// MaxDetailed bounds per-kind evidence records in the produced tracer.
	MaxDetailed int
}

// Replay re-runs the full chain from frozen inputs: clock mapping, segment
// selection, interval merge, timeline order, and cut-time conversion. No
// external tooling is invoked; keyframe snapping uses the same pure function
// as the real renderer, which keeps replay evidence bit-for-bit comparable to
// the evidence recorded during an actual export.
func Replay(snap domain.TraceSnapshot, opts ReplayOptions) (*domain.Tracer, *domain.Project) {
	cutMode := opts.CutMode
	if cutMode == "" {
		cutMode = snap.CutMode
	}
	if cutMode == "" {
		cutMode = media.CutReencode
	}
	frameRate := opts.FrameRate
	if frameRate == 0 {
		frameRate = snap.FrameRate
	}
	keyframes := opts.KeyframeTimes
	if keyframes == nil {
		keyframes = snap.KeyframeTimes
	}

	tr := domain.NewTracer(domain.TraceConfig{
		MaxDetailed:   opts.MaxDetailed,
		FrameRate:     frameRate,
		KeyframeTimes: keyframes,
		CutMode:       cutMode,
	})
	tr.Stage(domain.StageInput)
	tr.RecordSnapshot(snap)

	cm, err := domain.NewClockMap(snap.Anchors)
	if err != nil {
		// A saved snapshot must always hold valid anchors; surface the failure
		// as an attributed issue rather than panicking during replay.
		tr.RecordIssue(domain.TraceIssue{
			Code: "replay-invalid-anchors", Stage: domain.StageInput,
			Severity: SeverityError, Message: err.Error(),
		})
		return tr, nil
	}

	segments, unmapped := domain.BuildSegments(snap.Events, cm, snap.Meta, snap.Paddings, tr)
	blocks := domain.BuildBlocks(segments, snap.OverlapPolicy, tr)
	order := snap.BlockOrder
	if len(order) == 0 {
		order = make([]string, len(blocks))
		for i, b := range blocks {
			order[i] = b.ID
		}
	}
	included := snap.Included
	ordered := domain.ReorderBlocks(blocks, order)

	tr.Stage(domain.StageTimeline)
	tr.Stage(domain.StageCut)
	for _, b := range ordered {
		if included != nil {
			if on, ok := included[b.ID]; ok && !on {
				continue
			}
		} else if !b.Included {
			continue
		}
		for pieceIdx, pc := range b.Pieces {
			planned := media.PiecePlan{Start: pc.Start, End: pc.End}
			actual := planned
			snapped := false
			if cutMode == media.CutKeyframe && len(keyframes) > 0 {
				actual = media.SnapToKeyframes(planned, keyframes)
				snapped = !actual.Equal(planned)
			}
			cfg := tr.Config()
			cut := domain.CutTrace{
				BlockID: b.ID, SegmentKeys: append([]string(nil), b.Segments...),
				Order:        pieceIdx,
				PlannedStart: planned.Start, PlannedEnd: planned.End,
				ActualStart: actual.Start, ActualEnd: actual.End,
				StartFrame:  domain.FrameIndexOf(actual.Start, cfg.FrameRate),
				EndFrame:    domain.FrameIndexOf(actual.End, cfg.FrameRate),
				Snapped:     snapped,
				StartOnGrid: domain.OnFrameGrid(planned.Start, cfg.FrameRate),
				EndOnGrid:   domain.OnFrameGrid(planned.End, cfg.FrameRate),
			}
			if len(keyframes) > 0 {
				cut.NearestStartKf = nearestAtOrBefore(planned.Start, keyframes)
				cut.NearestEndKf = nearestAtOrAfter(planned.End, keyframes)
			}
			tr.RecordCut(cut)
		}
	}

	project := &domain.Project{
		VideoMeta: snap.Meta, Anchors: snap.Anchors, Events: snap.Events,
		Paddings: snap.Paddings, OverlapPolicy: snap.OverlapPolicy,
		Segments: segments, Unmapped: unmapped, Blocks: blocks, BlockOrder: order,
	}
	return tr, project
}

func nearestAtOrBefore(t float64, keys []float64) float64 {
	if len(keys) == 0 {
		return 0
	}
	best := keys[0]
	for _, k := range keys {
		if k <= t {
			best = k
		} else {
			break
		}
	}
	return best
}

func nearestAtOrAfter(t float64, keys []float64) float64 {
	if len(keys) == 0 {
		return 0
	}
	for _, k := range keys {
		if k >= t {
			return k
		}
	}
	return keys[len(keys)-1]
}
