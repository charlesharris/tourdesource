package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/draft"
)

func newDraftCmd() *cobra.Command {
	var repo, mapDir, out, audience string
	var landmarks int

	cmd := &cobra.Command{
		Use:   "draft [path]",
		Short: "Generate a tour draft to curate",
		Long: `Generate a curated-ready tour skeleton from a repository's map.

It ranks the repo's entrypoints, landmarks and git hotspots, pours them into the
onboarding template (design §7), and writes a *.tour.md whose anchors all name
symbols that exist in the map. The prose is left as TODO placeholders carrying
the evidence tds has: curating means fixing and pruning rather than starting
from a blank page.

Run ` + "`tds map`" + ` first to produce the map.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				repo = args[0]
			}
			res, err := draft.Generate(cmd.Context(), draft.Options{
				Root:     repo,
				MapDir:   mapDir,
				Out:      out,
				Audience: audience,
				Assemble: draft.AssembleOptions{MaxLandmarks: landmarks},
				Warnf: func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
				},
			})
			if err != nil {
				return err
			}
			printDraftSummary(cmd, res)
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", ".", "repository root")
	cmd.Flags().StringVar(&mapDir, "map-dir", "", "directory holding map.sqlite (default <repo>/.tds)")
	cmd.Flags().StringVar(&out, "out", "", "output .tour.md (default <map-dir>/<project>.tour.md)")
	cmd.Flags().StringVar(&audience, "audience", "", "who the tour is for (frontmatter)")
	cmd.Flags().IntVar(&landmarks, "landmarks", 6, "how many landmark stops to propose")
	return cmd
}

func printDraftSummary(cmd *cobra.Command, res *draft.Result) {
	out := cmd.OutOrStdout()
	commit := res.Commit
	if commit == "" {
		commit = "(no git commit)"
	} else if len(commit) > 12 {
		commit = commit[:12]
	}

	fmt.Fprintf(out, "Drafted tour @ %s\n", commit)
	fmt.Fprintf(out, "  template:    %s\n", res.Template)
	fmt.Fprintf(out, "  chapters:    %d\n", res.Chapters)
	fmt.Fprintf(out, "  stops:       %d (%d symbol-anchored)\n", res.Stops, res.Anchors)
	fmt.Fprintf(out, "  landmarks:   %d\n", res.Landmarks)
	fmt.Fprintf(out, "  hotspots:    %d\n", res.Hotspots)
	fmt.Fprintf(out, "  wrote:       %s\n", res.Path)
	fmt.Fprintf(out, "\nNext: curate the prose, then `tds build %s`\n", res.Path)
}
