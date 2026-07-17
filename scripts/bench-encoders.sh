#!/usr/bin/env bash
#
# bench-encoders.sh - compare go-aac against FFmpeg's native AAC encoder (the
# C this library is ported from) on the same input: encode single-threaded,
# measuring wall time, CPU seconds and peak RSS (GNU time), plus the resulting
# stream size. This is the re-runnable basis for the performance baseline.
#
# Usage:
#   scripts/bench-encoders.sh [input.wav]
#
# With no argument it generates a deterministic 2-minute mono 48 kHz/16-bit
# tone+noise mix (fixed seed) so the run is reproducible across machines.
#
# The FFmpeg used MUST be the pinned oracle build (d09d5afc3a or compatible),
# named by GOAAC_FFMPEG. Distro FFmpeg will not do: 7.x and earlier ship a
# different coder set (anmr/twoloop/fast, twoloop default) whose 'anmr' is a
# different implementation from the 'nmr' scalefactor trellis this library
# ports and which FFmpeg made the default. Comparing against it measures the
# wrong C. The script refuses to run if the binary lacks an 'nmr' coder.
#
# Requires: go, GNU time, and GOAAC_FFMPEG pointing at the pinned ffmpeg.
#
# This script only ever reads its input and writes outputs into a private temp
# directory; it never deletes or modifies the input file.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

have() { command -v "$1" >/dev/null 2>&1; }

# macOS ships a BSD time with no -f, so look for GNU's gtime as well.
gnu_time=""
for t in gtime /opt/homebrew/bin/gtime /usr/bin/time time; do
  if command -v "$t" >/dev/null 2>&1 && "$t" -f '%e' true >/dev/null 2>&1; then gnu_time="$t"; break; fi
done
[ -n "$gnu_time" ] || { echo "GNU time not found (Linux: install the 'time' package; macOS: brew install gnu-time)"; exit 1; }
have go || { echo "go not found"; exit 1; }

ff="${GOAAC_FFMPEG:-}"
[ -n "$ff" ] || { echo "set GOAAC_FFMPEG to the pinned ffmpeg binary (see PROVENANCE.md)"; exit 1; }
[ -x "$ff" ] || { echo "GOAAC_FFMPEG=$ff is not an executable"; exit 1; }
if ! "$ff" -hide_banner -h encoder=aac 2>/dev/null | grep -qE '^ +nmr +[0-9]'; then
  echo "GOAAC_FFMPEG=$ff has no 'nmr' coder, so it is not the pinned oracle."
  echo "Distro builds ship anmr/twoloop/fast and would measure the wrong C."
  exit 1
fi

bitrate="${GOAAC_BENCH_BR:-128000}"
kbps=$((bitrate / 1000))

input="${1:-}"
if [ -z "$input" ]; then
  input="$work/bench_input.wav"
  echo "generating deterministic 2-min mono 48 kHz/16-bit input..."
  "$ff" -hide_banner -loglevel error -y \
    -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=120" \
    -f lavfi -i "anoisesrc=sample_rate=48000:amplitude=0.25:duration=120:seed=42" \
    -filter_complex "amix=inputs=2" -ac 1 -ar 48000 -c:a pcm_s16le "$input"
fi
[ -f "$input" ] || { echo "input not found: $input"; exit 1; }

filesize() { wc -c <"$1" | tr -d ' '; }
insz=$(filesize "$input")
echo "input: $input ($insz bytes)"
echo "ffmpeg: $("$ff" -hide_banner -version 2>/dev/null | head -1)"

echo "building wav2aac..."
go build -o "$work/wav2aac" "$repo_root/tools/wav2aac"

cat "$input" >/dev/null # warm the page cache

# Duration in seconds, for the realtime multiple. Ask ffmpeg rather than
# assuming 48 kHz mono 16-bit: this script accepts any WAV, and a hardcoded
# stride silently reports the wrong realtime figure for stereo, 44.1 kHz or
# 32-bit input, which is the one number the whole script exists to produce.
# `ffmpeg -i` with no output file always exits nonzero ("At least one output
# file must be specified"), which under set -e plus pipefail would kill the
# script on this assignment even though the parse succeeded.
dur=$({ "$ff" -hide_banner -i "$input" 2>&1 || true; } |
  awk -F'[:,]' '/Duration:/ {print ($2*3600)+($3*60)+$4; exit}')
case "$dur" in
  ''|*[!0-9.]*) echo "could not determine duration of $input"; exit 1 ;;
esac
awk "BEGIN{exit !($dur > 0)}" || { echo "input $input has zero duration"; exit 1; }
echo "duration: ${dur}s"

# bench NAME OUTFILE -- CMD...   (the output path is explicit; the input is read-only)
bench() {
  local name="$1" out="$2"; shift 2
  [ "$1" = "--" ] && shift
  if "$gnu_time" -f '%e %U %S %P %M' -o "$work/tm" "$@" >"$work/log" 2>&1; then
    read -r e u s p m <"$work/tm"
    local osz cpu rt
    osz=$(filesize "$out")
    cpu=$(awk "BEGIN{printf \"%.2f\", $u+$s}")
    rt=$(awk "BEGIN{e=$e; if (e>0) printf \"%.0fx\", $dur/e; else printf \"n/a\"}")
    printf '%-16s wall=%ss cpu=%ss %%cpu=%s rss=%sKB realtime=%s out=%s\n' \
      "$name" "$e" "$cpu" "$p" "$m" "$rt" "$osz"
  else
    printf '%-16s FAILED (see output below)\n' "$name"
    cat "$work/log"
    return 1
  fi
}

# Track whether any measurement failed, so the script cannot exit green after
# printing FAILED. set -e does not catch bench because it runs in an if/||
# context below.
failed=0

for coder in nmr twoloop fast; do
  echo "=== encode, ${kbps} kbps, coder=${coder}, single-threaded ==="
  bench "go-aac/$coder" "$work/o_go_$coder.aac" -- \
    "$work/wav2aac" -b "$bitrate" -coder "$coder" "$input" "$work/o_go_$coder.aac" || failed=1
  bench "ffmpeg/$coder" "$work/o_ff_$coder.aac" -- \
    "$ff" -hide_banner -loglevel error -y -threads 1 -i "$input" \
    -c:a aac -aac_coder "$coder" -b:a "$bitrate" -threads 1 \
    -f adts "$work/o_ff_$coder.aac" || failed=1
done

echo
echo "note: go-aac and FFmpeg are not byte-identical end to end (the port is"
echo "validated per subsystem and on decoded quality, not on the whole stream),"
echo "so compare sizes for parity, not equality."

if [ "$failed" -ne 0 ]; then
  echo
  echo "one or more measurements FAILED; the numbers above are incomplete"
  exit 1
fi
