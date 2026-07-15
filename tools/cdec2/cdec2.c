/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cdec2 dumps the D2 reconstruction stages of the REAL fixed-point AAC decoder
 * from the pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a):
 *   - DQ:   sce->coeffs_fixed after decode_spectrum_and_dequant (dequantized
 *           spectrum, pre stereo: pow43+subband_scale + PNS + pulses)
 *   - SPEC: sce->coeffs_fixed at imdct_and_windowing entry (post M/S,
 *           intensity, TNS)
 *   - PCM:  che->ch[*].output_fixed after clip_output (the S32P samples)
 * Also emits the cbrt fixed table (CBRT line) and a fixed_sqrt probe (FSQ).
 * Compiles libavcodec/aac/aacdec_fixed.c into this TU like tools/cdec; the
 * decode path is the shipping pinned code driven through the vtable hooks.
 */
#define USE_FIXED 1
#include "libavcodec/aac_defines.h"
#include "libavcodec/avcodec.h"
#include "libavutil/mem.h"
#include "libavcodec/adts_header.h"
#include "libavcodec/adts_parser.h"
#include "libavcodec/aac/aacdec.h"
#include "libavcodec/aac/aacdec_tab.h"
#include "libavcodec/cbrt_data.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "libavcodec/aac/aacdec_fixed.c"

/* ------------------------------------------------------------------ */

static FILE *g_out;
static int   g_frame;

static void dump_ints(const char *tag, const int *v, int n)
{
    fprintf(g_out, "%s %d", tag, n);
    for (int i = 0; i < n; i++)
        fprintf(g_out, " %d", v[i]);
    fputc('\n', g_out);
}

/* Locate the (type,id,ch) coordinates of an sce inside ac->che. */
static void find_sce(AACDecContext *ac, SingleChannelElement *sce,
                     int *t, int *id, int *ch)
{
    *t = *id = *ch = -1;
    for (int ty = 0; ty < 4; ty++)
        for (int i = 0; i < MAX_ELEM_ID; i++) {
            ChannelElement *c = ac->che[ty][i];
            if (!c) continue;
            if (sce == &c->ch[0]) { *t = ty; *id = i; *ch = 0; return; }
            if (sce == &c->ch[1]) { *t = ty; *id = i; *ch = 1; return; }
        }
}

static int (*real_sad)(AACDecContext *ac, GetBitContext *gb,
                       const Pulse *pulse, SingleChannelElement *sce);
static void (*real_imdct)(AACDecContext *ac, SingleChannelElement *sce);
static void (*real_clip)(AACDecContext *ac, ChannelElement *che,
                         int type, int samples);

static int hook_sad(AACDecContext *ac, GetBitContext *gb,
                    const Pulse *pulse, SingleChannelElement *sce)
{
    int ret = real_sad(ac, gb, pulse, sce);
    int t, id, ch;
    find_sce(ac, sce, &t, &id, &ch);
    fprintf(g_out, "DQ f=%d t=%d id=%d ch=%d\n", g_frame, t, id, ch);
    dump_ints("C", sce->coeffs_fixed, 1024);
    return ret;
}

static void hook_imdct(AACDecContext *ac, SingleChannelElement *sce)
{
    int t, id, ch;
    find_sce(ac, sce, &t, &id, &ch);
    fprintf(g_out, "SPEC f=%d t=%d id=%d ch=%d\n", g_frame, t, id, ch);
    dump_ints("C", sce->coeffs_fixed, 1024);
    real_imdct(ac, sce);
}

static void hook_clip(AACDecContext *ac, ChannelElement *che, int type, int samples)
{
    real_clip(ac, che, type, samples);
    /* find the che coordinates */
    int t = -1, id = -1;
    for (int ty = 0; ty < 4 && t < 0; ty++)
        for (int i = 0; i < MAX_ELEM_ID; i++)
            if (ac->che[ty][i] == che) { t = ty; id = i; break; }
    fprintf(g_out, "PCM f=%d t=%d id=%d n=%d\n", g_frame, t, id, samples);
    dump_ints("L", che->ch[0].output_fixed, samples);
    if (type == TYPE_CPE)
        dump_ints("R", che->ch[1].output_fixed, samples);
}

/* Dump the cbrt fixed table and a fixed_sqrt probe: validated once, not per
 * stream. */
static void dump_tables(void)
{
    fprintf(g_out, "CBRT %d", LUT_SIZE);
    for (int i = 0; i < LUT_SIZE; i++)
        fprintf(g_out, " %u", ff_cbrt_tab_fixed[i]);
    fputc('\n', g_out);

    fprintf(g_out, "FSQ");
    for (int64_t x = 0; x <= 0x7fffffffLL; x = x ? x + (x >> 3) + 1 : 1)
        fprintf(g_out, " %d:%d", (int)x, fixed_sqrt((int)x, 31));
    fputc('\n', g_out);
}

static uint8_t *read_all(const char *path, size_t *size)
{
    FILE *f = fopen(path, "rb");
    if (!f) { perror(path); exit(1); }
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    uint8_t *buf = av_mallocz(n + AV_INPUT_BUFFER_PADDING_SIZE);
    if (fread(buf, 1, n, f) != (size_t)n) { perror("read"); exit(1); }
    fclose(f);
    *size = n;
    return buf;
}

