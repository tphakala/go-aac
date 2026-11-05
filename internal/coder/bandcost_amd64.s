// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"
#include "go_asm.h"

// func bandCostAsm(in []float32, q []int32, bits []uint8, vectors []float32, pow43 *[8192]float32, rng, off, flags int, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited bool)
// Pass 1 computes four coefficients per iteration, one per lane: each group's rd*lambda + bits and
// each coefficient's quantized energy term, every product and sum rounded separately as in the scalar
// loop. Pass 2 then sums cost, totBits and qenergy in coefficient order, so every float addition runs
// in the scalar loop's order and the early exit at cost >= uplim fires on the same group.
// The frame holds pass 1's results for a band of at most 96 coefficients: the group costs at
// 0(SP) and the group bit counts at 192(SP), 192 bytes each for up to 48 pairs, and the energy
// terms at 384(SP), 384 bytes for 96 coefficients. The escape path reads its constant vectors
// (8191, 127, 257, 21) from bandcostEsc and keeps the per-lane sign bits in X7.
// Requires AVX2 and POPCNT.
TEXT ·bandCostAsm(SB), 0, $768-169
	MOVQ    in_base+0(FP), SI
	MOVQ    in_len+8(FP), CX
	SHRQ    $2, CX
	MOVQ    q_base+24(FP), DI
	MOVQ    bits_base+48(FP), R8
	MOVQ    vectors_base+72(FP), R9
	MOVQ    pow43+96(FP), R10
	MOVQ    rng+104(FP), R11
	MOVQ    off+112(FP), R12
	VBROADCASTSS iq+128(FP), X2
	VBROADCASTSS qs+132(FP), X3
	VBROADCASTSS rounding+136(FP), X4
	VBROADCASTSS clipEsc+140(FP), X5
	VBROADCASTSS lambda+144(FP), X6
	VPCMPEQD X8, X8, X8
	VPSRLD  $1, X8, X8
	MOVL    $0x42800000, AX
	VMOVD   AX, X9
	VBROADCASTSS X9, X9
	XORQ    R14, R14
	XORQ    R15, R15
	MOVB    $0, exited+168(FP)
	MOVQ    flags+120(FP), AX
	CMPQ    AX, $(const_bandCostPair|const_bandCostUnsigned|const_bandCostEscape)
	JEQ     pue
	CMPQ    AX, $(const_bandCostPair|const_bandCostUnsigned)
	JEQ     pu
	CMPQ    AX, $const_bandCostPair
	JEQ     ps
	CMPQ    AX, $const_bandCostUnsigned
	JEQ     qu
	JMP     qsigned

pue:
	MOVLQSX 0(DI), AX
	IMULQ   R11, AX
	MOVLQSX 4(DI), BX
	ADDQ    BX, AX
	MOVLQSX 8(DI), DX
	IMULQ   R11, DX
	MOVLQSX 12(DI), BX
	ADDQ    BX, DX
	VMOVQ   (R9)(AX*8), X10
	VPINSRQ $1, (R9)(DX*8), X10, X10
	MOVBQZX (R8)(AX*1), AX
	MOVBQZX (R8)(DX*1), DX
	VMOVUPS (SI), X11
	VANDPS  X8, X11, X11
	VXORPS  X15, X15, X15
	VCMPPS  $4, X15, X10, X13
	VPSUBD  X13, X15, X13
	VMULPS  X2, X10, X12
	VCMPPS  $0, X9, X10, X14
	VMOVMSKPS X14, BX
	TESTQ   BX, BX
	JZ      pue_plain
	VMOVDQU X13, X7
	VMULPS  X3, X11, X15
	VSQRTPS X15, X10
	VMULPS  X10, X15, X15
	VSQRTPS X15, X15
	VADDPS  X4, X15, X15
	VCVTTPS2DQ X15, X15
	VPXOR   X10, X10, X10
	VPMAXSD X10, X15, X15
	VPMINSD bandcostEsc<>+0(SB), X15, X15
	VPCMPEQD X10, X10, X10
	VGATHERDPS X10, (R10)(X15*4), X13
	VMULPS  X2, X13, X13
	VCVTDQ2PS X15, X10
	VPSRLD  $23, X10, X10
	VPMAXSD bandcostEsc<>+16(SB), X10, X10
	VPSLLD  $1, X10, X10
	VPSUBD  bandcostEsc<>+32(SB), X10, X10
	VCMPPS  $13, X5, X11, X15
	VBLENDVPS X15, bandcostEsc<>+48(SB), X10, X10
	VBLENDVPS X15, X5, X13, X13
	VBLENDVPS X14, X13, X12, X12
	VPAND   X14, X10, X10
	VPADDD  X7, X10, X13
