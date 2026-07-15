/* SPDX-License-Identifier: LGPL-2.1-or-later
 * ctwoloop dumps golden fixtures for the go-aac Phase 4 twoloop coder from
 * the pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a).
 * Same TU pattern as tools/cnmr: aaccoder.c (which includes
 * aaccoder_twoloop.h) + aacenc.c, so search_for_quantizers_twoloop,
 * mark_pns and search_for_pns are file-local callables compiled with THIS
 * build's flags (-ffp-contract=off), linking the prebuilt static libs.
 */
#include "libavutil/internal.h"
#include "libavutil/cpu.h"
#include "libavcodec/aaccoder.c"
#include "libavcodec/aacenc.c"
#include "libavcodec/aacenc_is.c"

#include <stdio.h>

static FILE *out;
static int cdump_wr_err;       /* set when any fwrite writes short */
static void put_f32(float v)   { if (fwrite(&v, 4, 1, out) != 1) cdump_wr_err = 1; }
static void put_i32(int32_t v) { if (fwrite(&v, 4, 1, out) != 1) cdump_wr_err = 1; }
static void put_u8(uint8_t v)  { if (fwrite(&v, 1, 1, out) != 1) cdump_wr_err = 1; }
static void put_u32(uint32_t v){ if (fwrite(&v, 4, 1, out) != 1) cdump_wr_err = 1; }

static uint32_t lcgv = 0x517cc1b7;
static uint32_t lcg(void) { lcgv = lcgv * 1664525u + 1013904223u; return lcgv; }
static float lcgf(void) { return (float)(lcg() >> 8) / 16777216.0f; }

static AACEncContext sctx;
static AVCodecContext actx;
static FFPsyChannel  psych[2];

/* per-band enveloped MA noise: env from a coarse LCG profile so some bands
 * are loud, some quiet, some near-zero; adds + one product only. */
static void synth_frame(SingleChannelElement *sce, float amp)
{
    static float n[1040];
    for (int i = 0; i < 1040; i++)
        n[i] = 2.0f*lcgf() - 1.0f;
    for (int w = 0; w < sce->ics.num_windows; w += sce->ics.group_len[w]) {
        for (int g = 0; g < sce->ics.num_swb; g++) {
            int profile = (int)(lcg() % 5); /* 0 silent .. 4 loud */
            float e = profile == 0 ? 0.0f :
                      profile == 1 ? 0.02f :
                      profile == 2 ? 0.6f  :
                      profile == 3 ? 4.0f  : 40.0f;
            for (int w2 = 0; w2 < sce->ics.group_len[w]; w2++) {
                int s0 = sce->ics.swb_offset[g], s1 = sce->ics.swb_offset[g+1];
                for (int k = s0; k < s1; k++) {
                    float acc = 0.0f;
                    for (int j = 0; j <= 3; j++)
                        acc += n[(w+w2)*128 + k + j];
                    sce->coeffs[(w+w2)*128 + k] = amp * e * acc;
                }
            }
        }
    }
}

static void synth_psy(SingleChannelElement *sce, float thr_r_lo, float thr_r_hi,
                      float spr_lo, float spr_hi)
{
    for (int w = 0; w < sce->ics.num_windows; w++) {
        for (int g = 0; g < sce->ics.num_swb; g++) {
            FFPsyBand *b = &psych[0].psy_bands[w*16+g];
            int s0 = sce->ics.swb_offset[g], s1 = sce->ics.swb_offset[g+1];
            float e = 0.0f;
            for (int k = s0; k < s1; k++) {
                float t = sce->coeffs[w*128+k]*sce->coeffs[w*128+k];
                e += t;
            }
            b->energy = e;
            b->threshold = e * (thr_r_lo + (thr_r_hi - thr_r_lo)*lcgf());
            b->spread = spr_lo + (spr_hi - spr_lo)*lcgf();
        }
    }
}

