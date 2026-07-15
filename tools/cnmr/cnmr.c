/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cnmr dumps golden fixtures for the go-aac Phase 3 NMR coder from the
 * pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a). It
 * #includes libavcodec/aaccoder.c AND libavcodec/aacenc.c in one TU,
 * making the file-local nmr_band_curve, nmr_solve,
 * search_for_quantizers_nmr, mark_pns, set_special_band_scalefactors and
 * nmr_decide_stereo callable, and links the prebuilt static libs
 * (libavcodec.a + libswresample.a + libavutil.a).
 *
 * av_force_cpu_flags(0) pins the SCALAR DSP (abs_pow34/quant_bands; the
 * scalar nmr_trellis_step is the only one on arm64 at this pin anyway),
 * matching the Go port's arithmetic exactly.
 */
#include "libavutil/internal.h"
#include "libavutil/cpu.h"
#include "libavcodec/aaccoder.c"
#include "libavcodec/aacenc.c"

#include <stdio.h>

static FILE *out;

static void put_f32(float v)   { fwrite(&v, 4, 1, out); }
static void put_i32(int32_t v) { fwrite(&v, 4, 1, out); }
static void put_u8(uint8_t v)  { fwrite(&v, 1, 1, out); }

static uint32_t lcgv = 0x1f2e3d4c;
static uint32_t lcg(void) { lcgv = lcgv * 1664525u + 1013904223u; return lcgv; }
/* uniform float32 in [0,1): top 24 bits / 2^24, exact in float32 */
static float lcgf(void) { return (float)(lcg() >> 8) / 16777216.0f; }

static AACEncContext sctx;    /* holds the 512 KiB cost cache */
static AACNMRCurves  nmrst;
static AVCodecContext actx;
static FFPsyChannel  psych[2];
static ChannelElement cpe;

/* ---------------- fixture 1: nmr_solve (the Viterbi trellis) ------------- */

static void fix_trellis(void)
{
    static float nd[128][NMR_NCAND];
    static int   nb[128][NMR_NCAND];
    int blo[128], bnc[128], act[128], chosen[128];

    out = fopen("nmr_trellis.bin", "wb");
    if (!out) { perror("nmr_trellis.bin"); exit(1); }

    /* case 0: crafted all-equal costs; the winner is pure tie-breaking */
    {
        int nact = 4;
        for (int b = 0; b < nact; b++) {
            blo[b] = 100; bnc[b] = 5; act[b] = b;
            for (int o = 0; o < 5; o++) { nd[b][o] = 1.0f; nb[b][o] = 40; }
        }
        memset(chosen, -1, sizeof(chosen));
        float lam = nmr_solve(&sctx, nd, nb, blo, bnc, 1, act, nact,
                              200, chosen, 1e-9f, 1e4f, NMR_ITERS);
        put_f32(lam);
        for (int b = 0; b < nact; b++) put_i32(chosen[b]);
    }

    /* cases 1..24: LCG-driven with deliberately coarse value grids so
     * (nd + lam*nb) collisions and symmetric sf-delta bit ties occur */
    for (int cs = 1; cs <= 24; cs++) {
        int nact = (cs % 5 == 0) ? 1 : 2 + (int)(lcg() % 45);
        int step = (cs & 1) ? 1 : 8;
        int prevlo = 60 + (int)(lcg() % 60);
        for (int b = 0; b < nact; b++) {
            act[b] = b;
            bnc[b] = 1 + (int)(lcg() % NMR_NCAND);
            prevlo += (int)(lcg() % 31) - 15;
            if (prevlo < 0) prevlo = 0;
            if (prevlo > 200) prevlo = 200;
            blo[b] = prevlo;
            for (int o = 0; o < bnc[b]; o++) {
                nd[b][o] = 0.5f * (float)(lcg() % 8);
                nb[b][o] = 40 + 8 * (int)(lcg() % 6);
                if (o > 0 && (lcg() % 4) == 0) { /* duplicate candidates */
                    nd[b][o] = nd[b][o-1];
                    nb[b][o] = nb[b][o-1];
                }
            }
        }
        int destbits = 100 + (int)(lcg() % 4000);
        float lo_l = 1e-9f, hi_l = 1e4f;
        int iters = NMR_ITERS;
        switch (cs % 4) {
        case 0: break;
        case 1: iters = NMR_CITERS; break;
        case 2: lo_l = 0.5f; hi_l = 512.0f; iters = NMR_CWARM; break;
        case 3: lo_l = 2.0f; hi_l = 2.0f; iters = 1; break;
        }
        memset(chosen, -1, sizeof(chosen));
        float lam = nmr_solve(&sctx, nd, nb, blo, bnc, step, act, nact,
                              destbits, chosen, lo_l, hi_l, iters);
        put_i32(nact);
        put_f32(lam);
        for (int b = 0; b < nact; b++) put_i32(chosen[b]);
    }
    fclose(out);
}

