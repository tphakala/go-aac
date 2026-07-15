# cquant

Generates golden fixtures for `internal/coder` by running the REAL C coder of
the pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a) on known
inputs: `search_for_quantizers_fast`, `codebook_trellis_rate` and
`quantize_and_encode_band`. The Go ports are then asserted to reproduce the
C's band decisions (sf_idx, band_type, zeroes) and its bitstreams exactly.

Unlike tools/gentables, this one DOES link libavcodec.a:

    clang -O2 -I<ffbuild> -I<FFmpeg> cquant.c \
        <ffbuild>/libavcodec/libavcodec.a \
        <ffbuild>/libavutil/libavutil.a \
        -lm -lpthread -o cquant

Phase 2 added grouped EIGHT_SHORT fixtures (short_*.f32, short_*.i32,
short_*.u8): the same three
C functions run on a castanet-like 8-short-window frame with group_len
{1,3,0,0,3,0,0,1}, pinning the w*16+g convention on real window groups.

Fixtures are committed under internal/coder/testdata/; the binary is not.
Rerun only if the pin changes, and regenerate ALL fixtures together (they
share one input signal).
