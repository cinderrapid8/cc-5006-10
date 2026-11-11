package domain

import (
	"testing"
)

func TestParseClock(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"45:10", 45*60 + 10},
		{"45:10.5", 45*60 + 10.5},
		{"90+3:12", 90*60 + 3*60 + 12},
		{"2710.5", 2710.5},
		{"1:30:00", 5400},
		{"1:30", 90},
		{"0:05", 5},
	}
	for _, c := range cases {
		got, err := ParseClock(c.in)
		if err != nil {
			t.Fatalf("ParseClock(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseClock(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := ParseClock(""); err == nil {
		t.Error("empty clock should error")
	}
	if _, err := ParseClock("abc"); err == nil {
		t.Error("invalid clock should error")
	}
}

func TestClockMapInterpolationAndRefusal(t *testing.T) {
	// Record clock 10:00 -> video 00:00, clock 50:00 -> video 20:00.
	// Drift: 40 record minutes span only 20 video minutes (0.5 rate).
	cm, err := NewClockMap([]Anchor{
		{ID: "A1", Clock: 600, VideoTime: 0},
		{ID: "A2", Clock: 3000, VideoTime: 1200},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := cm.Map(1800) // halfway in record clock
	if !r.Mapped || r.VideoSec != 600 {
		t.Errorf("interpolation = %+v, want 600", r)
	}
	r = cm.Map(600)
	if !r.Mapped || r.VideoSec != 0 {
		t.Errorf("anchor point = %+v", r)
	}
	// Outside coverage must be unmapped, never numeric equality.
	for _, c := range []float64{100, 3600} {
		if r := cm.Map(c); r.Mapped {
			t.Errorf("clock %v outside anchors must be unmapped, got %+v", c, r)
		}
	}
}

func TestClockMapRequiresTwoAnchors(t *testing.T) {
	if _, err := NewClockMap(nil); err == nil {
		t.Error("zero anchors should error")
	}
	if _, err := NewClockMap([]Anchor{{Clock: 1, VideoTime: 1}}); err == nil {
		t.Error("single anchor should error")
	}
	if _, err := NewClockMap([]Anchor{
		{Clock: 1, VideoTime: 10},
		{Clock: 1, VideoTime: 20},
	}); err == nil {
		t.Error("duplicate clock anchors should error")
	}
	if _, err := NewClockMap([]Anchor{
		{Clock: 1, VideoTime: 20},
		{Clock: 2, VideoTime: 10},
	}); err == nil {
		t.Error("non-increasing video times should error")
	}
}

func TestBuildSegmentsPaddingClampingAndGaps(t *testing.T) {
	meta := VideoMeta{
		DurationSec: 60, AvailableStart: 0, AvailableEnd: 60,
		Gaps: []Range{{Start: 20, End: 23}},
	}
	cm, _ := NewClockMap([]Anchor{
		{Clock: 0, VideoTime: 0},
		{Clock: 60, VideoTime: 60},
	})
	events := []Event{
		{EventID: "E1", Type: "goal", Clock: 10, Occurrence: 0},      // video 10
		{EventID: "E2", Type: "foul", Clock: 22, Occurrence: 0},       // video 22, crosses gap
		{EventID: "E3", Type: "goal", Clock: 58, Occurrence: 0},       // short end
		{EventID: "E4", Type: "goal", Clock: 90, Occurrence: 0},       // unmapped
	}
	pads := map[string]Padding{
		"goal": {BeforeSec: 5, AfterSec: 5},
		"foul": {BeforeSec: 2, AfterSec: 2},
	}
	segs, unmapped := BuildSegments(events, cm, meta, pads)

	if len(unmapped) != 1 || unmapped[0].Event.EventID != "E4" {
		t.Fatalf("unmapped queue = %+v", unmapped)
	}
	if len(segs) != 3 {
		t.Fatalf("want 3 segments, got %d", len(segs))
	}

	// E1: full margins 5s before and after.
	e1 := findSeg(segs, "E1#0")
	if e1.Wanted.Start != 5 || e1.Wanted.End != 15 {
		t.Errorf("E1 wanted = %+v", e1.Wanted)
	}
	if !e1.Complete() || e1.ShortStart || e1.ShortEnd {
		t.Errorf("E1 should be complete: %+v", e1)
	}
	if len(e1.Pieces) != 1 || e1.Pieces[0] != (Piece{5, 15}) {
		t.Errorf("E1 pieces = %+v", e1.Pieces)
	}

	// E2 crosses the gap [20,23): pre-event footage is fully inside the gap,
	// leaving only the post-event piece; missing seconds are still reported.
	e2 := findSeg(segs, "E2#0")
	if !e2.CrossedGap || e2.MissingInGap != 3 {
		t.Errorf("E2 gap flags = %+v missing=%v", e2, e2.MissingInGap)
	}
	if len(e2.Pieces) != 1 || e2.Pieces[0] != (Piece{23, 24}) {
		t.Errorf("E2 pieces = %+v", e2.Pieces)
	}
	if e2.PlannedDuration != 1 {
		t.Errorf("E2 planned duration = %v, want 1", e2.PlannedDuration)
	}

	// E3 wanted end 63 clamped to 60: short end, must not read as complete.
	e3 := findSeg(segs, "E3#0")
	if !e3.ShortEnd || e3.Complete() {
		t.Errorf("E3 should be short-end: %+v", e3)
	}
	if e3.Pieces[0].End != 60 {
		t.Errorf("E3 end clamp = %+v", e3.Pieces)
	}
}

func TestBuildSegmentsShortStart(t *testing.T) {
	meta := VideoMeta{DurationSec: 60, AvailableStart: 0, AvailableEnd: 60}
	cm, _ := NewClockMap([]Anchor{{Clock: 0, VideoTime: 0}, {Clock: 60, VideoTime: 60}})
	events := []Event{{EventID: "E1", Type: "goal", Clock: 2, Occurrence: 0}}
	segs, _ := BuildSegments(events, cm, meta, map[string]Padding{"goal": {BeforeSec: 10, AfterSec: 2}})
	s := segs[0]
	if !s.ShortStart || s.Complete() {
		t.Fatalf("expected short start: %+v", s)
	}
	if s.Pieces[0].Start != 0 {
		t.Errorf("start should clamp at 0, got %v", s.Pieces[0].Start)
	}
}

func findSeg(segs []*Segment, key string) *Segment {
	for _, s := range segs {
		if s.Key == key {
			return s
		}
	}
	return nil
}
