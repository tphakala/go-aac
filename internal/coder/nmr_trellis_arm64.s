// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

#include "textflag.h"

// The float32 vector instructions are WORD-encoded: the Go 1.26 assembler
// this module supports has no mnemonics for them.

// func nmrNodeCostNEON(dst *float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, n int, lam float32)
TEXT ·nmrNodeCostNEON(SB), NOSPLIT, $0-36
	MOVD  dst+0(FP), R0
	MOVD  nd+8(FP), R1
	MOVD  nb+16(FP), R2
	MOVD  n+24(FP), R3
	FMOVS lam+32(FP), F31
	VDUP  V31.S[0], V31.S4
	SUB   $8, R3, R4
	LSL   $2, R4
	MOVD  $0, R5

nodeLoop:
	ADD  R5, R2, R6
	VLD1 (R6), [V0.S4, V1.S4]
	WORD $0x4E21D800 // scvtf v0.4s, v0.4s
	WORD $0x4E21D821 // scvtf v1.4s, v1.4s
	WORD $0x6E3FDC00 // fmul v0.4s, v0.4s, v31.4s
	WORD $0x6E3FDC21 // fmul v1.4s, v1.4s, v31.4s
	ADD  R5, R1, R6
	VLD1 (R6), [V2.S4, V3.S4]
	WORD $0x4E20D440 // fadd v0.4s, v2.4s, v0.4s
	WORD $0x4E21D461 // fadd v1.4s, v3.4s, v1.4s
	ADD  R5, R0, R6
	VST1 [V0.S4, V1.S4], (R6)
	CMP  R4, R5
	BEQ  nodeDone
	ADD  $32, R5
	CMP  R4, R5
	BLE  nodeLoop
	MOVD R4, R5
	B    nodeLoop

nodeDone:
	RET

// func nmrTrellisReconcileNEON(dp *float32, bp *uint8, node, vals *float32, idxs *int32, n, dHi int)
// V2/V3 = (MaxFloat32 > vals), V4/V5 = dp, V6/V7 = the back-pointers
// o - dHi + idxs, narrowed to bytes through V8. R5 is the first row of the
// group.
TEXT ·nmrTrellisReconcileNEON(SB), NOSPLIT, $0-56
	MOVD  dp+0(FP), R0
	MOVD  bp+8(FP), R1
	MOVD  node+16(FP), R2
	MOVD  vals+24(FP), R3
	MOVD  idxs+32(FP), R4
	MOVD  n+40(FP), R6
	MOVD  dHi+48(FP), R7
	NEG   R7, R7
	VDUP  R7, V27.S4
	VMOVQ $0x7f7fffff7f7fffff, $0x7f7fffff7f7fffff, V31
	VMOVQ $0x0000000100000000, $0x0000000300000002, V30
	VMOVQ $0x0000000500000004, $0x0000000700000006, V28
	SUB   $8, R6, R6
	MOVD  $0, R5

reconcileLoop:
	LSL    $2, R5, R8
	ADD    R8, R3, R9
	VLD1   (R9), [V0.S4, V1.S4]
	WORD $0x6EA0E7E2 // fcmgt v2.4s, v31.4s, v0.4s
	WORD $0x6EA1E7E3 // fcmgt v3.4s, v31.4s, v1.4s
	ADD    R8, R2, R9
	VLD1   (R9), [V4.S4, V5.S4]
	WORD $0x4E20D484 // fadd v4.4s, v4.4s, v0.4s
	WORD $0x4E21D4A5 // fadd v5.4s, v5.4s, v1.4s
	VBIF   V2.B16, V31.B16, V4.B16
	VBIF   V3.B16, V31.B16, V5.B16
	ADD    R8, R0, R9
	VST1   [V4.S4, V5.S4], (R9)
	ADD    R8, R4, R9
	VLD1   (R9), [V6.S4, V7.S4]
	VDUP   R5, V8.S4
	VADD   V30.S4, V8.S4, V9.S4
	VADD   V28.S4, V8.S4, V10.S4
	VADD   V9.S4, V6.S4, V6.S4
	VADD   V27.S4, V6.S4, V6.S4
	VADD   V10.S4, V7.S4, V7.S4
	VADD   V27.S4, V7.S4, V7.S4
	VAND   V2.B16, V6.B16, V6.B16
	VAND   V3.B16, V7.B16, V7.B16
	VUZP1  V7.H8, V6.H8, V8.H8
	VUZP1  V8.B16, V8.B16, V8.B16
	VMOV   V8.D[0], R9
	MOVD   R9, (R1)(R5)
	CMP    R6, R5
	BEQ    reconcileDone
	ADD    $8, R5
	CMP    R6, R5
	BLE    reconcileLoop
	MOVD   R6, R5
	B      reconcileLoop

reconcileDone:
	RET
