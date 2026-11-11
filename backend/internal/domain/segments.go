package domain

import (
	"fmt"
	"sort"
)

// DefaultPadding is used for event types without an explicit entry.
var DefaultPadding = Padding{BeforeSec: 5, AfterSec: 5}

// BuildSegments maps every event and turns mapped ones into candidate
// segments. Unmapped events are returned separately for a pending queue;
// they are never cut by assuming equal numbers.
func BuildSegments(events []Event, cm *ClockMap, meta VideoMeta, paddings map[string]Padding) ([]*Segment, []UnmappedEvent) {
	availLo, availHi := meta.AvailableStart, meta.AvailableEnd
	if availHi <= 0 {
		availHi = meta.DurationSec
	}

	var segs []*Segment
	var unmapped []UnmappedEvent
	for idx, ev := range events {
		res := cm.Map(ev.Clock)
		if !res.Mapped {
			unmapped = append(unmapped, UnmappedEvent{Event: ev, Reason: res.Reason})
			continue
		}
		pad, ok := paddings[ev.Type]
		if !ok {
			pad = DefaultPadding
		}
		want := Range{
			Start: res.VideoSec - pad.BeforeSec,
			End:   res.VideoSec + pad.AfterSec,
		}
		seg := &Segment{
			Key: SegmentKey(ev),
			Ref: Ref{
				EventID:     ev.EventID,
				Occurrence:  ev.Occurrence,
				Type:        ev.Type,
				Clock:       ev.Clock,
				ClockText:   ev.ClockText,
				Detail:      ev.Detail,
				MappedVideo: res.VideoSec,
			},
			Wanted:   want,
			Included: true,
		}

		clamped := want
		if clamped.Start < availLo {
			clamped.Start = availLo
			seg.ShortStart = true
		}
		if clamped.End > availHi {
			clamped.End = availHi
			seg.ShortEnd = true
		}
		if clamped.End <= clamped.Start {
			clamped.End = clamped.Start
		}

		pieces, missing := subtractGaps(clamped, meta.Gaps)
		seg.Pieces = pieces
		if missing > 0 {
			seg.CrossedGap = true
			seg.MissingInGap = missing
		}
		// A window fully swallowed by gaps still exists as an empty segment:
		// it must surface in the UI as unusable, never disappear silently.
		if len(pieces) == 0 {
			seg.Empty = true
		}
		for _, p := range pieces {
			seg.PlannedDuration += p.End - p.Start
		}
		segs = append(segs, seg)
		_ = idx
	}
	return segs, unmapped
}

// SegmentKey is the stable identity of one event occurrence.
func SegmentKey(ev Event) string {
	return fmt.Sprintf("%s#%d", ev.EventID, ev.Occurrence)
}

// subtractGaps removes declared unavailable ranges from a window.
// The returned pieces cover only renderable footage; missing seconds are
// reported so a segment crossing a gap is visibly incomplete.
func subtractGaps(window Range, gaps []Range) ([]Piece, float64) {
	bounds := []float64{window.Start, window.End}
	for _, g := range gaps {
		if g.Overlaps(window) {
			if g.Start >= window.Start && g.Start < window.End {
				bounds = append(bounds, g.Start)
			}
			if g.End > window.Start && g.End <= window.End {
				bounds = append(bounds, g.End)
			}
		}
	}
	sort.Float64s(bounds)
	var pieces []Piece
	missing := 0.0
	for i := 0; i+1 < len(bounds); i++ {
		s, e := bounds[i], bounds[i+1]
		if e <= s {
			continue
		}
		mid := (s + e) / 2
		inGap := false
		for _, g := range gaps {
			if g.Contains(mid) {
				inGap = true
				break
			}
		}
		if inGap {
			missing += e - s
		} else {
			if len(pieces) > 0 && pieces[len(pieces)-1].End == s {
				pieces[len(pieces)-1].End = e
			} else {
				pieces = append(pieces, Piece{Start: s, End: e})
			}
		}
	}
	return pieces, missing
}
