// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"

// Instructions the Go 1.26 assembler has no mnemonic for (the float32 vector
// arithmetic, compares and conversions, SMAX, SMIN, CLZ, UMAXV and MOVI of
// 32-bit lanes) are WORD-encoded.

// func bandCostNEON(in []float32, q []int32, bits []uint8, vectors []float32, pow43 *[8192]float32, rng, off, flags int, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited bool)
// Pass 1 computes four coefficients per iteration, one per lane: each group's rd*lambda + bits and
// each coefficient's quantized energy term, every product and sum rounded separately as in the scalar
// loop. Pass 2 then sums cost, totBits and qenergy in coefficient order, so every float addition runs
// in the scalar loop's order and the early exit at cost >= uplim fires on the same group.
// The frame holds pass 1's results for a band of at most 96 coefficients: the group costs at
// 8(RSP) and the group bit counts at 200(RSP), 192 bytes each for up to 48 pairs, and the energy
// terms at 392(RSP), 384 bytes for 96 coefficients.
TEXT ·bandCostNEON(SB), 0, $768-169
	MOVD   in_base+0(FP), R0
	MOVD   in_len+8(FP), R7
	LSR    $2, R7, R7
	MOVD   q_base+24(FP), R1
	MOVD   bits_base+48(FP), R2
	MOVD   vectors_base+72(FP), R3
	MOVD   pow43+96(FP), R4
	MOVD   rng+104(FP), R5
	MOVD   off+112(FP), R6
	MOVD   flags+120(FP), R22
	FMOVS  iq+128(FP), F2
	FMOVS  qs+132(FP), F3
	FMOVS  rounding+136(FP), F4
	FMOVS  clipEsc+140(FP), F5
	FMOVS  lambda+144(FP), F6
	MOVB   ZR, exited+168(FP)
	VDUP   V2.S[0], V2.S4
	VDUP   V3.S[0], V3.S4
	VDUP   V4.S[0], V4.S4
	VDUP   V5.S[0], V5.S4
	VDUP   V6.S[0], V6.S4
	MOVW   $0x42800000, R10
	VDUP   R10, V9.S4
	MOVW   $8191, R10
	VDUP   R10, V16.S4
	WORD $0x4F000431 // movi v17.4s, #1
	WORD $0x4F010772 // movi v18.4s, #59
	WORD $0x4F0006B3 // movi v19.4s, #21
	WORD $0x4F000414 // movi v20.4s, #0
	ADD    $8, RSP, R8
	ADD    $200, RSP, R19
	ADD    $392, RSP, R9
	CMP    $7, R22
	BEQ    pue
	CMP    $3, R22
	BEQ    pu
	CMP    $1, R22
	BEQ    ps
	CMP    $2, R22
	BEQ    qu
	B      qs

