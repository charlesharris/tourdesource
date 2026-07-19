// Package cli wires up the tds command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X ...cli.version=...".
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tds",
		Short: "tour-de-source: generate shareable, interactive tours of a codebase",
		Long: `tds (tour-de-source) analyzes a source repository and produces a
shareable, interactive "tour" of the project — guided narration anchored to
real code, compiled into a self-contained static site.

The pipeline is a set of discrete stages:

  map      build the structural index of a repo (via language providers)
  analyze  run language tooling (linters, types, coverage) into findings
  draft    generate a tour draft with AI assistance (curated by a human)
  build    compile a tour into a browsable static site (needs hugo)
  check    re-resolve a tour's anchors against HEAD and report drift

See docs/design.md and docs/implementation-plan.md for the full picture.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newMapCmd(),
		newAnalyzeCmd(),
		newDraftCmd(),
		newBuildCmd(),
		newStageCmd("check", "Report tour anchor drift against HEAD"),
	)

	return root
}

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tds: "+err.Error())
		os.Exit(1)
	}
}
