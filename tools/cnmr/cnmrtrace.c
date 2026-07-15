/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cnmrtrace runs the REAL pinned libavcodec AAC encoder (the archive's
 * aacenc.o/aaccoder.o, not re-included copies) through the public API on
 * raw float PCM and dumps the NMR coder's per-frame internals by casting
 * avctx->priv_data to AACEncContext (same headers, same ABI). This is the
 * full-pipeline reference for the Go port's per-frame state comparison.
 *
 * Usage: cnmrtrace in.f32 rate channels bitrate out.bin
 * (in.f32: interleaved float32 PCM)
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "libavcodec/avcodec.h"
#include "libavutil/channel_layout.h"
#include "libavutil/opt.h"
#include "libavutil/cpu.h"

/* AACEncContext layout (must match the archive build: same tree+config) */
#include "libavcodec/aacenc.h"

static FILE *out;
static void put_f32(float v)   { fwrite(&v, 4, 1, out); }
static void put_i32(int32_t v) { fwrite(&v, 4, 1, out); }
static void put_u8(uint8_t v)  { fwrite(&v, 1, 1, out); }

static void dump_state(AVCodecContext *avctx, int pktsize, int channels)
{
    AACEncContext *s = avctx->priv_data;
    ChannelElement *cpe = &s->cpe[0];

    put_i32(pktsize);
    put_i32(s->last_frame_pb_count);
    put_f32(s->lambda);
    put_f32(s->nmr->lam_rc);
    put_i32(s->nmr->rc_fill);
    put_f32(s->nmr->side_ema);
    put_i32(s->nmr->side_inited);
    put_i32(s->nmr->frames_since_short);
    put_i32(s->nmr->prev_was_short);
    put_f32(s->nmr->run_burst);
    for (int ch = 0; ch < channels; ch++) {
        put_f32(s->nmr->lam[ch]);
        put_i32(s->nmr->counted[ch]);
    }
    put_i32(cpe->common_window);
    put_i32(cpe->ms_mode);
    put_u8(cpe->is_mode);
    for (int i = 0; i < 128; i++) put_u8(cpe->ms_mask[i]);
    for (int i = 0; i < 128; i++) put_u8(cpe->is_mask[i]);
    for (int ch = 0; ch < channels; ch++) {
        SingleChannelElement *sce = &cpe->ch[ch];
        put_i32(sce->ics.window_sequence[0]);
        put_i32(sce->ics.max_sfb);
        for (int i = 0; i < 128; i++) put_i32(sce->sf_idx[i]);
        for (int i = 0; i < 128; i++) put_i32(sce->band_type[i]);
        for (int i = 0; i < 128; i++) put_u8(sce->zeroes[i]);
    }
}

int main(int argc, char **argv)
{
    if (argc != 6) {
        fprintf(stderr, "usage: cnmrtrace in.f32 rate channels bitrate out.bin\n");
        return 1;
    }
    const char *inpath = argv[1];
    int rate = atoi(argv[2]);
    int channels = atoi(argv[3]);
    int bitrate = atoi(argv[4]);

    /* buf holds 2*1024 floats and fread reads 1024*channels into it, so a
     * channels value above 2 (or a non-numeric argv[3] that atoi turns into 0)
     * would overrun it. Reject anything outside 1..2 before opening the codec. */
    if (channels < 1 || channels > 2) {
        fprintf(stderr, "cnmrtrace: channels must be 1 or 2, got %d\n", channels);
        return 2;
    }
    if (rate <= 0 || bitrate <= 0) {
        fprintf(stderr, "cnmrtrace: rate and bitrate must be positive (got %d, %d)\n", rate, bitrate);
        return 2;
    }

    av_force_cpu_flags(0); /* scalar runtime DSP (NEON abs_pow34 etc. off) */

    const AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_AAC);
    if (!codec) { fprintf(stderr, "no aac encoder\n"); return 1; }
    AVCodecContext *avctx = avcodec_alloc_context3(codec);
    avctx->sample_rate = rate;
    avctx->bit_rate = bitrate;
    avctx->sample_fmt = AV_SAMPLE_FMT_FLTP;
    avctx->flags |= AV_CODEC_FLAG_BITEXACT;
    av_channel_layout_default(&avctx->ch_layout, channels);
    if (avcodec_open2(avctx, codec, NULL) < 0) {
        fprintf(stderr, "open failed\n"); return 1;
    }

    FILE *in = fopen(inpath, "rb");
    if (!in) { perror(inpath); return 1; }
    out = fopen(argv[5], "wb");
    if (!out) { perror(argv[5]); return 1; }

    AVFrame *frame = av_frame_alloc();
    AVPacket *pkt = av_packet_alloc();
    static float buf[2 * 1024];
    int64_t pts = 0;

    for (;;) {
        size_t n = fread(buf, sizeof(float) * channels, 1024, in);
        if (n == 0)
            break;
        frame->nb_samples = 1024;
        frame->format = AV_SAMPLE_FMT_FLTP;
        av_channel_layout_copy(&frame->ch_layout, &avctx->ch_layout);
        av_frame_get_buffer(frame, 0);
        for (int ch = 0; ch < channels; ch++) {
            float *dst = (float *)frame->data[ch];
            for (int i = 0; i < 1024; i++)
                dst[i] = i < (int)n ? buf[i * channels + ch] : 0.0f;
        }
        frame->pts = pts;
        pts += 1024;
        if (avcodec_send_frame(avctx, frame) < 0) { fprintf(stderr, "send\n"); return 1; }
        av_frame_unref(frame);
        while (avcodec_receive_packet(avctx, pkt) == 0) {
            dump_state(avctx, pkt->size, channels);
            av_packet_unref(pkt);
        }
    }
    avcodec_send_frame(avctx, NULL);
    while (avcodec_receive_packet(avctx, pkt) == 0) {
        dump_state(avctx, pkt->size, channels);
        av_packet_unref(pkt);
    }

    fclose(in);
    fclose(out);
    fprintf(stderr, "cnmrtrace: done\n");
    return 0;
}
