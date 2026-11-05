// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"

// hpScale is 32768.0, the output scaling of psy_hp_filter.
DATA hpScale<>+0(SB)/4, $0x47000000
GLOBL hpScale<>(SB), RODATA|NOPTR, $4

// func hpFilterAVX2(firbuf *float32, coeffs *[10]float32, out *[1024]float32)
// Each lane computes one output sample i in psyHpFilterScalar's operation order:
// sum1 starts at firbuf[i+10] and adds coeffs[j]*(firbuf[i+j]+firbuf[i+21-j])
// for j = 0, 2, 4, 6, 8, sum2 starts at 0 and adds
// coeffs[j+1]*(firbuf[i+j+1]+firbuf[i+20-j]), and the output is
// (sum1+sum2)*32768. Every product and sum is rounded separately (no FMA).
TEXT ·hpFilterAVX2(SB), NOSPLIT, $0-24
	MOVQ firbuf+0(FP), SI
	MOVQ coeffs+8(FP), AX
	MOVQ out+16(FP), DI
	VBROADCASTSS 0(AX), Y5
	VBROADCASTSS 4(AX), Y6
	VBROADCASTSS 8(AX), Y7
	VBROADCASTSS 12(AX), Y8
	VBROADCASTSS 16(AX), Y9
	VBROADCASTSS 20(AX), Y10
	VBROADCASTSS 24(AX), Y11
	VBROADCASTSS 28(AX), Y12
	VBROADCASTSS 32(AX), Y13
	VBROADCASTSS 36(AX), Y14
	VBROADCASTSS hpScale<>(SB), Y4
	MOVQ $128, CX

loop:
	VMOVUPS 40(SI), Y0
	VXORPS  Y1, Y1, Y1
	VMOVUPS 0(SI), Y2
	VADDPS  84(SI), Y2, Y2
	VMULPS  Y5, Y2, Y2
	VADDPS  Y2, Y0, Y0
	VMOVUPS 4(SI), Y3
	VADDPS  80(SI), Y3, Y3
	VMULPS  Y6, Y3, Y3
	VADDPS  Y3, Y1, Y1
	VMOVUPS 8(SI), Y2
	VADDPS  76(SI), Y2, Y2
	VMULPS  Y7, Y2, Y2
	VADDPS  Y2, Y0, Y0
	VMOVUPS 12(SI), Y3
	VADDPS  72(SI), Y3, Y3
	VMULPS  Y8, Y3, Y3
	VADDPS  Y3, Y1, Y1
	VMOVUPS 16(SI), Y2
	VADDPS  68(SI), Y2, Y2
	VMULPS  Y9, Y2, Y2
	VADDPS  Y2, Y0, Y0
	VMOVUPS 20(SI), Y3
	VADDPS  64(SI), Y3, Y3
	VMULPS  Y10, Y3, Y3
	VADDPS  Y3, Y1, Y1
	VMOVUPS 24(SI), Y2
	VADDPS  60(SI), Y2, Y2
	VMULPS  Y11, Y2, Y2
	VADDPS  Y2, Y0, Y0
	VMOVUPS 28(SI), Y3
	VADDPS  56(SI), Y3, Y3
	VMULPS  Y12, Y3, Y3
	VADDPS  Y3, Y1, Y1
	VMOVUPS 32(SI), Y2
	VADDPS  52(SI), Y2, Y2
	VMULPS  Y13, Y2, Y2
	VADDPS  Y2, Y0, Y0
	VMOVUPS 36(SI), Y3
	VADDPS  48(SI), Y3, Y3
	VMULPS  Y14, Y3, Y3
	VADDPS  Y3, Y1, Y1
	VADDPS  Y1, Y0, Y0
	VMULPS  Y4, Y0, Y0
	VMOVUPS Y0, (DI)
	ADDQ    $32, SI
	ADDQ    $32, DI
	DECQ    CX
	JNZ     loop
	VZEROUPPER
	RET
