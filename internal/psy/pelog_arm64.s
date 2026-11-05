// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"

// Float64 constants for peLog2NEON in LD1R order: the mantissa mask,
// sqrt(2)/2, the vendored log's L7 down to L1, Ln2Lo, Ln2Hi and 1/Ln2, the
// exponent bias frexp subtracts, then the -Inf, NaN and +Inf the scalar
// special cases return.
DATA peLogConst<>+0(SB)/8, $0x000fffffffffffff
DATA peLogConst<>+8(SB)/8, $0x3fe6a09e667f3bcd
DATA peLogConst<>+16(SB)/8, $0x3fc2f112df3e5244
DATA peLogConst<>+24(SB)/8, $0x3fc39a09d078c69f
DATA peLogConst<>+32(SB)/8, $0x3fc7466496cb03de
DATA peLogConst<>+40(SB)/8, $0x3fcc71c51d8e78af
DATA peLogConst<>+48(SB)/8, $0x3fd2492494229359
DATA peLogConst<>+56(SB)/8, $0x3fd999999997fa04
DATA peLogConst<>+64(SB)/8, $0x3fe5555555555593
DATA peLogConst<>+72(SB)/8, $0x3dea39ef35793c76
DATA peLogConst<>+80(SB)/8, $0x3fe62e42fee00000
DATA peLogConst<>+88(SB)/8, $0x3ff71547652b82fe
DATA peLogConst<>+96(SB)/8, $1022
DATA peLogConst<>+104(SB)/8, $0xfff0000000000000
DATA peLogConst<>+112(SB)/8, $0x7ff8000000000001
DATA peLogConst<>+120(SB)/8, $0x7ff0000000000000
GLOBL peLogConst<>(SB), RODATA|NOPTR, $128