pue_plain:
	VSUBPS  X12, X11, X11
	VMULPS  X12, X12, X12
	VMULPS  X11, X11, X11
	VMOVUPS X12, 384(SP)(R15*1)
	VHADDPS X11, X11, X11
	VPHADDD X13, X13, X13
	VPINSRD $0, AX, X14, X14
	VPINSRD $1, DX, X14, X14
	VPADDD  X14, X13, X13
	VMULPS  X6, X11, X11
	VCVTDQ2PS X13, X14
	VADDPS  X14, X11, X11
	VMOVQ   X11, 0(SP)(R14*1)
	VMOVQ   X13, 192(SP)(R14*1)
	ADDQ    $8, R14
	ADDQ    $16, R15
	ADDQ    $16, SI
	ADDQ    $16, DI
	DECQ    CX
	JNZ     pue
	JMP     sum

pu:
	MOVLQSX 0(DI), AX
	IMULQ   R11, AX
	MOVLQSX 4(DI), BX
	ADDQ    BX, AX
	MOVLQSX 8(DI), DX
	IMULQ   R11, DX
	MOVLQSX 12(DI), BX
	ADDQ    BX, DX
	VMOVQ   (R9)(AX*8), X10
	VPINSRQ $1, (R9)(DX*8), X10, X10
	MOVBQZX (R8)(AX*1), AX
	MOVBQZX (R8)(DX*1), DX
	VMOVUPS (SI), X11
	VANDPS  X8, X11, X11
	VXORPS  X15, X15, X15
	VCMPPS  $4, X15, X10, X13
	VPSUBD  X13, X15, X13
	VMULPS  X2, X10, X12
	VSUBPS  X12, X11, X11
	VMULPS  X12, X12, X12
	VMULPS  X11, X11, X11
	VMOVUPS X12, 384(SP)(R15*1)
	VHADDPS X11, X11, X11
	VPHADDD X13, X13, X13
	VPINSRD $0, AX, X14, X14
	VPINSRD $1, DX, X14, X14
	VPADDD  X14, X13, X13
	VMULPS  X6, X11, X11
	VCVTDQ2PS X13, X14
	VADDPS  X14, X11, X11
	VMOVQ   X11, 0(SP)(R14*1)
	VMOVQ   X13, 192(SP)(R14*1)
	ADDQ    $8, R14
	ADDQ    $16, R15
	ADDQ    $16, SI
	ADDQ    $16, DI
	DECQ    CX
	JNZ     pu
	JMP     sum

ps:
	MOVLQSX 0(DI), AX
	ADDQ    R12, AX
	IMULQ   R11, AX
	MOVLQSX 4(DI), BX
	ADDQ    R12, BX
	ADDQ    BX, AX
	MOVLQSX 8(DI), DX
	ADDQ    R12, DX
	IMULQ   R11, DX
	MOVLQSX 12(DI), BX
	ADDQ    R12, BX
	ADDQ    BX, DX
	VMOVQ   (R9)(AX*8), X10
	VPINSRQ $1, (R9)(DX*8), X10, X10
	MOVBQZX (R8)(AX*1), AX
	MOVBQZX (R8)(DX*1), DX
	VMOVUPS (SI), X11
	VMULPS  X2, X10, X12
	VSUBPS  X12, X11, X11
	VMULPS  X12, X12, X12
	VMULPS  X11, X11, X11
	VMOVUPS X12, 384(SP)(R15*1)
	VHADDPS X11, X11, X11
	VPXOR   X13, X13, X13
	VPINSRD $0, AX, X14, X14
	VPINSRD $1, DX, X14, X14
	VPADDD  X14, X13, X13
	VMULPS  X6, X11, X11
	VCVTDQ2PS X13, X14
	VADDPS  X14, X11, X11
	VMOVQ   X11, 0(SP)(R14*1)
	VMOVQ   X13, 192(SP)(R14*1)
	ADDQ    $8, R14
	ADDQ    $16, R15
	ADDQ    $16, SI
	ADDQ    $16, DI
	DECQ    CX
	JNZ     ps
	JMP     sum

qu:
	MOVLQSX 0(DI), AX
	IMULQ   R11, AX
	MOVLQSX 4(DI), BX
	ADDQ    BX, AX
	IMULQ   R11, AX
	MOVLQSX 8(DI), BX
	ADDQ    BX, AX
	IMULQ   R11, AX
	MOVLQSX 12(DI), BX
	ADDQ    BX, AX
	MOVQ    AX, BX
	SHLQ    $4, BX
	VMOVUPS (R9)(BX*1), X10
	MOVBQZX (R8)(AX*1), AX
	VMOVUPS (SI), X11
	VANDPS  X8, X11, X11
	VXORPS  X15, X15, X15
	VCMPPS  $4, X15, X10, X13
	VMOVMSKPS X13, DX
	POPCNTL DX, DX
	ADDQ    DX, AX
	VMULPS  X2, X10, X12
	VSUBPS  X12, X11, X11
	VMULPS  X12, X12, X12
	VMULPS  X11, X11, X11
	VMOVUPS X12, 384(SP)(R15*1)
	VMOVSHDUP X11, X14
	VADDSS  X14, X11, X14
	VMOVHLPS X11, X11, X15
	VADDSS  X15, X14, X14
	VSHUFPS $3, X11, X11, X15
	VADDSS  X15, X14, X14
	VMULSS  X6, X14, X14
	VCVTSI2SSQ AX, X15, X15
	VADDSS  X15, X14, X14
	VMOVSS  X14, 0(SP)(R14*1)
	MOVL    AX, 192(SP)(R14*1)
	ADDQ    $4, R14
	ADDQ    $16, R15
	ADDQ    $16, SI
	ADDQ    $16, DI
	DECQ    CX
	JNZ     qu
	JMP     sum

