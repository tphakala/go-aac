/* SPDX-License-Identifier: LGPL-2.1-or-later
 * gentables emits the AAC encoder/decoder tables of the pinned FFmpeg tree
 * (d09d5afc3aebede25d2d245ee23b75a47ea17c3a) in two forms:
 *
 *   gentables go    ready-to-commit Go source for internal/tables
 *   gentables bin   binary fixture records for the Go golden tests
 *
 * The pinned table translation units are #included directly below, so every
 * file-local (static) table is visible in this translation unit with full
 * compile-time size information. Nothing is hand-transcribed: the compiler
 * parses the pinned sources and this program prints what it finds.
 */
#include <assert.h> /* aacpsy.c uses assert() and relies on the FFmpeg build
                     * pulling it in; provide it before the include below */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

/* The pinned table sources. aactab.c carries the shared codec tables,
 * aacenctab.c the encoder swb size tables, aacenctab.h the encoder maps
 * (static const in a header, unreachable through the archive), aacpsy.c the
 * psy model presets (file-local statics). */
#include "libavcodec/aactab.c"
#include "libavcodec/aacenctab.c"
#include "libavcodec/aacpsy.c"

/* The included translation units are compiled but never executed beyond
 * aac_tableinit (pure table math). Stub the functions they reference so the
 * generator links against libavutil only; linking libavcodec.a would clash
 * with the tables this file re-defines via the #includes above. */
void ff_kbd_window_init(float *window, float alpha, int n)
{
    (void)window; (void)alpha; (void)n;
}

void ff_init_ff_sine_windows(int index)
{
    (void)index;
}

FFPsyChannelGroup *ff_psy_find_group(FFPsyContext *ctx, int channel)
{
    (void)ctx; (void)channel;
    return 0;
}

/* ---- compile-time ground truth for every array length used below ---- */
#define ELEMS(a) (sizeof(a) / sizeof((a)[0]))
_Static_assert(ELEMS(codes1) == 81 && ELEMS(bits1) == 81, "cb1");
_Static_assert(ELEMS(codes2) == 81 && ELEMS(bits2) == 81, "cb2");
_Static_assert(ELEMS(codes3) == 81 && ELEMS(bits3) == 81, "cb3");
_Static_assert(ELEMS(codes4) == 81 && ELEMS(bits4) == 81, "cb4");
_Static_assert(ELEMS(codes5) == 81 && ELEMS(bits5) == 81, "cb5");
_Static_assert(ELEMS(codes6) == 81 && ELEMS(bits6) == 81, "cb6");
_Static_assert(ELEMS(codes7) == 64 && ELEMS(bits7) == 64, "cb7");
_Static_assert(ELEMS(codes8) == 64 && ELEMS(bits8) == 64, "cb8");
_Static_assert(ELEMS(codes9) == 169 && ELEMS(bits9) == 169, "cb9");
_Static_assert(ELEMS(codes10) == 169 && ELEMS(bits10) == 169, "cb10");
_Static_assert(ELEMS(codes11) == 289 && ELEMS(bits11) == 289, "cb11");
_Static_assert(ELEMS(codebook_vector0) == 324, "vec0");
_Static_assert(ELEMS(codebook_vector2) == 324, "vec2");
_Static_assert(ELEMS(codebook_vector4) == 162, "vec4");
_Static_assert(ELEMS(codebook_vector6) == 128, "vec6");
_Static_assert(ELEMS(codebook_vector8) == 338, "vec8");
_Static_assert(ELEMS(codebook_vector10) == 578, "vec10");
_Static_assert(ELEMS(tns_tmp2_map_0_3) == 8, "tns03");
_Static_assert(ELEMS(tns_tmp2_map_0_4) == 16, "tns04");
_Static_assert(ELEMS(tns_tmp2_map_1_3) == 4, "tns13");
_Static_assert(ELEMS(tns_tmp2_map_1_4) == 8, "tns14");
_Static_assert(ELEMS(ff_aac_scalefactor_code) == 121, "sfcode");
_Static_assert(ELEMS(ff_aac_scalefactor_bits) == 121, "sfbits");
_Static_assert(ELEMS(ff_aac_num_swb_1024) == 13, "nswb1024");
_Static_assert(ELEMS(ff_aac_num_swb_128) == 13, "nswb128");
_Static_assert(ELEMS(ff_tns_max_bands_1024) == 13, "tnsmb1024");
_Static_assert(ELEMS(ff_tns_max_bands_128) == 13, "tnsmb128");
_Static_assert(ELEMS(ff_aac_swb_size_1024) == 13, "swbsz1024");
_Static_assert(ELEMS(ff_aac_swb_size_128) == 13, "swbsz128");
_Static_assert(ELEMS(ff_swb_offset_1024) == 13, "swboff1024");
_Static_assert(ELEMS(ff_swb_offset_128) == 13, "swboff128");
_Static_assert(ELEMS(aac_chan_configs) == 14, "chancfg");
_Static_assert(ELEMS(aac_chan_maps) == 14 && ELEMS(aac_chan_maps[0]) == 16, "chanmap");
_Static_assert(ELEMS(run_value_bits_long) == 64, "rvbl");
_Static_assert(ELEMS(run_value_bits_short) == 16, "rvbs");
_Static_assert(ELEMS(tns_min_sfb_long) == 16, "tmsl");
_Static_assert(ELEMS(tns_min_sfb_short) == 16, "tmss");
_Static_assert(ELEMS(aac_cb_out_map) == 15, "cbout");
_Static_assert(ELEMS(aac_cb_in_map) == 16, "cbin");
_Static_assert(ELEMS(aac_cb_range) == 12, "cbrange");
_Static_assert(ELEMS(aac_cb_maxval) == 12, "cbmaxval");
_Static_assert(ELEMS(aac_maxval_cb) == 14, "maxvalcb");
_Static_assert(ELEMS(psy_abr_map) == 13, "abrmap");
_Static_assert(ELEMS(psy_vbr_map) == 11, "vbrmap");
_Static_assert(ELEMS(psy_fir_coeffs) == 10, "fir");
_Static_assert(ELEMS(window_grouping) == 9, "wgroup");
_Static_assert(ELEMS(ff_aac_pow2sf_tab) == 428, "pow2sf");
_Static_assert(ELEMS(ff_aac_pow34sf_tab) == 428, "pow34sf");

