package media

import "testing"

func TestSnapToKeyframesClampsEndToFinalKeyframe(t *testing.T) {
	keys := []float64{0, 2, 4, 6, 8, 10}

	// Normal case: earliest keyframe >= requested end wins.
	got := SnapToKeyframes(PiecePlan{Start: 5, End: 7}, keys)
	if got.Start != 4 || got.End != 8 {
		t.Errorf("normal snap = %+v, want {4 8}", got)
	}

	// Requested end beyond the final keyframe must clamp to the final
	// keyframe rather than keep an unseekable requested boundary.
	got = SnapToKeyframes(PiecePlan{Start: 9.5, End: 12.5}, keys)
	if got.Start != 8 || got.End != 10 {
		t.Errorf("EOF snap = %+v, want {8 10}", got)
	}

	// Requested start before the first keyframe keeps the first keyframe.
	got = SnapToKeyframes(PiecePlan{Start: -1, End: 3}, keys)
	if got.Start != 0 || got.End != 4 {
		t.Errorf("leading snap = %+v, want {0 4}", got)
	}

	// Empty keyframe list leaves the plan untouched instead of panicking.
	got = SnapToKeyframes(PiecePlan{Start: 1, End: 2}, nil)
	if got.Start != 1 || got.End != 2 {
		t.Errorf("empty keyframes should not alter plan, got %+v", got)
	}
}
