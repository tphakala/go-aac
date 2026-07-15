/* SPDX-License-Identifier: LGPL-2.1-or-later
 * ctns dumps golden fixtures for the go-aac Phase 4 TNS port from the
 * pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a). It
 * #includes libavcodec/aaccoder.c, libavcodec/aacenc.c AND
 * libavcodec/aacenc_tns.c in one TU so ff_aac_search_for_tns,
 * ff_aac_apply_tns and ff_aac_encode_tns_info are compiled with THIS
 * build's flags (-ffp-contract=off for the bit-exact reference), and
 * links the prebuilt static libs. The LPC layer (ff_lpc_calc_ref_coefs_f)
 * comes from the ARCHIVE's lpc.o, the exact object Phase 0b validated the
 * Go port against bit-exactly.
 */
#include "libavutil/internal.h"
#include "libavutil/cpu.h"
#include "libavcodec/aaccoder.c"
#include "libavcodec/aacenc.c"
#include "libavcodec/aacenc_tns.c"

#include <stdio.h>

static FILE *out;
static int cdump_wr_err;       /* set when any fwrite writes short */
static void put_f32(float v)   { if (fwrite(&v, 4, 1, out) != 1) cdump_wr_err = 1; }
static void put_f64(double v)  { if (fwrite(&v, 8, 1, out) != 1) cdump_wr_err = 1; }
static void put_i32(int32_t v) { if (fwrite(&v, 4, 1, out) != 1) cdump_wr_err = 1; }
static void put_u8(uint8_t v)  { if (fwrite(&v, 1, 1, out) != 1) cdump_wr_err = 1; }

static uint32_t lcgv = 0x1f2e3d4c;
static uint32_t lcg(void) { lcgv = lcgv * 1664525u + 1013904223u; return lcgv; }
static float lcgf(void) { return (float)(lcg() >> 8) / 16777216.0f; }

static AACEncContext sctx;
static FFPsyChannel  psych[2];

/* MA(m) smoothing across the spectral index makes the band LPC-predictable
 * without any multiply-add (adds of independent noise + one product), so
 * fused and unfused builds generate bit-identical INPUTS. */
static void synth_coeffs(SingleChannelElement *sce, int ma, float amp)
{
    static float n[1040];
    for (int i = 0; i < 1040; i++)
        n[i] = 2.0f*lcgf() - 1.0f;
    for (int k = 0; k < 1024; k++) {
        float acc = 0.0f;
        for (int j = 0; j <= ma; j++)
            acc += n[k+j];
        sce->coeffs[k] = amp * acc;
    }
}

/* ret_buf: iid noise with a step energy profile; front>back per window when
 * front is set (drives tdir). Long windows use one 2048 block, short eight
 * 256 blocks. */
static void synth_retbuf(SingleChannelElement *sce, int is8, const int *front)
{
    int tlen = is8 ? 256 : 2048;
    int nwin = is8 ? 8 : 1;
    for (int w = 0; w < nwin; w++) {
        float ahead = front[w] ? 2.0f : 0.25f;
        float atail = front[w] ? 0.25f : 2.0f;
        for (int t = 0; t < tlen; t++) {
            float a = t < tlen/2 ? ahead : atail;
            sce->ret_buf[w*tlen + t] = a * (2.0f*lcgf() - 1.0f);
        }
    }
}

static void dump_tns(SingleChannelElement *sce)
{
    TemporalNoiseShaping *tns = &sce->tns;
    put_i32(sce->tns.present);
    for (int w = 0; w < sce->ics.num_windows; w++) {
        put_i32(tns->n_filt[w]);
        for (int f = 0; f < 4; f++) {
            put_i32(tns->length[w][f]);
            put_i32(tns->order[w][f]);
            put_i32(tns->direction[w][f]);
            for (int o = 0; o < TNS_MAX_ORDER; o++) {
                put_i32(tns->coef_idx[w][f][o]);
                put_f32(tns->coef[w][f][o]);
            }
        }
    }
}