static void run_case(int sr_idx, int rate, int short_frame, int bitrate,
                     int channels, int bitres_alloc, float lambda, int pns,
                     float amp, float thr_r_lo, float thr_r_hi,
                     float spr_lo, float spr_hi)
{
    SingleChannelElement sce = {0};

    if (short_frame) {
        sce.ics.window_sequence[0] = EIGHT_SHORT_SEQUENCE;
        sce.ics.num_windows = 8;
        int gl[8] = {1,3,0,0,3,0,0,1};
        for (int w = 0; w < 8; w++) sce.ics.group_len[w] = gl[w];
        sce.ics.swb_sizes  = ff_aac_swb_size_128[sr_idx];
        sce.ics.swb_offset = ff_swb_offset_128[sr_idx];
        sce.ics.num_swb    = ff_aac_num_swb_128[sr_idx];
    } else {
        sce.ics.window_sequence[0] = ONLY_LONG_SEQUENCE;
        sce.ics.num_windows = 1;
        sce.ics.group_len[0] = 1;
        sce.ics.swb_sizes  = ff_aac_swb_size_1024[sr_idx];
        sce.ics.swb_offset = ff_swb_offset_1024[sr_idx];
        sce.ics.num_swb    = ff_aac_num_swb_1024[sr_idx];
    }
    sce.ics.max_sfb = sce.ics.num_swb;

    actx.bit_rate = bitrate;
    actx.sample_rate = rate;
    actx.flags = 0;
    actx.global_quality = 0;
    av_channel_layout_default(&actx.ch_layout, channels);

    sctx.psy.ch = psych;
    sctx.cur_channel = 0;
    sctx.psy.bitres.alloc = bitres_alloc;
    sctx.lambda = lambda;
    sctx.options.pns = pns;
    /* the coder's coding bandwidth, fixed at init in the real pipeline */
    sctx.bandwidth = rate / 2 > 20000 ? 20000 : rate / 2;

    synth_frame(&sce, amp);
    synth_psy(&sce, thr_r_lo, thr_r_hi, spr_lo, spr_hi);

    if (pns)
        mark_pns(&sctx, &actx, &sce);
    for (int i = 0; i < 128; i++) put_u8(sce.can_pns[i]);
    for (int i = 0; i < 128; i++) put_f32(sce.pns_ener[i]);

    search_for_quantizers_twoloop(&actx, &sctx, &sce, lambda);

    for (int i = 0; i < 128; i++) put_i32(sce.sf_idx[i]);
    for (int i = 0; i < 128; i++) put_i32(sce.band_type[i]);
    for (int i = 0; i < 128; i++) put_u8(sce.zeroes[i]);

    /* search_for_pns afterwards, exactly like the twoloop pipeline order,
     * pinning the LFSR use and the NOISE_BT conversions */
    if (pns) {
        sctx.random_state = 0x1f2e3d4c;
        search_for_pns(&sctx, &actx, &sce);
        put_u32(sctx.random_state);
        for (int i = 0; i < 128; i++) put_i32(sce.sf_idx[i]);
        for (int i = 0; i < 128; i++) put_i32(sce.band_type[i]);
        for (int i = 0; i < 128; i++) put_u8(sce.zeroes[i]);
        for (int i = 0; i < 128; i++) put_f32(sce.pns_ener[i]);
    }
}

/* stereo fixture: two correlated channels, twoloop per channel, then the
 * non-NMR stereo pipeline order: search_for_is, apply_intensity_stereo,
 * search_for_ms, apply_mid_side_stereo. */
static void fix_stereo(int sr_idx, int rate, int bitrate, float lambda,
                       float alpha, float beta)
{
    static ChannelElement cpe;
    memset(&cpe, 0, sizeof(cpe));
    SingleChannelElement *s0 = &cpe.ch[0], *s1 = &cpe.ch[1];

    for (int ch = 0; ch < 2; ch++) {
        SingleChannelElement *sc = &cpe.ch[ch];
        sc->ics.window_sequence[0] = ONLY_LONG_SEQUENCE;
        sc->ics.num_windows = 1;
        sc->ics.group_len[0] = 1;
        sc->ics.swb_sizes  = ff_aac_swb_size_1024[sr_idx];
        sc->ics.swb_offset = ff_swb_offset_1024[sr_idx];
        sc->ics.num_swb    = ff_aac_num_swb_1024[sr_idx];
        sc->ics.max_sfb    = sc->ics.num_swb;
    }
    cpe.common_window = 1;

    actx.bit_rate = bitrate;
    actx.sample_rate = rate;
    actx.flags = 0;
    actx.global_quality = 0;
    av_channel_layout_default(&actx.ch_layout, 2);

    sctx.psy.ch = psych;
    sctx.psy.bitres.alloc = -1;
    sctx.lambda = lambda;
    sctx.options.pns = 0;
    sctx.bandwidth = rate / 2 > 20000 ? 20000 : rate / 2;

    synth_frame(s0, 1.0f);
    /* ch1: correlated lows (alpha*L + beta*noise), scaled highs for IS */
    {
        static float n[1040];
        for (int i = 0; i < 1040; i++)
            n[i] = 2.0f*lcgf() - 1.0f;
        for (int k = 0; k < 1024; k++) {
            float a = alpha * s0->coeffs[k];
            float b = beta * n[k];
            s1->coeffs[k] = a + b;
        }
        int hf = s0->ics.swb_offset[30];
        for (int k = hf; k < 1024; k++)
            s1->coeffs[k] = 0.6f * s0->coeffs[k];
    }
    /* psy for both channels */
    for (int ch = 0; ch < 2; ch++) {
        SingleChannelElement *sc = &cpe.ch[ch];
        for (int g = 0; g < sc->ics.num_swb; g++) {
            FFPsyBand *b = &psych[ch].psy_bands[g];
            int a0 = sc->ics.swb_offset[g], a1 = sc->ics.swb_offset[g+1];
            float e = 0.0f;
            for (int k = a0; k < a1; k++) {
                float t = sc->coeffs[k]*sc->coeffs[k];
                e += t;
            }
            b->energy = e;
            b->threshold = e * (0.002f + 0.02f*lcgf());
            b->spread = 0.6f + 1.2f*lcgf();
        }
    }

    sctx.cur_channel = 0;
    search_for_quantizers_twoloop(&actx, &sctx, s0, lambda);
    sctx.cur_channel = 1;
    search_for_quantizers_twoloop(&actx, &sctx, s1, lambda);

    sctx.cur_channel = 0;
    ff_aac_search_for_is(&sctx, &actx, &cpe);
    apply_intensity_stereo(&cpe);

    put_u8(cpe.is_mode);
    for (int i = 0; i < 128; i++) put_u8(cpe.is_mask[i]);
    for (int i = 0; i < 128; i++) put_f32(s0->is_ener[i]);
    for (int i = 0; i < 128; i++) put_f32(s1->is_ener[i]);
    for (int i = 0; i < 128; i++) put_i32(s1->band_type[i]);
    for (int k = 0; k < 1024; k++) put_f32(s0->coeffs[k]);
    for (int k = 0; k < 1024; k++) put_f32(s1->coeffs[k]);

    search_for_ms(&sctx, &cpe);
    apply_mid_side_stereo(&cpe);

    for (int i = 0; i < 128; i++) put_u8(cpe.ms_mask[i]);
    for (int i = 0; i < 128; i++) put_i32(s0->sf_idx[i]);
    for (int i = 0; i < 128; i++) put_i32(s1->sf_idx[i]);
    for (int i = 0; i < 128; i++) put_i32(s0->band_type[i]);
    for (int i = 0; i < 128; i++) put_i32(s1->band_type[i]);
    for (int k = 0; k < 1024; k++) put_f32(s0->coeffs[k]);
    for (int k = 0; k < 1024; k++) put_f32(s1->coeffs[k]);
}

