/* SPDX-License-Identifier: LGPL-2.1-or-later
 * cdec dumps per-frame decoded symbol streams from the REAL fixed-point AAC
 * decoder of the pinned FFmpeg tree (d09d5afc3aebede25d2d245ee23b75a47ea17c3a).
 * It compiles libavcodec/aac/aacdec_fixed.c into this TU with ONE seam: the
 * include guard of aacdec_fixed_dequant.h is pre-claimed so the header can be
 * included first with vector_pow43 renamed, and a wrapping vector_pow43 dumps
 * the raw quantized spectral integers before applying the real conversion.
 * Everything else, including the frame-level decoder (archive aacdec.o) and
 * the ADTS parser (archive adts_header.o), is the shipping code. The proc
 * vtable entry decode_spectrum_and_dequant is hooked after avcodec_open2 to
 * dump ICS info, MS masks, TNS data, pulses, band types and scalefactor
 * offsets before delegating to the real function.
 */
#define USE_FIXED 1
#include "libavcodec/aac_defines.h"
#include "libavcodec/avcodec.h"
#include "libavutil/avassert.h"
#include "libavutil/mem.h"
#include "libavcodec/adts_header.h"
#include "libavcodec/adts_parser.h"
#include "libavcodec/aac/aacdec.h"
#include "libavcodec/aac/aacdec_tab.h"
#include "libavcodec/cbrt_data.h"

#include <stdio.h>
#include <string.h>

/* Include the real dequant header with vector_pow43 renamed, then define the
 * dumping wrapper under the real name; aacdec_fixed.c's own include of the
 * header is skipped by its guard, so its templates call the wrapper. */
#define vector_pow43 cdec_vector_pow43_real
#include "libavcodec/aac/aacdec_fixed_dequant.h"
#undef vector_pow43

static void cdec_dump_band(const int *coefs, int len);
static inline void vector_pow43(int *coefs, int len)
{
    cdec_dump_band(coefs, len);
    cdec_vector_pow43_real(coefs, len);
}

#include "libavcodec/aac/aacdec_fixed.c"

/* ------------------------------------------------------------------ */

static FILE *g_out;
static int g_dumped_m4ac;

static void cdec_dump_band(const int *coefs, int len)
{
    fprintf(g_out, "SD %d", len);
    for (int i = 0; i < len; i++)
        fprintf(g_out, " %d", coefs[i]);
    fputc('\n', g_out);
}

static int (*real_sad)(AACDecContext *ac, GetBitContext *gb,
                       const Pulse *pulse, SingleChannelElement *sce);

static int hook_sad(AACDecContext *ac, GetBitContext *gb,
                    const Pulse *pulse, SingleChannelElement *sce)
{
    int et = -1, ei = -1, ch = -1;
    ChannelElement *che = NULL;
    for (int t = 0; t < 4 && et < 0; t++)
        for (int i = 0; i < MAX_ELEM_ID; i++) {
            ChannelElement *c = ac->che[t][i];
            if (!c) continue;
            if (sce == &c->ch[0]) { et = t; ei = i; ch = 0; che = c; break; }
            if (sce == &c->ch[1]) { et = t; ei = i; ch = 1; che = c; break; }
        }

    if (!g_dumped_m4ac) {
        MPEG4AudioConfig *m = &ac->oc[1].m4ac;
        fprintf(g_out, "M4A obj=%d sri=%d sr=%d cfg=%d sbr=%d ps=%d fls=%d\n",
                m->object_type, m->sampling_index, m->sample_rate,
                m->chan_config, m->sbr, m->ps, m->frame_length_short);
        g_dumped_m4ac = 1;
    }

    IndividualChannelStream *ics = &sce->ics;
    fprintf(g_out, "ELEM t=%d id=%d ch=%d\n", et, ei, ch);
    fprintf(g_out, "ICS ws=%d kb=%d msfb=%d nw=%d nwg=%d gl=",
            ics->window_sequence[0], ics->use_kb_window[0], ics->max_sfb,
            ics->num_windows, ics->num_window_groups);
    for (int g = 0; g < ics->num_window_groups; g++)
        fprintf(g_out, "%s%d", g ? "," : "", ics->group_len[g]);
    fprintf(g_out, " swb=%d pred=%d\n", ics->num_swb, ics->predictor_present);

    if (et == TYPE_CPE && ch == 0 && che) {
        fprintf(g_out, "MS");
        for (int i = 0; i < ics->num_window_groups * ics->max_sfb; i++)
            fprintf(g_out, " %d", che->ms_mask[i]);
        fputc('\n', g_out);
    }

    TemporalNoiseShaping *tns = &sce->tns;
    fprintf(g_out, "TNS %d\n", tns->present);
    if (tns->present) {
        for (int w = 0; w < ics->num_windows; w++) {
            fprintf(g_out, "TNSW w=%d nf=%d\n", w, tns->n_filt[w]);
            for (int f = 0; f < tns->n_filt[w]; f++) {
                fprintf(g_out, "TNSF l=%d o=%d d=%d c=",
                        tns->length[w][f], tns->order[w][f],
                        tns->direction[w][f]);
                for (int o = 0; o < tns->order[w][f]; o++)
                    fprintf(g_out, "%s%d", o ? "," : "",
                            tns->coef_fixed[w][f][o]);
                fputc('\n', g_out);
            }
        }
    }

    if (pulse) {
        fprintf(g_out, "PUL %d", pulse->num_pulse);
        for (int i = 0; i < pulse->num_pulse; i++)
            fprintf(g_out, " %d:%d", pulse->pos[i], pulse->amp[i]);
        fputc('\n', g_out);
    }

    int n = ics->num_window_groups * ics->max_sfb;
    fprintf(g_out, "BT");
    for (int i = 0; i < n; i++)
        fprintf(g_out, " %d", sce->band_type[i]);
    fprintf(g_out, "\nSFO");
    for (int i = 0; i < n; i++)
        fprintf(g_out, " %d", sce->sfo[i]);
    fputc('\n', g_out);

    return real_sad(ac, gb, pulse, sce);
}

