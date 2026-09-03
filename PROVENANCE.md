# Provenance

go-aac is a derivative work of FFmpeg's native AAC-LC encoder **and decoder**.
The codec logic in this repository is ported, function by function, from the
FFmpeg source tree pinned at commit:

    d09d5afc3aebede25d2d245ee23b75a47ea17c3a

Because it derives from LGPL-licensed FFmpeg code, this repository is
licensed under the GNU Lesser General Public License, version 2.1 or later
(see LICENSE), and can never be relicensed under a permissive license.

## The oracle build

The differential gates and the C harnesses under `tools/` compare against a
locally built FFmpeg from the pinned commit. That commit alone does not
specify the binary. The compiler and its flags decide the arithmetic, so the
build recipe is part of the pin:

    ./configure --disable-doc --disable-network --disable-autodetect \
                --disable-programs --enable-ffmpeg \
                --extra-cflags=-ffp-contract=off

`--disable-programs --enable-ffmpeg` only skips building ffprobe and ffplay,
which the gates never invoke; it does not change libav* behaviour. The
contraction flag does, and it is required.

### Why `-ffp-contract=off` is required

FFmpeg's own configure never passes `-ffp-contract`, so every build inherits
its compiler's default, and the defaults disagree. GCC in `-std=c17` mode,
which is what FFmpeg configures, selects the ISO behaviour and does not
contract. Apple clang contracts by default. Baseline x86-64 has no FMA
instruction, so it cannot contract whatever the flag says. The same source
commit therefore compiles to different arithmetic on different hosts.

The site that matters is the intensity-stereo energy accumulation in
`ff_aac_search_for_is` (libavcodec/aacenc_is.c:136-139):

    ener0  += coef0*coef0;
    ener1  += coef1*coef1;
    ener01 += (coef0 + coef1)*(coef0 + coef1);
    ener01p += (coef0 - coef1)*(coef0 - coef1);

Each `x += y*z` is a single expression, so contraction fuses it into one
`fmadd` and changes the result by a ULP or two. That is enough to flip
quantization decisions downstream, which is why only the stereo gate cases
diverge and the mono ones do not. Measured on the twoloop stereo castanets
192k cell, contraction moves the C encoder's own PSNR by about 0.41 dB, an
order of magnitude more than the two other candidate variables: differing
libm implementations measured 0.00 dB and FFmpeg's hand-written SIMD kernels
0.015 dB.

Contraction-off is the semantics this port reproduces, not an arbitrary
choice. Every committed C fixture under `internal/*/testdata/` was generated
by a contraction-off build, the generators in `tools/ctwoloop` and `tools/ctns`
record that in their headers, and `internal/coder/stereo_tools.go` splits the
product into a temporary specifically to stop the Go compiler fusing the same
expression. A contraction-on oracle does not fail loudly. It quietly becomes a
different reference, and the PSNR gates then measure the port against
arithmetic it was never written to reproduce.

### Keeping the recipe honest

CI builds the oracle from `FFMPEG_CONFIGURE_FLAGS` in
`.github/workflows/ci.yml`, hashes that same variable into the FFmpeg cache
key so a flag change cannot be served a stale binary, and asserts the flag is
present in the binary it ends up using. Anyone building a local oracle must
use the recipe above; check an existing build with:

    ffmpeg -hide_banner -buildconf | grep -F -- '-ffp-contract=off'

The C harnesses in `tools/` `#include` FFmpeg `.c` files directly, so they
must be compiled with the same flag as the libraries they link against, or the
fixtures they regenerate will not match the committed ones.

## Primary C sources

The port draws from these files of the pinned tree (some arrive in later
phases of the port):

- libavcodec/aacenc.c, aacenc.h (encoder core, rate control)
- libavcodec/aaccoder.c, aaccoder_nmr.h, aaccoder_twoloop.h (coders)
- libavcodec/aacpsy.c (3GPP psychoacoustic model)
- libavcodec/aacenc_tns.c and related tool files (TNS, PNS, IS/MS)
- libavcodec/aactab.c, aacenctab.h, libavcodec/mpeg4audio.c (tables)
- libavcodec/kbdwin.c, sinewin_tablegen.h (analysis windows)
- libavcodec/put_bits.h (bit writer semantics)
- libavcodec/lpc.c (TNS LPC)
- libavutil/tx.c, tx_template.c (MDCT), libavutil/mathematics.c (Bessel I0)
- libavformat/adtsenc.c (ADTS framing)

### Decoder

The decoder is ported from the same pinned tree. Note it lives under
`libavcodec/aac/`, not `libavcodec/`, and there is no `aacdec_template.c` at
this commit:

- libavcodec/aac/aacdec.c (decoder core, ADTS/ASC parsing, syntax elements)
- libavcodec/adts_header.c (ff_adts_header_parse, the ADTS frame header the
  streaming decode API frames on)
