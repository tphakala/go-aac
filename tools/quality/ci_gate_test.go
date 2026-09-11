package main

import (
	"os"
	"regexp"
	"testing"
)

// ciWorkflowPath is the CI workflow, relative to this package directory.
const ciWorkflowPath = "../../.github/workflows/ci.yml"

// gateEnvLine matches the env line that turns TestQualityBaseline on in CI.
var gateEnvLine = regexp.MustCompile(`(?m)^\s*GOAAC_QUALITY_BASELINE:\s*['"]?1['"]?\s*$`)

// TestQualityGateActivatedInCI pins that the CI workflow actually enables the
// quality regression gate. Without this, the gate is guarded by an env var that
// nothing in CI sets, so a bare `go test ./...` skips it and a regression sails
// through green; deleting the env line from ci.yml must fail this test.
//
// It guards the PRESENCE of the env line, not that the line is scoped to the
// step that actually runs the gate: a YAML job-graph check is out of scope for
// a Go test.
func TestQualityGateActivatedInCI(t *testing.T) {
	data, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	if !gateEnvLine.Match(data) {
		t.Fatalf("%s does not set GOAAC_QUALITY_BASELINE=1; the quality gate would never run in CI", ciWorkflowPath)
	}
}
