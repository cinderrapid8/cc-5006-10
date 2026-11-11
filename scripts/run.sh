#!/usr/bin/env bash
# Build frontend, run all tests, and start the assembly server.
# Override binaries via FFMPEG_BIN / FFPROBE_BIN; defaults assume PATH.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
FFMPEG_BIN="${FFMPEG_BIN:-ffmpeg}"
FFPROBE_BIN="${FFPROBE_BIN:-ffprobe}"
GO="${GO_BIN:-go}"

echo "== frontend build =="
(cd "$ROOT/frontend" && npm install && npm run test && npm run build)

echo "== backend tests =="
(cd "$ROOT/backend" && CGO_ENABLED=0 "$GO" test ./...)

echo "== start server =="
cd "$ROOT/backend"
exec "$GO" run ./cmd/server \
  -addr "${ADDR:-:8080}" \
  -data "${DATA_DIR:-$ROOT/backend/data}" \
  -ffmpeg "$FFMPEG_BIN" \
  -ffprobe "$FFPROBE_BIN" \
  -static "$ROOT/frontend/dist"
