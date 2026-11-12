package domain

import "math"

// Pipeline stages. Every trace record is attributed to exactly one of them so
// a reported anomaly always names the processing stage that produced it.
const (
	StageInput    = "input"
	StageMapping  = "clock-mapping"
	StageSegments = "segment-selection"
	StageMerge    = "interval-merge"
	StageTimeline = "timeline-order"
	StageCut      = "media-cut"
	// StageNone marks informational evidence that indicates no defect, so an
	// observation is never mistaken for an attributed anomaly.
	StageNone = "none"
)

// Relations between two candidate segment windows. "Touch" (boundary meeting)
// is deliberately distinct from overlap: under a half-open model touching
// windows share no footage, so they cannot be the cause of a business-merge
// duplicate, which points the investigation at the cut stage instead.
const (
	RelationNone      = "none"
	RelationOverlap   = "overlap"
	RelationTouch     = "touch"
	RelationContained = "contained"
	RelationEqual     = "equal"
)

// TraceConfig bounds the evidence collected in one run. Counters stay exact
// even when detailed records are truncated, so diagnostic log cost is capped.
type TraceConfig struct {
	// MaxDetailed caps per-kind detailed records (windows, joins, cuts).
	MaxDetailed int
	// FrameRate marks boundaries against frame grids; 0 disables frame marks.
	FrameRate float64
	// KeyframeTimes, when present, lets the cut stage classify keyframe snaps.
	KeyframeTimes []float64
	// CutMode is media.CutReencode or media.CutKeyframe, kept as a string to
	// avoid an import cycle with the media package.
	CutMode string
}

// SegmentWindow is the stage-selection evidence for one mapped event.
type SegmentWindow struct {
	SegmentKey  string  `json:"segmentKey"`
	EventID     string  `json:"eventId"`
	Occurrence  int     `json:"occurrence"`
	ClockSec    float64 `json:"clockSec"`
	MappedVideo float64 `json:"mappedVideoSec"`
	WantedStart float64 `json:"wantedStart"`
	WantedEnd   float64 `json:"wantedEnd"`
	Pieces      []Piece `json:"pieces"`
	Empty       bool    `json:"empty"`
	ShortStart  bool    `json:"shortStart"`
	ShortEnd    bool    `json:"shortEnd"`
	CrossedGap  bool    `json:"crossedGap"`
}

// MergeJoin records one pairwise relation observed while building blocks.
type MergeJoin struct {
	KeyA       string  `json:"keyA"`
	KeyB       string  `json:"keyB"`
	Relation   string  `json:"relation"`
	StartA     float64 `json:"startA"`
	EndA       float64 `json:"endA"`
	StartB     float64 `json:"startB"`
	EndB       float64 `json:"endB"`
	SameBlock  bool    `json:"sameBlock"`
	BlockID    string  `json:"blockId,omitempty"`
	OverlapSec float64 `json:"overlapSec"`
}

// CutTrace is the media-stage evidence for one renderable piece. Planned is
// what the pipeline asked for; Actual is what the cut produced (the two differ
// only under keyframe approximation). Frame indices make the real frame order
// directly observable without re-probing the output file.
type CutTrace struct {
	BlockID        string   `json:"blockId"`
	SegmentKeys    []string `json:"segmentKeys"`
	Order          int      `json:"order"`
	PlannedStart   float64  `json:"plannedStart"`
	PlannedEnd     float64  `json:"plannedEnd"`
	ActualStart    float64  `json:"actualStart"`
	ActualEnd      float64  `json:"actualEnd"`
	StartFrame     int      `json:"startFrame"`
	EndFrame       int      `json:"endFrame"`
	Snapped        bool     `json:"snapped"`
	StartOnGrid    bool     `json:"startOnGrid"`
	EndOnGrid      bool     `json:"endOnGrid"`
	NearestStartKf float64  `json:"nearestStartKeyframe,omitempty"`
	NearestEndKf   float64  `json:"nearestEndKeyframe,omitempty"`
}

