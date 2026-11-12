#!/usr/bin/env bash
# Runs the highlight-chain tracing/diagnostic evidence suite.
#
# The suite is pure Go: frame snapping and replay use the same functions as the
# real renderer, so it does not need ffmpeg/ffprobe and stays fast enough to run
# on every change. Set FFMPEG_BIN/FFPROBE_BIN to additionally exercise the
# media and end-to-end API tests.
set -euo pipefail

GO_BIN="${GO_BIN:-go}"
export CGO_ENABLED=0

echo "== highlight-chain tracer + diagnosis tests =="
"$GO_BIN" test ./internal/domain/ ./internal/trace/ ./internal/media/ ./internal/api/ \
  -count=1 -run 'Trace|Tracer|Relation|Frame|Replay|Diagnose|Snap|Tracing' -v

echo
echo "== evidence bound check (verbose records are capped, totals stay exact) =="
"$GO_BIN" test ./internal/domain/ -count=1 -run TestTracerBoundsEvidenceAndKeepsExactTotals -v
"$GO_BIN" test ./internal/trace/ -count=1 -run TestReplayEvidenceIsBoundedButTotalsStayExact -v

echo
echo "DIAGNOSTIC TRACE TESTS OK"