pue:
	LDPW   (R1), (R10, R11)
	LDPW   8(R1), (R12, R13)
	ADD    $16, R1, R1
	MADDW  R5, R11, R10, R10
	MADDW  R5, R13, R12, R12
	ADD    R10<<3, R3, R20
	ADD    R12<<3, R3, R21
	VLD1   (R20), V10.D[0]
	VLD1   (R21), V10.D[1]
	MOVBU  (R2)(R10), R10
	MOVBU  (R2)(R12), R12
	VLD1.P 16(R0), [V11.S4]
	WORD $0x4EA0F96B // fabs v11.4s, v11.4s
	WORD $0x4EA0D94D // fcmeq v13.4s, v10.4s, #0.0
	VADD   V17.S4, V13.S4, V13.S4
	WORD $0x6E22DD4C // fmul v12.4s, v10.4s, v2.4s
	WORD $0x4E29E54E // fcmeq v14.4s, v10.4s, v9.4s
	WORD $0x6EB0A9CF // umaxv s15, v14.4s
	FMOVS  F15, R11
	CBZW   R11, pue_plain
	WORD $0x6E23DD6F // fmul v15.4s, v11.4s, v3.4s
	WORD $0x6EA1F9F5 // fsqrt v21.4s, v15.4s
	WORD $0x6E35DDF5 // fmul v21.4s, v15.4s, v21.4s
	WORD $0x6EA1FAB5 // fsqrt v21.4s, v21.4s
	WORD $0x4E24D6B5 // fadd v21.4s, v21.4s, v4.4s
	WORD $0x4EA1BAB5 // fcvtzs v21.4s, v21.4s
	WORD $0x4EB466B5 // smax v21.4s, v21.4s, v20.4s
	WORD $0x4EB06EB5 // smin v21.4s, v21.4s, v16.4s
	VMOV   V21.S[0], R11
	FMOVS  (R4)(R11<<2), F22
	VMOV   V21.S[1], R11
	ADD    R11<<2, R4, R20
	VLD1   (R20), V22.S[1]
	VMOV   V21.S[2], R11
	ADD    R11<<2, R4, R20
	VLD1   (R20), V22.S[2]
	VMOV   V21.S[3], R11
	ADD    R11<<2, R4, R20
	VLD1   (R20), V22.S[3]
	WORD $0x6E22DED6 // fmul v22.4s, v22.4s, v2.4s
	WORD $0x4EB166B7 // smax v23.4s, v21.4s, v17.4s
	WORD $0x6EA04AF7 // clz v23.4s, v23.4s
	VSHL   $1, V23.S4, V23.S4
	VSUB   V23.S4, V18.S4, V23.S4
	WORD $0x6E25E578 // fcmge v24.4s, v11.4s, v5.4s
	VBIT   V24.B16, V5.B16, V22.B16
	VBIT   V24.B16, V19.B16, V23.B16
	VBIT   V14.B16, V22.B16, V12.B16
	VAND   V14.B16, V23.B16, V23.B16
	VADD   V23.S4, V13.S4, V13.S4
pue_plain:
	WORD $0x4EACD56B // fsub v11.4s, v11.4s, v12.4s
	WORD $0x6E2CDD8C // fmul v12.4s, v12.4s, v12.4s
	WORD $0x6E2BDD6B // fmul v11.4s, v11.4s, v11.4s
	VST1.P [V12.S4], 16(R9)
	WORD $0x6E2BD56B // faddp v11.4s, v11.4s, v11.4s
	FMOVS  R10, F25
	VMOV   R12, V25.S[1]
	VADDP  V13.S4, V13.S4, V13.S4
	VADD   V25.S2, V13.S2, V13.S2
	WORD $0x2E26DD6B // fmul v11.2s, v11.2s, v6.2s
	WORD $0x0E21D9AE // scvtf v14.2s, v13.2s
	WORD $0x0E2ED56B // fadd v11.2s, v11.2s, v14.2s
	VST1.P [V11.S2], 8(R8)
	VST1.P [V13.S2], 8(R19)
	SUBS   $1, R7, R7
	BNE    pue
	B      sum

