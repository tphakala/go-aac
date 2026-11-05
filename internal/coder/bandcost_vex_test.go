// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestBandCostAMD64VexOnly requires every instruction of bandcost_amd64.s that
// names an XMM register to be VEX-encoded (a V mnemonic), as AGENTS.md rule 5
// asks of amd64 kernels: the legacy-SSE form of a move such as MOVQ AX, X9
// pays an AVX-SSE transition penalty when the caller leaves the upper YMM
// state dirty. Scalar integer instructions (POPCNTL included) name no XMM
// register and are not checked.
func TestBandCostAMD64VexOnly(t *testing.T) {
	src, err := os.ReadFile("bandcost_amd64.s")
	if err != nil {
		t.Fatal(err)
	}
	xmm := regexp.MustCompile(`\bX\d+\b`)
	lineNo := 0
	for line := range strings.Lines(string(src)) {
		lineNo++
		if j := strings.Index(line, "//"); j >= 0 {
			line = line[:j]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !xmm.MatchString(line) || strings.HasPrefix(fields[0], "V") {
			continue
		}
		if strings.HasSuffix(fields[0], ":") {
			fields = fields[1:]
			if len(fields) < 2 || strings.HasPrefix(fields[0], "V") {
				continue
			}
		}
		t.Errorf("bandcost_amd64.s:%d: non-VEX instruction on an XMM register: %s", lineNo, strings.TrimSpace(line))
	}
}