/* swb_offset arrays are reached through pointer tables; recover the real
 * (compiler-known) length by matching the pointer against every candidate. */
static int swb_offset_len(const uint16_t *p)
{
#define L(a) if (p == (a)) return (int)ELEMS(a);
    L(swb_offset_1024_96) L(swb_offset_1024_64) L(swb_offset_1024_48)
    L(swb_offset_1024_32) L(swb_offset_1024_24) L(swb_offset_1024_16)
    L(swb_offset_1024_8)
    L(swb_offset_128_96) L(swb_offset_128_48) L(swb_offset_128_24)
    L(swb_offset_128_16) L(swb_offset_128_8)
#undef L
    return -1;
}

/* The dequant helper arrays are reached through pointer tables too; recover
 * the compiler-known length of each by matching the pointer. */
static int vals_len(const float *p)
{
#define L(a) if (p == (a)) return (int)ELEMS(a);
    L(codebook_vector0_vals) L(codebook_vector4_vals) L(codebook_vector10_vals)
#undef L
    return -1;
}

static int idx_len(const uint16_t *p)
{
#define L(a) if (p == (a)) return (int)ELEMS(a);
    L(codebook_vector02_idx) L(codebook_vector4_idx) L(codebook_vector6_idx)
    L(codebook_vector8_idx) L(codebook_vector10_idx)
#undef L
    return -1;
}

static const int spectral_dim[11] = {4, 4, 4, 4, 2, 2, 2, 2, 2, 2, 2};
static int vector_vals_len[11], vector_idx_len[11];

static void die(const char *msg)
{
    fprintf(stderr, "gentables: %s\n", msg);
    exit(1);
}

/* ---------------------------- Go emission ---------------------------- */

static int first_emit = 1;

static void hdr(const char *goname, const char *cname, const char *cfile)
{
    if (!first_emit)
        printf("\n");
    first_emit = 0;
    printf("// %s mirrors %s (libavcodec/%s @ d09d5afc3a).\n", goname, cname, cfile);
}

static void sep(int i, int n, int per)
{
    if ((i + 1) % per == 0 || i == n - 1)
        printf("\n");
    else
        printf(" ");
}

static void emit_u8(const char *goname, const char *cname, const char *cfile,
                    const uint8_t *v, int n, int hex)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d]uint8{\n", goname, n);
    for (int i = 0; i < n; i++) {
        if (i % 16 == 0)
            printf("\t");
        printf(hex ? "0x%02x," : "%u,", v[i]);
        sep(i, n, 16);
    }
    printf("}\n");
}

