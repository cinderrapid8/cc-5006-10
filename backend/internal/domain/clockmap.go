package domain

import (
	"errors"
	"sort"
)

// Anchor binds one record-clock instant to one video-time instant.
// At least two anchors are required; mapping is piecewise linear between
// consecutive anchors (slope can differ per segment to tolerate clock drift).
type Anchor struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Clock     float64 `json:"clockSec"`
	VideoTime float64 `json:"videoSec"`
}

// ClockMap converts record-clock seconds into video-time seconds.
// Instances outside the range covered by anchors are deliberately unmapped:
// equal-looking numbers must never be treated as a correspondence.
type ClockMap struct {
	anchors []Anchor
}

// NewClockMap validates anchors and returns a mapper.
func NewClockMap(anchors []Anchor) (*ClockMap, error) {
	if len(anchors) < 2 {
		return nil, errors.New("at least two anchors are required to establish clock-to-video mapping")
	}
	a := make([]Anchor, len(anchors))
	copy(a, anchors)
	sort.Slice(a, func(i, j int) bool { return a[i].Clock < a[j].Clock })
	for i := 1; i < len(a); i++ {
		if a[i].Clock == a[i-1].Clock {
			return nil, errors.New("duplicate anchor clock value: " + FormatClock(a[i].Clock))
		}
		if a[i].VideoTime <= a[i-1].VideoTime {
			return nil, errors.New("anchor video times must be strictly increasing")
		}
	}
	return &ClockMap{anchors: a}, nil
}

// MapResult reports a single conversion.
type MapResult struct {
	VideoSec float64
	Mapped   bool
	Reason   string
}

// Map converts a record-clock instant. Outside anchor coverage it returns
// Mapped=false rather than extrapolating or assuming equal numbers.
func (m *ClockMap) Map(clockSec float64) MapResult {
	lo := m.anchors[0]
	hi := m.anchors[len(m.anchors)-1]
	if clockSec < lo.Clock || clockSec > hi.Clock {
		return MapResult{Mapped: false, Reason: "event clock is outside the range covered by explicit anchors"}
	}
	i := sort.Search(len(m.anchors)-1, func(i int) bool {
		return m.anchors[i+1].Clock >= clockSec
	})
	a, b := m.anchors[i], m.anchors[i+1]
	if clockSec == a.Clock {
		return MapResult{Mapped: true, VideoSec: a.VideoTime}
	}
	frac := (clockSec - a.Clock) / (b.Clock - a.Clock)
	return MapResult{Mapped: true, VideoSec: a.VideoTime + frac*(b.VideoTime-a.VideoTime)}
}

// Anchors returns the ordered anchor list.
func (m *ClockMap) Anchors() []Anchor {
	out := make([]Anchor, len(m.anchors))
	copy(out, m.anchors)
	return out
}

// Coverage returns the clock and video-time spans covered by anchors.
func (m *ClockMap) Coverage() (clockLo, clockHi, videoLo, videoHi float64) {
	lo := m.anchors[0]
	hi := m.anchors[len(m.anchors)-1]
	return lo.Clock, hi.Clock, lo.VideoTime, hi.VideoTime
}
