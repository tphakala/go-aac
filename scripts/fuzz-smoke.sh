#!/usr/bin/env bash
# Runs one bounded fuzz-smoke target and classifies the outcome so a slow-runner
# timeout does not fail CI while a real crasher still does. See issue #62.
#
# A genuine crasher stops fuzzing immediately, writes a reproducer under
# testdata/fuzz/<Target>/, and prints "Failing input written to ..."
# (Go src/testing/fuzz.go, emitted only on the crasher code path). A worker that
# hangs, OOMs, or dies mid-fuzz surfaces as "fuzzing process hung or terminated
# unexpectedly" (Go src/internal/fuzz/worker.go); a fuzz input that merely runs
# slowly is caught by the same path once it exceeds the per-input hang limit and
# is then written as a reproducer. A run that merely exhausted -fuzztime on a
# loaded runner can exit non-zero with "context deadline exceeded" and NO
# reproducer. Both real-failure markers come from code paths distinct from a
# clean deadline, and a hang can co-occur with a deadline, so they are checked
# first and fail closed; only a bare deadline is then treated as a tolerable
# slow-runner timeout. A seed-corpus crash runs before fuzzing starts, so it can
# never carry the deadline text and is caught by the fail-closed final branch.
#
# The marker strings below are substrings of go test / fuzz output and carry no
# stability contract; they were verified against the Go 1.26 toolchain (CI pins
# GO_VERSION 1.26). Re-verify them if that version bumps. The fail-closed default
# means a reworded *timeout* marker only turns a tolerated flake into a hard fail
# (the safe direction).
#
# Note: -e is intentionally NOT set; we must inspect go test's exit code.
set -uo pipefail

target=${1:?usage: fuzz-smoke.sh <FuzzTarget> <pkg> <fuzztime-seconds>}
pkg=${2:?missing package}
fuzztime=${3:?missing fuzztime seconds}

crash_marker='Failing input written to'
hung_marker='fuzzing process hung or terminated unexpectedly'
timeout_marker='context deadline exceeded'
nomatch_marker='no fuzz tests to fuzz'

# Capture combined output and the real exit code (no pipe, so $? is go test's).
output=$(go test -run='^$' -fuzz="^${target}\$" -fuzztime="${fuzztime}s" "$pkg" 2>&1)
status=$?
printf '%s\n' "$output"

if [ "$status" -eq 0 ]; then
  # go test exits 0 when -fuzz matches no target, so a typo'd target name or
  # wrong package would pass this smoke step without fuzzing anything. Fail
  # loudly instead: the step must actually exercise a target.
  if printf '%s\n' "$output" | grep -qF "$nomatch_marker"; then
    printf '::error::%s matched no fuzz target in %s (typo or wrong package?); nothing was fuzzed\n' "$target" "$pkg"
    exit 1
  fi
  exit 0
fi

# A written reproducer, or a worker that hung/terminated, is a real failure
# regardless of any timeout text, so these are checked first and fail closed.
if printf '%s\n' "$output" | grep -qF -e "$crash_marker" -e "$hung_marker"; then
  printf '::error::%s found a crasher or the fuzzing process terminated unexpectedly; check the log and commit any reproducer under testdata/fuzz/%s/\n' "$target" "$target"
  exit 1
fi

# No reproducer, deadline surfaced: known slow-runner timeout. Warn and pass.
if printf '%s\n' "$output" | grep -qF "$timeout_marker"; then
  printf '::warning::%s hit the -fuzztime deadline on a slow runner (no crasher found); treating as pass\n' "$target"
  exit 0
fi

# Anything else (build error, seed-corpus panic, killed process) is a real
# failure and must not be masked.
printf '::error::%s failed for a reason other than a slow-runner timeout (exit %d)\n' "$target" "$status"
exit 1
