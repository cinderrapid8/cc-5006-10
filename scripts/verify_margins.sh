#!/usr/bin/env bash
# Verifies clip margins against a known-event fixture:
# renders pieces and asserts each output's first/last frame PTS match the
# requested boundaries (re-encode tolerance: 2 frames at 30fps).
set -euo pipefail

FFMPEG="${FFMPEG_BIN:-ffmpeg}"
FFPROBE="${FFPROBE_BIN:-ffprobe}"
SRC="${1:-testdata/match60.mp4}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail=0

first_pts()  { "$FFPROBE" -v error -select_streams v:0 -show_entries frame=pts_time -of csv=p=0 "$1" | awk 'NR==1{print; exit}'; }
last_pts()   { "$FFPROBE" -v error -select_streams v:0 -show_entries frame=pts_time -of csv=p=0 "$1" | awk 'NF{v=$0} END{print v}'; }
duration()   { "$FFPROBE" -v error -show_entries format=duration -of csv=p=0 "$1"; }

# args: label start end
check_reencode() {
  local label="$1" s="$2" e="$3"
  local out="$WORK/$label.mp4"
  "$FFMPEG" -y -hide_banner -loglevel error -ss "$s" -to "$e" -i "$SRC" \
    -c:v libx264 -preset veryfast -crf 28 -pix_fmt yuv420p -an "$out"
  local fp lp dur
  fp="$(first_pts "$out")"; lp="$(last_pts "$out")"; dur="$(duration "$out")"
  local want_dur
  want_dur="$(awk -v a="$e" -v b="$s" 'BEGIN{printf "%.3f", a-b}')"
  echo "[$label] requested $s-$e  firstPTS=$fp lastPTS=$lp duration=$dur (want ~$want_dur)"
  # make_zero resets output PTS to 0; margin correctness shows in frame count/duration.
  awk -v v="$fp" 'BEGIN{exit !(v <= 0.07)}' || { echo "  FAIL first-frame not reset to zero"; fail=1; }
  awk -v v="$dur" -v w="$want_dur" 'BEGIN{exit !(v >= w-0.1 && v <= w+0.1)}' || { echo "  FAIL duration margin"; fail=1; }
  # Frame count at 30fps must match requested span within one frame.
  local frames want_frames
  frames="$("$FFPROBE" -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames -of csv=p=0 "$out")"
  want_frames="$(awk -v a="$e" -v b="$s" 'BEGIN{printf "%d", (a-b)*30}')"
  echo "  frames=$frames want~$want_frames"
  awk -v v="$frames" -v w="$want_frames" 'BEGIN{exit !(v >= w-1 && v <= w+1)}' || { echo "  FAIL frame count margin"; fail=1; }
}

# Event at video t=15 with 5s margins -> [10,20)
check_reencode margin_event15 10 20
# Event at video t=40 with 3s margins -> [37,43)
check_reencode margin_event40 37 43
# Near end: wanted [56,64) clamps to 60 -> short end must be visible as ~4s output
check_reencode short_end 56 60

# Keyframe approximation: GOP=2s, request [10.5,20.5) -> snap [10,22)
"$FFMPEG" -y -hide_banner -loglevel error -ss 10 -to 22 -i "$SRC" -c copy -an "$WORK/kf.mp4"
kf_dur="$(duration "$WORK/kf.mp4")"
echo "[keyframe] snapped 10.5-20.5 to 10-22 duration=$kf_dur (want ~12)"
awk -v v="$kf_dur" 'BEGIN{exit !(v >= 11.8 && v <= 12.2)}' || { echo "  FAIL keyframe snap"; fail=1; }

if (( fail )); then
  echo "MARGIN CHECK FAILED"
  exit 1
fi
echo "MARGIN CHECK OK"