int main(void)
{
    av_force_cpu_flags(0);
    ff_aac_float_common_init();
    ff_aacenc_dsp_init(&sctx.aacdsp);
    sctx.fdsp = avpriv_float_dsp_alloc(0);
    if (!sctx.fdsp) return 1;

    out = fopen("twoloop_search.bin", "wb");
    if (!out) { perror("twoloop_search.bin"); return 1; }

    /* sr rate short bitrate ch alloc lambda pns amp thrlo thrhi sprlo sprhi */
    run_case(3, 48000, 0, 128000, 1, -1,   120.0f, 1, 1.0f, 0.001f, 0.01f, 0.4f, 2.0f);
    run_case(3, 48000, 0, 128000, 1, -1,   120.0f, 0, 1.0f, 0.001f, 0.01f, 0.4f, 2.0f);
    run_case(3, 48000, 0,  32000, 1, -1,   700.0f, 1, 1.0f, 0.02f, 0.3f, 0.9f, 2.0f);
    run_case(3, 48000, 0,  24000, 1, -1,  2600.0f, 1, 0.4f, 0.05f, 0.4f, 1.2f, 2.0f);
    run_case(3, 48000, 0, 192000, 1, 6100,  40.0f, 1, 2.5f, 0.0005f, 0.004f, 0.4f, 2.0f);
    run_case(4, 44100, 1, 128000, 1, -1,   120.0f, 1, 1.5f, 0.002f, 0.02f, 0.5f, 2.0f);
    run_case(4, 44100, 1,  48000, 1, -1,   900.0f, 1, 0.8f, 0.03f, 0.3f, 1.0f, 2.0f);
    run_case(3, 48000, 0, 128000, 2, -1,   120.0f, 1, 1.0f, 0.001f, 0.01f, 0.4f, 2.0f);
    run_case(3, 48000, 0,  96000, 1, 750,  300.0f, 1, 1.0f, 0.01f, 0.1f, 0.8f, 2.0f);
    run_case(4, 44100, 0,  64000, 1, -1,   450.0f, 1, 0.15f, 0.01f, 0.2f, 0.9f, 2.0f);
    run_case(3, 48000, 0,  16000, 1, -1,  3000.0f, 1, 2.0f, 5e-5f, 5e-4f, 1.4f, 2.0f);
    run_case(4, 44100, 1,  24000, 1, -1,  2000.0f, 1, 3.0f, 1e-4f, 1e-3f, 1.4f, 2.0f);
    run_case(3, 48000, 0,  16000, 1, -1,  3500.0f, 1, 2.0f, 2e-4f, 2.05e-4f, 1.8f, 1.85f);

    if (fclose(out) != 0 || cdump_wr_err) {
        fprintf(stderr, "ctwoloop: twoloop_search.bin write failed\n");
        return 1;
    }

    out = fopen("twoloop_stereo.bin", "wb");
    if (!out) { perror("twoloop_stereo.bin"); return 1; }
    cdump_wr_err = 0;
    lcgv = 0x243f6a88;
    fix_stereo(4, 44100, 128000, 120.0f, 0.95f, 0.05f);
    fix_stereo(3, 48000,  96000, 260.0f, 0.80f, 0.30f);
    fix_stereo(4, 44100, 192000,  70.0f, 0.99f, 0.01f);
    if (fclose(out) != 0 || cdump_wr_err) {
        fprintf(stderr, "ctwoloop: twoloop_stereo.bin write failed\n");
        return 1;
    }

    fprintf(stderr, "ctwoloop: fixtures written\n");
    return 0;
}
