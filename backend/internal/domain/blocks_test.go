package domain

import "testing"

func TestOverlapMergeKeepsAllEventRefs(t *testing.T) {
	// Two distinct events with windows [5,15) and [10,20): overlapping.
	s1 := &Segment{
		Key: "E1#0", Wanted: Range{5, 15}, Pieces: []Piece{{5, 15}},
		Ref:      Ref{EventID: "E1", Clock: 10, MappedVideo: 10, Type: "goal"},
		Included: true,
	}
	s2 := &Segment{
		Key: "E2#0", Wanted: Range{10, 20}, Pieces: []Piece{{10, 20}},
		Ref:      Ref{EventID: "E2", Clock: 14, MappedVideo: 15, Type: "shot"},
		Included: true,
	}
	blocks := BuildBlocks([]*Segment{s1, s2}, PolicyMerge)
	if len(blocks) != 1 {
		t.Fatalf("merge policy should yield 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if len(b.Refs) != 2 {
		t.Fatalf("merged block must retain BOTH event refs, got %d", len(b.Refs))
	}
	if len(b.Segments) != 2 {
		t.Fatalf("merged block must retain both segment keys, got %v", b.Segments)
	}
	// Footage is cut once: union [5,20).
	if len(b.Pieces) != 1 || b.Pieces[0] != (Piece{5, 20}) {
		t.Errorf("merged pieces = %+v, want single [5,20)", b.Pieces)
	}
	if b.Duration != 15 {
		t.Errorf("merged duration = %v, want 15", b.Duration)
	}
}

func TestRepeatedEventNumbersAreNotDeduplicated(t *testing.T) {
	// Same EventID appearing twice (e.g. two fouls logged under one number).
	s1 := &Segment{
		Key: "E9#0", Wanted: Range{5, 12}, Pieces: []Piece{{5, 12}},
		Ref: Ref{EventID: "E9", Occurrence: 0, Clock: 8, MappedVideo: 8}, Included: true,
	}
	s2 := &Segment{
		Key: "E9#1", Wanted: Range{6, 14}, Pieces: []Piece{{6, 14}},
		Ref: Ref{EventID: "E9", Occurrence: 1, Clock: 9, MappedVideo: 10}, Included: true,
	}
	blocks := BuildBlocks([]*Segment{s1, s2}, PolicyMerge)
	if len(blocks) != 1 {
		t.Fatalf("want 1 merged block, got %d", len(blocks))
	}
	b := blocks[0]
	if len(b.Refs) != 2 {
		t.Fatalf("repeated event number must produce two traceable refs, got %d", len(b.Refs))
	}
	if len(b.Segments) != 2 || b.Segments[0] != "E9#0" || b.Segments[1] != "E9#1" {
		t.Errorf("segment keys = %v", b.Segments)
	}
	// Under separate policy they must remain independently addressable.
	sep := BuildBlocks([]*Segment{s1, s2}, PolicySeparate)
	if len(sep) != 2 {
		t.Fatalf("separate policy should keep 2 blocks, got %d", len(sep))
	}
}

func TestNonOverlappingSegmentsStaySeparateUnderMerge(t *testing.T) {
	s1 := &Segment{Key: "E1#0", Wanted: Range{0, 5}, Pieces: []Piece{{0, 5}}, Included: true}
	s2 := &Segment{Key: "E2#0", Wanted: Range{6, 11}, Pieces: []Piece{{6, 11}}, Included: true}
	blocks := BuildBlocks([]*Segment{s1, s2}, PolicyMerge)
	if len(blocks) != 2 {
		t.Fatalf("disjoint windows should stay separate, got %d blocks", len(blocks))
	}
}

func TestReorderBlocksPreservesAllBlocks(t *testing.T) {
	b1 := &Block{ID: "B1"}
	b2 := &Block{ID: "B2"}
	b3 := &Block{ID: "B3"}
	out := ReorderBlocks([]*Block{b1, b2, b3}, []string{"B3", "B1"})
	if len(out) != 3 {
		t.Fatalf("reorder must not drop blocks, got %d", len(out))
	}
	if out[0].ID != "B3" || out[1].ID != "B1" || out[2].ID != "B2" {
		t.Errorf("order = %v %v %v", out[0].ID, out[1].ID, out[2].ID)
	}
}

func TestMergedBlockPropagatesShortageAndGapFlags(t *testing.T) {
	full := &Segment{Key: "E1#0", Wanted: Range{0, 10}, Pieces: []Piece{{0, 10}}, Included: true}
	short := &Segment{Key: "E2#0", Wanted: Range{8, 20}, Pieces: []Piece{{8, 15}}, ShortEnd: true, Included: true}
	gapped := &Segment{
		Key: "E3#0", Wanted: Range{12, 30}, Pieces: []Piece{{12, 20}, {25, 30}},
		CrossedGap: true, Included: true,
	}
	blocks := BuildBlocks([]*Segment{full, short, gapped}, PolicyMerge)
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if !b.ShortEnd || !b.CrossedGap {
		t.Errorf("block flags lost: shortEnd=%v crossedGap=%v", b.ShortEnd, b.CrossedGap)
	}
	// Pieces unioned: [0,20) and [25,30).
	if len(b.Pieces) != 2 || b.Pieces[0] != (Piece{0, 20}) || b.Pieces[1] != (Piece{25, 30}) {
		t.Errorf("union pieces = %+v", b.Pieces)
	}
}
