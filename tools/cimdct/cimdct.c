/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cimdct dumps the int32 inverse MDCT internals of the REAL fixed-point AAC
 * decoder from the pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a):
 *   - the integer KBD and sine window tables the decoder computes at init
 *   - the int32 MDCT twiddle (exp) tables and permutation maps from the live
 *     AVTXContext of mdct1024/mdct128 (via tx_priv.h struct access)
 *   - IMDCT input/output pairs for sizes 1024 and 128 over deterministic LCG
 *     inputs, produced by calling the decoder's own mdct*_fn pointers
 *   - full imdct_and_windowing (overlap-add) frame chains across all four
 *     window sequences and both window shapes, produced by calling the
 *     decoder's own ac->dsp.imdct_and_windowing on a synthetic SCE
 * Like tools/cdec, it compiles libavcodec/aac/aacdec_fixed.c into this TU so
 * the static window tables are directly addressable; the linked decode path
 * is the shipping pinned code.
 */
#define USE_FIXED 1

/* tx_priv.h first, typed for int32, so AVTXContext exp/map are inspectable. */
#define TX_INT32
#include "libavutil/tx_priv.h"
#undef MULT
#undef CMUL
#undef SMUL
#undef UNSCALE
#undef RESCALE
#undef FOLD
#undef BF
#undef TX_TAB
#undef TX_NAME
#undef TX_NAME_STR
#undef TX_TYPE
#undef TX_FN_NAME
#undef TX_FN_NAME_STR
#undef TX_DECL_FN
#undef TX_DEF

#include "libavcodec/aac_defines.h"
#include "libavcodec/avcodec.h"
#include "libavutil/mem.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "libavcodec/aac/aacdec_fixed.c"

/* ------------------------------------------------------------------ */

static FILE *g_out;

static void dump_ints(const char *tag, const int *v, int n)
{
    fprintf(g_out, "%s %d", tag, n);
    for (int i = 0; i < n; i++)
        fprintf(g_out, " %d", v[i]);
    fputc('\n', g_out);
}

/* Numerical Recipes LCG; deterministic, mirrored by the Go fixture check. */
static uint32_t lcg_state;

static int32_t lcg_next(int shift)
{
    lcg_state = lcg_state * 1664525u + 1013904223u;
    return (int32_t)lcg_state >> shift;
}

static void dump_tx_ctx(const char *name, AVTXContext *tx)
{
    int len = tx->len;          /* MDCT ctx: len == coefficient count */
    fprintf(g_out, "TXCFG %s len=%d inv=%d scale_d=%.17g cd=%s sub=%s sublen=%d\n",
            name, tx->len, tx->inv, tx->scale_d,
            tx->cd_self && tx->cd_self->name ? tx->cd_self->name : "?",
            tx->sub && tx->cd[0] && tx->cd[0]->name ? tx->cd[0]->name : "?",
            tx->sub ? tx->sub[0].len : -1);
    dump_ints("TXMAP", tx->map, len >> 1);
    /* inverse MDCT exp table: len entries (shuffled copy + ordered copy) */
    fprintf(g_out, "TXEXP %d", 2 * (len >> 1));
    for (int i = 0; i < len; i++)
        fprintf(g_out, " %d %d", tx->exp[i].re, tx->exp[i].im);
    fputc('\n', g_out);
}

static void run_imdct(AACDecContext *ac, int size, int frames)
{
    static int in[1024], out[1024];
    AVTXContext *tx = size == 1024 ? ac->mdct1024 : ac->mdct128;
    av_tx_fn fn     = size == 1024 ? ac->mdct1024_fn : ac->mdct128_fn;

    for (int f = 0; f < frames; f++) {
        int shift = (f & 1) ? 8 : 0; /* alternate full-range / ~24-bit */
        for (int i = 0; i < size; i++)
            in[i] = lcg_next(shift);
        memset(out, 0, sizeof(out));
        fn(tx, out, in, sizeof(int));
        fprintf(g_out, "IMDCT n=%d frame=%d shift=%d\n", size, f, shift);
        dump_ints("IN", in, size);
        dump_ints("OUT", out, size);
    }
}