static void run_case(int sr_idx, int wseq, const int *gl, int max_sfb,
                     int ma_lo, int ma_hi, float amp, float thr_lo,
                     float thr_hi, int noise_g, const int *front)
{
    SingleChannelElement sce = {0};
    const int is8 = wseq == EIGHT_SHORT_SEQUENCE;

    sce.ics.window_sequence[0] = wseq;
    sce.ics.num_windows = is8 ? 8 : 1;
    if (is8) {
        for (int w = 0; w < 8; w++) sce.ics.group_len[w] = gl[w];
        sce.ics.swb_sizes  = ff_aac_swb_size_128[sr_idx];
        sce.ics.swb_offset = ff_swb_offset_128[sr_idx];
        sce.ics.num_swb    = ff_aac_num_swb_128[sr_idx];
        sce.ics.tns_max_bands = ff_tns_max_bands_128[sr_idx];
    } else {
        sce.ics.group_len[0] = 1;
        sce.ics.swb_sizes  = ff_aac_swb_size_1024[sr_idx];
        sce.ics.swb_offset = ff_swb_offset_1024[sr_idx];
        sce.ics.num_swb    = ff_aac_num_swb_1024[sr_idx];
        sce.ics.tns_max_bands = ff_tns_max_bands_1024[sr_idx];
    }
    sce.ics.max_sfb = max_sfb > 0 ? FFMIN(max_sfb, sce.ics.num_swb)
                                  : sce.ics.num_swb;

    sctx.samplerate_index = sr_idx;
    sctx.psy.ch = psych;
    sctx.cur_channel = 0;

    /* low bands smoothed with ma_lo, top half with ma_hi */
    synth_coeffs(&sce, ma_lo, amp);
    if (ma_hi != ma_lo) {
        static float n[1040];
        for (int i = 0; i < 1040; i++)
            n[i] = 2.0f*lcgf() - 1.0f;
        for (int k = 512; k < 1024; k++) {
            float acc = 0.0f;
            for (int j = 0; j <= ma_hi; j++)
                acc += n[k+j];
            sce.coeffs[k] = amp * acc;
        }
    }
    synth_retbuf(&sce, is8, front);

    for (int i = 0; i < 128; i++) {
        float t = thr_lo + (thr_hi - thr_lo) * lcgf();
        psych[0].psy_bands[i].threshold = t * t;
        psych[0].psy_bands[i].energy = 100.0f * t * t;
        sce.band_type[i] = 0;
        sce.zeroes[i] = 0;
    }
    if (noise_g >= 0)
        sce.band_type[noise_g] = NOISE_BT;

    ff_aac_search_for_tns(&sctx, &sce);
    dump_tns(&sce);

    ff_aac_apply_tns(&sctx, &sce);
    for (int k = 0; k < 1024; k++)
        put_f32(sce.coeffs[k]);

    {
        static uint8_t buf[1024];
        init_put_bits(&sctx.pb, buf, sizeof(buf));
        ff_aac_encode_tns_info(&sctx, &sce);
        int bits = put_bits_count(&sctx.pb);
        flush_put_bits(&sctx.pb);
        put_i32(bits);
        for (int i = 0; i < (bits + 7) / 8; i++)
            put_u8(buf[i]);
    }
}

