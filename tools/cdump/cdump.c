/* SPDX-License-Identifier: LGPL-2.1-or-later
 * Dumps golden fixtures from the pinned FFmpeg tree (d09d5afc3a) for go-aac
 * tests. Input signals use the encoder's own LCG so Go tests can regenerate
 * the identical input without reading the _in files.
 */
#include <stdio.h>
#include <stdint.h>
#include "libavutil/tx.h"
#include "libavcodec/kbdwin.h"
#include "libavcodec/lpc.h"

static uint32_t lcg(uint32_t v) { return v * 1664525u + 1013904223u; }

static void dump(const char *name, const float *data, int n)
{
    FILE *f = fopen(name, "wb");
    if (!f) { perror(name); return; }
    fwrite(data, sizeof(float), n, f);
    fclose(f);
}

static void dump64(const char *name, const double *data, int n)
{
    FILE *f = fopen(name, "wb");
    if (!f) { perror(name); return; }
    fwrite(data, sizeof(double), n, f);
    fclose(f);
}

static void mdct_case(int n, const char *inname, const char *outname)
{
    AVTXContext *tx = NULL;
    av_tx_fn fn;
    float scale = 32768.0f;
    static float in[2048], out[1024];
    uint32_t state = 0x1f2e3d4c;
    for (int i = 0; i < 2 * n; i++) {
        state = lcg(state);
        in[i] = (float)(int32_t)state / 2147483648.0f;
    }
    if (av_tx_init(&tx, &fn, AV_TX_FLOAT_MDCT, 0, n, &scale, 0) < 0) {
        fprintf(stderr, "av_tx_init(%d) failed\n", n);
        return;
    }
    fn(tx, out, in, sizeof(float));
    dump(inname, in, 2 * n);
    dump(outname, out, n);
    av_tx_uninit(&tx);
}

/* Reflection coefficients from ff_lpc_calc_ref_coefs_f for an LCG noise
 * input scaled to +-1024.0 (TNS sees MDCT coefficients of that order).
 * Output layout: gain, then order ref coefficients, as raw doubles. */
static void lpc_case(int order, int apply_window, const char *outname)
{
    LPCContext lpc;
    static float in[1024];
    double ref[32] = {0};
    double out[33];
    uint32_t state = 0x1f2e3d4c;
    for (int i = 0; i < 1024; i++) {
        state = lcg(state);
        in[i] = 1024.0f * ((float)(int32_t)state / 2147483648.0f);
    }
    if (ff_lpc_init(&lpc, 1024, 32, FF_LPC_TYPE_LEVINSON) < 0) {
        fprintf(stderr, "ff_lpc_init failed\n");
        return;
    }
    out[0] = ff_lpc_calc_ref_coefs_f(&lpc, in, 1024, order, ref, apply_window);
    for (int i = 0; i < order; i++)
        out[1 + i] = ref[i];
    dump64(outname, out, 1 + order);
    ff_lpc_end(&lpc);
}

int main(void)
{
    static float kbd1024[1024], kbd128[128];
    mdct_case(1024, "mdct1024_in.f32", "mdct1024_out.f32");
    mdct_case(128,  "mdct128_in.f32",  "mdct128_out.f32");
    ff_kbd_window_init(kbd1024, 4.0f, 1024);
    ff_kbd_window_init(kbd128,  6.0f, 128);
    dump("kbd1024.f32", kbd1024, 1024);
    dump("kbd128.f32", kbd128, 128);
    lpc_case(2,  0, "lpc_o2_rect.f64");
    lpc_case(12, 1, "lpc_o12_hann.f64");
    return 0;
}
