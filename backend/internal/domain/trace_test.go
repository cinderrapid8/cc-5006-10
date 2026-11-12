package domain

import (
	"testing"
)

func TestClassifyRelationDistinguishesOverlapFromTouch(t *testing.T) {
	cases := []struct {
		name string
		a, b Range
		want string
	}{
		{"overlap", Range{5, 15}, Range{10, 20}, RelationOverlap},
		{"touch end-to-start is not overlap", Range{5, 10}, Range{10, 15}, RelationTouch},
		{"touch reverse", Range{10, 15}, Range{5, 10}, RelationTouch},
		{"contained", Range{0, 20}, Range{5, 10}, RelationContained},
		{"equal", Range{5, 10}, Range{5, 10}, RelationEqual},
		{"disjoint", Range{0, 5}, Range{6, 11}, RelationNone},
	}
	for _, c := range cases {
		if got := ClassifyRelation(c.a, c.b); got != c.want {
			t.Errorf("%s: ClassifyRelation = %s, want %s", c.name, got, c.want)
		}
	}
	// Touching half-open windows share no footage; overlapping ones do.
	if OverlapLength(Range{5, 10}, Range{10, 15}) != 0 {
		t.Error("touching windows must have zero overlap length")
	}
	if OverlapLength(Range{5, 15}, Range{10, 20}) != 5 {
		t.Error("overlapping windows must report 5s shared footage")
	}
}

func TestFrameGridMarksAt30FPS(t *testing.T) {
	const fps = 30.0
	// Annotations landing exactly on frame boundaries (k/30 seconds).
	grid := []float64{0, 1.0 / 30, 10.0 / 30, 300.0 / 30, 599.0 / 30}
	for _, g := range grid {
		if !OnFrameGrid(g, fps) {
			t.Errorf("%.6f should be on the 30fps frame grid", g)
		}
	}
	off := []float64{1.0/30 + 0.01, 10.0/30 + 0.005, 10.25}
	for _, g := range off {
		if OnFrameGrid(g, fps) {
			t.Errorf("%.6f should be off the 30fps frame grid", g)
		}
	}
	// 10.5s = frame 315 exactly: a frame-boundary annotation must be recognized.
	if !OnFrameGrid(10.5, fps) {
		t.Error("10.5s is frame 315 at 30fps and must be on-grid")
	}
	if FrameIndexOf(1.0, fps) != 30 || FrameIndexOf(0.5, fps) != 15 {
		t.Error("frame index conversion wrong")
	}
	if FrameIndexOf(1.0, 0) != -1 {
		t.Error("zero frame rate must yield -1, not a fabricated index")
	}
}

func TestNilTracerIsNoOp(t *testing.T) {
	var tr *Tracer
	if tr.Enabled() {
		t.Fatal("nil tracer must be disabled")
	}
	// Every call must be safe on a nil receiver.
	tr.Stage(StageSegments)
	tr.RecordEvent(TraceEvent{EventID: "E1"})
	tr.RecordWindow(SegmentWindow{SegmentKey: "E1#0"})
	tr.RecordJoin(MergeJoin{KeyA: "E1#0", KeyB: "E2#0"})
	tr.RecordCut(CutTrace{BlockID: "B0"})
	tr.RecordIssue(TraceIssue{Code: "x"})
	tr.RecordSnapshot(TraceSnapshot{})
	tr.Finish()
	rep := tr.Report()
	if len(rep.Issues) != 0 || len(rep.Windows) != 0 || rep.TotalCounts["cuts"] != 0 {
		t.Errorf("nil tracer report must be empty: %+v", rep)
	}
}

func TestTracerBoundsEvidenceAndKeepsExactTotals(t *testing.T) {
	tr := NewTracer(TraceConfig{MaxDetailed: 3})
	for i := 0; i < 10; i++ {
		tr.RecordWindow(SegmentWindow{SegmentKey: "k"})
		tr.RecordCut(CutTrace{BlockID: "B0"})
	}
	rep := tr.Report()
	if len(rep.Windows) != 3 || len(rep.Cuts) != 3 {
		t.Errorf("detailed records must cap at 3: windows=%d cuts=%d", len(rep.Windows), len(rep.Cuts))
	}
	if rep.TotalCounts["windows"] != 10 || rep.TotalCounts["cuts"] != 10 {
		t.Errorf("total counts must stay exact: %+v", rep.TotalCounts)
	}
	if rep.TruncatedCounts["windows"] != 7 || rep.TruncatedCounts["cuts"] != 7 {
		t.Errorf("truncation counts wrong: %+v", rep.TruncatedCounts)
	}
}

