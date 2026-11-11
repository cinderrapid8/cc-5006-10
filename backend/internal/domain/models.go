package domain

// Event is one entry of the annotated event list. Times are record-clock.
// Duplicate event numbers are allowed: identity is EventID + occurrence.
type Event struct {
	EventID string  `json:"eventId"`
	Type    string  `json:"type"`
	Clock   float64 `json:"clockSec"`
	ClockText string `json:"clockText,omitempty"`
	Team    string  `json:"team,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	// Occurrence disambiguates repeated event numbers; 0-based.
	Occurrence int `json:"occurrence"`
}

// Ref points back to an event that contributed to a segment/block.
type Ref struct {
	EventID    string  `json:"eventId"`
	Occurrence int     `json:"occurrence"`
	Type       string  `json:"type"`
	Clock      float64 `json:"clockSec"`
	ClockText  string  `json:"clockText,omitempty"`
	Detail     string  `json:"detail,omitempty"`
	// MappedVideo is the mapped event instant inside the video.
	MappedVideo float64 `json:"mappedVideoSec"`
}

// VideoMeta describes usable video bounds. Clips outside [Start,End] are
// truncated rather than silently treated as complete.
type VideoMeta struct {
	DurationSec float64 `json:"durationSec"`
	// AvailableStart/End default to 0 and DurationSec.
	AvailableStart float64 `json:"availableStart"`
	AvailableEnd   float64 `json:"availableEnd"`
	// Gaps are unavailable video-time ranges (missing footage, black bursts).
	Gaps []Range `json:"gaps"`
}

// Range is a half-open interval [Start, End) in video seconds.
type Range struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func (r Range) Len() float64 { return r.End - r.Start }

func (r Range) Overlaps(o Range) bool {
	return r.Start < o.End && o.Start < r.End
}

func (r Range) Contains(t float64) bool { return t >= r.Start && t < r.End }

// Padding is how much footage to keep before/after an event, per type.
type Padding struct {
	BeforeSec float64 `json:"beforeSec"`
	AfterSec  float64 `json:"afterSec"`
}

// Piece is one renderable sub-interval of a segment, after gap subtraction.
type Piece struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Segment is a candidate clip generated for one mapped event.
type Segment struct {
	Key      string  `json:"key"`
	Ref      Ref     `json:"ref"`
	// Wanted is the ideal clip window before clamping/gap subtraction.
	Wanted Range `json:"wanted"`
	// Pieces are the actually renderable video intervals.
	Pieces []Piece `json:"pieces"`
	// PlannedDuration is sum of piece lengths.
	PlannedDuration float64 `json:"plannedDuration"`
	// ShortStart/ShortEnd mean a side was clamped to available footage.
	ShortStart bool `json:"shortStart"`
	ShortEnd   bool `json:"shortEnd"`
	// CrossedGap means a declared gap removed part of the window.
	CrossedGap bool `json:"crossedGap"`
	// MissingInGap is footage seconds lost to gaps.
	MissingInGap float64 `json:"missingInGap"`
	// Empty means the whole window fell inside gaps: nothing renderable.
	Empty bool `json:"empty"`
	// Included marks the user's selection in the timeline.
	Included bool `json:"included"`
}

// Complete reports whether the segment covers its full wanted window.
// A short end or short start must never be presented as a full highlight.
func (s *Segment) Complete() bool {
	return !s.ShortStart && !s.ShortEnd && !s.CrossedGap
}

// Block is a timeline item: either one segment or a merge of overlapping ones.
// Merging never drops event references, so every source event stays traceable.
type Block struct {
	ID       string    `json:"id"`
	Refs     []Ref     `json:"refs"`
	Pieces   []Piece   `json:"pieces"`
	Segments []string  `json:"segmentKeys"`
	Start    float64   `json:"start"`
	End      float64   `json:"end"`
	Duration float64   `json:"duration"`
	ShortStart bool    `json:"shortStart"`
	ShortEnd   bool    `json:"shortEnd"`
	CrossedGap bool    `json:"crossedGap"`
	// Empty means no renderable footage remains (whole window in gaps).
	Empty    bool      `json:"empty"`
	Included bool      `json:"included"`
}

// Project is the saveable selection/editing document.
type Project struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	VideoPath   string             `json:"videoPath"`
	VideoMeta   VideoMeta          `json:"videoMeta"`
	Anchors     []Anchor           `json:"anchors"`
	Events      []Event            `json:"events"`
	Paddings    map[string]Padding `json:"paddings"`
	OverlapPolicy string           `json:"overlapPolicy"` // "merge" | "separate"
	Segments    []*Segment         `json:"segments"`
	Unmapped    []UnmappedEvent    `json:"unmapped"`
	// BlockOrder is the user-adjusted timeline order (block IDs).
	BlockOrder  []string           `json:"blockOrder"`
	Blocks      []*Block           `json:"blocks"`
	Version     int                `json:"version"`
}

// UnmappedEvent is an event that cannot be located without more anchors.
type UnmappedEvent struct {
	Event Event  `json:"event"`
	Reason string `json:"reason"`
}
