package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/builder"
	"github.com/charlesharris/tourdesource/internal/site"
)

func newBuildCmd() *cobra.Command {
	var repo, mapPath, outDir, format, projectDir string
	var keepProject bool
	cmd := &cobra.Command{
		Use:   "build <tour.md>",
		Short: "Compile a tour into a static bundle",
		Long: `Compile a *.tour.md into a self-contained static bundle: it resolves the
tour's anchors against the map, highlights the referenced code from the pinned
repository snapshot, and writes an index.html that opens in a browser with no
server. Run ` + "`tds map`" + ` first to produce the map.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			warnf := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
			}
			logf := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
			}

			switch format {
			case "site":
				return buildSite(cmd, args[0], repo, mapPath, outDir, projectDir, keepProject, warnf, logf)
			case "bundle", "":
				// fall through to the single-file bundle below
			default:
				return fmt.Errorf("unknown --format %q: want \"bundle\" or \"site\"", format)
			}

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
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default <repo>/.tds/tour)")
	cmd.Flags().StringVar(&format, "format", "bundle",
		"output format: \"bundle\" (self-contained single page, no external tools) or \"site\" (multi-page explorer; requires hugo)")
	cmd.Flags().StringVar(&projectDir, "project-dir", "",
		"site format: where to generate the Hugo project (default: a temp dir)")
	cmd.Flags().BoolVar(&keepProject, "keep-project", false,
		"site format: keep the generated Hugo project so the theme can be iterated with `hugo server`")
	return cmd
}

// buildSite renders the multi-page explorer site.
func buildSite(cmd *cobra.Command, tourPath, repo, mapPath, outDir, projectDir string, keep bool,
	warnf, logf func(string, ...any)) error {
	in, err := site.LoadInput(site.FromMapOptions{
		TourPath: tourPath, Repo: repo, MapPath: mapPath, Warnf: warnf,
	})
	if err != nil {
		return err
	}
	if outDir == "" {
		root, _ := filepath.Abs(orDefaultStr(repo, "."))
		outDir = filepath.Join(root, ".tds", "site")
	}

	res, err := site.Build(cmd.Context(), in, site.Options{
		OutDir: outDir, WorkDir: projectDir, KeepProject: keep,
		Warnf: warnf, Logf: logf,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Built site\n")
	fmt.Fprintf(out, "  file pages:  %d\n", res.Pages)
	fmt.Fprintf(out, "  subsystems:  %d\n", res.Subsystems)
	fmt.Fprintf(out, "  symbols:     %d indexed\n", res.Symbols)
	fmt.Fprintf(out, "  tour stops:  %d\n", res.TourStops)
	fmt.Fprintf(out, "  wrote:       %s\n", res.OutDir)
	fmt.Fprintf(out, "\nOpen it:  %s\n", filepath.Join(res.OutDir, "index.html"))
	return nil
}

func orDefaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
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
