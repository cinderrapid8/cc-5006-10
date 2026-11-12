package trace

import (
	"reflect"
	"testing"

	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
)

// baseSnapshot builds overlapping events plus a boundary-touching neighbor on
// a 30fps source, so one fixture exercises every diagnostic branch.
func baseSnapshot() domain.TraceSnapshot {
	return domain.TraceSnapshot{
		Anchors: []domain.Anchor{
			{ID: "A1", Clock: 0, VideoTime: 0},
			{ID: "A2", Clock: 60, VideoTime: 60},
		},
		Events: []domain.Event{
			{EventID: "E1", Type: "goal", Clock: 10, Occurrence: 0},
			{EventID: "E2", Type: "shot", Clock: 12, Occurrence: 0},
			{EventID: "E3", Type: "foul", Clock: 20, Occurrence: 0},
			{EventID: "E5", Type: "goal", Clock: 58, Occurrence: 0}, // short end
		},
		Meta: domain.VideoMeta{DurationSec: 60, AvailableStart: 0, AvailableEnd: 60},
		Paddings: map[string]domain.Padding{
			"goal": {BeforeSec: 5, AfterSec: 5}, // E1 [5,15); E5 [53,60) clamped
			"shot": {BeforeSec: 2, AfterSec: 2}, // E2 [10,14) contained in E1
			"foul": {BeforeSec: 2, AfterSec: 2}, // E3 [18,22)
		},
		OverlapPolicy: domain.PolicyMerge,
		CutMode:       media.CutReencode,
		FrameRate:     30,
	}
}

func findDupe(d Diagnosis, code string) *DuplicateCandidate {
	for i := range d.DuplicateCandidates {
		if d.DuplicateCandidates[i].Code == code {
			return &d.DuplicateCandidates[i]
		}
	}
	return nil
}

func TestReplayAttributesOverlapDuplicateToMergeStage(t *testing.T) {
	snap := baseSnapshot()
	tr, project := Replay(snap, ReplayOptions{})
	if project == nil {
		t.Fatal("replay should rebuild the project")
	}
	d := Diagnose(tr.Report())

	// E1 [5,15) and E2 [10,14) share footage: the duplicate is a merge-stage
	// property regardless of cut mode.
	dupe := findDupe(d, "dup-shared-footage-merged")
	if dupe == nil {
		t.Fatalf("overlap duplicate not diagnosed: %+v", d.DuplicateCandidates)
	}
	if dupe.RootStage != domain.StageMerge || dupe.OverlapSec != 4 {
		t.Errorf("overlap dupe = %+v", dupe)
	}
	if !dupe.SameBlock {
		t.Error("overlapping windows should share one merged block")
	}
}

func TestTouchDuplicateWithoutCutExpansionIsNotCutStage(t *testing.T) {
	snap := baseSnapshot()
	// Add a window that only meets the E1 window [5,15) at its boundary.
	snap.Events = append(snap.Events, domain.Event{EventID: "E6", Type: "card", Clock: 15, Occurrence: 0})
	snap.Paddings["card"] = domain.Padding{BeforeSec: 0, AfterSec: 2} // [15,17)
	tr, _ := Replay(snap, ReplayOptions{CutMode: media.CutReencode, FrameRate: 30})
	d := Diagnose(tr.Report())
	dupe := findDupe(d, "dup-touch-no-shared-footage")
	if dupe == nil {
		t.Fatalf("boundary-touch pair not recorded: %+v", d.DuplicateCandidates)
	}
	if dupe.RootStage != domain.StageNone || dupe.OverlapSec != 0 {
		t.Errorf("touch pair without cut expansion must be informational: %+v", dupe)
	}
}

func TestTouchDuplicateWithKeyframeExpansionIsCutStage(t *testing.T) {
	snap := baseSnapshot()
	// GOP 2s: keyframes at even seconds. The touching boundary at 15 is not a
	// keyframe, so the [18,22) cut snaps its start back to 18... instead craft
	// a cut whose planned start is the touch point 15 via a 0-margin event.
	snap.Events = append(snap.Events, domain.Event{EventID: "E6", Type: "card", Clock: 15, Occurrence: 0})
	snap.Paddings["card"] = domain.Padding{BeforeSec: 0, AfterSec: 2} // [15,17), touches E1 end
	keyframes := []float64{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22}
	tr, _ := Replay(snap, ReplayOptions{CutMode: media.CutKeyframe, KeyframeTimes: keyframes, FrameRate: 30})
	d := Diagnose(tr.Report())

	dupe := findDupe(d, "dup-touch-expanded-by-keyframe")
	if dupe == nil {
		t.Fatalf("keyframe-expanded touch duplicate not diagnosed: %+v", d.DuplicateCandidates)
	}
	if dupe.RootStage != domain.StageCut {
		t.Errorf("expanded touch duplicate must be attributed to media-cut: %+v", dupe)
	}

	// The same scene under reencode must not produce a cut-stage duplicate.
	tr2, _ := Replay(snap, ReplayOptions{CutMode: media.CutReencode, FrameRate: 30})
	d2 := Diagnose(tr2.Report())
	if findDupe(d2, "dup-touch-expanded-by-keyframe") != nil {
		t.Error("reencode mode must not report keyframe expansion")
	}
}

