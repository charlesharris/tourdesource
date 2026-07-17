package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/builder"
)

func newBuildCmd() *cobra.Command {
	var repo, mapPath, outDir string
	cmd := &cobra.Command{
		Use:   "build <tour.md>",
		Short: "Compile a tour into a static bundle",
		Long: `Compile a *.tour.md into a self-contained static bundle: it resolves the
tour's anchors against the map, highlights the referenced code from the pinned
repository snapshot, and writes an index.html that opens in a browser with no
server. Run ` + "`tds map`" + ` first to produce the map.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := builder.Build(cmd.Context(), builder.Options{
				TourPath: args[0],
				Repo:     repo,
				MapPath:  mapPath,
				OutDir:   outDir,
				Warnf: func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
				},
			})
			if err != nil {
				return err
			}
			printBuildSummary(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", ".", "repository root")
	cmd.Flags().StringVar(&mapPath, "map", "", "path to map.sqlite (default <repo>/.tds/map.sqlite)")
	cmd.Flags().StringVar(&outDir, "out", "", "output bundle directory (default <repo>/.tds/tour)")
	return cmd
}

func printBuildSummary(cmd *cobra.Command, res *builder.Result) {
	out := cmd.OutOrStdout()
	commit := res.Commit
	if commit == "" {
		commit = "(no git commit)"
	} else if len(commit) > 12 {
		commit = commit[:12]
	}

	fmt.Fprintf(out, "Built tour @ %s\n", commit)
	fmt.Fprintf(out, "  stops:       %d\n", res.Stops)
	fmt.Fprintf(out, "  code files:  %d highlighted\n", res.CodeFiles)
	if res.EmbedFiles > 0 {
		fmt.Fprintf(out, "  embedded:    %d files (pinned)\n", res.EmbedFiles)
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintf(out, "  warnings:    %d (unresolved anchors — see manifest.json)\n", len(res.Warnings))
	}
	fmt.Fprintf(out, "  bundle:      %s\n", res.BundleDir)
	fmt.Fprintf(out, "\nOpen it:  %s\n", res.IndexPath)
}