/* ---------------- fixture 2: nmr_band_curve ------------------------------ */

static void fix_curve(void)
{
    static float nd_row[NMR_NCAND];
    static int   nb_row[NMR_NCAND];
    SingleChannelElement sce = {0};
    const int sr_idx = 4; /* 44100 */

    out = fopen("nmr_curve.bin", "wb");
    if (!out) { perror("nmr_curve.bin"); exit(1); }

    /* long-window case set */
    sce.ics.num_windows = 1;
    sce.ics.group_len[0] = 1;
    sce.ics.swb_sizes = ff_aac_swb_size_1024[sr_idx];
    sce.ics.swb_offset = ff_swb_offset_1024[sr_idx];
    sce.ics.num_swb = ff_aac_num_swb_1024[sr_idx];

    static const float amps[6] = { 0.4f, 3.0f, 12.0f, 90.0f, 500.0f, 9000.0f };
    for (int a = 0; a < 6; a++) {
        for (int i = 0; i < 1024; i++)
            sce.coeffs[i] = amps[a] * (2.0f*lcgf() - 1.0f);
        sctx.aacdsp.abs_pow34(sctx.scoefs, sce.coeffs, 1024);
        for (int gsel = 0; gsel < 3; gsel++) {
            int g = gsel == 0 ? 2 : gsel == 1 ? 20 : 38;
            int start = sce.ics.swb_offset[g];
            float maxval = find_max_val(1, sce.ics.swb_sizes[g], sctx.scoefs + start);
            if (maxval <= 0) { put_i32(-1); continue; }
            int lo = av_clip(coef2minsf(maxval), 0, SCALE_MAX_POS);
            float thr = (0.001f + 0.05f*lcgf());
            float esum = 0.0f;
            for (int i = 0; i < sce.ics.swb_sizes[g]; i++)
                esum += sce.coeffs[start+i]*sce.coeffs[start+i];
            float invthr = 1.0f / FFMAX(esum*thr, 1e-9f);
            put_f32(thr);
            put_f32(esum);
            for (int st = 0; st < 2; st++) {
                int step = st ? 1 : 8;
                ff_quantize_band_cost_cache_init(&sctx);
                int ncand = nmr_band_curve(&sctx, &sce, 0, g, start, lo, step,
                                           NMR_NCAND, invthr, maxval, nd_row, nb_row);
                put_i32(ncand);
                put_i32(lo);
                put_f32(invthr);
                put_f32(maxval);
                for (int o = 0; o < ncand; o++) { put_f32(nd_row[o]); put_i32(nb_row[o]); }
            }
        }
    }

    /* grouped short-window case (group_len 3 at w=1) */
    {
        SingleChannelElement ssce = {0};
        ssce.ics.num_windows = 8;
        int gl[8] = {1,3,0,0,3,0,0,1};
        for (int w = 0; w < 8; w++) ssce.ics.group_len[w] = gl[w];
        ssce.ics.swb_sizes = ff_aac_swb_size_128[sr_idx];
        ssce.ics.swb_offset = ff_swb_offset_128[sr_idx];
        ssce.ics.num_swb = ff_aac_num_swb_128[sr_idx];
        for (int i = 0; i < 1024; i++)
            ssce.coeffs[i] = 25.0f * (2.0f*lcgf() - 1.0f);
        sctx.aacdsp.abs_pow34(sctx.scoefs, ssce.coeffs, 1024);
        int w = 1, g = 3;
        int start = w*128 + ssce.ics.swb_offset[g];
        float maxval = find_max_val(3, ssce.ics.swb_sizes[g], sctx.scoefs + start);
        int lo = av_clip(coef2minsf(maxval), 0, SCALE_MAX_POS);
        float invthr = 1.0f / 3.7f;
        ff_quantize_band_cost_cache_init(&sctx);
        int ncand = nmr_band_curve(&sctx, &ssce, w, g, start, lo, 1,
                                   NMR_NCAND, invthr, maxval, nd_row, nb_row);
        put_i32(ncand);
        put_i32(lo);
        put_f32(invthr);
        put_f32(maxval);
        for (int o = 0; o < ncand; o++) { put_f32(nd_row[o]); put_i32(nb_row[o]); }
    }
    fclose(out);
}