func TestSeparatePolicyFlagsUnmergedOverlap(t *testing.T) {
	snap := baseSnapshot()
	snap.OverlapPolicy = domain.PolicySeparate
	tr, _ := Replay(snap, ReplayOptions{})
	d := Diagnose(tr.Report())
	dupe := findDupe(d, "dup-overlap-not-merged")
	if dupe == nil {
		t.Fatalf("separate-policy overlap should be flagged: %+v", d.DuplicateCandidates)
	}
	if dupe.RootStage != domain.StageMerge || dupe.Severity != SeverityWarning {
		t.Errorf("unmerged overlap = %+v", dupe)
	}
}

func TestShortEndTailLossAttributedToSelection(t *testing.T) {
	snap := baseSnapshot()
	tr, _ := Replay(snap, ReplayOptions{})
	d := Diagnose(tr.Report())
	var found *TailLoss
	for i := range d.TailLosses {
		if d.TailLosses[i].Code == "tail-clamped-to-available" {
			found = &d.TailLosses[i]
		}
	}
	if found == nil {
		t.Fatalf("short-end tail loss missing: %+v", d.TailLosses)
	}
	if found.RootStage != domain.StageSegments || found.LostSec != 3 {
		t.Errorf("short-end loss = %+v", found)
	}
}

func TestGapTailLossAttributedToSelectionNotCut(t *testing.T) {
	snap := baseSnapshot()
	snap.Meta.Gaps = []domain.Range{{Start: 21, End: 22}} // bites E3 window [18,22)
	tr, _ := Replay(snap, ReplayOptions{})
	d := Diagnose(tr.Report())
	var found bool
	for _, tl := range d.TailLosses {
		if tl.RootStage == domain.StageCut {
			t.Errorf("reencode must not report cut-stage tail loss: %+v", tl)
		}
		if tl.Code == "tail-partly-in-gap" && tl.RootStage == domain.StageSegments {
			found = true
		}
	}
	if !found {
		t.Fatalf("gap tail loss not attributed to selection: %+v", d.TailLosses)
	}
}

func TestKeyframeSnapTailLossAttributedToCut(t *testing.T) {
	snap := baseSnapshot()
	// Isolated window [40.5,41.5) with a keyframe list that ends at 40: the
	// end has no later keyframe to snap to, so SnapToKeyframes clamps it to the
	// final keyframe 40 and the cut loses the tail entirely at the cut stage.
	snap.Events = append(snap.Events, domain.Event{EventID: "E8", Type: "card", Clock: 41, Occurrence: 0})
	snap.Paddings["card"] = domain.Padding{BeforeSec: 0.5, AfterSec: 0.5}
	keyframes := []float64{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 32, 34, 36, 38, 40}
	tr, _ := Replay(snap, ReplayOptions{CutMode: media.CutKeyframe, KeyframeTimes: keyframes, FrameRate: 30})
	d := Diagnose(tr.Report())
	var found bool
	for _, tl := range d.TailLosses {
		if tl.Code == "tail-lost-to-keyframe-snap" && tl.RootStage == domain.StageCut {
			found = true
		}
	}
	if !found {
		t.Fatalf("keyframe tail loss not attributed to cut stage: %+v", d.TailLosses)
	}
}

