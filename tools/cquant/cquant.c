/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cquant dumps golden fixtures for the go-aac Phase 1 coder from the pinned
 * FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a). It #includes
 * libavcodec/aaccoder.c directly, making the file-local
 * search_for_quantizers_fast and codebook_trellis_rate callable, and links
 * against the prebuilt static libs. ff_quantize_band_cost_cache_init is
 * redefined here (5 lines, aacenc.c:373) so aacenc.o is never pulled from
 * the archive (it would drag in the whole encoder core).
 */
#include "libavcodec/aaccoder.c"
#include "libavcodec/sinewin.h"

#include <stdio.h>

/* Mirrors aacenc.c:ff_quantize_band_cost_cache_init @ d09d5afc3a; defined
 * here so the archive's aacenc.o (which also defines it) stays unpulled. */
void ff_quantize_band_cost_cache_init(struct AACEncContext *s)
{
    ++s->quantize_band_cost_cache_generation;
    if (s->quantize_band_cost_cache_generation == 0) {
        memset(s->quantize_band_cost_cache, 0, sizeof(s->quantize_band_cost_cache));
        s->quantize_band_cost_cache_generation = 1;
    }
}

static void dump(const char *name, const void *data, size_t n)
{
    FILE *f = fopen(name, "wb");
    if (!f) { perror(name); exit(1); }
    fwrite(data, 1, n, f);
    fclose(f);
}

static uint32_t lcg(uint32_t v) { return v * 1664525u + 1013904223u; }

static AACEncContext sctx;   /* static: the ctx holds a 512 KiB cache */
static AVCodecContext actx;
static FFPsyChannel psych;

