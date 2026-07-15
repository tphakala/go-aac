#!/usr/bin/env python3
# SPDX-License-Identifier: LGPL-2.1-or-later
"""Generate internal/window/sinefixed_tables.go from a tools/cimdct dump.

Usage: gensinefix.py <imdct_seedN.dump> > sinefixed_tables.go

The sine_*_fixed tables cannot be computed portably: the C initializer
(libavcodec/sinewin_fixed_tablegen.h:56-63 @ d09d5afc3a) rounds sinf()
output and the oracle libm's sinf is not correctly rounded (Apple libm:
14/1024 and 3/128 values one float ulp off the correctly rounded result).
Baking the dumped runtime values mirrors FFmpeg's CONFIG_HARDCODED_TABLES.
"""

import sys

HEADER = '''// SPDX-License-Identifier: LGPL-2.1-or-later

// Code generated from the pinned C oracle build; DO NOT EDIT by hand.
//
// These are the runtime values of sine_1024_fixed and sine_128_fixed
// computed by init_sine_windows_fixed (libavcodec/sinewin_fixed_tablegen.h:
// 56-75 @ d09d5afc3a) in the pinned FFmpeg build, dumped by tools/cimdct.
// They CANNOT be regenerated portably: SIN_FIX rounds sinf() output, and
// the host libm's sinf is not correctly rounded (on this oracle's Apple
// libm, 14 of 1024 and 3 of 128 values differ from the correctly rounded
// float32(sin(x))). Baking the dumped values mirrors FFmpeg's own
// CONFIG_HARDCODED_TABLES escape hatch for exactly this problem and keeps
// the decoder bit-exact against the pinned oracle on every platform.
// Regenerate with tools/cimdct (WIN sine_* lines) if the pin ever moves;
// TestIMDCTDump locks every value.

package window

'''


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: gensinefix.py <imdct_seedN.dump> > sinefixed_tables.go")
    tabs = {}
    with open(sys.argv[1]) as fh:
        for ln in fh:
            if ln.startswith("WIN sine_"):
                parts = ln.split()
                tabs[parts[1]] = parts[3:]
    out = [HEADER]
    for name, size in (("sine_1024", 1024), ("sine_128", 128)):
        vals = tabs[name]
        assert len(vals) == size, (name, len(vals))
        gname = "sine%dFixedTab" % size
        out.append(f"var {gname} = [{size}]int32{{\n")
        for i in range(0, size, 8):
            out.append("\t" + ", ".join(vals[i:i + 8]) + ",\n")
        out.append("}\n\n")
    # gofmt: single trailing newline
    sys.stdout.write("".join(out).rstrip("\n") + "\n")


if __name__ == "__main__":
    main()
