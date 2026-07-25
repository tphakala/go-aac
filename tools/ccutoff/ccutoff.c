/* SPDX-License-Identifier: LGPL-2.1-or-later
 * ccutoff prints the coding bandwidth the REAL shipping AAC encoder of the
 * pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a) fixes at
 * init time (aacenc.c:1590-1616). It opens the encoder through the public
 * API and reads AACEncContext->bandwidth out of avctx->priv_data, so the
 * number is the encoder's own, not a transcription of the branch.
 *
 * It is the oracle for the Go port's bandwidth computation in
 * internal/enc.(*Encoder).Reset, and in particular for the 15% widening at
 * aacenc.c:1609-1610, which keys on the pns and intensity_stereo option
 * flags rather than on the coder (issue #49).
 *
 * Build (same recipe as tools/cpsy, which also links the prebuilt libs):
 *
 *     # FFMPEG_SRC is the pinned FFmpeg checkout, FFMPEG_BUILD its build tree
 *     clang -O2 -DNDEBUG -I "$FFMPEG_BUILD" -I "$FFMPEG_SRC" ccutoff.c \
 *         "$FFMPEG_BUILD/libavcodec/libavcodec.a" \
 *         "$FFMPEG_BUILD/libavutil/libavutil.a" \
 *         "$FFMPEG_BUILD/libswresample/libswresample.a" \
 *         -lm -lpthread -o ccutoff
 *
 * Usage:
 *
 *     ccutoff                 # CSV grid over coder x rate x channels x bitrate x tools
 *     ccutoff <coder> <rate> <channels> <bitrate> <cutoff> <pns> <is>
 *
 * <bitrate> is the total across channels, as avctx->bit_rate is.
 */
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "libavcodec/avcodec.h"
#include "libavutil/channel_layout.h"
#include "libavutil/opt.h"
#include "libavcodec/aacenc.h"

/* bandwidth_for opens the pinned encoder with the given settings and returns
 * the bandwidth it fixed at init, or -1 on failure. */
static int bandwidth_for(const char *coder, int rate, int channels,
                         int64_t bitrate, int cutoff, int pns, int is)
{
    const AVCodec *codec = avcodec_find_encoder_by_name("aac");
    AVCodecContext *avctx;
    AACEncContext *s;
    int bw;

    if (!codec) {
        fprintf(stderr, "no aac encoder in this build\n");
        return -1;
    }
    avctx = avcodec_alloc_context3(codec);
    if (!avctx)
        return -1;
    avctx->sample_rate = rate;
    avctx->sample_fmt = AV_SAMPLE_FMT_FLTP;
    avctx->bit_rate = bitrate;
    avctx->cutoff = cutoff;
    av_channel_layout_default(&avctx->ch_layout, channels);

    /* A rejected option would leave the encoder on the AVOption default and
     * this tool would print a bandwidth labelled with a coder or tool state
     * that was never applied. Since the printed numbers are transcribed into
     * the Go regression test as ground truth, a silently mislabelled row is
     * the one failure mode that matters here: fail loudly instead. */
    if (av_opt_set(avctx->priv_data, "aac_coder", coder, 0) < 0) {
        fprintf(stderr, "unknown coder %s\n", coder);
        avcodec_free_context(&avctx);
        return -1;
    }
    if (av_opt_set_int(avctx->priv_data, "aac_pns", pns, 0) < 0 ||
        av_opt_set_int(avctx->priv_data, "aac_is", is, 0) < 0) {
        fprintf(stderr, "rejected pns=%d is=%d\n", pns, is);
        avcodec_free_context(&avctx);
        return -1;
    }

    if (avcodec_open2(avctx, codec, NULL) < 0) {
        fprintf(stderr, "avcodec_open2 failed (%s %d Hz %d ch %lld bps)\n",
                coder, rate, channels, (long long)bitrate);
        avcodec_free_context(&avctx);
        return -1;
    }
    s = avctx->priv_data;
    bw = s->bandwidth;
    avcodec_free_context(&avctx);
    return bw;
}

int main(int argc, char **argv)
{
    static const char *coders[] = { "nmr", "twoloop", "fast" };
    static const int rates[]    = { 44100, 48000 };
    static const int brs[]      = { 16000, 30000, 32000, 48000, 64000, 96000,
                                    128000, 192000, 256000 };
    int ci, ri, bi, ch, tools, bw;
    long long v[6];

    av_log_set_level(AV_LOG_ERROR);

    if (argc == 8) {
        /* Parse strictly. atoi() maps a typo to 0, which would print a
         * plausible-looking bandwidth for settings nobody asked for. */
        for (ci = 0; ci < 6; ci++) {
            char *end;
            errno = 0;
            v[ci] = strtoll(argv[ci + 2], &end, 10);
            if (errno || end == argv[ci + 2] || *end) {
                fprintf(stderr, "argument %d (%s) is not an integer\n",
                        ci + 2, argv[ci + 2]);
                return 2;
            }
        }
        if (v[1] < 1 || v[1] > 2) {
            fprintf(stderr, "channels must be 1 or 2, got %lld\n", v[1]);
            return 2;
        }
        bw = bandwidth_for(argv[1], (int)v[0], (int)v[1], v[2], (int)v[3],
                           (int)v[4], (int)v[5]);
        if (bw < 0)
            return 1;
        printf("%d\n", bw);
        return 0;
    }
    if (argc != 1) {
        fprintf(stderr, "usage: %s [coder rate channels bitrate cutoff pns is]\n",
                argv[0]);
        return 2;
    }

    /* tools: 0 = pns and is both off, 1 = defaults (both on) */
    printf("coder,rate,channels,bitrate_total,br_per_ch,pns,is,bandwidth\n");
    for (ci = 0; ci < (int)FF_ARRAY_ELEMS(coders); ci++)
        for (ri = 0; ri < (int)FF_ARRAY_ELEMS(rates); ri++)
            for (ch = 1; ch <= 2; ch++)
                for (tools = 0; tools <= 1; tools++)
                    for (bi = 0; bi < (int)FF_ARRAY_ELEMS(brs); bi++) {
                        int64_t total = (int64_t)brs[bi] * ch;
                        bw = bandwidth_for(coders[ci], rates[ri], ch, total, 0,
                                           tools, tools);
                        if (bw < 0)
                            return 1;
                        printf("%s,%d,%d,%lld,%d,%d,%d,%d\n", coders[ci],
                               rates[ri], ch, (long long)total, brs[bi],
                               tools, tools, bw);
                    }
    return 0;
}
