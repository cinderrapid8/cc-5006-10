package trace

import "highlight-assembly/internal/domain"

// SnapshotFromProject freezes a stored project into replayable inputs. It is
// a pure projection: the project is not modified, so diagnosing an existing
// project cannot change its saved segment selection.
func SnapshotFromProject(p *domain.Project, cutMode string, frameRate float64, keyframes []float64) domain.TraceSnapshot {
	included := map[string]bool{}
	for _, b := range p.Blocks {
		included[b.ID] = b.Included
	}
	return domain.TraceSnapshot{
		Events:        append([]domain.Event(nil), p.Events...),
		Meta:          p.VideoMeta,
		Anchors:       append([]domain.Anchor(nil), p.Anchors...),
		Paddings:      clonePaddings(p.Paddings),
		OverlapPolicy: p.OverlapPolicy,
		BlockOrder:    append([]string(nil), p.BlockOrder...),
		Included:      included,
		CutMode:       cutMode,
		FrameRate:     frameRate,
		KeyframeTimes: append([]float64(nil), keyframes...),
	}
}

func clonePaddings(in map[string]domain.Padding) map[string]domain.Padding {
	if in == nil {
		return nil
	}
	out := make(map[string]domain.Padding, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