int main(void)
{
    av_force_cpu_flags(0);
    ff_aac_float_common_init();
    ff_aacenc_dsp_init(&sctx.aacdsp);
    if (ff_lpc_init(&sctx.lpc, 2 * 1024, TNS_MAX_ORDER, FF_LPC_TYPE_LEVINSON) < 0) {
        fprintf(stderr, "lpc init failed\n");
        return 1;
    }

    out = fopen("tns_search.bin", "wb");
    if (!out) { perror("tns_search.bin"); return 1; }

    static const int fr_front[8] = {1,1,1,1,1,1,1,1};
    static const int fr_back[8]  = {0,0,0,0,0,0,0,0};
    static const int fr_mix[8]   = {1,0,1,0,0,1,0,1};
    static const int gl_grp[8]   = {1,3,0,0,3,0,0,1};
    static const int gl_flat[8]  = {1,1,1,1,1,1,1,1};

    /* sr_idx wseq gl maxsfb ma_lo ma_hi amp thr_lo thr_hi noise_g front */
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 0.5f, 2.0f, -1, fr_front);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 0.5f, 2.0f, -1, fr_back);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 0, 0, 12.0f, 0.5f, 2.0f, -1, fr_front);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 0, 12.0f, 0.5f, 2.0f, -1, fr_front);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 12, 12, 25.0f, 0.5f, 2.0f, -1, fr_front);
    run_case(3, LONG_START_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 0.5f, 2.0f, -1, fr_back);
    run_case(3, LONG_STOP_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 0.5f, 2.0f, -1, fr_front);
    run_case(3, EIGHT_SHORT_SEQUENCE, gl_grp, 0, 4, 4, 30.0f, 0.5f, 2.0f, -1, fr_mix);
    run_case(3, EIGHT_SHORT_SEQUENCE, gl_flat, 0, 3, 3, 30.0f, 0.5f, 2.0f, -1, fr_mix);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 0.5f, 2.0f, 30, fr_front);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 20, 4, 4, 12.0f, 0.5f, 2.0f, -1, fr_front);
    run_case(4, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 0.5f, 2.0f, -1, fr_back);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 4, 12.0f, 1e-6f, 1e-5f, -1, fr_front);
    run_case(3, ONLY_LONG_SEQUENCE, gl_flat, 0, 4, 4, 0.02f, 30.0f, 90.0f, -1, fr_front);

    /* crafted coef_compress cases: every index outside [4,11], long and
     * short, so the 1-bit-shorter coefficient form is exercised. */
    {
        SingleChannelElement sce = {0};
        sce.ics.window_sequence[0] = ONLY_LONG_SEQUENCE;
        sce.ics.num_windows = 1;
        sce.tns.present = 1;
        sce.tns.n_filt[0] = 2;
        sce.tns.length[0][0] = 12; sce.tns.order[0][0] = 6;
        sce.tns.direction[0][0] = 1;
        int idx0[6] = {12, 13, 14, 15, 0, 3};
        for (int i = 0; i < 6; i++) sce.tns.coef_idx[0][0][i] = idx0[i];
        sce.tns.length[0][1] = 12; sce.tns.order[0][1] = 6;
        sce.tns.direction[0][1] = 0;
        int idx1[6] = {2, 1, 12, 15, 3, 0};
        for (int i = 0; i < 6; i++) sce.tns.coef_idx[0][1][i] = idx1[i];
        static uint8_t buf[64];
        init_put_bits(&sctx.pb, buf, sizeof(buf));
        ff_aac_encode_tns_info(&sctx, &sce);
        int bits = put_bits_count(&sctx.pb);
        flush_put_bits(&sctx.pb);
        put_i32(bits);
        for (int i = 0; i < (bits + 7) / 8; i++) put_u8(buf[i]);
        /* the compress in place mutates coef_idx; dump the mutated set */
        for (int f2 = 0; f2 < 2; f2++)
            for (int i = 0; i < 6; i++) put_i32(sce.tns.coef_idx[0][f2][i]);
    }
    {
        SingleChannelElement sce = {0};
        sce.ics.window_sequence[0] = EIGHT_SHORT_SEQUENCE;
        sce.ics.num_windows = 8;
        sce.tns.present = 1;
        sce.tns.n_filt[3] = 1;
        sce.tns.length[3][0] = 11; sce.tns.order[3][0] = 7;
        sce.tns.direction[3][0] = 1;
        int idx0[7] = {15, 14, 13, 12, 1, 2, 3};
        for (int i = 0; i < 7; i++) sce.tns.coef_idx[3][0][i] = idx0[i];
        static uint8_t buf[64];
        init_put_bits(&sctx.pb, buf, sizeof(buf));
        ff_aac_encode_tns_info(&sctx, &sce);
        int bits = put_bits_count(&sctx.pb);
        flush_put_bits(&sctx.pb);
        put_i32(bits);
        for (int i = 0; i < (bits + 7) / 8; i++) put_u8(buf[i]);
        for (int i = 0; i < 7; i++) put_i32(sce.tns.coef_idx[3][0][i]);
    }

    if (fclose(out) != 0 || cdump_wr_err) {
        fprintf(stderr, "ctns: tns_search.bin write failed\n");
        return 1;
    }
    fprintf(stderr, "ctns: tns_search.bin written\n");
    return 0;
}