/* ---------------- fixture 3: search_for_quantizers_nmr sequences --------- */

/* Build one frame's synthetic psy bands + coeffs. All arithmetic is exact
 * float32 the Go driver mirrors verbatim. */
static float thrbase = 0.0005f, thrspan = 0.03f;
static int   pnsmix = 0;
static int   g_speed = 0;   /* nmr_speed for the search fixtures */   /* give some HF bands near-masked noise character */

static void synth_frame(SingleChannelElement *sce, FFPsyChannel *pch,
                        int short_frame, float amp, float noisiness)
{
    const int sr_idx = 4;
    if (short_frame) {
        static const int gl[8] = {1,3,0,0,3,0,0,1};
        sce->ics.num_windows = 8;
        for (int w = 0; w < 8; w++) sce->ics.group_len[w] = gl[w];
        sce->ics.swb_sizes = ff_aac_swb_size_128[sr_idx];
        sce->ics.swb_offset = ff_swb_offset_128[sr_idx];
        sce->ics.num_swb = ff_aac_num_swb_128[sr_idx];
    } else {
        sce->ics.num_windows = 1;
        sce->ics.group_len[0] = 1;
        sce->ics.swb_sizes = ff_aac_swb_size_1024[sr_idx];
        sce->ics.swb_offset = ff_swb_offset_1024[sr_idx];
        sce->ics.num_swb = ff_aac_num_swb_1024[sr_idx];
    }
    sce->ics.window_sequence[0] = short_frame ? EIGHT_SHORT_SEQUENCE : ONLY_LONG_SEQUENCE;

    for (int i = 0; i < 1024; i++) {
        /* decaying spectral profile with noise */
        float dec = 1.0f / (1.0f + (float)(i % (short_frame ? 128 : 1024)) * 0.01f);
        sce->coeffs[i] = amp * dec * (2.0f*lcgf() - 1.0f);
    }
    memset(pch->psy_bands, 0, sizeof(pch->psy_bands));
    for (int w = 0; w < sce->ics.num_windows; w++) {
        int start = 0;
        for (int g = 0; g < sce->ics.num_swb; g++) {
            FFPsyBand *b = &pch->psy_bands[w*16+g];
            float e = 0.0f;
            for (int i = 0; i < sce->ics.swb_sizes[g]; i++) {
                float c = sce->coeffs[w*128 + start + i];
                e += c*c;
            }
            b->energy = e;
            b->threshold = e * (thrbase + thrspan*lcgf());
            b->spread = 0.3f + noisiness * lcgf();
            if (pnsmix && g >= 24 && (g % 4) >= 2) {
                /* near-masked wide noise: a PNS candidate under pressure */
                b->threshold = e * (0.15f + 0.1f*lcgf());
                b->spread = 1.6f + 0.4f*lcgf();
            }
            if (b->spread > 2.0f) b->spread = 2.0f;
            start += sce->ics.swb_sizes[g];
        }
    }
}

static void dump_channel_state(SingleChannelElement *sce)
{
    for (int i = 0; i < 128; i++) put_i32(sce->sf_idx[i]);
    for (int i = 0; i < 128; i++) put_i32(sce->band_type[i]);
    for (int i = 0; i < 128; i++) put_u8(sce->zeroes[i]);
    for (int i = 0; i < 128; i++) put_u8(sce->can_pns[i]);
    for (int i = 0; i < 128; i++) put_f32(sce->pns_ener[i]);
}