pu:
	LDPW   (R1), (R10, R11)
	LDPW   8(R1), (R12, R13)
	ADD    $16, R1, R1
	MADDW  R5, R11, R10, R10
	MADDW  R5, R13, R12, R12
	ADD    R10<<3, R3, R20
	ADD    R12<<3, R3, R21
	VLD1   (R20), V10.D[0]
	VLD1   (R21), V10.D[1]
	MOVBU  (R2)(R10), R10
	MOVBU  (R2)(R12), R12
	VLD1.P 16(R0), [V11.S4]
	WORD $0x4EA0F96B // fabs v11.4s, v11.4s
	WORD $0x4EA0D94D // fcmeq v13.4s, v10.4s, #0.0
	VADD   V17.S4, V13.S4, V13.S4
	WORD $0x6E22DD4C // fmul v12.4s, v10.4s, v2.4s
	WORD $0x4EACD56B // fsub v11.4s, v11.4s, v12.4s
	WORD $0x6E2CDD8C // fmul v12.4s, v12.4s, v12.4s
	WORD $0x6E2BDD6B // fmul v11.4s, v11.4s, v11.4s
	VST1.P [V12.S4], 16(R9)
	WORD $0x6E2BD56B // faddp v11.4s, v11.4s, v11.4s
	FMOVS  R10, F25
	VMOV   R12, V25.S[1]
	VADDP  V13.S4, V13.S4, V13.S4
	VADD   V25.S2, V13.S2, V13.S2
	WORD $0x2E26DD6B // fmul v11.2s, v11.2s, v6.2s
	WORD $0x0E21D9AE // scvtf v14.2s, v13.2s
	WORD $0x0E2ED56B // fadd v11.2s, v11.2s, v14.2s
	VST1.P [V11.S2], 8(R8)
	VST1.P [V13.S2], 8(R19)
	SUBS   $1, R7, R7
	BNE    pu
	B      sum

ps:
	LDPW   (R1), (R10, R11)
	LDPW   8(R1), (R12, R13)
	ADD    $16, R1, R1
	ADDW   R6, R10, R10
	ADDW   R6, R11, R11
	ADDW   R6, R12, R12
	ADDW   R6, R13, R13
	MADDW  R5, R11, R10, R10
	MADDW  R5, R13, R12, R12
	ADD    R10<<3, R3, R20
	ADD    R12<<3, R3, R21
	VLD1   (R20), V10.D[0]
	VLD1   (R21), V10.D[1]
	MOVBU  (R2)(R10), R10
	MOVBU  (R2)(R12), R12
	VLD1.P 16(R0), [V11.S4]
	WORD $0x6E22DD4C // fmul v12.4s, v10.4s, v2.4s
	WORD $0x4EACD56B // fsub v11.4s, v11.4s, v12.4s
	WORD $0x6E2CDD8C // fmul v12.4s, v12.4s, v12.4s
	WORD $0x6E2BDD6B // fmul v11.4s, v11.4s, v11.4s
	VST1.P [V12.S4], 16(R9)
	WORD $0x6E2BD56B // faddp v11.4s, v11.4s, v11.4s
	FMOVS  R10, F25
	VMOV   R12, V25.S[1]
	VMOV   V25.B8, V13.B8
	WORD $0x2E26DD6B // fmul v11.2s, v11.2s, v6.2s
	WORD $0x0E21D9AE // scvtf v14.2s, v13.2s
	WORD $0x0E2ED56B // fadd v11.2s, v11.2s, v14.2s
	VST1.P [V11.S2], 8(R8)
	VST1.P [V13.S2], 8(R19)
	SUBS   $1, R7, R7
	BNE    ps
	B      sum

qu:
	LDPW   (R1), (R10, R11)
	LDPW   8(R1), (R12, R13)
	ADD    $16, R1, R1
	MADDW  R5, R11, R10, R10
	MADDW  R5, R12, R10, R10
	MADDW  R5, R13, R10, R10
	ADD    R10<<4, R3, R20
	VLD1   (R20), [V10.S4]
	MOVBU  (R2)(R10), R10
	VLD1.P 16(R0), [V11.S4]
	WORD $0x4EA0F96B // fabs v11.4s, v11.4s
	WORD $0x4EA0D94D // fcmeq v13.4s, v10.4s, #0.0
	VADD   V17.S4, V13.S4, V13.S4
	WORD $0x6E22DD4C // fmul v12.4s, v10.4s, v2.4s
	WORD $0x4EACD56B // fsub v11.4s, v11.4s, v12.4s
	WORD $0x6E2CDD8C // fmul v12.4s, v12.4s, v12.4s
	WORD $0x6E2BDD6B // fmul v11.4s, v11.4s, v11.4s
	VST1.P [V12.S4], 16(R9)
	WORD $0x7E30D96E // faddp s14, v11.2s
	VMOV   V11.S[2], V15
	FADDS  F15, F14, F14
	VMOV   V11.S[3], V15
	FADDS  F15, F14, F14
	FMULS  F6, F14, F14
	VADDV  V13.S4, V13
	FMOVS  F13, R11
	ADDW   R11, R10, R10
	SCVTFWS R10, F15
	FADDS  F15, F14, F14
	FMOVS.P F14, 4(R8)
	MOVW.P R10, 4(R19)
	SUBS   $1, R7, R7
	BNE    qu
	B      sum

