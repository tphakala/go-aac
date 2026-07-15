# Provenance

go-aac is a derivative work of FFmpeg's native AAC-LC encoder **and decoder**.
The codec logic in this repository is ported, function by function, from the
FFmpeg source tree pinned at commit:

    d09d5afc3aebede25d2d245ee23b75a47ea17c3a

Because it derives from LGPL-licensed FFmpeg code, this repository is
licensed under the GNU Lesser General Public License, version 2.1 or later
(see LICENSE), and can never be relicensed under a permissive license.

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

## Per-function provenance

Every ported function carries a comment naming its C origin and the pinned
commit, for example:

    // Mirrors libavcodec/kbdwin.c:kbd_window_init @ d09d5afc3a.

This keeps the derivation auditable and upstream diffs cherry-pickable.

## Non-derived files

The lint rule definitions under rules/ originate from the same author's
go-flac project, are not derived from FFmpeg, and are provided here under
this repository's LGPL-2.1-or-later license.