int main(void)
{
    const int rate = 44100, bitrate = 128000, sr_idx = 4;
    static float src[4096], winbuf[2048], coeffs[1024];
    SingleChannelElement sce = {0};
    int i, g;

    ff_aac_float_common_init(); /* pow tables + KBD/sine windows */
    ff_aacenc_dsp_init(&sctx.aacdsp);
    sctx.psy.ch = &psych;
    sctx.cur_channel = 0;
    actx.bit_rate = bitrate;
    actx.sample_rate = rate;
    actx.ch_layout.nb_channels = 1;

    /* Window input: samples [2048, 4096) of the Phase 1 tonal gate signal,
     * KBD long window on both halves (steady state). */
    for (i = 0; i < 4096; i++) {
        double ts = (double)i / rate;
        src[i] = (float)(0.35 * sin(2 * M_PI * 220 * ts) +
                         0.25 * sin(2 * M_PI * 997 * ts) +
                         0.10 * sin(2 * M_PI * 3800 * ts));
    }
    for (i = 0; i < 1024; i++) {
        winbuf[i]        = src[2048 + i] * ff_aac_kbd_long_1024[i];
        winbuf[1024 + i] = src[3072 + i] * ff_aac_kbd_long_1024[1023 - i];
    }
    {
        AVTXContext *tx = NULL;
        av_tx_fn fn;
        float scale = 32768.0f;
        if (av_tx_init(&tx, &fn, AV_TX_FLOAT_MDCT, 0, 1024, &scale, 0) < 0)
            return 1;
        fn(tx, coeffs, winbuf, sizeof(float));
        av_tx_uninit(&tx);
    }
    dump("fast_in_coeffs.f32", coeffs, sizeof(coeffs));

    /* ICS setup, long window, 44.1 kHz. */
    sce.ics.num_windows = 1;
    sce.ics.group_len[0] = 1;
    sce.ics.swb_sizes = ff_aac_swb_size_1024[sr_idx];
    sce.ics.swb_offset = ff_swb_offset_1024[sr_idx];
    sce.ics.num_swb = ff_aac_num_swb_1024[sr_idx];
    memcpy(sce.coeffs, coeffs, sizeof(coeffs));

    /* Placeholder psy, exactly as internal/enc computes it: cutoff from
     * AAC_CUTOFF_FROM_BITRATE, threshold = energy * 0.001258925f. */
    {
        int bw = FFMAX(3000, AAC_CUTOFF_FROM_BITRATE(bitrate, 1, rate));
        int cutoff;
        int start = 0;
        bw = FFMIN(FFMAX(bw, 8000), rate / 2);
        cutoff = bw * 2048 / rate;
        fprintf(stderr, "bandwidth %d cutoff %d\n", bw, cutoff);
        for (g = 0; g < sce.ics.num_swb; g++) {
            FFPsyBand *band = &sctx.psy.ch[0].psy_bands[g];
            float energy = 0.0f;
            if (start < cutoff)
                for (i = 0; i < sce.ics.swb_sizes[g]; i++)
                    energy += sce.coeffs[start + i] * sce.coeffs[start + i];
            band->energy = energy;
            band->threshold = energy * 0.001258925f;
            start += sce.ics.swb_sizes[g];
        }
    }

    search_for_quantizers_fast(&actx, &sctx, &sce, 120.0f);
    {
        int32_t sf[128], bt[128];
        uint8_t z[128];
        for (i = 0; i < 128; i++) {
            sf[i] = sce.sf_idx[i];
            bt[i] = sce.band_type[i];
            z[i] = sce.zeroes[i];
        }
        dump("fast_sf.i32", sf, sizeof(sf));
        dump("fast_bt.i32", bt, sizeof(bt));
        dump("fast_zero.u8", z, sizeof(z));
    }

    /* adjust_frame_information, mono long-window subset (aacenc.c:503). */
    {
        int maxsfb = sce.ics.num_swb;
        while (maxsfb > 0 && sce.zeroes[maxsfb - 1])
            maxsfb--;
        sce.ics.max_sfb = maxsfb;
    }

    /* Sectioning trellis; dump the section bitstream and final band map. */
    {
        uint8_t buf[1536] = {0};
        int32_t misc[3], bt[128];
        uint8_t z[128];
        init_put_bits(&sctx.pb, buf, sizeof(buf));
        codebook_trellis_rate(&sctx, &sce, 0, 1, 120.0f);
        misc[0] = sce.ics.max_sfb;
        misc[1] = put_bits_count(&sctx.pb);
        flush_put_bits(&sctx.pb);
        misc[2] = put_bytes_output(&sctx.pb);
        dump("trellis_misc.i32", misc, sizeof(misc));
        dump("trellis_bits.bin", buf, misc[2]);
        for (i = 0; i < 128; i++) {
            bt[i] = sce.band_type[i];
            z[i] = sce.zeroes[i];
        }
        dump("trellis_bt.i32", bt, sizeof(bt));
        dump("trellis_zero.u8", z, sizeof(z));
    }

    /* quantize_and_encode_band byte dumps: for each codebook, 32 LCG
     * coefficients scaled so the band exercises the codebook's range
     * (including ESC escapes for cb 11), quantized at sf 140 and written.
     * Records: per cb, int32 bit count, int32 byte count, bytes. */
    {
        FILE *f = fopen("qeb_bytes.bin", "wb");
        uint32_t state = 0x1f2e3d4c;
        int cb;
        if (!f) { perror("qeb_bytes.bin"); exit(1); }
        for (cb = 1; cb <= 11; cb++) {
            static const float amps[12] = {0, 1.5f, 1.5f, 2.5f, 2.5f, 4.5f,
                                           4.5f, 7.5f, 7.5f, 12.5f, 12.5f, 500.0f};
            float in[32];
            uint8_t buf[512] = {0};
            int32_t nbits, nbytes;
            for (i = 0; i < 32; i++) {
                state = lcg(state);
                in[i] = amps[cb] * ((float)(int32_t)state / 2147483648.0f);
            }
            init_put_bits(&sctx.pb, buf, sizeof(buf));
            quantize_and_encode_band(&sctx, &sctx.pb, in, NULL, 32,
                                     SCALE_ONE_POS, cb, 120.0f, 0);
            nbits = put_bits_count(&sctx.pb);
            flush_put_bits(&sctx.pb);
            nbytes = put_bytes_output(&sctx.pb);
            fwrite(&nbits, 4, 1, f);
            fwrite(&nbytes, 4, 1, f);
            fwrite(buf, 1, nbytes, f);
        }
        fclose(f);
    }

    /* Phase 2: grouped EIGHT_SHORT fixtures. A castanet-like frame is
     * windowed with apply_eight_short_window semantics (sine short, both
     * shapes current), transformed with 8x mdct128, given per-window
     * placeholder psy values, then run through the REAL grouped
     * search_for_quantizers_fast, codebook_trellis_rate per window group
     * and the spectral writer. The Go ports must reproduce the decisions
     * and bytes exactly; this is what pins the w*16+g convention with
     * real multiple window groups. Grouping: group_len = {1,3,0,0,3,0,0,1}
     * (window_grouping[3] = 0xB2, a real trace value). */
    {
        static float in2048[2048], winbuf2[2048], scoeffs[1024];
        SingleChannelElement ssce = {0};
        uint32_t st = 0x00c0ffee;
        int w, w2, g2;
        AVTXContext *tx128 = NULL;
        av_tx_fn fn128;
        float scale = 32768.0f;
        int cutoff_s;
        static const int group_len[8] = {1, 3, 0, 0, 3, 0, 0, 1};

        /* deterministic transient: tone + decaying click at sample 900 */
        for (i = 0; i < 2048; i++) {
            double ts = (double)i / rate;
            in2048[i] = (float)(0.10 * sin(2 * M_PI * 330 * ts));
        }
        for (i = 0; i < 700 && 900 + i < 2048; i++) {
            st = lcg(st);
            in2048[900 + i] += (float)(0.75 * exp(-i / 120.0) *
                                       ((double)(int32_t)st / 2147483648.0));
        }
        /* apply_eight_short_window (aacenc.c:421-436), sine both shapes */
        for (w = 0; w < 8; w++) {
            const float *inp = in2048 + 448 + w * 128;
            float *o = winbuf2 + 2 * w * 128;
            for (i = 0; i < 128; i++)
                o[i] = inp[i] * ff_sine_128[i];
            for (i = 0; i < 128; i++)
                o[128 + i] = inp[128 + i] * ff_sine_128[127 - i];
        }
        if (av_tx_init(&tx128, &fn128, AV_TX_FLOAT_MDCT, 0, 128, &scale, 0) < 0)
            return 1;
        for (i = 0; i < 1024; i += 128)
            fn128(tx128, &scoeffs[i], winbuf2 + i * 2, sizeof(float));
        av_tx_uninit(&tx128);
        dump("short_in_samples.f32", in2048, sizeof(in2048));
        dump("short_in_coeffs.f32", scoeffs, sizeof(scoeffs));

        ssce.ics.num_windows = 8;
        for (w = 0; w < 8; w++)
            ssce.ics.group_len[w] = group_len[w];
        ssce.ics.swb_sizes = ff_aac_swb_size_128[sr_idx];
        ssce.ics.swb_offset = ff_swb_offset_128[sr_idx];
        ssce.ics.num_swb = ff_aac_num_swb_128[sr_idx];
        memcpy(ssce.coeffs, scoeffs, sizeof(scoeffs));

        /* per-window placeholder psy: threshold = energy * 0.001258925,
         * short cutoff = bandwidth * 2048 / 8 / rate (aacpsy.c:683) */
        {
            int bw = FFMAX(3000, AAC_CUTOFF_FROM_BITRATE(bitrate, 1, rate));
            bw = FFMIN(FFMAX(bw, 8000), rate / 2);
            cutoff_s = bw * 2048 / 8 / rate;
            for (w = 0; w < 8; w++) {
                int start = w * 128, wstart = 0;
                for (g2 = 0; g2 < ssce.ics.num_swb; g2++) {
                    FFPsyBand *band = &sctx.psy.ch[0].psy_bands[w * 16 + g2];
                    float energy = 0.0f;
                    if (wstart < cutoff_s)
                        for (i = 0; i < ssce.ics.swb_sizes[g2]; i++)
                            energy += ssce.coeffs[start + i] * ssce.coeffs[start + i];
                    band->energy = energy;
                    band->threshold = energy * 0.001258925f;
                    start += ssce.ics.swb_sizes[g2];
                    wstart += ssce.ics.swb_sizes[g2];
                }
            }
        }

        search_for_quantizers_fast(&actx, &sctx, &ssce, 120.0f);
        {
            int32_t sf[128], bt[128];
            uint8_t z[128];
            for (i = 0; i < 128; i++) {
                sf[i] = ssce.sf_idx[i];
                bt[i] = ssce.band_type[i];
                z[i] = ssce.zeroes[i];
            }
            dump("short_fast_sf.i32", sf, sizeof(sf));
            dump("short_fast_bt.i32", bt, sizeof(bt));
            dump("short_fast_zero.u8", z, sizeof(z));
        }

        /* adjust_frame_information, grouped subset (aacenc.c:503-531) */
        {
            int maxsfb = 0, cmaxsfb;
            for (w = 0; w < ssce.ics.num_windows; w += ssce.ics.group_len[w]) {
                for (cmaxsfb = ssce.ics.num_swb;
                     cmaxsfb > 0 && ssce.zeroes[w * 16 + cmaxsfb - 1]; cmaxsfb--)
                    ;
                maxsfb = FFMAX(maxsfb, cmaxsfb);
            }
            ssce.ics.max_sfb = maxsfb;
            for (w = 0; w < ssce.ics.num_windows; w += ssce.ics.group_len[w]) {
                for (g2 = 0; g2 < ssce.ics.max_sfb; g2++) {
                    i = 1;
                    for (w2 = w; w2 < w + ssce.ics.group_len[w]; w2++) {
                        if (!ssce.zeroes[w2 * 16 + g2]) {
                            i = 0;
                            break;
                        }
                    }
                    ssce.zeroes[w * 16 + g2] = i;
                }
            }
        }

        /* grouped sectioning trellis + spectral writer, one bitstream */
        {
            uint8_t buf[3072] = {0};
            int32_t misc[3], bt[128];
            uint8_t z[128];
            int start_g, i2;
            init_put_bits(&sctx.pb, buf, sizeof(buf));
            for (w = 0; w < ssce.ics.num_windows; w += ssce.ics.group_len[w])
                codebook_trellis_rate(&sctx, &ssce, w, ssce.ics.group_len[w], 120.0f);
            /* encode_spectral_coeffs (aacenc.c:900-923) */
            for (w = 0; w < ssce.ics.num_windows; w += ssce.ics.group_len[w]) {
                start_g = 0;
                for (i2 = 0; i2 < ssce.ics.max_sfb; i2++) {
                    if (ssce.zeroes[w * 16 + i2]) {
                        start_g += ssce.ics.swb_sizes[i2];
                        continue;
                    }
                    for (w2 = w; w2 < w + ssce.ics.group_len[w]; w2++)
                        quantize_and_encode_band(&sctx, &sctx.pb,
                                                 &ssce.coeffs[start_g + w2 * 128], NULL,
                                                 ssce.ics.swb_sizes[i2],
                                                 ssce.sf_idx[w * 16 + i2],
                                                 ssce.band_type[w * 16 + i2],
                                                 120.0f, 0);
                    start_g += ssce.ics.swb_sizes[i2];
                }
            }
            misc[0] = ssce.ics.max_sfb;
            misc[1] = put_bits_count(&sctx.pb);
            flush_put_bits(&sctx.pb);
            misc[2] = put_bytes_output(&sctx.pb);
            dump("short_misc.i32", misc, sizeof(misc));
            dump("short_bits.bin", buf, misc[2]);
            for (i = 0; i < 128; i++) {
                bt[i] = ssce.band_type[i];
                z[i] = ssce.zeroes[i];
            }
            dump("short_bt.i32", bt, sizeof(bt));
            dump("short_zero.u8", z, sizeof(z));
        }
    }

    fprintf(stderr, "cquant: fixtures written\n");
    return 0;
}
