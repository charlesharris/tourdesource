package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/site"
)

func newBuildCmd() *cobra.Command {
	var repo, mapPath, outDir, projectDir string
	var keepProject bool
	cmd := &cobra.Command{
		Use:   "build <tour.md>",
		Short: "Compile a tour into a static explorer site",
		Long: `Compile a *.tour.md into a browsable multi-page site: it resolves the tour's
anchors against the map, emits a page per source file from the pinned repository
snapshot, and renders an overview, architecture map, explorer, tour and symbol
index. Run ` + "`tds map`" + ` first to produce the map.

Requires Hugo extended >= 0.128 on PATH.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			warnf := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
			}
			logf := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
			}
			return buildSite(cmd, args[0], repo, mapPath, outDir, projectDir, keepProject, warnf, logf)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", ".", "repository root")
	cmd.Flags().StringVar(&mapPath, "map", "", "path to map.sqlite (default <repo>/.tds/map.sqlite)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default <repo>/.tds/site)")
	cmd.Flags().StringVar(&projectDir, "project-dir", "",
		"where to generate the Hugo project (default: a temp dir)")
	cmd.Flags().BoolVar(&keepProject, "keep-project", false,
		"keep the generated Hugo project so the theme can be iterated with `hugo server`")
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