static void emit_u8_2d(const char *goname, const char *cname, const char *cfile,
                       const uint8_t *v, int rows, int cols)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d][%d]uint8{\n", goname, rows, cols);
    for (int r = 0; r < rows; r++) {
        printf("\t{");
        for (int c = 0; c < cols; c++)
            printf(c ? ", %u" : "%u", v[r * cols + c]);
        printf("},\n");
    }
    printf("}\n");
}

static void emit_u32_row(const uint32_t *v, int n, int hex, int per, const char *ind)
{
    for (int i = 0; i < n; i++) {
        if (i % per == 0)
            printf("%s", ind);
        printf(hex ? "0x%x," : "%u,", v[i]);
        sep(i, n, per);
    }
}

static void emit_u32(const char *goname, const char *cname, const char *cfile,
                     const uint32_t *v, int n, int hex)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d]uint32{\n", goname, n);
    emit_u32_row(v, n, hex, 8, "\t");
    printf("}\n");
}

static void u16_row(const uint16_t *v, int n, int hex, int per, const char *ind)
{
    for (int i = 0; i < n; i++) {
        if (i % per == 0)
            printf("%s", ind);
        printf(hex ? "0x%x," : "%u,", v[i]);
        sep(i, n, per);
    }
}

static void u8_row(const uint8_t *v, int n, int per, const char *ind)
{
    for (int i = 0; i < n; i++) {
        if (i % per == 0)
            printf("%s", ind);
        printf("%u,", v[i]);
        sep(i, n, per);
    }
}

static void f32_row(const float *v, int n, int per, const char *ind)
{
    for (int i = 0; i < n; i++) {
        if (i % per == 0)
            printf("%s", ind);
        printf("%a,", (double)v[i]);
        sep(i, n, per);
    }
}

static void emit_u16_slices(const char *goname, const char *cname, const char *cfile,
                            const uint16_t *const *rows, const int *lens, int n, int hex)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d][]uint16{\n", goname, n);
    for (int r = 0; r < n; r++) {
        printf("\t{\n");
        u16_row(rows[r], lens[r], hex, 8, "\t\t");
        printf("\t},\n");
    }
    printf("}\n");
}

static void emit_u8_slices(const char *goname, const char *cname, const char *cfile,
                           const uint8_t *const *rows, const int *lens, int n)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d][]uint8{\n", goname, n);
    for (int r = 0; r < n; r++) {
        printf("\t{");
        for (int i = 0; i < lens[r]; i++)
            printf(i ? ", %u" : "%u", rows[r][i]);
        printf("},\n");
    }
    printf("}\n");
}

static void emit_f32_slices(const char *goname, const char *cname, const char *cfile,
                            const float *const *rows, const int *lens, int n)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d][]float32{\n", goname, n);
    for (int r = 0; r < n; r++) {
        printf("\t{\n");
        f32_row(rows[r], lens[r], 4, "\t\t");
        printf("\t},\n");
    }
    printf("}\n");
}

static void emit_f32(const char *goname, const char *cname, const char *cfile,
                     const float *v, int n)
{
    hdr(goname, cname, cfile);
    printf("var %s = [%d]float32{\n", goname, n);
    f32_row(v, n, 4, "\t");
    printf("}\n");
}

static void emit_psy_map(const char *goname, const char *cname,
                         const PsyLamePreset *v, int n)
{
    hdr(goname, cname, "aacpsy.c");
    printf("var %s = [%d]PsyLamePreset{\n", goname, n);
    for (int i = 0; i < n; i++)
        printf("\t{Quality: %d, StLrm: %a},\n", v[i].quality, (double)v[i].st_lrm);
    printf("}\n");
}

/* --------------------------- binary emission --------------------------- */

typedef struct {
    uint8_t *p;
    size_t len, cap;
} Buf;

static void bput(Buf *b, const void *d, size_t n)
{
    if (b->len + n > b->cap) {
        b->cap = b->cap ? b->cap : 4096;
        while (b->cap < b->len + n)
            b->cap *= 2;
        b->p = realloc(b->p, b->cap);
        if (!b->p)
            die("oom");
    }
    memcpy(b->p + b->len, d, n);
    b->len += n;
}