// TestTracingDoesNotChangeSelection proves the diagnostic switch is
// observational: the same inputs produce identical segments and blocks with
// and without a tracer.
func TestTracingDoesNotChangeSelection(t *testing.T) {
	meta := VideoMeta{DurationSec: 60, AvailableStart: 0, AvailableEnd: 60}
	cm, _ := NewClockMap([]Anchor{{Clock: 0, VideoTime: 0}, {Clock: 60, VideoTime: 60}})
	events := []Event{
		{EventID: "E1", Type: "goal", Clock: 10, Occurrence: 0},
		{EventID: "E2", Type: "shot", Clock: 12, Occurrence: 0}, // overlaps E1 window
		{EventID: "E3", Type: "foul", Clock: 20, Occurrence: 0}, // window [18,22)
	}
	pads := map[string]Padding{
		"goal": {BeforeSec: 5, AfterSec: 5}, // [5,15)
		"shot": {BeforeSec: 2, AfterSec: 2}, // [10,14)
		"foul": {BeforeSec: 2, AfterSec: 2}, // [18,22), disjoint from the first group
	}

	plainSegs, plainUnmapped := BuildSegments(events, cm, meta, pads)
	plainBlocks := BuildBlocks(plainSegs, PolicyMerge)

	tr := NewTracer(TraceConfig{FrameRate: 30})
	trSegs, trUnmapped := BuildSegments(events, cm, meta, pads, tr)
	trBlocks := BuildBlocks(trSegs, PolicyMerge, tr)

	if len(plainSegs) != len(trSegs) || len(plainUnmapped) != len(trUnmapped) {
		t.Fatal("tracer changed segment/unmapped counts")
	}
	for i := range plainSegs {
		a, b := plainSegs[i], trSegs[i]
		if a.Key != b.Key || a.Wanted != b.Wanted || a.PlannedDuration != b.PlannedDuration ||
			len(a.Pieces) != len(b.Pieces) || a.Empty != b.Empty {
			t.Fatalf("segment %d changed by tracer: %+v vs %+v", i, a, b)
		}
	}
	if len(plainBlocks) != len(trBlocks) {
		t.Fatalf("block count changed by tracer: %d vs %d", len(plainBlocks), len(trBlocks))
	}
	for i := range plainBlocks {
		a, b := plainBlocks[i], trBlocks[i]
		if a.ID != b.ID || len(a.Pieces) != len(b.Pieces) || a.Duration != b.Duration ||
			len(a.Segments) != len(b.Segments) {
			t.Fatalf("block %d changed by tracer: %+v vs %+v", i, a, b)
		}
	}

	// The tracer must also have captured the evidence behind the grouping:
	// E1/E2 overlap and share one block, with the per-event window recorded.
	rep := tr.Report()
	if len(rep.Windows) != 3 {
		t.Fatalf("want 3 window records, got %d", len(rep.Windows))
	}
	var sawSharedFootage bool
	for _, j := range rep.Joins {
		if (j.Relation == RelationOverlap || j.Relation == RelationContained) && j.SameBlock && j.OverlapSec > 0 {
			sawSharedFootage = true
		}
	}
	if !sawSharedFootage {
		t.Error("overlap/containment join evidence missing")
	}

	// A boundary-touching pair (end == neighbor start, no shared footage) is
	// recorded as a distinct relation and stays in separate blocks.
	touchSegs := []*Segment{
		{Key: "T1#0", Wanted: Range{5, 10}, Pieces: []Piece{{5, 10}}, Included: true},
		{Key: "T2#0", Wanted: Range{10, 15}, Pieces: []Piece{{10, 15}}, Included: true},
	}
	touchTr := NewTracer(TraceConfig{})
	touchBlocks := BuildBlocks(touchSegs, PolicyMerge, touchTr)
	if len(touchBlocks) != 2 {
		t.Fatalf("touching windows must stay in separate blocks, got %d", len(touchBlocks))
	}
	touchRep := touchTr.Report()
	if len(touchRep.Joins) != 1 || touchRep.Joins[0].Relation != RelationTouch {
		t.Fatalf("want one touch join, got %+v", touchRep.Joins)
	}
	if touchRep.Joins[0].SameBlock || touchRep.Joins[0].OverlapSec != 0 {
		t.Errorf("touch join must cross blocks with zero overlap: %+v", touchRep.Joins[0])
	}
}