/* One synthetic frame through the decoder's own imdct_and_windowing. */
static void run_iaw(AACDecContext *ac, int frames)
{
    static SingleChannelElement sce; /* zero-initialized like calloc'd che */
    static int outbuf[2048];

    /* ws chain covering all four sequences, incl. transitions real encoders
     * never emit (START->STOP) which the C handles via the short-short path. */
    static const int ws_chain[] = { 0, 1, 2, 2, 3, 0, 0, 1, 3, 2, 3, 0 };
    static const int kb_chain[] = { 0, 1, 1, 0, 1, 1, 0, 0, 1, 1, 0, 1 };

    int prev_ws = 0, prev_kb = 0;
    for (int f = 0; f < frames; f++) {
        int ws = ws_chain[f % (int)FF_ARRAY_ELEMS(ws_chain)];
        int kb = kb_chain[f % (int)FF_ARRAY_ELEMS(kb_chain)];
        int shift = (f % 3) ? 8 : 0;

        sce.ics.window_sequence[0] = ws;
        sce.ics.window_sequence[1] = prev_ws;
        sce.ics.use_kb_window[0]   = kb;
        sce.ics.use_kb_window[1]   = prev_kb;
        sce.output_fixed           = outbuf;
        for (int i = 0; i < 1024; i++)
            sce.coeffs_fixed[i] = lcg_next(shift);

        fprintf(g_out, "IAW frame=%d ws=%d pws=%d kb=%d pkb=%d shift=%d\n",
                f, ws, prev_ws, kb, prev_kb, shift);
        dump_ints("COEF", sce.coeffs_fixed, 1024);
        ac->dsp.imdct_and_windowing(ac, &sce);
        dump_ints("OUT", outbuf, 1024);
        dump_ints("SAVED", sce.saved_fixed, 512);

        prev_ws = ws;
        prev_kb = kb;
    }
}

int main(int argc, char **argv)
{
    if (argc < 2) {
        fprintf(stderr, "usage: cimdct out.dump [seed]\n");
        return 2;
    }
    g_out = fopen(argv[1], "wb");
    if (!g_out) { perror(argv[1]); return 1; }
    lcg_state = argc > 2 ? (uint32_t)strtoul(argv[2], NULL, 0) : 0x1f2e3d4cu;
    fprintf(g_out, "SEED %u\n", lcg_state);

    const AVCodec *codec = avcodec_find_decoder_by_name("aac_fixed");
    if (!codec) { fprintf(stderr, "no aac_fixed\n"); return 1; }
    AVCodecContext *avctx = avcodec_alloc_context3(codec);
    if (!avctx) { fprintf(stderr, "alloc context failed\n"); return 1; }
    avctx->thread_count = 1;
    avctx->flags |= AV_CODEC_FLAG_BITEXACT;
    if (avcodec_open2(avctx, codec, NULL) < 0) {
        fprintf(stderr, "open failed\n");
        return 1;
    }
    AACDecContext *ac = avctx->priv_data;

    /* 1. integer window tables, straight from the decoder's static arrays */
    dump_ints("WIN kbd_long_1024", aac_kbd_long_1024_fixed, 1024);
    dump_ints("WIN kbd_short_128", aac_kbd_short_128_fixed, 128);
    dump_ints("WIN sine_1024", sine_1024_fixed, 1024);
    dump_ints("WIN sine_128", sine_128_fixed, 128);

    /* 2. transform configuration + tables from the live contexts */
    dump_tx_ctx("mdct1024", ac->mdct1024);
    dump_tx_ctx("mdct128", ac->mdct128);

    /* 3. raw IMDCT vectors */
    run_imdct(ac, 1024, 8);
    run_imdct(ac, 128, 8);

    /* 4. overlap-add chains */
    run_iaw(ac, 24);

    fprintf(g_out, "END\n");
    fclose(g_out);
    fprintf(stderr, "cimdct: done\n");
    return 0;
}
