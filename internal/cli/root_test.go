package cli

import (
	"bytes"
	"strings"
	"testing"
)

// runCmd executes the root command with args and captures stdout/stderr.
func runCmd(args ...string) (string, error) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestHelpListsPipelineStages(t *testing.T) {
	out, err := runCmd("--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	for _, stage := range []string{"map", "analyze", "draft", "build", "check"} {
		if !strings.Contains(out, stage) {
			t.Errorf("--help output missing stage %q\n%s", stage, out)
		}
	}
}

func TestStageStubsReturnNotImplemented(t *testing.T) {
	// map (TDS-13) and build (TDS-22) are implemented; the rest are still stubs.
	for _, stage := range []string{"analyze", "draft", "check"} {
		if _, err := runCmd(stage); err != errNotImplemented {
			t.Errorf("stage %q: got err %v, want errNotImplemented", stage, err)
		}
	}
}