int main(int argc, char **argv)
{
    if (argc < 3) {
        fprintf(stderr, "usage: cdec2 tables out.dump\n"
                        "       cdec2 adts in.bin out.dump\n"
                        "       cdec2 raw in.bin out.dump asc-hex\n");
        return 2;
    }
    /* tables mode: dump only the cbrt table + fixed_sqrt probe, then exit. */
    if (!strcmp(argv[1], "tables")) {
        g_out = fopen(argv[2], "w");
        if (!g_out) { perror(argv[2]); return 1; }
        const AVCodec *tc = avcodec_find_decoder_by_name("aac_fixed");
        if (!tc) { fprintf(stderr, "no aac_fixed\n"); return 1; }
        AVCodecContext *tctx = avcodec_alloc_context3(tc);
        tctx->flags |= AV_CODEC_FLAG_BITEXACT;
        if (avcodec_open2(tctx, tc, NULL) < 0) { /* runs init_tables_fixed_fn */
            fprintf(stderr, "open failed\n");
            return 1;
        }
        dump_tables();
        fprintf(g_out, "END\n");
        fclose(g_out);
        return 0;
    }
    if (argc < 4) {
        fprintf(stderr, "usage: cdec2 adts in.bin out.dump\n");
        return 2;
    }
    int raw = !strcmp(argv[1], "raw");
    if ((!raw && strcmp(argv[1], "adts")) || (raw && argc < 5)) {
        fprintf(stderr, "invalid mode, or missing ASC hex for raw mode\n");
        return 2;
    }
    size_t size;
    uint8_t *data = read_all(argv[2], &size);
    g_out = fopen(argv[3], "w");
    if (!g_out) { perror(argv[3]); return 1; }

    const AVCodec *codec = avcodec_find_decoder_by_name("aac_fixed");
    if (!codec) { fprintf(stderr, "no aac_fixed\n"); return 1; }
    AVCodecContext *avctx = avcodec_alloc_context3(codec);
    avctx->thread_count = 1;
    avctx->flags |= AV_CODEC_FLAG_BITEXACT;
    if (raw) {
        static uint8_t asc[64];
        int alen = 0;
        size_t hex_len = strlen(argv[4]);
        if (!hex_len || hex_len % 2 || hex_len / 2 > sizeof(asc)) {
            fprintf(stderr, "invalid ASC hex length\n");
            return 2;
        }
        for (const char *p = argv[4]; *p; p += 2, alen++) {
            int n = 0;
            if (sscanf(p, "%2hhx%n", &asc[alen], &n) != 1 || n != 2) {
                fprintf(stderr, "invalid ASC hex\n");
                return 2;
            }
        }
        avctx->extradata = av_mallocz(alen + AV_INPUT_BUFFER_PADDING_SIZE);
        memcpy(avctx->extradata, asc, alen);
        avctx->extradata_size = alen;
    }
    if (avcodec_open2(avctx, codec, NULL) < 0) {
        fprintf(stderr, "open failed\n");
        return 1;
    }
    AACDecContext *ac = avctx->priv_data;
    real_sad   = ac->proc.decode_spectrum_and_dequant;
    real_imdct = ac->dsp.imdct_and_windowing;
    real_clip  = ac->dsp.clip_output;
    ac->proc.decode_spectrum_and_dequant = hook_sad;
    ac->dsp.imdct_and_windowing          = hook_imdct;
    ac->dsp.clip_output                  = hook_clip;

    AVPacket *pkt = av_packet_alloc();
    AVFrame *frame = av_frame_alloc();
    size_t pos = 0;
    int fidx = 0;
    int failed = 0;
    while (pos < size) {
        size_t flen;
        if (raw) {
            if (pos + 2 > size) break;
            flen = (size_t)data[pos] << 8 | data[pos + 1];
            pos += 2;
            if (pos + flen > size) break;
        } else {
            if (pos + AV_AAC_ADTS_HEADER_SIZE > size) break;
            uint8_t hdrbuf[AV_AAC_ADTS_HEADER_SIZE + AV_INPUT_BUFFER_PADDING_SIZE] = {0};
            memcpy(hdrbuf, data + pos, AV_AAC_ADTS_HEADER_SIZE);
            AACADTSHeaderInfo hdr;
            int r = ff_adts_header_parse_buf(hdrbuf, &hdr);
            if (r < 0) { pos++; continue; }
            flen = hdr.frame_length;
            if (pos + flen > size) break;
        }
        g_frame = fidx;
        fprintf(g_out, "FRAME %d\n", fidx);
        av_packet_unref(pkt);
        av_new_packet(pkt, flen);
        memcpy(pkt->data, data + pos, flen);
        int serr = avcodec_send_packet(avctx, pkt);
        int rerr = avcodec_receive_frame(avctx, frame);
        if (serr < 0 || rerr < 0) {
            fprintf(g_out, "FRAMEEND err\n");
            failed = 1;
        } else
            fprintf(g_out, "FRAMEEND ok samples=%d\n", frame->nb_samples);
        pos += flen;
        fidx++;
    }
    fprintf(g_out, "STREAMEND frames=%d\n", fidx);
    fclose(g_out);
    fprintf(stderr, "cdec2: %d frames dumped\n", fidx);
    return failed ? 1 : 0;
}