static void dump_nmr_state(void)
{
    put_f32(nmrst.lam[0]);
    put_f32(nmrst.lam[1]);
    put_i32(nmrst.counted[0]);
    put_i32(nmrst.counted[1]);
    put_f32(nmrst.side_ema);
    put_i32(nmrst.side_inited);
    put_i32((int32_t)nmrst.rc_frame_num);
    put_f32(nmrst.lam_rc);
    put_i32(nmrst.rc_fill);
    put_i32(nmrst.frames_since_short);
    put_i32(nmrst.prev_was_short);
    put_f32(nmrst.run_burst);
}

static void fix_search(const char *name, int bitrate, float noisiness)
{
    SingleChannelElement sce;
    const int rate = 44100;
    const int nframes = 30;

    out = fopen(name, "wb");
    if (!out) { perror(name); exit(1); }

    memset(&nmrst, 0, sizeof(nmrst));
    memset(&sce, 0, sizeof(sce));
    sctx.nmr = &nmrst;
    sctx.psy.ch = psych;
    sctx.cur_channel = 0;
    sctx.channels = 1;
    sctx.lambda = 120.0f;
    sctx.options.nmr_speed = g_speed;
    sctx.last_frame_pb_count = 0;
    actx.bit_rate = bitrate;
    actx.sample_rate = rate;
    actx.ch_layout.nb_channels = 1;
    actx.bit_rate_tolerance = bitrate; /* nonzero: rc eligible */
    actx.flags = 0;
    actx.global_quality = 0;
    sctx.psy.avctx = &actx;

    /* NMR bandwidth law (aacenc.c:1600-1607) */
    {
        int frame_br = bitrate;
        static const int rates[] = { 32000, 48000, 64000, 96000, 192000 };
        static const int bws[]   = { 14000, 15000, 16000, 18000, 20000 };
        if (frame_br >= 32000) {
            int bw_i = 0;
            for (; bw_i < 3 && frame_br > rates[bw_i + 1]; bw_i++);
            sctx.bandwidth = bws[bw_i] + (int)((int64_t)(bws[bw_i + 1] - bws[bw_i]) *
                                 (frame_br - rates[bw_i]) / (rates[bw_i + 1] - rates[bw_i]));
            sctx.bandwidth = FFMIN3(sctx.bandwidth, 22000, rate / 2);
        } else {
            sctx.bandwidth = FFMAX(3000, AAC_CUTOFF_FROM_BITRATE(frame_br, 1, rate));
        }
        sctx.bandwidth = FFMIN(FFMAX(sctx.bandwidth, 8000), rate / 2);
        put_i32(sctx.bandwidth);
    }

    int rate_frame = (int)(bitrate * 1024.0 / rate);
    for (int f = 0; f < nframes; f++) {
        /* frame script: shorts at 18,19 (after >= NMR_BURST_GAP longs)
         * and again at 24 (gap 4: no burst) */
        int short_frame = (f == 18 || f == 19 || f == 24);
        float amp = 20.0f + 60.0f*lcgf();
        if (f >= 8 && f <= 10) amp *= 4.0f;      /* loud stretch */
        if (f == 12) amp *= 0.05f;               /* near-quiet frame */
        synth_frame(&sce, &psych[0], short_frame, amp, noisiness);

        actx.frame_num = f;
        sctx.psy.bitres.alloc = (f < 2) ? -1
            : rate_frame + (int)(lcg() % (2*rate_frame/4)) - rate_frame/4;
        /* synthetic previous-frame bits, consistent feedback below */

        mark_pns(&sctx, &actx, &sce);
        search_for_quantizers_nmr(&actx, &sctx, &sce, sctx.lambda);

        /* replicate the encoder's side accounting (aacenc.c:1394-1409)
         * with synthetic side bits */
        {
            int side = 120 + (int)(lcg() % 90);
            int counted = nmrst.counted[0];
            sctx.last_frame_pb_count = counted + side;
            if (counted > 0) {
                float sd = (float)sctx.last_frame_pb_count - counted;
                if (nmrst.side_inited) {
                    nmrst.side_ema += 0.125f * (sd - nmrst.side_ema);
                } else {
                    nmrst.side_ema = sd;
                    nmrst.side_inited = 1;
                }
            }
        }

        put_i32(f);
        put_i32(short_frame);
        dump_channel_state(&sce);
        dump_nmr_state();
        put_i32(sctx.last_frame_pb_count);
    }
    fclose(out);
}

