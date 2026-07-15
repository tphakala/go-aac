# D0 rehearsal artifacts (2026-07-14)

Working output of the plan-d0.md rehearsal. Everything here was BUILT and
RUN on this machine; the measured results are recorded in plan-d0.md.

- `cdec.c` — the instrumented C decoder harness (tools/cdec in the plan).
  Compiles `libavcodec/aac/aacdec_fixed.c` from the pinned tree with the
  `vector_pow43` dump seam plus a `decode_spectrum_and_dequant` vtable
  hook. Link recipe (note libswresample, which the encoder harness recipe
  lacks):

      # FFMPEG_SRC is the pinned FFmpeg checkout, FFMPEG_BUILD its build tree
      # (the same variables tools/gentables/generate.sh uses).
      clang -O2 -DNDEBUG -I "$FFMPEG_BUILD" -I "$FFMPEG_SRC" cdec.c \
        "$FFMPEG_BUILD/libavcodec/libavcodec.a" \
        "$FFMPEG_BUILD/libavutil/libavutil.a" \
        "$FFMPEG_BUILD/libswresample/libswresample.a" \
        -lm -lpthread -o cdec

- `gencorpus/`, `gencraft/` — Go corpus generators. They import
  `github.com/tphakala/go-aac`, so run them from the repository root.
  `go run ./tools/cdec/gencorpus <dir>` writes the go-aac encoder
  streams (`goenc_s48_128k.adts`, `goenc_raw.rawau`, `goenc_raw.asc`).
  `go run ./tools/cdec/gencraft <dir>` crafts the streams no encoder
  emits (`pulse_m48.adts`, and `crc_s48.adts`, which it derives from a
  `tonal_s48_128k.adts` that must already be present in `<dir>`).
- `src/` — the rehearsed Go sources exactly as embedded in plan-d0.md
  (internal/bits reader, internal/vlc, internal/dec, root round-trip
  test). `src/dec/` maps to `internal/dec/`.
- `testdata/` — the committed-fixture corpus the rehearsal produced:
  11 streams + gzipped C dumps (1.3 MB). Regeneration is deterministic;
  the plan's Task 7 script reproduced these byte-identically twice.

Gate result at rehearsal: 11/11 streams, 2,409 frames, 140,219 dump lines
byte-identical, 1,999,224 symbol values identical, 0 errors.
