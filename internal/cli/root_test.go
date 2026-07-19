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
	// map (TDS-13), analyze (TDS-30), draft (TDS-39/40) and build (TDS-22) are
	// implemented. `tds check` (TDS-24) is the last stub.
	for _, stage := range []string{"check"} {
		if _, err := runCmd(stage); err != errNotImplemented {
			t.Errorf("stage %q: got err %v, want errNotImplemented", stage, err)
		}
	}
}

// TestDraftRequiresAMap checks that the implemented draft command fails with
// guidance rather than the stub error.
func TestDraftRequiresAMap(t *testing.T) {
	_, err := runCmd("draft", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no map exists")
	}
	if err == errNotImplemented {
		t.Fatal("draft is implemented; it must not return the stub error")
	}
	if !strings.Contains(err.Error(), "tds map") {
		t.Errorf("error should point at `tds map`, got %q", err)
	}
}

// TestDraftNarrateFailureExitsNonZero covers the silent-failure bug: a narrate
// run that produces nothing still writes a TODO skeleton, potentially over a
// previously narrated file, so it must not report success.
func TestDraftNarrateFailureExitsNonZero(t *testing.T) {
	// No map, so this fails earlier — but the error must still be a real error
	// rather than a success path. The full narrate-produced-nothing case is
	// covered in internal/draft; this pins that the command surfaces errors.
	if _, err := runCmd("draft", t.TempDir(), "--narrate"); err == nil {
		t.Fatal("expected a non-zero exit when narration cannot run")
	}
}
