package trace

import (
	"fmt"

	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
)

// Severities used on every attributed issue.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// DuplicateCandidate is one suspected repeated-clip finding with the evidence
// needed to decide whether the cause is business merging or time conversion.
type DuplicateCandidate struct {
	KeyA        string  `json:"keyA"`
	KeyB        string  `json:"keyB"`
	Relation    string  `json:"relation"`
	OverlapSec  float64 `json:"overlapSec"`
	SameBlock   bool    `json:"sameBlock"`
	BlockID     string  `json:"blockId,omitempty"`
	RootStage   string  `json:"rootStage"`
	Code        string  `json:"code"`
	Severity    string  `json:"severity"`
	Explanation string  `json:"explanation"`
}

// TailLoss is one event whose planned tail was shortened or removed.
type TailLoss struct {
	SegmentKey  string  `json:"segmentKey"`
	BlockID     string  `json:"blockId,omitempty"`
	PlannedEnd  float64 `json:"plannedEnd"`
	ActualEnd   float64 `json:"actualEnd"`
	LostSec     float64 `json:"lostSec"`
	RootStage   string  `json:"rootStage"`
	Code        string  `json:"code"`
	Severity    string  `json:"severity"`
	Explanation string  `json:"explanation"`
}

// OrderAnomaly is one actual frame-order violation against the timeline.
type OrderAnomaly struct {
	BlockID     string `json:"blockId,omitempty"`
	FromFrame   int    `json:"fromFrame"`
	ToFrame     int    `json:"toFrame"`
	RootStage   string `json:"rootStage"`
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Explanation string `json:"explanation"`
}

// FrameBoundaryFinding records a planned cut boundary that misses the frame
// grid, i.e. the time-conversion source of tail loss/overlap at cut time.
type FrameBoundaryFinding struct {
	BlockID     string  `json:"blockId,omitempty"`
	At          string  `json:"at"`
	Time        float64 `json:"time"`
	FrameRate   float64 `json:"frameRate"`
	RootStage   string  `json:"rootStage"`
	Code        string  `json:"code"`
	Severity    string  `json:"severity"`
	Explanation string  `json:"explanation"`
}

// Diagnosis is the root-cause view derived from a tracer report.
type Diagnosis struct {
	CutMode             string                 `json:"cutMode"`
	FrameRate           float64                `json:"frameRate"`
	DuplicateCandidates []DuplicateCandidate   `json:"duplicateCandidates"`
	TailLosses          []TailLoss             `json:"tailLosses"`
	OrderAnomalies      []OrderAnomaly         `json:"orderAnomalies"`
	FrameBoundary       []FrameBoundaryFinding `json:"frameBoundaryFindings"`
	Issues              []domain.TraceIssue    `json:"issues"`
	// allCuts retains cut evidence for block-level cross-checks; it is bounded
	// by the tracer's per-kind cap.
	allCuts []domain.CutTrace
	// SummaryCounts are exact even when detailed evidence was truncated.
	SummaryCounts map[string]int `json:"summaryCounts"`
}

// Diagnose consumes the evidence collected by a tracer and attributes each
// anomaly. It only reads the report: running it twice on the same report
// produces the same diagnosis, and it never changes segment selection.
func Diagnose(rep domain.TraceReport) Diagnosis {
	d := Diagnosis{
		CutMode:       rep.Config.CutMode,
		FrameRate:     rep.Config.FrameRate,
		allCuts:       rep.Cuts,
		SummaryCounts: map[string]int{},
	}

	blockByKey := map[string]string{}
	for _, j := range rep.Joins {
		blockByKey[j.KeyA] = j.BlockID
		blockByKey[j.KeyB] = j.BlockID
	}

	d.classifyDuplicates(rep)
	d.classifyTailLosses(rep, blockByKey)
	d.classifyOrder(rep)
	d.classifyFrameBoundaries(rep)

	d.SummaryCounts = map[string]int{
		"duplicateCandidates": len(d.DuplicateCandidates),
		"tailLosses":          len(d.TailLosses),
		"orderAnomalies":      len(d.OrderAnomalies),
		"frameBoundary":       len(d.FrameBoundary),
		"recordedJoins":       rep.TotalCounts["joins"],
		"recordedCuts":        rep.TotalCounts["cuts"],
		"truncatedRecords":    sumTruncated(rep.TruncatedCounts),
	}
	// Replay-time issues (e.g. invalid anchors) are forwarded as-is.
	d.Issues = append(d.Issues, rep.Issues...)
	return d
}