static void bu32(Buf *b, uint32_t v) { bput(b, &v, 4); }

static void rec(const char *name, const void *data, size_t nbytes)
{
    uint8_t nl = (uint8_t)strlen(name);
    uint32_t len = (uint32_t)nbytes;
    fwrite(&nl, 1, 1, stdout);
    fwrite(name, 1, nl, stdout);
    fwrite(&len, 4, 1, stdout);
    fwrite(data, 1, nbytes, stdout);
}

static void rec_u16_slices(const char *name, const uint16_t *const *rows,
                           const int *lens, int n)
{
    Buf b = {0};
    for (int r = 0; r < n; r++) {
        bu32(&b, (uint32_t)lens[r]);
        bput(&b, rows[r], (size_t)lens[r] * 2);
    }
    rec(name, b.p, b.len);
    free(b.p);
}

static void rec_u8_slices(const char *name, const uint8_t *const *rows,
                          const int *lens, int n)
{
    Buf b = {0};
    for (int r = 0; r < n; r++) {
        bu32(&b, (uint32_t)lens[r]);
        bput(&b, rows[r], (size_t)lens[r]);
    }
    rec(name, b.p, b.len);
    free(b.p);
}

static void rec_f32_slices(const char *name, const float *const *rows,
                           const int *lens, int n)
{
    Buf b = {0};
    for (int r = 0; r < n; r++) {
        bu32(&b, (uint32_t)lens[r]);
        bput(&b, rows[r], (size_t)lens[r] * 4);
    }
    rec(name, b.p, b.len);
    free(b.p);
}

static void rec_psy_map(const char *name, const PsyLamePreset *v, int n)
{
    Buf b = {0};
    for (int i = 0; i < n; i++) {
        int32_t q = v[i].quality;
        bput(&b, &q, 4);
        bput(&b, &v[i].st_lrm, 4);
    }
    rec(name, b.p, b.len);
    free(b.p);
}

/* ------------------------------- driver ------------------------------- */

static int swb_lens_1024[13], swb_lens_128[13];
static int swb_size_lens_1024[13], swb_size_lens_128[13];
static int spectral_lens[11], vec_lens[11];
static const int tns_tmp2_lens[4] = {8, 16, 4, 8};

static void validate(void)
{
    const uint16_t e = 1;
    if (*(const uint8_t *)&e != 1)
        die("host is not little-endian; fixture format assumes LE");
    for (int i = 0; i < 13; i++) {
        int sum;
        swb_lens_1024[i] = swb_offset_len(ff_swb_offset_1024[i]);
        swb_lens_128[i] = swb_offset_len(ff_swb_offset_128[i]);
        if (swb_lens_1024[i] != ff_aac_num_swb_1024[i] + 1)
            die("swb_offset_1024 length != num_swb+1");
        if (swb_lens_128[i] != ff_aac_num_swb_128[i] + 1)
            die("swb_offset_128 length != num_swb+1");
        if (ff_swb_offset_1024[i][ff_aac_num_swb_1024[i]] != 1024)
            die("swb_offset_1024 sentinel != 1024");
        if (ff_swb_offset_128[i][ff_aac_num_swb_128[i]] != 128)
            die("swb_offset_128 sentinel != 128");
        swb_size_lens_1024[i] = ff_aac_num_swb_1024[i];
        swb_size_lens_128[i] = ff_aac_num_swb_128[i];
        sum = 0;
        for (int g = 0; g < swb_size_lens_1024[i]; g++)
            sum += ff_aac_swb_size_1024[i][g];
        if (sum != 1024)
            die("swb_size_1024 does not sum to 1024");
        sum = 0;
        for (int g = 0; g < swb_size_lens_128[i]; g++)
            sum += ff_aac_swb_size_128[i][g];
        if (sum != 128)
            die("swb_size_128 does not sum to 128");
    }
    for (int i = 0; i < 11; i++) {
        spectral_lens[i] = ff_aac_spectral_sizes[i];
        vec_lens[i] = spectral_lens[i] * spectral_dim[i];
        vector_vals_len[i] = vals_len(ff_aac_codebook_vector_vals[i]);
        vector_idx_len[i] = idx_len(ff_aac_codebook_vector_idx[i]);
        if (vector_vals_len[i] < 0 || vector_idx_len[i] < 0)
            die("unmatched vals/idx pointer");
        if (vector_idx_len[i] != spectral_lens[i])
            die("vector_idx length != codeword count");
    }
    if (vec_lens[0] != 324 || vec_lens[4] != 162 || vec_lens[6] != 128 ||
        vec_lens[8] != 338 || vec_lens[10] != 578)
        die("codebook vector length mismatch vs sizeof ground truth");
}

