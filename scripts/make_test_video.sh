#!/usr/bin/env bash
# Generates a known test fixture: 60s, 30fps, GOP=2s, yuv420p + sine audio.
# No drawtext needed: tests verify cut points via ffprobe frame PTS metadata.
set -euo pipefail

FFMPEG="${FFMPEG_BIN:-ffmpeg}"
OUT="${1:-testdata/match60.mp4}"
mkdir -p "$(dirname "$OUT")"

"$FFMPEG" -y -hide_banner -loglevel error \
  -f lavfi -i "testsrc2=size=640x360:rate=30:duration=60" \
  -f lavfi -i "sine=frequency=440:duration=60" \
  -c:v libx264 -preset veryfast -crf 28 -pix_fmt yuv420p \
  -g 60 -keyint_min 60 -sc_threshold 0 \
  -c:a aac -b:a 64k -shortest \
  "$OUT"

echo "created $OUT"
"${FFPROBE_BIN:-ffprobe}" -v error -show_entries format=duration -of csv=p=0 "$OUT"