// classifyDuplicates splits candidates by relation: shared footage (overlap /
// contained / equal) can only originate in business merging; boundary touching
// shares no footage under the half-open model, so a rendered duplicate there
// must come from cut-time keyframe expansion.
func (d *Diagnosis) classifyDuplicates(rep domain.TraceReport) {
	for _, j := range rep.Joins {
		switch j.Relation {
		case domain.RelationOverlap, domain.RelationContained, domain.RelationEqual:
			sev, code := SeverityInfo, "dup-shared-footage-merged"
			if !j.SameBlock {
				// Separate policy (or a grouping bug): the same footage is now
				// cut twice instead of being represented once.
				sev, code = SeverityWarning, "dup-overlap-not-merged"
			}
			d.DuplicateCandidates = append(d.DuplicateCandidates, DuplicateCandidate{
				KeyA: j.KeyA, KeyB: j.KeyB, Relation: j.Relation,
				OverlapSec: j.OverlapSec, SameBlock: j.SameBlock, BlockID: j.BlockID,
				RootStage: domain.StageMerge, Code: code, Severity: sev,
				Explanation: fmt.Sprintf(
					"windows share %.3fs of footage; under the half-open model this duplicate is decided at interval merge, not at cut time",
					j.OverlapSec),
			})
		case domain.RelationTouch:
			cand := DuplicateCandidate{
				KeyA: j.KeyA, KeyB: j.KeyB, Relation: j.Relation,
				OverlapSec: 0, SameBlock: j.SameBlock, BlockID: j.BlockID,
			}
			if d.cutExpandsBoundary(j) {
				cand.RootStage = domain.StageCut
				cand.Code = "dup-touch-expanded-by-keyframe"
				cand.Severity = SeverityWarning
				cand.Explanation = "windows only meet at a boundary (no shared footage); keyframe snapping expands the cut across the boundary, so the repeated frames originate in media cutting"
			} else {
				// No cut expansion: touching half-open windows share no footage
				// and stay independently addressable, so there is no defect to
				// attribute. Record it as informational evidence only.
				cand.RootStage = domain.StageNone
				cand.Code = "dup-touch-no-shared-footage"
				cand.Severity = SeverityInfo
				cand.Explanation = "windows only meet at a boundary and no cut expansion is present; no footage is repeated, so this is evidence, not a defect"
			}
			d.DuplicateCandidates = append(d.DuplicateCandidates, cand)
		}
	}
}

// cutExpandsBoundary reports whether any cut touching the join boundary grew
// past its planned range, which is the only mechanism by which boundary-touching
// segments can end up sharing rendered frames.
func (d *Diagnosis) cutExpandsBoundary(j domain.MergeJoin) bool {
	if d.CutMode != media.CutKeyframe {
		return false
	}
	for _, c := range d.cutsForBlock(j.BlockID) {
		if !c.Snapped {
			continue
		}
		// The cut grew to cover the neighbor's side of the boundary.
		if (c.PlannedStart == j.EndA && c.ActualStart < c.PlannedStart) ||
			(c.PlannedStart == j.EndB && c.ActualStart < c.PlannedStart) ||
			(c.PlannedEnd == j.EndA && c.ActualEnd > c.PlannedEnd) ||
			(c.PlannedEnd == j.EndB && c.ActualEnd > c.PlannedEnd) {
			return true
		}
	}
	return false
}

func (d *Diagnosis) cutsForBlock(blockID string) []domain.CutTrace {
	var out []domain.CutTrace
	for _, c := range d.allCuts {
		if c.BlockID == blockID {
			out = append(out, c)
		}
	}
	return out
}