func TestFrameBoundaryFindingsOnAndOffGrid(t *testing.T) {
	snap := baseSnapshot()
	// Isolated event at 40.01s (30fps -> 1200.3 frames, genuinely off-grid)
	// with an isolated window [38.01,42.01) so it cannot inherit another
	// block's grid-aligned piece boundaries.
	snap.Events = append(snap.Events, domain.Event{EventID: "E7", Type: "card", Clock: 40.01, Occurrence: 0})
	snap.Paddings["card"] = domain.Padding{BeforeSec: 2, AfterSec: 2}
	tr, _ := Replay(snap, ReplayOptions{FrameRate: 30})
	d := Diagnose(tr.Report())
	if len(d.FrameBoundary) == 0 {
		t.Fatal("off-grid boundaries should be reported")
	}
	for _, fb := range d.FrameBoundary {
		if fb.RootStage != domain.StageCut {
			t.Errorf("frame boundary finding must name the cut stage: %+v", fb)
		}
	}
	// All base boundaries are grid-aligned: a clean fixture yields none.
	clean, _ := Replay(baseSnapshot(), ReplayOptions{FrameRate: 30})
	if len(Diagnose(clean.Report()).FrameBoundary) != 0 {
		t.Error("grid-aligned boundaries must not produce findings")
	}
}

func TestReplayIsDeterministicAcrossRuns(t *testing.T) {
	snap := baseSnapshot()
	keyframes := []float64{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22}
	opts := ReplayOptions{CutMode: media.CutKeyframe, KeyframeTimes: keyframes, FrameRate: 30, MaxDetailed: 50}
	tr1, p1 := Replay(snap, opts)
	tr2, p2 := Replay(snap, opts)
	r1, r2 := tr1.Report(), tr2.Report()

	if len(r1.Cuts) != len(r2.Cuts) || len(r1.Joins) != len(r2.Joins) || len(r1.Windows) != len(r2.Windows) {
		t.Fatal("replay evidence length differs across runs")
	}
	for i := range r1.Cuts {
		if !reflect.DeepEqual(r1.Cuts[i], r2.Cuts[i]) {
			t.Fatalf("cut evidence differs at %d:\n%+v\n%+v", i, r1.Cuts[i], r2.Cuts[i])
		}
	}
	for i := range r1.Joins {
		if !reflect.DeepEqual(r1.Joins[i], r2.Joins[i]) {
			t.Fatalf("join evidence differs at %d", i)
		}
	}
	if len(p1.Blocks) != len(p2.Blocks) {
		t.Fatal("replay block count differs")
	}
	for i := range p1.Blocks {
		b1, b2 := p1.Blocks[i], p2.Blocks[i]
		if b1.ID != b2.ID || len(b1.Pieces) != len(b2.Pieces) || b1.Duration != b2.Duration {
			t.Fatalf("replayed block %d differs", i)
		}
	}
	d1, d2 := Diagnose(r1), Diagnose(r2)
	if len(d1.DuplicateCandidates) != len(d2.DuplicateCandidates) ||
		len(d1.TailLosses) != len(d2.TailLosses) ||
		len(d1.OrderAnomalies) != len(d2.OrderAnomalies) {
		t.Fatal("diagnosis differs across identical replays")
	}
}

func TestReplayEvidenceIsBoundedButTotalsStayExact(t *testing.T) {
	snap := baseSnapshot()
	tr, _ := Replay(snap, ReplayOptions{MaxDetailed: 2, FrameRate: 30})
	rep := tr.Report()
	if len(rep.Cuts) > 2 || len(rep.Windows) > 2 {
		t.Errorf("evidence must be bounded: cuts=%d windows=%d", len(rep.Cuts), len(rep.Windows))
	}
	d := Diagnose(rep)
	if d.SummaryCounts["recordedCuts"] < len(rep.Cuts) {
		t.Error("summary counts must retain exact totals despite truncation")
	}
	if d.SummaryCounts["truncatedRecords"] == 0 {
		t.Error("truncation must be visible in the diagnosis summary")
	}
}

func TestReplayRecordsTimelineReversal(t *testing.T) {
	snap := baseSnapshot()
	_, project := Replay(snap, ReplayOptions{FrameRate: 30})
	// Force a non-chronological timeline: late block first.
	if len(project.BlockOrder) < 2 {
		t.Fatal("need at least two blocks to reorder")
	}
	snap.BlockOrder = append([]string(nil), project.BlockOrder...)
	snap.BlockOrder[0], snap.BlockOrder[len(snap.BlockOrder)-1] =
		snap.BlockOrder[len(snap.BlockOrder)-1], snap.BlockOrder[0]
	tr2, _ := Replay(snap, ReplayOptions{FrameRate: 30})
	d := Diagnose(tr2.Report())
	if len(d.OrderAnomalies) == 0 {
		t.Fatalf("reversed timeline should produce an order anomaly: %+v", d.OrderAnomalies)
	}
	for _, oa := range d.OrderAnomalies {
		if oa.RootStage != domain.StageTimeline {
			t.Errorf("timeline reversal attributed to %s: %+v", oa.RootStage, oa)
		}
	}
}