qsigned:
	MOVLQSX 0(DI), AX
	ADDQ    R12, AX
	IMULQ   R11, AX
	MOVLQSX 4(DI), BX
	ADDQ    R12, BX
	ADDQ    BX, AX
	IMULQ   R11, AX
	MOVLQSX 8(DI), BX
	ADDQ    R12, BX
	ADDQ    BX, AX
	IMULQ   R11, AX
	MOVLQSX 12(DI), BX
	ADDQ    R12, BX
	ADDQ    BX, AX
	MOVQ    AX, BX
	SHLQ    $4, BX
	VMOVUPS (R9)(BX*1), X10
	MOVBQZX (R8)(AX*1), AX
	VMOVUPS (SI), X11
	VMULPS  X2, X10, X12
	VSUBPS  X12, X11, X11
	VMULPS  X12, X12, X12
	VMULPS  X11, X11, X11
	VMOVUPS X12, 384(SP)(R15*1)
	VMOVSHDUP X11, X14
	VADDSS  X14, X11, X14
	VMOVHLPS X11, X11, X15
	VADDSS  X15, X14, X14
	VSHUFPS $3, X11, X11, X15
	VADDSS  X15, X14, X14
	VMULSS  X6, X14, X14
	VCVTSI2SSQ AX, X15, X15
	VADDSS  X15, X14, X14
	VMOVSS  X14, 0(SP)(R14*1)
	MOVL    AX, 192(SP)(R14*1)
	ADDQ    $4, R14
	ADDQ    $16, R15
	ADDQ    $16, SI
	ADDQ    $16, DI
	DECQ    CX
	JNZ     qsigned
	JMP     sum

sum:
	VXORPS  X0, X0, X0
	VXORPS  X1, X1, X1
	VMOVSS  uplim+148(FP), X7
	XORQ    R13, R13
	XORQ    AX, AX
	XORQ    DX, DX
	MOVQ    flags+120(FP), BX
	TESTQ   $const_bandCostPair, BX
	JZ      sumquad
sumpair:
	VADDSS  0(SP)(AX*1), X0, X0
	MOVLQSX 192(SP)(AX*1), BX
	ADDQ    BX, R13
	VUCOMISS X7, X0
	JAE     exit
	VADDSS  384(SP)(DX*1), X1, X1
	VADDSS  388(SP)(DX*1), X1, X1
	ADDQ    $4, AX
	ADDQ    $8, DX
	CMPQ    AX, R14
	JLT     sumpair
	JMP     finish
sumquad:
	VADDSS  0(SP)(AX*1), X0, X0
	MOVLQSX 192(SP)(AX*1), BX
	ADDQ    BX, R13
	VUCOMISS X7, X0
	JAE     exit
	VADDSS  384(SP)(DX*1), X1, X1
	VADDSS  388(SP)(DX*1), X1, X1
	VADDSS  392(SP)(DX*1), X1, X1
	VADDSS  396(SP)(DX*1), X1, X1
	ADDQ    $4, AX
	ADDQ    $16, DX
	CMPQ    AX, R14
	JLT     sumquad

finish:
	VMOVSS  X0, cost+152(FP)
	VMOVSS  X1, qenergy+156(FP)
	MOVQ    R13, totBits+160(FP)
	RET

exit:
	MOVB    $1, exited+168(FP)
	JMP     finish

// Escape-path constants, four lanes each: the index clamp 8191, the exponent floor 127, the bias
// 257 of the escape length, and the 21 bits of a clipped escape.
DATA bandcostEsc<>+0(SB)/4, $8191
DATA bandcostEsc<>+4(SB)/4, $8191
DATA bandcostEsc<>+8(SB)/4, $8191
DATA bandcostEsc<>+12(SB)/4, $8191
DATA bandcostEsc<>+16(SB)/4, $127
DATA bandcostEsc<>+20(SB)/4, $127
DATA bandcostEsc<>+24(SB)/4, $127
DATA bandcostEsc<>+28(SB)/4, $127
DATA bandcostEsc<>+32(SB)/4, $257
DATA bandcostEsc<>+36(SB)/4, $257
DATA bandcostEsc<>+40(SB)/4, $257
DATA bandcostEsc<>+44(SB)/4, $257
DATA bandcostEsc<>+48(SB)/4, $21
DATA bandcostEsc<>+52(SB)/4, $21
DATA bandcostEsc<>+56(SB)/4, $21
DATA bandcostEsc<>+60(SB)/4, $21
GLOBL bandcostEsc<>(SB), RODATA|NOPTR, $64
