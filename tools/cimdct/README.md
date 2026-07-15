# D1 rehearsal artifacts (2026-07-14)

Working output of the plan-d1.md rehearsal. Everything here was BUILT and
RUN on this machine; the measured results are recorded in plan-d1.md.

- `cimdct.c` — the instrumented C oracle harness (tools/cimdct in the
  plan). Compiles `libavcodec/aac/aacdec_fixed.c` from the pinned tree into
  its own TU (the proven cdec pattern) plus a typed `tx_priv.h` include so
  the live AVTXContext twiddle/permutation tables are dumpable. Dumps
  integer windows, TX tables, IMDCT in/out pairs (sizes 1024 and 128) and
  chained `imdct_and_windowing` frames from LCG inputs. Link recipe
  (identical to cdec, note libswresample):

      # FFMPEG_SRC is the pinned FFmpeg checkout, FFMPEG_BUILD its build tree
      # (the same variables tools/gentables/generate.sh uses).
      clang -O2 -DNDEBUG -I "$FFMPEG_BUILD" -I "$FFMPEG_SRC" cimdct.c \
        "$FFMPEG_BUILD/libavcodec/libavcodec.a" \
        "$FFMPEG_BUILD/libavutil/libavutil.a" \
        "$FFMPEG_BUILD/libswresample/libswresample.a" \
        -lm -lpthread -o cimdct

- `fixtures/` — the two committed-fixture dumps (seeds 0x1f2e3d4c and
  0xdeadbeef), gzipped exactly as they land in
  `internal/dec/testdata/*.imdct.gz`. Regeneration is deterministic
  (cmp-verified twice).
- `src/` — the rehearsed Go sources exactly as embedded in plan-d1.md
  (`src/tx` -> `internal/tx`, `src/window` -> `internal/window`,
  `src/fdsp` -> `internal/fdsp`, `src/dec` -> `internal/dec`), plus
  `dec_go_and_lint.diff` (the SCE fields + depguard exemption).
- `gensinefix.py` — regenerates `sinefixed_tables.go` from a cimdct dump.
  The sine tables MUST be baked: Apple libm's sinf is not correctly
  rounded (14/1024 + 3/128 values one float ulp off), so no portable
  formula reproduces the oracle's runtime tables. See plan decision 4.

Gate result at rehearsal: 18/18 dumps (2 fixtures + 16-seed sweep)
byte-identical — 156 lines, 85,379 fields per dump; 51,264 computed values
compared per dump, 922,752 total. Full repo suite green under -race;
golangci-lint 0 issues. Benchmarks: IMDCT1024 3.54 us/op, IMDCT128 326
ns/op, 0 allocs.