// classifyTailLosses distinguishes footage lost before cutting (availability
// clamp, declared gaps, fully-empty windows) from footage lost while cutting
// (keyframe snap or frame rounding).
func (d *Diagnosis) classifyTailLosses(rep domain.TraceReport, blockByKey map[string]string) {
	for _, w := range rep.Windows {
		blockID := blockByKey[w.SegmentKey]
		switch {
		case w.Empty:
			d.TailLosses = append(d.TailLosses, TailLoss{
				SegmentKey: w.SegmentKey, BlockID: blockID,
				PlannedEnd: w.WantedEnd, ActualEnd: w.WantedStart, LostSec: w.WantedEnd - w.WantedStart,
				RootStage: domain.StageSegments, Code: "tail-lost-in-gaps", Severity: SeverityError,
				Explanation: "the whole wanted window falls inside declared gaps; nothing renderable survives segment selection",
			})
		case w.ShortEnd:
			d.TailLosses = append(d.TailLosses, TailLoss{
				SegmentKey: w.SegmentKey, BlockID: blockID,
				PlannedEnd: w.WantedEnd, ActualEnd: w.Pieces[len(w.Pieces)-1].End,
				LostSec:   w.WantedEnd - w.Pieces[len(w.Pieces)-1].End,
				RootStage: domain.StageSegments, Code: "tail-clamped-to-available", Severity: SeverityWarning,
				Explanation: "the wanted end exceeds available footage and was clamped; the event tail is absent before any cut runs",
			})
		case w.CrossedGap:
			last := w.Pieces[len(w.Pieces)-1]
			if last.End < w.WantedEnd {
				d.TailLosses = append(d.TailLosses, TailLoss{
					SegmentKey: w.SegmentKey, BlockID: blockID,
					PlannedEnd: w.WantedEnd, ActualEnd: last.End, LostSec: w.WantedEnd - last.End,
					RootStage: domain.StageSegments, Code: "tail-partly-in-gap", Severity: SeverityWarning,
					Explanation: "a declared gap removes part of the window; the loss happens at segment selection, not at cut time",
				})
			}
		}
	}

	// Cut-time losses: actual range shorter than the planned renderable range.
	for _, c := range rep.Cuts {
		lost := c.PlannedEnd - c.ActualEnd
		if lost <= 1e-6 {
			continue
		}
		code, sev := "tail-lost-at-cut", SeverityWarning
		if c.Snapped {
			code = "tail-lost-to-keyframe-snap"
		}
		d.TailLosses = append(d.TailLosses, TailLoss{
			BlockID: c.BlockID, PlannedEnd: c.PlannedEnd, ActualEnd: c.ActualEnd,
			LostSec: lost, RootStage: domain.StageCut, Code: code, Severity: sev,
			Explanation: fmt.Sprintf("planned end %.3f was cut at %.3f; the tail disappears during time-to-frame conversion", c.PlannedEnd, c.ActualEnd),
		})
	}
}

// classifyOrder checks the actual frame sequence produced by the cut stage
// against timeline order. Within one output, later cuts must not start on an
// earlier source frame than the cut before them.
func (d *Diagnosis) classifyOrder(rep domain.TraceReport) {
	var prev *domain.CutTrace
	for i := range rep.Cuts {
		c := rep.Cuts[i]
		if prev != nil && c.StartFrame >= 0 && prev.StartFrame >= 0 && c.StartFrame < prev.StartFrame {
			rootStage, code := domain.StageTimeline, "order-timeline-reversed"
			if c.BlockID == prev.BlockID && c.Snapped {
				rootStage, code = domain.StageCut, "order-keyframe-overlap"
			}
			d.OrderAnomalies = append(d.OrderAnomalies, OrderAnomaly{
				BlockID: c.BlockID, FromFrame: prev.StartFrame, ToFrame: c.StartFrame,
				RootStage: rootStage, Code: code, Severity: SeverityWarning,
				Explanation: fmt.Sprintf("cut starts at frame %d after a previous cut starting at frame %d; actual frame order contradicts the planned sequence", c.StartFrame, prev.StartFrame),
			})
		}
		cur := c
		prev = &cur
	}
}

// classifyFrameBoundaries flags planned cut instants that do not land on a
// frame of the source. Such boundaries are the time-conversion precondition
// for rounding/snapping differences and are attributed to the cut stage.
func (d *Diagnosis) classifyFrameBoundaries(rep domain.TraceReport) {
	if rep.Config.FrameRate <= 0 {
		return
	}
	for _, c := range rep.Cuts {
		if !c.StartOnGrid {
			d.FrameBoundary = append(d.FrameBoundary, FrameBoundaryFinding{
				BlockID: c.BlockID, At: "start", Time: c.PlannedStart, FrameRate: rep.Config.FrameRate,
				RootStage: domain.StageCut, Code: "boundary-off-frame-grid", Severity: SeverityInfo,
				Explanation: "planned start is not a frame boundary; cutting must round or keyframe-snap it",
			})
		}
		if !c.EndOnGrid {
			d.FrameBoundary = append(d.FrameBoundary, FrameBoundaryFinding{
				BlockID: c.BlockID, At: "end", Time: c.PlannedEnd, FrameRate: rep.Config.FrameRate,
				RootStage: domain.StageCut, Code: "boundary-off-frame-grid", Severity: SeverityInfo,
				Explanation: "planned end is not a frame boundary; cutting must round or keyframe-snap it",
			})
		}
	}
}

func sumTruncated(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