static void go_mode(void)
{
    printf("// SPDX-License-Identifier: LGPL-2.1-or-later\n\n");
    printf("// Code generated by tools/gentables/gentables.c from FFmpeg @ d09d5afc3a. DO NOT EDIT.\n\n");
    printf("package tables\n\n");

    emit_u32("ScalefactorCode", "ff_aac_scalefactor_code", "aactab.c",
             ff_aac_scalefactor_code, 121, 1);
    emit_u8("ScalefactorBits", "ff_aac_scalefactor_bits", "aactab.c",
            ff_aac_scalefactor_bits, 121, 0);
    emit_u16_slices("SpectralCodes", "ff_aac_spectral_codes", "aactab.c",
                    ff_aac_spectral_codes, spectral_lens, 11, 1);
    {
        hdr("SpectralBits", "ff_aac_spectral_bits", "aactab.c");
        printf("var SpectralBits = [11][]uint8{\n");
        for (int r = 0; r < 11; r++) {
            printf("\t{\n");
            u8_row(ff_aac_spectral_bits[r], spectral_lens[r], 16, "\t\t");
            printf("\t},\n");
        }
        printf("}\n");
    }
    emit_f32_slices("CodebookVectors", "ff_aac_codebook_vectors", "aactab.c",
                    ff_aac_codebook_vectors, vec_lens, 11);
    emit_f32_slices("CodebookVectorVals", "ff_aac_codebook_vector_vals", "aactab.c",
                    ff_aac_codebook_vector_vals, vector_vals_len, 11);
    emit_u16_slices("CodebookVectorIdx", "ff_aac_codebook_vector_idx", "aactab.c",
                    ff_aac_codebook_vector_idx, vector_idx_len, 11, 1);
    emit_u8("NumSwb1024", "ff_aac_num_swb_1024", "aactab.c", ff_aac_num_swb_1024, 13, 0);
    emit_u8("NumSwb128", "ff_aac_num_swb_128", "aactab.c", ff_aac_num_swb_128, 13, 0);
    emit_u16_slices("SwbOffset1024", "ff_swb_offset_1024", "aactab.c",
                    ff_swb_offset_1024, swb_lens_1024, 13, 0);
    emit_u16_slices("SwbOffset128", "ff_swb_offset_128", "aactab.c",
                    ff_swb_offset_128, swb_lens_128, 13, 0);
    emit_u8("TNSMaxBands1024", "ff_tns_max_bands_1024", "aactab.c",
            ff_tns_max_bands_1024, 13, 0);
    emit_u8("TNSMaxBands128", "ff_tns_max_bands_128", "aactab.c",
            ff_tns_max_bands_128, 13, 0);
    emit_f32_slices("TNSTmp2Map", "ff_tns_tmp2_map", "aactab.c",
                    ff_tns_tmp2_map, tns_tmp2_lens, 4);
    emit_u8_slices("SwbSize1024", "ff_aac_swb_size_1024", "aacenctab.c",
                   ff_aac_swb_size_1024, swb_size_lens_1024, 13);
    emit_u8_slices("SwbSize128", "ff_aac_swb_size_128", "aacenctab.c",
                   ff_aac_swb_size_128, swb_size_lens_128, 13);
    emit_u8_2d("ChanConfigs", "aac_chan_configs", "aacenctab.h",
               &aac_chan_configs[0][0], 14, 6);
    emit_u8_2d("ChanMaps", "aac_chan_maps", "aacenctab.h",
               &aac_chan_maps[0][0], 14, 16);
    emit_u8("RunValueBitsLong", "run_value_bits_long", "aacenctab.h",
            run_value_bits_long, 64, 0);
    emit_u8("RunValueBitsShort", "run_value_bits_short", "aacenctab.h",
            run_value_bits_short, 16, 0);
    emit_u8("TNSMinSfbLong", "tns_min_sfb_long", "aacenctab.h",
            tns_min_sfb_long, 16, 0);
    emit_u8("TNSMinSfbShort", "tns_min_sfb_short", "aacenctab.h",
            tns_min_sfb_short, 16, 0);
    emit_u8("CBOutMap", "aac_cb_out_map", "aacenctab.h", aac_cb_out_map, 15, 0);
    emit_u8("CBInMap", "aac_cb_in_map", "aacenctab.h", aac_cb_in_map, 16, 0);
    emit_u8("CBRange", "aac_cb_range", "aacenctab.h", aac_cb_range, 12, 0);
    emit_u8("CBMaxval", "aac_cb_maxval", "aacenctab.h", aac_cb_maxval, 12, 0);
    emit_u8("MaxvalCB", "aac_maxval_cb", "aacenctab.h", aac_maxval_cb, 14, 0);
    emit_psy_map("PsyABRMap", "psy_abr_map", psy_abr_map, 13);
    emit_psy_map("PsyVBRMap", "psy_vbr_map", psy_vbr_map, 11);
    emit_f32("PsyFirCoeffs", "psy_fir_coeffs", "aacpsy.c", psy_fir_coeffs, 10);
    emit_u8("WindowGrouping", "window_grouping", "aacpsy.c", window_grouping, 9, 1);
}

