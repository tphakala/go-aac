// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"

// nmrTrellisConst holds MaxFloat32, the lane indices 0..7, the VPSHUFB mask that
// gathers the low byte of each dword to the bottom of its 128-bit half, and the
// VPERMD indices that join the two halves' four bytes.
DATA nmrTrellisConst<>+0x00(SB)/4, $0x7f7fffff
DATA nmrTrellisConst<>+0x20(SB)/4, $0
DATA nmrTrellisConst<>+0x24(SB)/4, $1
DATA nmrTrellisConst<>+0x28(SB)/4, $2
DATA nmrTrellisConst<>+0x2c(SB)/4, $3
DATA nmrTrellisConst<>+0x30(SB)/4, $4
DATA nmrTrellisConst<>+0x34(SB)/4, $5
DATA nmrTrellisConst<>+0x38(SB)/4, $6
DATA nmrTrellisConst<>+0x3c(SB)/4, $7
DATA nmrTrellisConst<>+0x40(SB)/8, $0x808080800c080400
DATA nmrTrellisConst<>+0x48(SB)/8, $0x8080808080808080
DATA nmrTrellisConst<>+0x50(SB)/8, $0x808080800c080400
DATA nmrTrellisConst<>+0x58(SB)/8, $0x8080808080808080
DATA nmrTrellisConst<>+0x60(SB)/4, $0
DATA nmrTrellisConst<>+0x64(SB)/4, $4
DATA nmrTrellisConst<>+0x68(SB)/4, $0
DATA nmrTrellisConst<>+0x6c(SB)/4, $0
DATA nmrTrellisConst<>+0x70(SB)/4, $0
DATA nmrTrellisConst<>+0x74(SB)/4, $0
DATA nmrTrellisConst<>+0x78(SB)/4, $0
DATA nmrTrellisConst<>+0x7c(SB)/4, $0
GLOBL nmrTrellisConst<>(SB), RODATA|NOPTR, $0x80

// func nmrNodeCostAVX2(dst *float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, n int, lam float32)
TEXT ·nmrNodeCostAVX2(SB), NOSPLIT, $0-36
	MOVQ         dst+0(FP), DI
	MOVQ         nd+8(FP), SI
	MOVQ         nb+16(FP), DX
	MOVQ         n+24(FP), R8
	VBROADCASTSS lam+32(FP), Y15
	SUBQ         $8, R8
	SHLQ         $2, R8
	XORQ         AX, AX

nodeLoop:
	VCVTDQ2PS (DX)(AX*1), Y0
	VMULPS    Y15, Y0, Y0
	VADDPS    (SI)(AX*1), Y0, Y0
	VMOVUPS   Y0, (DI)(AX*1)
	CMPQ      AX, R8
	JEQ       nodeDone
	ADDQ      $32, AX
	CMPQ      AX, R8
	JLE       nodeLoop
	MOVQ      R8, AX
	JMP       nodeLoop

nodeDone:
	VZEROUPPER
	RET

// func nmrTrellisReconcileAVX2(dp *float32, bp *uint8, node, vals *float32, idxs *int32, n, dHi int)
// Y1 = (vals < MaxFloat32), Y2 = dp, Y4 = the back-pointers o - dHi + idxs.
// The GPR to XMM moves are VMOVQ: a legacy-SSE MOVQ inside this 256-bit loop
// costs an SSE/AVX transition per iteration on Zen 3 and Intel Granite Rapids.
TEXT ·nmrTrellisReconcileAVX2(SB), NOSPLIT, $0-56
	MOVQ         dp+0(FP), DI
	MOVQ         bp+8(FP), R9
	MOVQ         node+16(FP), SI
	MOVQ         vals+24(FP), R10
	MOVQ         idxs+32(FP), R11
	MOVQ         n+40(FP), R8
	MOVQ         dHi+48(FP), BX
	NEGQ         BX
	VMOVQ        BX, X12
	VPBROADCASTD X12, Y12
	VBROADCASTSS nmrTrellisConst<>+0x00(SB), Y14
	VMOVDQU      nmrTrellisConst<>+0x20(SB), Y13
	VMOVDQU      nmrTrellisConst<>+0x40(SB), Y11
	VMOVDQU      nmrTrellisConst<>+0x60(SB), Y10
	SUBQ         $8, R8
	XORQ         AX, AX

reconcileLoop:
	VMOVUPS      (R10)(AX*4), Y0
	VCMPPS       $0x11, Y14, Y0, Y1
	VADDPS       (SI)(AX*4), Y0, Y2
	VBLENDVPS    Y1, Y2, Y14, Y2
	VMOVUPS      Y2, (DI)(AX*4)
	VMOVQ        AX, X4
	VPBROADCASTD X4, Y4
	VPADDD       Y13, Y4, Y4
	VPADDD       (R11)(AX*4), Y4, Y4
	VPADDD       Y12, Y4, Y4
	VPAND        Y1, Y4, Y4
	VPSHUFB      Y11, Y4, Y4
	VPERMD       Y4, Y10, Y4
	VMOVQ        X4, (R9)(AX*1)
	CMPQ         AX, R8
	JEQ          reconcileDone
	ADDQ         $8, AX
	CMPQ         AX, R8
	JLE          reconcileLoop
	MOVQ         R8, AX
	JMP          reconcileLoop

reconcileDone:
	VZEROUPPER
	RET