/* ---------------- fixture 4: nmr_decide_stereo + stereo search ----------- */

static void fix_stereo(void)
{
    const int rate = 44100, bitrate = 96000;
    const int sr_idx = 4;

    out = fopen("nmr_stereo.bin", "wb");
    if (!out) { perror("nmr_stereo.bin"); exit(1); }

    memset(&nmrst, 0, sizeof(nmrst));
    memset(&cpe, 0, sizeof(cpe));
    sctx.nmr = &nmrst;
    sctx.psy.ch = psych;
    sctx.channels = 2;
    sctx.lambda = 120.0f;
    sctx.options.nmr_speed = 0;
    sctx.options.mid_side = -1;          /* auto (the default) */
    sctx.options.intensity_stereo = 1;
    sctx.options.pns = 1;
    sctx.last_frame_pb_count = 0;
    actx.bit_rate = bitrate;
    actx.sample_rate = rate;
    actx.ch_layout.nb_channels = 2;
    actx.bit_rate_tolerance = bitrate;
    actx.flags = 0;
    actx.global_quality = 0;
    sctx.psy.avctx = &actx;
    sctx.bandwidth = 15500; /* 48 kbps/ch via the NMR law: computed below */
    {
        int frame_br = bitrate / 2;
        static const int rates[] = { 32000, 48000, 64000, 96000, 192000 };
        static const int bws[]   = { 14000, 15000, 16000, 18000, 20000 };
        int bw_i = 0;
        for (; bw_i < 3 && frame_br > rates[bw_i + 1]; bw_i++);
        sctx.bandwidth = bws[bw_i] + (int)((int64_t)(bws[bw_i + 1] - bws[bw_i]) *
                             (frame_br - rates[bw_i]) / (rates[bw_i + 1] - rates[bw_i]));
        sctx.bandwidth = FFMIN3(sctx.bandwidth, 22000, rate / 2);
        sctx.bandwidth = FFMIN(FFMAX(sctx.bandwidth, 8000), rate / 2);
        put_i32(sctx.bandwidth);
    }

    int rate_frame = (int)(bitrate * 1024.0 / rate);
    for (int f = 0; f < 8; f++) {
        SingleChannelElement *s0 = &cpe.ch[0], *s1 = &cpe.ch[1];
        memset(s0->band_type, 0, sizeof(s0->band_type));
        memset(s1->band_type, 0, sizeof(s1->band_type));
        memset(cpe.ms_mask, 0, sizeof(cpe.ms_mask));
        memset(cpe.is_mask, 0, sizeof(cpe.is_mask));

        /* per-band stereo character: g%3==0 near-mono (M/S), g%3==1
         * near-collinear scaled pair (I/S in HF; the irreducible image
         * error is tiny), g%3==2 wide decorrelated noise (PNS reserve) */
        synth_frame(s0, &psych[0], 0, 45.0f, 1.7f);
        s1->ics = s0->ics;
        {
            int start = 0;
            for (int g = 0; g < s0->ics.num_swb; g++) {
                for (int i = 0; i < s0->ics.swb_sizes[g]; i++) {
                    float l = s0->coeffs[start+i];
                    float r;
                    if (g % 3 == 0)      r = l * (1.0f + 0.002f*(2.0f*lcgf()-1.0f));
                    else if (g % 3 == 1) r = 0.7f*l + 0.01f*l*(2.0f*lcgf()-1.0f);
                    else                 r = (2.0f*lcgf()-1.0f) * 40.0f / (1.0f + g);
                    s1->coeffs[start+i] = r;
                }
                start += s0->ics.swb_sizes[g];
            }
            /* lift the I/S bands' masking so the image test can pass */
            start = 0;
            for (int g = 0; g < s0->ics.num_swb; g++) {
                if (g % 3 == 1) {
                    FFPsyBand *b = &psych[0].psy_bands[g];
                    b->threshold = b->energy * (0.02f + 0.05f*lcgf());
                }
                start += s0->ics.swb_sizes[g];
            }
            /* right-channel psy from its own coeffs */
            start = 0;
            for (int g = 0; g < s1->ics.num_swb; g++) {
                FFPsyBand *b = &psych[1].psy_bands[g];
                float e = 0.0f;
                for (int i = 0; i < s1->ics.swb_sizes[g]; i++)
                    e += s1->coeffs[start+i]*s1->coeffs[start+i];
                b->energy = e;
                b->threshold = e * (0.0005f + 0.03f*lcgf());
                b->spread = 0.3f + 1.7f*lcgf();
                if (b->spread > 2.0f) b->spread = 2.0f;
                start += s1->ics.swb_sizes[g];
            }
        }

        actx.frame_num = f;
        sctx.psy.bitres.alloc = rate_frame + (int)(lcg() % (rate_frame/2)) - rate_frame/4;
        if (f == 4) nmrst.rc_fill = -1800;   /* exercise the I/S deficit bonus */

        /* the encoder's stereo PNS intersection (aacenc.c:1203-1212) */
        sctx.cur_channel = 0; mark_pns(&sctx, &actx, s0);
        sctx.cur_channel = 1; mark_pns(&sctx, &actx, s1);
        for (int b = 0; b < 128; b++)
            if (!s0->can_pns[b] || !s1->can_pns[b])
                s0->can_pns[b] = s1->can_pns[b] = 0;

        sctx.cur_channel = 0;
        nmr_decide_stereo(&sctx, &cpe);

        for (int i = 0; i < 128; i++) put_u8(cpe.ms_mask[i]);
        for (int i = 0; i < 128; i++) put_u8(cpe.is_mask[i]);
        put_u8(cpe.is_mode);
        for (int i = 0; i < 128; i++) put_i32(s1->band_type[i]);
        for (int i = 0; i < 128; i++) { put_f32(s0->is_ener[i]); put_f32(s1->is_ener[i]); }
        for (int i = 0; i < 128; i++) {
            put_f32(psych[0].psy_bands[i].energy);
            put_f32(psych[0].psy_bands[i].threshold);
            put_f32(psych[1].psy_bands[i].energy);
            put_f32(psych[1].psy_bands[i].threshold);
        }
        for (int i = 0; i < 128; i++) put_u8(s0->can_pns[i]);

        /* both-channel search + special band scalefactors, as encode does */
        sctx.cur_channel = 0;
        search_for_quantizers_nmr(&actx, &sctx, s0, sctx.lambda);
        sctx.cur_channel = 1;
        search_for_quantizers_nmr(&actx, &sctx, s1, sctx.lambda);
        set_special_band_scalefactors(&sctx, s0);
        set_special_band_scalefactors(&sctx, s1);
        dump_channel_state(s0);
        dump_channel_state(s1);
        dump_nmr_state();

        { /* side accounting with synthetic side bits, both channels */
            int side = 260 + (int)(lcg() % 120);
            int counted = nmrst.counted[0] + nmrst.counted[1];
            sctx.last_frame_pb_count = counted + side;
            if (counted > 0) {
                float sd = (float)sctx.last_frame_pb_count - counted;
                if (nmrst.side_inited) {
                    nmrst.side_ema += 0.125f * (sd - nmrst.side_ema);
                } else {
                    nmrst.side_ema = sd;
                    nmrst.side_inited = 1;
                }
            }
            put_i32(sctx.last_frame_pb_count);
        }
    }
    fclose(out);
}

int main(void)
{
    av_force_cpu_flags(0);          /* scalar DSP everywhere */
    ff_aac_float_common_init();
    ff_aacenc_dsp_init(&sctx.aacdsp);

    fix_trellis();
    lcgv = 0x2b7e1516; fix_curve();
    lcgv = 0x3c6ef372; fix_search("nmr_search_hi.bin", 128000, 1.2f);
    thrbase = 2e-5f; thrspan = 5e-4f; pnsmix = 1;
    lcgv = 0x510e527f; fix_search("nmr_search_lo.bin", 32000, 1.9f);
    thrbase = 0.0005f; thrspan = 0.03f; pnsmix = 0;
    g_speed = 3;
    lcgv = 0x9b05688c; fix_search("nmr_search_sp3.bin", 96000, 1.2f);
    g_speed = 0;
    lcgv = 0x6a09e667; fix_stereo();

    fprintf(stderr, "cnmr: fixtures written\n");
    return 0;
}
