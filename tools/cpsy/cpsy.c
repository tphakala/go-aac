/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cpsy dumps golden fixtures for the go-aac Phase 2 psychoacoustic model
 * from the pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a).
 * It #includes libavcodec/aacpsy.c directly, making the file-local
 * psy_3gpp_init, psy_lame_window and psy_3gpp_analyze callable, and links
 * against the prebuilt static libs (ff_psy_init comes from the archive's
 * psymodel.o; the extern ff_aac_psy_model it references resolves to the
 * copy compiled here, so the archive's aacpsy.o is never pulled).
 *
 * Modes:
 *   cpsy init <out.bin> <rate> <bitrate> <channels>
 *       Dump psy init coefficients (AacPsyCoeffs for long+short) and the
 *       context scalars for one configuration.
 *   cpsy trace <in.f32> <out.bin> <rate> <bitrate> <channels> <nsamples>
 *       Feed planar f32 PCM through the real window decision + MDCT +
 *       psy analysis exactly like aac_encode_frame does, and dump per
 *       frame per channel: the window decision, per-band psy outputs
 *       (energy/threshold/spread/bits), entropy, bitres state. Every
 *       analyze is invoked TWICE per frame with the same frame_num to
 *       exercise the rate-loop rewind path; both results are dumped.
 *       The produced-bits feedback (last_frame_pb_count) is a synthetic
 *       deterministic LCG sequence reproduced by the Go test.
 */
#include <assert.h>

#include "libavcodec/aacpsy.c"

#include "libavcodec/aactab.h"
#include "libavcodec/aacenctab.h"
#include "libavcodec/sinewin.h"
#include "libavcodec/mpeg4audio_sample_rates.h"
#include "libavutil/tx.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static FILE *out;

static void put_i32(int32_t v) { fwrite(&v, 4, 1, out); }
static void put_f32(float v)   { fwrite(&v, 4, 1, out); }

static uint32_t lcg_state = 0x2545f491u;
static uint32_t lcg_next(void)
{
    lcg_state = lcg_state * 1664525u + 1013904223u;
    return lcg_state;
}

/* Window application, hand-copied from aacenc.c:apply_*_window
 * @ d09d5afc3a with fdsp->vector_fmul(_reverse) expanded to plain loops
 * (elementwise multiplies are exactly rounded in any implementation). */
static void vec_mul(float *dst, const float *a, const float *b, int n)
{
    int i;
    for (i = 0; i < n; i++)
        dst[i] = a[i] * b[i];
}
static void vec_mul_rev(float *dst, const float *a, const float *b, int n)
{
    int i;
    for (i = 0; i < n; i++)
        dst[i] = a[i] * b[n - 1 - i];
}

static void apply_window(int seq, int kb0, int kb1, float *out_buf, const float *audio)
{
    const float *kl = ff_aac_kbd_long_1024, *sl = ff_sine_1024;
    const float *ks = ff_aac_kbd_short_128, *ss = ff_sine_128;
    int w;
    switch (seq) {
    case ONLY_LONG_SEQUENCE:
        vec_mul    (out_buf,        audio,        kb0 ? kl : sl, 1024);
        vec_mul_rev(out_buf + 1024, audio + 1024, kb1 ? kl : sl, 1024);
        break;
    case LONG_START_SEQUENCE:
        vec_mul(out_buf, audio, kb1 ? kl : sl, 1024);
        memcpy(out_buf + 1024, audio + 1024, sizeof(float) * 448);
        vec_mul_rev(out_buf + 1024 + 448, audio + 1024 + 448, kb0 ? ks : ss, 128);
        memset(out_buf + 1024 + 576, 0, sizeof(float) * 448);
        break;
    case LONG_STOP_SEQUENCE:
        memset(out_buf, 0, sizeof(float) * 448);
        vec_mul(out_buf + 448, audio + 448, kb1 ? ks : ss, 128);
        memcpy(out_buf + 576, audio + 576, sizeof(float) * 448);
        vec_mul_rev(out_buf + 1024, audio + 1024, kb0 ? kl : sl, 1024);
        break;
    case EIGHT_SHORT_SEQUENCE:
        for (w = 0; w < 8; w++) {
            const float *in = audio + 448 + w * 128;
            float *o = out_buf + 2 * w * 128;
            vec_mul    (o,       in,       w ? (kb1 ? ks : ss) : (kb0 ? ks : ss), 128);
            vec_mul_rev(o + 128, in + 128, kb0 ? ks : ss, 128);
        }
        break;
    }
}

static int compute_bandwidth(int bitrate, int channels, int rate)
{
    int frame_br = bitrate / channels;
    int bw = FFMAX(3000, AAC_CUTOFF_FROM_BITRATE(frame_br, 1, rate));
    return FFMIN(FFMAX(bw, 8000), rate / 2);
}

static AVCodecContext actx;
static FFPsyContext psy;

static int psy_setup(int rate, int bitrate, int channels)
{
    const uint8_t *sizes[2];
    int lengths[2];
    uint8_t grouping[1];
    int sr_idx, bw;

    for (sr_idx = 0; ff_mpeg4audio_sample_rates[sr_idx] != rate; sr_idx++)
        if (sr_idx >= 12) return -1;

    actx.codec_id = AV_CODEC_ID_AAC;
    actx.bit_rate = bitrate;
    actx.sample_rate = rate;
    actx.ch_layout.nb_channels = channels;
    actx.frame_num = 0;

    bw = compute_bandwidth(bitrate, channels, rate);
    sizes[0]   = ff_aac_swb_size_1024[sr_idx];
    sizes[1]   = ff_aac_swb_size_128[sr_idx];
    lengths[0] = ff_aac_num_swb_1024[sr_idx];
    lengths[1] = ff_aac_num_swb_128[sr_idx];
    grouping[0] = channels - 1; /* 1 SCE or 1 CPE */
    if (ff_psy_init(&psy, &actx, 2, sizes, lengths, 1, grouping, bw) < 0)
        return -1;
    fprintf(stderr, "cpsy: rate %d bitrate %d ch %d sr_idx %d bandwidth %d\n",
            rate, bitrate, channels, sr_idx, bw);
    return sr_idx;
}

static void dump_init(void)
{
    AacPsyContext *pctx = psy.model_priv_data;
    int j, g;
    put_i32(pctx->chan_bitrate);
    put_i32(pctx->frame_bits);
    put_f32(pctx->pe.min);
    put_f32(pctx->pe.max);
    put_i32(psy.bitres.size);
    put_i32(pctx->fill_level);
    put_f32(pctx->ch[0].attack_threshold);
    for (j = 0; j < 2; j++) {
        put_i32(psy.num_bands[j]);
        for (g = 0; g < psy.num_bands[j]; g++) {
            AacPsyCoeffs *c = &pctx->psy_coef[j][g];
            put_f32(c->ath);
            put_f32(c->barks);
            put_f32(c->spread_low[0]);
            put_f32(c->spread_low[1]);
            put_f32(c->spread_hi[0]);
            put_f32(c->spread_hi[1]);
            put_f32(c->min_snr);
        }
    }
}

static void dump_bands(int channels, const FFPsyWindowInfo *wi)
{
    AacPsyContext *pctx = psy.model_priv_data;
    int ch, w, g;
    for (ch = 0; ch < channels; ch++) {
        int nb = psy.num_bands[wi[ch].num_windows == 8];
        put_i32(wi[ch].num_windows);
        put_i32(nb);
        for (w = 0; w < wi[ch].num_windows * 16; w += 16) {
            for (g = 0; g < nb; g++) {
                FFPsyBand *b = &psy.ch[ch].psy_bands[w + g];
                put_f32(b->energy);
                put_f32(b->threshold);
                put_f32(b->spread);
                put_i32(b->bits);
            }
        }
        put_f32(psy.ch[ch].entropy);
    }
    put_i32(psy.bitres.alloc);
    put_i32(pctx->fill_level);
    put_f32(pctx->pe.min);
    put_f32(pctx->pe.max);
    put_f32(pctx->pe.previous);
}

int main(int argc, char **argv)
{
    if (argc >= 2 && !strcmp(argv[1], "init")) {
        if (argc < 6) {
            fprintf(stderr, "usage: cpsy init <out> <rate> <bitrate> <channels>\n");
            return 2;
        }
        int rate = atoi(argv[3]), bitrate = atoi(argv[4]), channels = atoi(argv[5]);
        if (channels < 1 || channels > 2) {
            fprintf(stderr, "cpsy: channels must be 1 or 2, got %d\n", channels);
            return 2;
        }
        if (psy_setup(rate, bitrate, channels) < 0) return 1;
        out = fopen(argv[2], "wb");
        if (!out) { perror(argv[2]); return 1; }
        dump_init();
        fclose(out);
        fprintf(stderr, "cpsy: init fixture written\n");
        return 0;
    }
    if (argc >= 2 && !strcmp(argv[1], "trace")) {
        if (argc < 8) {
            fprintf(stderr, "usage: cpsy trace <in> <out> <rate> <bitrate> <channels> <nsamples>\n");
            return 2;
        }
        int rate = atoi(argv[4]), bitrate = atoi(argv[5]), channels = atoi(argv[6]);
        long nsamples = atol(argv[7]);
        long nframes = nsamples / 1024;
        static float planar[2][3 * 1024];
        static float src[2][1 << 22];
        if (channels < 1 || channels > 2) {
            fprintf(stderr, "cpsy: channels must be 1 or 2, got %d\n", channels);
            return 2;
        }
        if (nsamples < 0 || nsamples > (long)(sizeof src[0] / sizeof src[0][0])) {
            fprintf(stderr, "cpsy: nsamples %ld outside [0, %ld]\n",
                    nsamples, (long)(sizeof src[0] / sizeof src[0][0]));
            return 2;
        }
        static float winbuf[2048];
        static float coeffs_buf[2][1024];
        const float *coeffs_ptr[2];
        int ics_seq[2] = { ONLY_LONG_SEQUENCE, ONLY_LONG_SEQUENCE };
        int ics_kb[2][2] = { { 1, 1 }, { 1, 1 } };
        FFPsyWindowInfo wi[2];
        AVTXContext *tx1024 = NULL, *tx128 = NULL;
        av_tx_fn fn1024, fn128;
        float scale = 32768.0f;
        FILE *in;
        long f;
        int ch, k, call;
        int last_frame_pb_count = 0;
        int rate_bits_total;

        if (psy_setup(rate, bitrate, channels) < 0) return 1;
        ff_aac_float_common_init();
        if (av_tx_init(&tx1024, &fn1024, AV_TX_FLOAT_MDCT, 0, 1024, &scale, 0) < 0)
            return 1;
        if (av_tx_init(&tx128, &fn128, AV_TX_FLOAT_MDCT, 0, 128, &scale, 0) < 0)
            return 1;

        in = fopen(argv[2], "rb");
        if (!in) { perror(argv[2]); return 1; }
        for (ch = 0; ch < channels; ch++)
            if (fread(src[ch], 4, nsamples, in) != (size_t)nsamples) {
                fprintf(stderr, "cpsy: short read\n");
                return 1;
            }
        fclose(in);
        out = fopen(argv[3], "wb");
        if (!out) { perror(argv[3]); return 1; }

        rate_bits_total = (int)((int64_t)bitrate * 1024 / rate);
        put_i32((int32_t)nframes); /* analyzed frames: f=1..nframes-1 plus one flush */

        /* frame loop: nframes input frames + 1 flush frame (la = NULL) */
        for (f = 0; f <= nframes; f++) {
            int flush = f == nframes;
            /* copy_input_samples (aacenc.c:990) */
            for (ch = 0; ch < channels; ch++) {
                memmove(&planar[ch][1024], &planar[ch][2048], 1024 * sizeof(float));
                if (!flush)
                    memcpy(&planar[ch][2048], &src[ch][f * 1024], 1024 * sizeof(float));
                else
                    memset(&planar[ch][2048], 0, 1024 * sizeof(float));
            }
            if (f == 0)
                continue; /* priming frame: no packet, no analysis */
            actx.frame_num = f;

            for (ch = 0; ch < channels; ch++) {
                float *samples2 = &planar[ch][1024];
                float *la = flush ? NULL : samples2 + 448 + 64;
                wi[ch] = psy.model->window(&psy, samples2, la, ch, ics_seq[ch]);
                ics_kb[ch][1] = ics_kb[ch][0];
                ics_kb[ch][0] = wi[ch].window_shape;
                ics_seq[ch]   = wi[ch].window_type[0];
                apply_window(wi[ch].window_type[0], ics_kb[ch][0], ics_kb[ch][1],
                             winbuf, &planar[ch][0]);
                if (wi[ch].window_type[0] != EIGHT_SHORT_SEQUENCE) {
                    fn1024(tx1024, coeffs_buf[ch], winbuf, sizeof(float));
                } else {
                    for (k = 0; k < 1024; k += 128)
                        fn128(tx128, &coeffs_buf[ch][k], winbuf + k * 2, sizeof(float));
                }
                coeffs_ptr[ch] = coeffs_buf[ch];
                /* window decision record */
                put_i32(wi[ch].window_type[0]);
                put_i32(wi[ch].window_type[1]);
                put_i32(wi[ch].window_shape);
                put_i32(wi[ch].num_windows);
                for (k = 0; k < 8; k++)
                    put_i32(wi[ch].grouping[k]);
            }

            /* two analyze calls with the same frame_num: the second mimics a
             * rate-loop retry and must reproduce the first exactly (rewind) */
            for (call = 0; call < 2; call++) {
                psy.bitres.alloc = -1;
                psy.bitres.bits = last_frame_pb_count / channels;
                psy.model->analyze(&psy, 0, coeffs_ptr, wi);
                dump_bands(channels, wi);
            }

            /* synthetic produced-bits feedback for the next frame */
            last_frame_pb_count = (int)((int64_t)rate_bits_total *
                                        (512 + (lcg_next() >> 22)) / 1024);
        }
        fclose(out);
        fprintf(stderr, "cpsy: trace fixture written (%ld frames)\n", nframes + 1);
        return 0;
    }
    fprintf(stderr, "usage: cpsy init|trace ...\n");
    return 1;
}