static void bin_mode(void)
{
    /* Fill the runtime tables with the pinned C code itself (aac_tableinit
     * is file-local in aactab.c, visible here through the #include). */
    aac_tableinit();

    rec("ScalefactorCode", ff_aac_scalefactor_code, 121 * 4);
    rec("ScalefactorBits", ff_aac_scalefactor_bits, 121);
    rec_u16_slices("SpectralCodes", ff_aac_spectral_codes, spectral_lens, 11);
    rec_u8_slices("SpectralBits", ff_aac_spectral_bits, spectral_lens, 11);
    rec_f32_slices("CodebookVectors", ff_aac_codebook_vectors, vec_lens, 11);
    rec_f32_slices("CodebookVectorVals", ff_aac_codebook_vector_vals, vector_vals_len, 11);
    rec_u16_slices("CodebookVectorIdx", ff_aac_codebook_vector_idx, vector_idx_len, 11);
    rec("NumSwb1024", ff_aac_num_swb_1024, 13);
    rec("NumSwb128", ff_aac_num_swb_128, 13);
    rec_u16_slices("SwbOffset1024", ff_swb_offset_1024, swb_lens_1024, 13);
    rec_u16_slices("SwbOffset128", ff_swb_offset_128, swb_lens_128, 13);
    rec("TNSMaxBands1024", ff_tns_max_bands_1024, 13);
    rec("TNSMaxBands128", ff_tns_max_bands_128, 13);
    rec_f32_slices("TNSTmp2Map", ff_tns_tmp2_map, tns_tmp2_lens, 4);
    rec_u8_slices("SwbSize1024", ff_aac_swb_size_1024, swb_size_lens_1024, 13);
    rec_u8_slices("SwbSize128", ff_aac_swb_size_128, swb_size_lens_128, 13);
    rec("ChanConfigs", aac_chan_configs, 14 * 6);
    rec("ChanMaps", aac_chan_maps, 14 * 16);
    rec("RunValueBitsLong", run_value_bits_long, 64);
    rec("RunValueBitsShort", run_value_bits_short, 16);
    rec("TNSMinSfbLong", tns_min_sfb_long, 16);
    rec("TNSMinSfbShort", tns_min_sfb_short, 16);
    rec("CBOutMap", aac_cb_out_map, 15);
    rec("CBInMap", aac_cb_in_map, 16);
    rec("CBRange", aac_cb_range, 12);
    rec("CBMaxval", aac_cb_maxval, 12);
    rec("MaxvalCB", aac_maxval_cb, 14);
    rec_psy_map("PsyABRMap", psy_abr_map, 13);
    rec_psy_map("PsyVBRMap", psy_vbr_map, 11);
    rec("PsyFirCoeffs", psy_fir_coeffs, 10 * 4);
    rec("WindowGrouping", window_grouping, 9);
    rec("Pow2SF", ff_aac_pow2sf_tab, 428 * 4);
    rec("Pow34SF", ff_aac_pow34sf_tab, 428 * 4);
}

int main(int argc, char **argv)
{
    validate();
    if (argc == 2 && !strcmp(argv[1], "go"))
        go_mode();
    else if (argc == 2 && !strcmp(argv[1], "bin"))
        bin_mode();
    else
        die("usage: gentables go|bin");
    return 0;
}