- libavcodec/aac/aacdec_tab.c (VLC construction, `init_base_tables`)
- libavcodec/aac/aacdec_proc_template.c (spectral symbol decode loop)
- libavcodec/aac/aacdec_dsp_template.c (`imdct_and_windowing`, USE_FIXED)
- libavcodec/aac/aacdec_fixed_dequant.h (DEC_SQUAD/UQUAD/SPAIR/UPAIR)
- libavcodec/aac/aacdec_fixed.c (fixed-point decoder instantiation)
- libavcodec/get_bits.h (bit reader semantics)
- libavcodec/kbdwin.c (integer KBD window), sinewin_fixed_tablegen.h (sine)
- libavutil/tx_priv.h, tx_template.c (int32 inverse MDCT, RESCALE/CMUL)
- libavutil/fixed_dsp.c (`vector_fmul_window`)

The D2 fixed-point reconstruction core (dequantization, PNS, stereo tools, TNS
and the S32P clip) additionally draws from:

- libavcodec/aac/aacdec_fixed_dequant.h (`vector_pow43`, `subband_scale`,
  `noise_scale`, `exp2tab`, the `fixed_sqrt` composition)
- libavcodec/aac/aacdec_proc_template.c (the PNS noise fill in
  `decode_spectrum_and_dequant`, `lcg_random`)
- libavcodec/aac/aacdec_dsp_template.c (`apply_mid_side_stereo`,
  `apply_intensity_stereo`, `apply_tns`, `clip_output`)
- libavutil/fixed_dsp.c, fixed_dsp.h (`scalarproduct_fixed`,
  `butterflies_fixed`, `fixed_sqrt`)
- libavcodec/lpc_functions.h (the fixed `compute_lpc_coefs` for TNS)
- libavcodec/cbrt_tablegen.h, cbrt_tablegen_common.c, cbrt_data.h (the computed
  cbrt fixed dequant table `ff_cbrt_tab_fixed`)
- libavcodec/mathops.h (`ff_sqrt`, the integer floor-sqrt reference for
  `fixed_sqrt`)

The baked integer sine window table (`internal/window/sinefixed_tables.go`) is
dumped from the pinned build rather than computed. FFmpeg generates it at
runtime with `sinf()` (`sinewin_fixed_tablegen.h`), which is not correctly
rounded on every platform, so a computed table would not be bit-exact against
the oracle. This mirrors FFmpeg's own `CONFIG_HARDCODED_TABLES` build option and
is therefore equally a derivative of the pinned tree.

## Go standard library derivations

A small set of float64 transcendentals is vendored from the Go standard library
rather than called through package `math`, so the encoder is bit-for-bit
architecture-deterministic (issue #79). The standard library dispatches `Exp`,
`Exp2`, `Log`, `Log2`, `Pow`, `Atan`, `Sin` and `Cos` to per-architecture
assembly or to a portable Go path that gc contracts into fused multiply-adds on
arm64, so the same input yields last-ulp-different results per architecture. A
fused `a*b+c` also disagrees with the `-ffp-contract=off` C reference above.

The vendored copies live in `internal/fmath/portable_*.go`, derived from Go
1.27.0:

- `portable_exp.go` from `src/math/exp.go` (`exp`, `exp2`, `expmulti`)
- `portable_log.go` from `src/math/log.go` and `log10.go` (`log`, `log2`)
- `portable_pow.go` from `src/math/pow.go` (`pow`, routed through the vendored
  `exp`/`log`)
- `portable_atan.go` from `src/math/atan.go` (`atan`, `satan`, `xatan`)
- `portable_sincos.go` from `src/math/sin.go` and `trig_reduce.go` (`sin` and
  `cos`, refactored into the `octantReduce`/`sinPoly`/`cosPoly` helpers, plus
  `trigReduce` with the stdlib `shift`/`mask`/`bias` constants renamed to the
  package-local `fshift`/`fmask`/`fbias`; `trigReduce` is exact integer
  arithmetic and needs no barrier)

The transformation is mechanical: every internal `a*b+c` gets an explicit
`float64(...)` rounding barrier on the product so gc cannot contract it, and all
nine vendored functions (the eight entry points above plus the `expmulti`
helper) are marked `//go:noinline` so an inlined product cannot fuse with a
caller's add. `float64(...)` of a float64 value is a no-op on `GOAMD64=v1`
(which never fuses), so the fusion-only copies (`Exp2`, `Atan`, `Sin`, `Cos`)
are bit-identical to the amd64 standard library and arm64 converges to them.
`Exp`, `Log`, `Log2`, and `Pow` (which computes `Exp(y*Log(x))`) additionally
leave the standard library's amd64 assembly path, so they change both arches to
one deterministic value; the differential oracle gate bounds that drift. `internal/fmath/portable_det_test.go` pins the
arch-independent output hash of every vendored function, and
`TestNoFloat64FMAInDeterministicMath` asserts the compiled code carries no
float64 FMA. These barriers also make a `GOAMD64=v3` build (which does fuse)
safe.

This vendored code is BSD-3-Clause licensed by The Go Authors, reproduced in
`LICENSE.golang`, and is compatible with this repository's LGPL-2.1-or-later
license.

## Per-function provenance

Every ported function carries a comment naming its C origin and the pinned
commit, for example:

    // Mirrors libavcodec/kbdwin.c:kbd_window_init @ d09d5afc3a.

This keeps the derivation auditable and upstream diffs cherry-pickable.

## Non-derived files

The lint rule definitions under rules/ originate from the same author's
go-flac project, are not derived from FFmpeg, and are provided here under
this repository's LGPL-2.1-or-later license.