// func peLog2NEON(x, r *[2]float32)
// Lane 0 takes the band energy and lane 1 its threshold. Each lane widens its
// float32 to float64 and follows log2 and log in internal/fmath/portable_log.go,
// in their operation order and with their constants: the frexp split (a widened
// float32 is never subnormal), the exact power-of-two branch, the reduction
// around sqrt(2)/2 and the L1 to L7 polynomial, with separate FMUL and FADD
// (never FMLA), and it narrows the result back to float32. Zero, negative, +Inf
// and NaN lanes take the values the scalar special cases return. The float64
// vector arithmetic, compares and conversions, the CMEQ against zero and the
// vector FMOV immediates are WORD-encoded: the Go 1.26 assembler has no
// mnemonic for them.
TEXT ·peLog2NEON(SB), NOSPLIT, $0-16
	MOVD x+0(FP), R1
	MOVD r+8(FP), R2
	MOVD $peLogConst<>(SB), R0
	FMOVD (R1), F0
	WORD $0x0E617801 // fcvtl v1.2d, v0.2s
	VLD1R.P 8(R0), [V16.D2]
	VLD1R.P 8(R0), [V17.D2]
	VLD1R.P 8(R0), [V18.D2]
	VLD1R.P 8(R0), [V19.D2]
	VLD1R.P 8(R0), [V20.D2]
	VLD1R.P 8(R0), [V21.D2]
	VLD1R.P 8(R0), [V22.D2]
	VLD1R.P 8(R0), [V23.D2]
	VLD1R.P 8(R0), [V24.D2]
	VLD1R.P 8(R0), [V25.D2]
	VLD1R.P 8(R0), [V26.D2]
	VLD1R.P 8(R0), [V27.D2]
	VLD1R.P 8(R0), [V28.D2]
	WORD $0x6F03F41D // fmov v29.2d, #0.5
	WORD $0x6F03F61E // fmov v30.2d, #1.0
	WORD $0x6F00F41F // fmov v31.2d, #2.0
	VUSHR $52, V1.D2, V2.D2
	VSUB V28.D2, V2.D2, V2.D2
	WORD $0x4E61D842 // scvtf v2.2d, v2.2d
	VAND V16.B16, V1.B16, V3.B16
	WORD $0x4EE09864 // cmeq v4.2d, v3.2d, #0
	VORR V29.B16, V3.B16, V3.B16
	WORD $0x6EE3E625 // fcmgt v5.2d, v17.2d, v3.2d
	WORD $0x4E63D466 // fadd v6.2d, v3.2d, v3.2d
	VBIT V5.B16, V6.B16, V3.B16
	WORD $0x6F07F606 // fmov v6.2d, #-1.0
	VAND V6.B16, V5.B16, V5.B16
	WORD $0x4EFED463 // fsub v3.2d, v3.2d, v30.2d
	WORD $0x4E7FD466 // fadd v6.2d, v3.2d, v31.2d
	WORD $0x6E66FC66 // fdiv v6.2d, v3.2d, v6.2d
	WORD $0x6E66DCC7 // fmul v7.2d, v6.2d, v6.2d
	WORD $0x6E67DCF0 // fmul v16.2d, v7.2d, v7.2d
	WORD $0x6E72DE11 // fmul v17.2d, v16.2d, v18.2d
	WORD $0x4E74D631 // fadd v17.2d, v17.2d, v20.2d
	WORD $0x6E71DE11 // fmul v17.2d, v16.2d, v17.2d
	WORD $0x4E76D631 // fadd v17.2d, v17.2d, v22.2d
	WORD $0x6E71DE11 // fmul v17.2d, v16.2d, v17.2d
	WORD $0x4E78D631 // fadd v17.2d, v17.2d, v24.2d
	WORD $0x6E71DCF1 // fmul v17.2d, v7.2d, v17.2d
	WORD $0x6E73DE12 // fmul v18.2d, v16.2d, v19.2d
	WORD $0x4E75D652 // fadd v18.2d, v18.2d, v21.2d
	WORD $0x6E72DE12 // fmul v18.2d, v16.2d, v18.2d
	WORD $0x4E77D652 // fadd v18.2d, v18.2d, v23.2d
	WORD $0x6E72DE12 // fmul v18.2d, v16.2d, v18.2d
	WORD $0x4E72D631 // fadd v17.2d, v17.2d, v18.2d
	WORD $0x6E63DFB2 // fmul v18.2d, v29.2d, v3.2d
	WORD $0x6E63DE52 // fmul v18.2d, v18.2d, v3.2d
	WORD $0x4E71D651 // fadd v17.2d, v18.2d, v17.2d
	WORD $0x6E71DCD1 // fmul v17.2d, v6.2d, v17.2d
	WORD $0x6E79DCB3 // fmul v19.2d, v5.2d, v25.2d
	WORD $0x4E73D631 // fadd v17.2d, v17.2d, v19.2d
	WORD $0x4EF1D651 // fsub v17.2d, v18.2d, v17.2d
	WORD $0x4EE3D631 // fsub v17.2d, v17.2d, v3.2d
	WORD $0x6E7ADCB3 // fmul v19.2d, v5.2d, v26.2d
	WORD $0x4EF1D671 // fsub v17.2d, v19.2d, v17.2d
	WORD $0x6E7BDE31 // fmul v17.2d, v17.2d, v27.2d
	WORD $0x4E62D631 // fadd v17.2d, v17.2d, v2.2d
	WORD $0x4EFED452 // fsub v18.2d, v2.2d, v30.2d
	VBIT V4.B16, V18.B16, V17.B16
	VLD1R.P 8(R0), [V20.D2]
	VLD1R.P 8(R0), [V21.D2]
	VLD1R.P 8(R0), [V22.D2]
	WORD $0x4EE0D837 // fcmeq v23.2d, v1.2d, #0.0
	VBIT V23.B16, V20.B16, V17.B16
	WORD $0x4EE0E837 // fcmlt v23.2d, v1.2d, #0.0
	VBIT V23.B16, V21.B16, V17.B16
	WORD $0x4E76E437 // fcmeq v23.2d, v1.2d, v22.2d
	VBIT V23.B16, V22.B16, V17.B16
	WORD $0x4E61E437 // fcmeq v23.2d, v1.2d, v1.2d
	VBIF V23.B16, V1.B16, V17.B16
	WORD $0x0E616A20 // fcvtn v0.2s, v17.2d
	FMOVD F0, (R2)
	RET