// TraceIssue is one diagnosed anomaly, attributed to a concrete stage.
type TraceIssue struct {
	Code       string `json:"code"`
	Stage      string `json:"stage"`
	Relation   string `json:"relation,omitempty"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	SegmentKey string `json:"segmentKey,omitempty"`
	BlockID    string `json:"blockId,omitempty"`
	KeyA       string `json:"keyA,omitempty"`
	KeyB       string `json:"keyB,omitempty"`
}

// TraceSnapshot is the complete replayable input of a pipeline run. Storing it
// alongside a report lets a later replay reconstruct identical evidence.
type TraceSnapshot struct {
	Events        []Event            `json:"events"`
	Meta          VideoMeta          `json:"meta"`
	Anchors       []Anchor           `json:"anchors"`
	Paddings      map[string]Padding `json:"paddings"`
	OverlapPolicy string             `json:"overlapPolicy"`
	BlockOrder    []string           `json:"blockOrder"`
	Included      map[string]bool    `json:"included,omitempty"`
	CutMode       string             `json:"cutMode"`
	FrameRate     float64            `json:"frameRate"`
	KeyframeTimes []float64          `json:"keyframeTimes,omitempty"`
}

// Tracer collects the highlight-chain evidence of one run. A nil *Tracer is a
// valid no-op: every method is nil-safe, so production code without a tracer
// pays for a nil check and nothing else, and selection results cannot change.
type Tracer struct {
	cfg TraceConfig

	stageOrder  []string
	stageCounts map[string]int

	events   []TraceEvent
	windows  []SegmentWindow
	joins    []MergeJoin
	cuts     []CutTrace
	issues   []TraceIssue
	snapshot *TraceSnapshot

	truncated map[string]int
	finished  bool
}

// TraceEvent records clock-mapping outcomes, including refusals.
type TraceEvent struct {
	EventID     string  `json:"eventId"`
	Occurrence  int     `json:"occurrence"`
	ClockSec    float64 `json:"clockSec"`
	Mapped      bool    `json:"mapped"`
	MappedVideo float64 `json:"mappedVideoSec,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

const defaultTraceDetailCap = 200

// NewTracer builds an enabled tracer; zero values get a safe default cap.
func NewTracer(cfg TraceConfig) *Tracer {
	if cfg.MaxDetailed <= 0 {
		cfg.MaxDetailed = defaultTraceDetailCap
	}
	return &Tracer{
		cfg:         cfg,
		stageCounts: map[string]int{},
		truncated:   map[string]int{},
	}
}

// Enabled reports whether tracing is on. A nil tracer is always disabled.
func (t *Tracer) Enabled() bool { return t != nil }

func (t *Tracer) allow(kind string, n int) bool {
	if n >= t.cfg.MaxDetailed {
		t.truncated[kind]++
		return false
	}
	return true
}

// Stage notes that the pipeline entered a stage; repeated entries are counted
// but the stage list stays ordered and de-duplicated.
func (t *Tracer) Stage(name string) {
	if t == nil {
		return
	}
	t.stageCounts[name]++
	for _, s := range t.stageOrder {
		if s == name {
			return
		}
	}
	t.stageOrder = append(t.stageOrder, name)
}

// RecordSnapshot freezes the replayable inputs. It is stored once, so it does
// not consume the bounded detailed-record budget.
func (t *Tracer) RecordSnapshot(s TraceSnapshot) {
	if t == nil {
		return
	}
	t.snapshot = &s
}

func (t *Tracer) RecordEvent(ev TraceEvent) {
	if t == nil {
		return
	}
	const kind = "events"
	if !t.allow(kind, len(t.events)) {
		return
	}
	t.events = append(t.events, ev)
}

func (t *Tracer) RecordWindow(w SegmentWindow) {
	if t == nil {
		return
	}
	const kind = "windows"
	if !t.allow(kind, len(t.windows)) {
		return
	}
	t.windows = append(t.windows, w)
}

func (t *Tracer) RecordJoin(j MergeJoin) {
	if t == nil {
		return
	}
	const kind = "joins"
	if !t.allow(kind, len(t.joins)) {
		return
	}
	t.joins = append(t.joins, j)
}

func (t *Tracer) RecordCut(c CutTrace) {
	if t == nil {
		return
	}
	const kind = "cuts"
	if !t.allow(kind, len(t.cuts)) {
		return
	}
	t.cuts = append(t.cuts, c)
}

// RecordIssue appends a diagnosed issue; issues share the same bounded budget
// so a pathological run cannot grow logs without limit.
func (t *Tracer) RecordIssue(i TraceIssue) {
	if t == nil {
		return
	}
	const kind = "issues"
	if !t.allow(kind, len(t.issues)) {
		return
	}
	t.issues = append(t.issues, i)
}

func (t *Tracer) Finish() {
	if t == nil {
		return
	}
	t.finished = true
}

// Config returns a copy of the tracer configuration.
func (t *Tracer) Config() TraceConfig {
	if t == nil {
		return TraceConfig{}
	}
	return t.cfg
}

// TraceReport is the serializable evidence document of one traced run.
type TraceReport struct {
	Stages          []string        `json:"stages"`
	StageEntries    map[string]int  `json:"stageEntries"`
	Config          TraceConfig     `json:"config"`
	Events          []TraceEvent    `json:"events"`
	Windows         []SegmentWindow `json:"segmentWindows"`
	Joins           []MergeJoin     `json:"mergeJoins"`
	Cuts            []CutTrace      `json:"cutTraces"`
	Issues          []TraceIssue    `json:"issues"`
	TruncatedCounts map[string]int  `json:"truncatedCounts"`
	TotalCounts     map[string]int  `json:"totalCounts"`
	Finished        bool            `json:"finished"`
}

// Report exports a value snapshot of all evidence plus exact totals. Exact
// counts (including truncated records) let diagnostics rely on numbers while
// the verbose evidence stays bounded.
func (t *Tracer) Report() TraceReport {
	if t == nil {
		return TraceReport{StageEntries: map[string]int{}, TruncatedCounts: map[string]int{}, TotalCounts: map[string]int{}}
	}
	return TraceReport{
		Stages:          append([]string(nil), t.stageOrder...),
		StageEntries:    copyCounts(t.stageCounts),
		Config:          t.cfg,
		Events:          append([]TraceEvent(nil), t.events...),
		Windows:         append([]SegmentWindow(nil), t.windows...),
		Joins:           append([]MergeJoin(nil), t.joins...),
		Cuts:            append([]CutTrace(nil), t.cuts...),
		Issues:          append([]TraceIssue(nil), t.issues...),
		TruncatedCounts: copyCounts(t.truncated),
		TotalCounts: map[string]int{
			"events":  len(t.events) + t.truncated["events"],
			"windows": len(t.windows) + t.truncated["windows"],
			"joins":   len(t.joins) + t.truncated["joins"],
			"cuts":    len(t.cuts) + t.truncated["cuts"],
			"issues":  len(t.issues) + t.truncated["issues"],
		},
		Finished: t.finished,
	}
}

// Snapshot returns the frozen replayable inputs, or false if none recorded.
func (t *Tracer) Snapshot() (TraceSnapshot, bool) {
	if t == nil || t.snapshot == nil {
		return TraceSnapshot{}, false
	}
	return *t.snapshot, true
}

func copyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ClassifyRelation distinguishes overlap from boundary touching. The half-open
// convention [start,end) makes touching windows (a.End == b.Start) share no
// footage; that distinction decides whether a duplicate can be a merge-stage
// fault or must originate at the cut stage.
func ClassifyRelation(a, b Range) string {
	switch {
	case a.Start == b.Start && a.End == b.End:
		return RelationEqual
	case (a.Start <= b.Start && b.End <= a.End) || (b.Start <= a.Start && a.End <= b.End):
		return RelationContained
	case a.Start < b.End && b.Start < a.End:
		return RelationOverlap
	case a.End == b.Start || b.End == a.Start:
		return RelationTouch
	default:
		return RelationNone
	}
}

// OverlapLength returns shared footage seconds; zero for touching/disjoint.
func OverlapLength(a, b Range) float64 {
	lo := math.Max(a.Start, b.Start)
	hi := math.Min(a.End, b.End)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

const frameEpsilon = 1e-6

// FrameIndexOf maps a video-time instant to its frame number at fps.
func FrameIndexOf(t, fps float64) int {
	if fps <= 0 {
		return -1
	}
	return int(math.Round(t*fps + frameEpsilon))
}

// OnFrameGrid reports whether t lands on a frame boundary at fps.
func OnFrameGrid(t, fps float64) bool {
	if fps <= 0 {
		return false
	}
	idx := t * fps
	return math.Abs(idx-math.Round(idx)) <= frameEpsilon
}