static uint8_t *read_all(const char *path, size_t *size)
{
    FILE *f = fopen(path, "rb");
    if (!f) { perror(path); exit(1); }
    if (fseek(f, 0, SEEK_END) < 0) { perror(path); exit(1); }
    long n = ftell(f);
    if (n < 0) { perror(path); exit(1); }
    if (fseek(f, 0, SEEK_SET) < 0) { perror(path); exit(1); }
    uint8_t *buf = av_mallocz((size_t)n + AV_INPUT_BUFFER_PADDING_SIZE);
    if (!buf) {
        fprintf(stderr, "%s: cannot allocate %ld bytes\n", path, n);
        exit(1);
    }
    if (fread(buf, 1, (size_t)n, f) != (size_t)n) { perror("read"); exit(1); }
    fclose(f);
    *size = (size_t)n;
    return buf;
}

int main(int argc, char **argv)
{
    if (argc < 4) {
        fprintf(stderr, "usage: cdec adts in.bin out.dump\n"
                        "       cdec raw in.bin out.dump asc-hex\n");
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
        /* argv[4] = hex ASC, e.g. 1190 */
        static uint8_t asc[64];
        int alen = 0;
        size_t hex_len = strlen(argv[4]);
        if (!hex_len || hex_len % 2 || hex_len / 2 > sizeof(asc)) {
            fprintf(stderr, "invalid ASC hex length\n");
            return 2;
        }
        for (const char *p = argv[4]; *p; p += 2, alen++) {
            if (sscanf(p, "%2hhx", &asc[alen]) != 1) {
                fprintf(stderr, "invalid ASC hex\n");
                return 2;
            }
        }
        avctx->extradata = av_mallocz((size_t)alen + AV_INPUT_BUFFER_PADDING_SIZE);
        if (!avctx->extradata) {
            fprintf(stderr, "extradata alloc failed\n");
            return 1;
        }
        memcpy(avctx->extradata, asc, (size_t)alen);
        avctx->extradata_size = alen;
    }
    if (avcodec_open2(avctx, codec, NULL) < 0) {
        fprintf(stderr, "open failed\n");
        return 1;
    }
    AACDecContext *ac = avctx->priv_data;
    real_sad = ac->proc.decode_spectrum_and_dequant;
    ac->proc.decode_spectrum_and_dequant = hook_sad;

    if (raw) {
        MPEG4AudioConfig *m = &ac->oc[1].m4ac;
        fprintf(g_out, "ASC obj=%d sri=%d sr=%d cfg=%d sbr=%d ps=%d fls=%d\n",
                m->object_type, m->sampling_index, m->sample_rate,
                m->chan_config, m->sbr, m->ps, m->frame_length_short);
    }

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
            int ret = ff_adts_header_parse_buf(hdrbuf, &hdr);
            if (ret < 0) { pos++; continue; } /* resync scan */
            fprintf(g_out,
                    "HDR obj=%d cfg=%d crcabs=%d rdb=%d sri=%d sr=%d len=%d\n",
                    hdr.object_type, hdr.chan_config, hdr.crc_absent,
                    hdr.num_aac_frames, hdr.sampling_index, hdr.sample_rate,
                    hdr.frame_length);
            flen = hdr.frame_length;
            if (pos + flen > size) break;
        }
        fprintf(g_out, "FRAME %d\n", fidx);
        av_packet_unref(pkt);
        if (av_new_packet(pkt, (int)flen) < 0) {
            fprintf(stderr, "cdec: av_new_packet(%zu) failed at frame %d\n",
                    flen, fidx);
            fclose(g_out);
            return 1;
        }
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
    fprintf(stderr, "cdec: %d frames dumped\n", fidx);
    return failed ? 1 : 0;
}