qs:
	LDPW   (R1), (R10, R11)
	LDPW   8(R1), (R12, R13)
	ADD    $16, R1, R1
	ADDW   R6, R10, R10
	ADDW   R6, R11, R11
	ADDW   R6, R12, R12
	ADDW   R6, R13, R13
	MADDW  R5, R11, R10, R10
	MADDW  R5, R12, R10, R10
	MADDW  R5, R13, R10, R10
	ADD    R10<<4, R3, R20
	VLD1   (R20), [V10.S4]
	MOVBU  (R2)(R10), R10
	VLD1.P 16(R0), [V11.S4]
	WORD $0x6E22DD4C // fmul v12.4s, v10.4s, v2.4s
	WORD $0x4EACD56B // fsub v11.4s, v11.4s, v12.4s
	WORD $0x6E2CDD8C // fmul v12.4s, v12.4s, v12.4s
	WORD $0x6E2BDD6B // fmul v11.4s, v11.4s, v11.4s
	VST1.P [V12.S4], 16(R9)
	WORD $0x7E30D96E // faddp s14, v11.2s
	VMOV   V11.S[2], V15
	FADDS  F15, F14, F14
	VMOV   V11.S[3], V15
	FADDS  F15, F14, F14
	FMULS  F6, F14, F14
	SCVTFWS R10, F15
	FADDS  F15, F14, F14
	FMOVS.P F14, 4(R8)
	MOVW.P R10, 4(R19)
	SUBS   $1, R7, R7
	BNE    qs
	B      sum

sum:
	FMOVS  uplim+148(FP), F7
	FMOVS  ZR, F0
	FMOVS  ZR, F1
	MOVD   ZR, R13
	ADD    $8, RSP, R8
	ADD    $200, RSP, R19
	ADD    $392, RSP, R9
	MOVD   in_len+8(FP), R7
	TBZ    $0, R22, sumquad
	LSR    $1, R7, R7
sumpair:
	FMOVS.P 4(R8), F14
	FADDS  F14, F0, F0
	MOVW.P 4(R19), R14
	ADD    R14, R13, R13
	FCMPS  F7, F0
	BGE    exit
	FMOVS.P 4(R9), F15
	FADDS  F15, F1, F1
	FMOVS.P 4(R9), F15
	FADDS  F15, F1, F1
	SUBS   $1, R7, R7
	BNE    sumpair
	B      finish

sumquad:
	LSR    $2, R7, R7
sumquadloop:
	FMOVS.P 4(R8), F14
	FADDS  F14, F0, F0
	MOVW.P 4(R19), R14
	ADD    R14, R13, R13
	FCMPS  F7, F0
	BGE    exit
	FMOVS.P 4(R9), F15
	FADDS  F15, F1, F1
	FMOVS.P 4(R9), F15
	FADDS  F15, F1, F1
	FMOVS.P 4(R9), F15
	FADDS  F15, F1, F1
	FMOVS.P 4(R9), F15
	FADDS  F15, F1, F1
	SUBS   $1, R7, R7
	BNE    sumquadloop

finish:
	FMOVS  F0, cost+152(FP)
	FMOVS  F1, qenergy+156(FP)
	MOVD   R13, totBits+160(FP)
	RET

exit:
	MOVD   $1, R10
	MOVB   R10, exited+168(FP)
	B      finish
