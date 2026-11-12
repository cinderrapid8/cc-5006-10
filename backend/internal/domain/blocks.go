package domain

import (
	"fmt"
	"sort"
)

const (
	PolicyMerge    = "merge"
	PolicySeparate = "separate"
)

// BuildBlocks applies the overlap policy. Under "merge", overlapping (or
// touching) segments become one block that retains every event reference;
// under "separate" each segment stays its own block.
//
// Merging is not de-duplication: two events with the same number are both
// kept, and overlapping footage is represented once while still traceable
// to all contributing events via Refs/SegmentKeys.
//
// The optional tracer records pairwise window relations (overlap vs boundary
// touching) and the block each segment landed in; it never alters grouping.
func BuildBlocks(segments []*Segment, policy string, tracer ...*Tracer) []*Block {
	var tr *Tracer
	if len(tracer) > 0 {
		tr = tracer[0]
	}
	tr.Stage(StageMerge)
	sorted := make([]*Segment, len(segments))
	copy(sorted, segments)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Wanted.Start < sorted[j].Wanted.Start
	})

	if policy == PolicySeparate {
		blocks := make([]*Block, 0, len(sorted))
		for i, s := range sorted {
			blocks = append(blocks, blockFromSegments(fmt.Sprintf("B%d", i), []*Segment{s}))
		}
		recordJoinEvidence(tr, sorted, blocks)
		return blocks
	}

	var blocks []*Block
	var group []*Segment
	flush := func(i int) {
		if len(group) == 0 {
			return
		}
		blocks = append(blocks, blockFromSegments(fmt.Sprintf("B%d", i), group))
		group = nil
	}
	for _, s := range sorted {
		if len(group) == 0 {
			group = []*Segment{s}
			continue
		}
		// Empty segments join overlapping groups by wanted window so their
		// events stay traceable; they never contribute renderable pieces.
		if rangesOverlap(groupSpan(group), s.Wanted) {
			group = append(group, s)
		} else {
			flush(len(blocks))
			group = []*Segment{s}
		}
	}
	flush(len(blocks))
	recordJoinEvidence(tr, sorted, blocks)
	return blocks
}

// recordJoinEvidence logs only pairs that share footage or meet at a boundary:
// disjoint pairs are uninteresting, which keeps diagnostic evidence bounded.
func recordJoinEvidence(tr *Tracer, sorted []*Segment, blocks []*Block) {
	if tr == nil {
		return
	}
	blockOf := map[string]string{}
	for _, b := range blocks {
		for _, k := range b.Segments {
			blockOf[k] = b.ID
		}
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			a, b := sorted[i], sorted[j]
			rel := ClassifyRelation(a.Wanted, b.Wanted)
			if rel == RelationNone {
				continue
			}
			blockA, blockB := blockOf[a.Key], blockOf[b.Key]
			tr.RecordJoin(MergeJoin{
				KeyA: a.Key, KeyB: b.Key, Relation: rel,
				StartA: a.Wanted.Start, EndA: a.Wanted.End,
				StartB: b.Wanted.Start, EndB: b.Wanted.End,
				SameBlock: blockA == blockB, BlockID: blockA,
				OverlapSec: OverlapLength(a.Wanted, b.Wanted),
			})
		}
	}
}

func rangesOverlap(a, b Range) bool {
	return a.Start < b.End && b.Start < a.End
}

func groupSpan(group []*Segment) Range {
	r := group[0].Wanted
	for _, s := range group[1:] {
		if s.Wanted.Start < r.Start {
			r.Start = s.Wanted.Start
		}
		if s.Wanted.End > r.End {
			r.End = s.Wanted.End
		}
	}
	return r
}

func blockFromSegments(id string, group []*Segment) *Block {
	refByKey := map[string]bool{}
	var refs []Ref
	var keys []string
	shortStart, shortEnd, crossed, empty := false, false, false, true
	for _, s := range group {
		keys = append(keys, s.Key)
		k := s.Key
		if !refByKey[k] {
			refByKey[k] = true
			refs = append(refs, s.Ref)
		}
		shortStart = shortStart || s.ShortStart
		shortEnd = shortEnd || s.ShortEnd
		crossed = crossed || s.CrossedGap
		if !s.Empty {
			empty = false
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Clock != refs[j].Clock {
			return refs[i].Clock < refs[j].Clock
		}
		return refs[i].EventID < refs[j].EventID
	})

	var all []Piece
	for _, s := range group {
		for _, p := range s.Pieces {
			all = append(all, p)
		}
	}
	merged := mergePieces(all)
	b := &Block{
		ID:         id,
		Refs:       refs,
		Segments:   keys,
		Pieces:     merged,
		ShortStart: shortStart,
		ShortEnd:   shortEnd,
		CrossedGap: crossed,
		Empty:      empty,
		Included:   !empty,
	}
	for _, p := range merged {
		b.Duration += p.End - p.Start
	}
	if len(merged) > 0 {
		b.Start = merged[0].Start
		b.End = merged[len(merged)-1].End
	} else {
		// Keep an empty block positioned at its wanted window for the timeline.
		b.Start = group[0].Wanted.Start
		b.End = group[0].Wanted.End
	}
	return b
}

// mergePieces unions overlapping/touching renderable intervals so merged
// footage is cut only once.
func mergePieces(in []Piece) []Piece {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	out := []Piece{in[0]}
	for _, p := range in[1:] {
		last := &out[len(out)-1]
		if p.Start <= last.End {
			if p.End > last.End {
				last.End = p.End
			}
		} else {
			out = append(out, p)
		}
	}
	return out
}

// ReorderBlocks returns blocks in the requested ID order; unknown IDs are
// appended in their natural order so nothing is silently dropped.
func ReorderBlocks(blocks []*Block, order []string) []*Block {
	byID := map[string]*Block{}
	for _, b := range blocks {
		byID[b.ID] = b
	}
	var out []*Block
	seen := map[string]bool{}
	for _, id := range order {
		if b, ok := byID[id]; ok {
			out = append(out, b)
			seen[id] = true
		}
	}
	for _, b := range blocks {
		if !seen[b.ID] {
			out = append(out, b)
		}
	}
	return out
}
