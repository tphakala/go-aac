/* SPDX-License-Identifier: LGPL-2.1-or-later
 * corder proves the coder-dependent tool ordering of the REAL archive
 * aac_encode_frame: it opens the shipping encoder through the public API,
 * replaces avctx->priv_data->coder with a logging copy of the vtable whose
 * wrappers record the tool-decision call sequence, feeds transient-rich
 * frames, and prints the per-frame tool call order for each coder.
 */
#include <stdio.h>
#include <string.h>
#include <math.h>
#include <errno.h>

#include "libavcodec/avcodec.h"
#include "libavutil/channel_layout.h"
#include "libavutil/opt.h"
#include "libavutil/cpu.h"
#include "libavcodec/aacenc.h"

static const AACCoefficientsEncoder *real;
static char seq[4096];
static void logcall(const char *name)
{
    size_t used = strlen(seq);
    size_t avail = sizeof(seq) - used - 1; /* chars free, excluding the NUL */
    if (used && avail) { strcat(seq, " "); avail--; }
    strncat(seq, name, avail);
}

static void w_sfq(AVCodecContext *a, struct AACEncContext *s, SingleChannelElement *e, const float l)
{ logcall("search_for_quantizers"); real->search_for_quantizers(a, s, e, l); }
/* encode_tns_info runs at bitstream-write time, outside the tool-ordering
 * window this harness captures, so it is intentionally passed through without
 * logcall(); the Go order_trace likewise omits it. */
static void w_tnsinfo(struct AACEncContext *s, SingleChannelElement *e)
{ real->encode_tns_info(s, e); }
static void w_applytns(struct AACEncContext *s, SingleChannelElement *e)
{ logcall("apply_tns_filt"); real->apply_tns_filt(s, e); }
static void w_pns(struct AACEncContext *s, AVCodecContext *a, SingleChannelElement *e)
{ logcall("search_for_pns"); real->search_for_pns(s, a, e); }
static void w_markpns(struct AACEncContext *s, AVCodecContext *a, SingleChannelElement *e)
{ logcall("mark_pns"); real->mark_pns(s, a, e); }
static void w_tns(struct AACEncContext *s, SingleChannelElement *e)
{ logcall("search_for_tns"); real->search_for_tns(s, e); }
static void w_ms(struct AACEncContext *s, ChannelElement *c)
{ logcall("search_for_ms"); real->search_for_ms(s, c); }
static void w_is(struct AACEncContext *s, AVCodecContext *a, ChannelElement *c)
{ logcall("search_for_is"); real->search_for_is(s, a, c); }

static AACCoefficientsEncoder hooked;

/* run traces one (coder, channels, tns) combination. Returns 0 on success and
 * non-zero if any FFmpeg call fails, so a rejected option or a transient codec
 * error fails the harness instead of falling through to a false pass. */
static int run(const char *coder, int channels, int tns_on)
{
    int ret = 0;
    AVFrame *frame = NULL;
    AVPacket *pkt = NULL;
    AVCodecContext *avctx = NULL;

    const AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_AAC);
    if (!codec) { fprintf(stderr, "AAC encoder not found\n"); return 1; }
    avctx = avcodec_alloc_context3(codec);
    if (!avctx) { fprintf(stderr, "alloc context failed\n"); return 1; }
    avctx->sample_rate = 48000;
    avctx->bit_rate = 128000;
    avctx->sample_fmt = AV_SAMPLE_FMT_FLTP;
    avctx->flags |= AV_CODEC_FLAG_BITEXACT;
    av_channel_layout_default(&avctx->ch_layout, channels);
    if (av_opt_set(avctx->priv_data, "aac_coder", coder, 0) < 0) {
        fprintf(stderr, "set aac_coder=%s failed\n", coder); ret = 1; goto cleanup;
    }
    if (av_opt_set_int(avctx->priv_data, "aac_tns", tns_on, 0) < 0) {
        fprintf(stderr, "set aac_tns=%d failed\n", tns_on); ret = 1; goto cleanup;
    }
    if (avcodec_open2(avctx, codec, NULL) < 0) {
        fprintf(stderr, "open failed\n"); ret = 1; goto cleanup;
    }

    AACEncContext *s = avctx->priv_data;
    real = s->coder;
    hooked = *real;
    hooked.search_for_quantizers = w_sfq;
    hooked.encode_tns_info = w_tnsinfo;
    if (real->apply_tns_filt)  hooked.apply_tns_filt = w_applytns;
    if (real->search_for_pns)  hooked.search_for_pns = w_pns;
    if (real->mark_pns)        hooked.mark_pns = w_markpns;
    if (real->search_for_tns)  hooked.search_for_tns = w_tns;
    if (real->search_for_ms)   hooked.search_for_ms = w_ms;
    if (real->search_for_is)   hooked.search_for_is = w_is;
    s->coder = &hooked;

    frame = av_frame_alloc();
    pkt = av_packet_alloc();
    if (!frame || !pkt) { fprintf(stderr, "frame/packet alloc failed\n"); ret = 1; goto cleanup; }
    int64_t pts = 0;
    for (int f = 0; f < 3; f++) {
        frame->nb_samples = 1024;
        frame->format = AV_SAMPLE_FMT_FLTP;
        if (av_channel_layout_copy(&frame->ch_layout, &avctx->ch_layout) < 0) {
            fprintf(stderr, "channel layout copy failed\n"); ret = 1; goto cleanup;
        }
        if (av_frame_get_buffer(frame, 0) < 0) {
            fprintf(stderr, "frame get_buffer failed\n"); ret = 1; goto cleanup;
        }
        for (int ch = 0; ch < channels; ch++) {
            float *dst = (float *)frame->data[ch];
            for (int i = 0; i < 1024; i++) {
                /* transient-ish: a burst then decaying tone */
                float env = i < 128 ? 0.9f : 0.3f * expf(-(i - 128) / 300.0f);
                dst[i] = env * sinf(0.21f * i + ch);
            }
        }
        frame->pts = pts; pts += 1024;
        seq[0] = 0;
        if (avcodec_send_frame(avctx, frame) < 0) {
            fprintf(stderr, "send_frame failed\n"); av_frame_unref(frame); ret = 1; goto cleanup;
        }
        av_frame_unref(frame);
        int rp;
        while ((rp = avcodec_receive_packet(avctx, pkt)) == 0)
            av_packet_unref(pkt);
        if (rp != AVERROR(EAGAIN) && rp != AVERROR_EOF) {
            fprintf(stderr, "receive_packet failed\n"); ret = 1; goto cleanup;
        }
        if (seq[0])
            printf("%-8s ch=%d tns=%d frame%d: %s\n", coder, channels, tns_on, f, seq);
    }

cleanup:
    av_frame_free(&frame);
    av_packet_free(&pkt);
    avcodec_free_context(&avctx);
    return ret;
}

int main(void)
{
    av_force_cpu_flags(0);
    int ret = 0;
    ret |= run("nmr", 1, 1);
    ret |= run("nmr", 2, 1);
    ret |= run("nmr", 1, 0);
    ret |= run("twoloop", 1, 1);
    ret |= run("twoloop", 2, 1);
    ret |= run("fast", 2, 1);
    return ret ? 1 : 0;
}
