# cpsy

Generates golden fixtures for `internal/psy` by running the REAL C
psychoacoustic model of the pinned FFmpeg tree
(d09d5afc3aebede25d2d245ee23b75a47ea17c3a) on known inputs: `ff_psy_init`
(from the archive's psymodel.o), `psy_3gpp_init`, `psy_lame_window` and
`psy_3gpp_analyze` (via `#include "libavcodec/aacpsy.c"`). The Go port is
then asserted to reproduce the init coefficients, every window decision,
the per-band analysis outputs and the bit reservoir state.

Unlike tools/gentables, this one DOES link libavcodec.a:

    clang -O2 -DNDEBUG -I<ffbuild> -I<FFmpeg> cpsy.c \
        <ffbuild>/libavcodec/libavcodec.a \
        <ffbuild>/libavutil/libavutil.a \
        -o cpsy

Modes:

    cpsy init  <out.bin> <rate> <bitrate> <channels>
    cpsy trace <in.f32> <out.bin> <rate> <bitrate> <channels> <nsamples>

The trace input PCM is written by the Go side
(`GOAAC_PSY_WRITE_INPUT=... go test ./internal/psy/ -run
TestWritePsyFixtureInput`) so both sides analyze bit-identical samples.
Each frame's analysis runs TWICE with the same frame_num to pin the
rate-loop rewind semantics.

Fixtures are committed under internal/psy/testdata/; the binary is not.
Rerun only if the pin changes, and regenerate ALL fixtures together (init
and traces share the harness).
