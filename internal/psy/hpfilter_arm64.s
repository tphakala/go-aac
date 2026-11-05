// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"

// func hpFilterNEON(firbuf *float32, coeffs *[10]float32, out *[1024]float32)
// The same lane scheme as hpFilterAVX2 with four lanes, separate FMUL and FADD
// (never FMLA). The float32 vector FADD and FMUL and the MOVI that clears sum2
// are WORD-encoded: the Go 1.26 assembler has no mnemonic for them.
TEXT ·hpFilterNEON(SB), NOSPLIT, $0-24
	MOVD firbuf+0(FP), R0
	MOVD coeffs+8(FP), R1
	MOVD out+16(FP), R2
	VLD1 (R1), [V26.S4, V27.S4]
	FMOVD 32(R1), F28
	VDUP V26.S[0], V16.S4
	VDUP V26.S[1], V17.S4
	VDUP V26.S[2], V18.S4
	VDUP V26.S[3], V19.S4
	VDUP V27.S[0], V20.S4
	VDUP V27.S[1], V21.S4
	VDUP V27.S[2], V22.S4
	VDUP V27.S[3], V23.S4
	VDUP V28.S[0], V24.S4
	VDUP V28.S[1], V25.S4
	MOVW $0x47000000, R4
	VDUP R4, V4.S4
	MOVD $256, R5

loop:
	FMOVQ 40(R0), F0
	WORD $0x6F00E401 // movi v1.2d, #0
	FMOVQ (R0), F2
	FMOVQ 84(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E30DC42 // fmul v2.4s, v2.4s, v16.4s
	WORD $0x4E22D400 // fadd v0.4s, v0.4s, v2.4s
	FMOVQ 4(R0), F2
	FMOVQ 80(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E31DC42 // fmul v2.4s, v2.4s, v17.4s
	WORD $0x4E22D421 // fadd v1.4s, v1.4s, v2.4s
	FMOVQ 8(R0), F2
	FMOVQ 76(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E32DC42 // fmul v2.4s, v2.4s, v18.4s
	WORD $0x4E22D400 // fadd v0.4s, v0.4s, v2.4s
	FMOVQ 12(R0), F2
	FMOVQ 72(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E33DC42 // fmul v2.4s, v2.4s, v19.4s
	WORD $0x4E22D421 // fadd v1.4s, v1.4s, v2.4s
	FMOVQ 16(R0), F2
	FMOVQ 68(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E34DC42 // fmul v2.4s, v2.4s, v20.4s
	WORD $0x4E22D400 // fadd v0.4s, v0.4s, v2.4s
	FMOVQ 20(R0), F2
	FMOVQ 64(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E35DC42 // fmul v2.4s, v2.4s, v21.4s
	WORD $0x4E22D421 // fadd v1.4s, v1.4s, v2.4s
	FMOVQ 24(R0), F2
	FMOVQ 60(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E36DC42 // fmul v2.4s, v2.4s, v22.4s
	WORD $0x4E22D400 // fadd v0.4s, v0.4s, v2.4s
	FMOVQ 28(R0), F2
	FMOVQ 56(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E37DC42 // fmul v2.4s, v2.4s, v23.4s
	WORD $0x4E22D421 // fadd v1.4s, v1.4s, v2.4s
	FMOVQ 32(R0), F2
	FMOVQ 52(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E38DC42 // fmul v2.4s, v2.4s, v24.4s
	WORD $0x4E22D400 // fadd v0.4s, v0.4s, v2.4s
	FMOVQ 36(R0), F2
	FMOVQ 48(R0), F3
	WORD $0x4E23D442 // fadd v2.4s, v2.4s, v3.4s
	WORD $0x6E39DC42 // fmul v2.4s, v2.4s, v25.4s
	WORD $0x4E22D421 // fadd v1.4s, v1.4s, v2.4s
	WORD $0x4E21D400 // fadd v0.4s, v0.4s, v1.4s
	WORD $0x6E24DC00 // fmul v0.4s, v0.4s, v4.4s
	VST1.P [V0.S4], 16(R2)
	ADD  $16, R0
	SUB  $1, R5
	CBNZ R5, loop
	RET
