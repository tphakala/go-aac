#!/bin/sh
# SPDX-License-Identifier: LGPL-2.1-or-later
# Regenerates internal/tables/tables_gen.go and testdata/ctables.bin from the
# pinned FFmpeg tree. Invoked via go generate ./internal/tables (the
# //go:generate directive in tables.go). Requires the pinned source checkout
# and the prebuilt out-of-tree build directory (for config.h); set
# FFMPEG_SRC and FFMPEG_BUILD to point at them.
set -eu

FFMPEG_SRC="${FFMPEG_SRC:?set FFMPEG_SRC to the pinned FFmpeg source tree}"
FFMPEG_BUILD="${FFMPEG_BUILD:?set FFMPEG_BUILD to the FFmpeg build directory}"
PIN=d09d5afc3aebede25d2d245ee23b75a47ea17c3a

HEAD=$(git -C "$FFMPEG_SRC" rev-parse HEAD)
if [ "$HEAD" != "$PIN" ]; then
    echo "gentables: FFmpeg tree at $FFMPEG_SRC is $HEAD, need $PIN" >&2
    exit 1
fi

TOOLS_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TABLES_DIR="$TOOLS_DIR/../../internal/tables"

clang -O2 -I"$FFMPEG_BUILD" -I"$FFMPEG_SRC" "$TOOLS_DIR/gentables.c" \
    "$FFMPEG_BUILD/libavutil/libavutil.a" \
    -lm -lpthread -o "$TOOLS_DIR/gentables"

"$TOOLS_DIR/gentables" go > "$TABLES_DIR/tables_gen.go"
"$TOOLS_DIR/gentables" bin > "$TABLES_DIR/testdata/ctables.bin"

UNFORMATTED=$(gofmt -l "$TABLES_DIR/tables_gen.go")
if [ -n "$UNFORMATTED" ]; then
    echo "gentables: generated Go is not gofmt-clean: $UNFORMATTED" >&2
    exit 1
fi

echo "gentables: wrote tables_gen.go and testdata/ctables.bin"
