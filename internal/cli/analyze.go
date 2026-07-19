package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/analyzer"
)

func newAnalyzeCmd() *cobra.Command {
	var mapDir string
	var analyzers []string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "analyze [path]",
		Short: "Run language tooling into normalized findings",
		Long: `Run each provider's analyzers — linters, security scanners, type checkers,
coverage and complexity — over the repository and store the results as findings
attributed to the symbols they land in.

Findings carry line numbers, so they are only meaningful against the exact
source the map indexed. Run ` + "`tds map`" + ` first; analyzing a repository whose map
is stale is refused rather than silently attributing findings to the wrong lines.

Each analyzer is availability-gated: a tool that is not installed is reported as
unavailable and skipped, never a hard failure.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			res, err := analyzer.Run(cmd.Context(), analyzer.Options{
				Root:      root,
				MapDir:    mapDir,
				Analyzers: analyzers,
				Timeout:   timeout,
				Warnf: func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
				},
			})
			if err != nil {
				return err
			}
			printAnalyzeSummary(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&mapDir, "map-dir", "", "directory holding map.sqlite (default <repo>/.tds)")
	cmd.Flags().StringSliceVar(&analyzers, "analyzer", nil,
		"restrict to these analyzers by name (repeatable; default: everything available)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"per-provider budget for the analyze request (default 15m)")
	return cmd
}

func printAnalyzeSummary(cmd *cobra.Command, res *analyzer.Result) {
	out := cmd.OutOrStdout()
	commit := res.Commit
	if commit == "" {
		commit = "(no git commit)"
	} else if len(commit) > 12 {
		commit = commit[:12]
	}

	fmt.Fprintf(out, "Analyzed %s @ %s\n", res.Root, commit)

	// "Nothing here can analyze" and "the thing that can, broke" need different
	// answers: telling someone to install a provider they already have sends
	// them to fix the wrong problem.
	if len(res.Providers) == 0 {
		if len(res.Attempted) > 0 {
			fmt.Fprintf(out, "  providers:   %s (all failed — see the warnings above)\n", joinComma(res.Attempted))
			fmt.Fprintf(out, "\nNo findings were stored. If the failure was a timeout, analysis of a\n")
			fmt.Fprintf(out, "large repository can take a while — raise it with --timeout.\n")
			return
		}
		fmt.Fprintf(out, "  providers:   (none offering analyze — no findings)\n")
		fmt.Fprintf(out, "\nNo provider advertised the analyze operation. Install a language\n")
		fmt.Fprintf(out, "provider for this repository, or check `tds map` reported one.\n")
		return
	}
	fmt.Fprintf(out, "  providers:   %s\n", joinComma(res.Providers))

	// Report what ran and, just as usefully, what would have run had its tool
	// been installed: a silently absent analyzer looks the same as a clean repo.
	ran, absent := splitByAvailability(res.Analyzers)
	for _, a := range ran {
		fmt.Fprintf(out, "  %-12s %d finding%s (%s %s)\n",
			a.Name+":", a.Findings, plural(a.Findings), a.Tool, orNoVersion(a.ToolVersion))
	}
	// The provider says *why* it could not run — simplecov needs a coverage
	// report, not an install — and that reason is already on stderr. Asserting
	// "not installed" here would contradict it.
	for _, a := range absent {
		fmt.Fprintf(out, "  %-12s skipped (see warnings above)\n", a.Name+":")
	}

	fmt.Fprintf(out, "  findings:    %d (%d attributed to a symbol", res.Findings, res.Resolved)
	if res.Unresolved > 0 {
		fmt.Fprintf(out, ", %d outside any known symbol", res.Unresolved)
	}
	fmt.Fprintf(out, ")\n")
	fmt.Fprintf(out, "  wrote:       %s\n", res.SQLitePath)

	if res.Findings > 0 {
		fmt.Fprintf(out, "\nNext: `tds draft` can rank hotspots by these findings, and `tds build`\n")
		fmt.Fprintf(out, "embeds them as views over the tour.\n")
	}
}

// splitByAvailability separates analyzers that ran from those whose tool is
// missing, each sorted for a stable report.
func splitByAvailability(runs []analyzer.AnalyzerRun) (ran, absent []analyzer.AnalyzerRun) {
	for _, a := range runs {
		if a.Available {
			ran = append(ran, a)
		} else {
			absent = append(absent, a)
		}
	}
	byName := func(s []analyzer.AnalyzerRun) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Name < s[j].Name })
	}
	byName(ran)
	byName(absent)
	return ran, absent
}

func orNoVersion(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
