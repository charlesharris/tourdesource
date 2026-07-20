package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/draft"
)

func newDraftCmd() *cobra.Command {
	var repo, mapDir, out, audience, workDir string
	var landmarks int
	var doNarrate bool
	var fullNarration bool
	var narrateFiles int
	var narrateFrom string
	var narrateTimeout time.Duration

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

			var narrateOpts *draft.NarrateOptions
			if doNarrate || fullNarration || narrateFrom != "" {
				narrateOpts = &draft.NarrateOptions{
					Root:          repo,
					WorkDir:       workDir,
					Timeout:       narrateTimeout,
					FromFile:      narrateFrom,
					FullNarration: fullNarration,
					MaxFiles:      narrateFiles,
				}
			}

			res, err := draft.Generate(cmd.Context(), draft.Options{
				Root:     repo,
				MapDir:   mapDir,
				Out:      out,
				Audience: audience,
				Assemble: draft.AssembleOptions{MaxLandmarks: landmarks},
				Narrate:  narrateOpts,
				Warnf: func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
				},
				Logf: func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
				},
			})
			if err != nil {
				return err
			}
			printDraftSummary(cmd, res)

			// Narration that produced nothing is a failed run, not a quiet
			// fallback. It still wrote a usable TODO skeleton, but the caller
			// asked for prose and got none — and the write may have replaced a
			// previously narrated file, so this must not exit 0.
			if res.NarrateRequested && res.Narrated == 0 && res.Stops > 0 {
				return fmt.Errorf("narration produced nothing: %d stops still have TODO prose "+
					"(see the warnings above; %s was still written)", res.Stops, res.Path)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", ".", "repository root")
	cmd.Flags().StringVar(&mapDir, "map-dir", "", "directory holding map.sqlite (default <repo>/.tds)")
	cmd.Flags().StringVar(&out, "out", "", "output .tour.md (default <map-dir>/<project>.tour.md)")
	cmd.Flags().StringVar(&audience, "audience", "", "who the tour is for (frontmatter)")
	cmd.Flags().IntVar(&landmarks, "landmarks", 6, "how many landmark stops to propose")
	cmd.Flags().BoolVar(&doNarrate, "narrate", false,
		"write the prose with an assistant instead of leaving TODOs (drives Claude in tmux; spends tokens on your subscription)")
	cmd.Flags().StringVar(&narrateFrom, "narrate-from", "",
		"replay a saved assistant response (the *-out.json from a previous --narrate run) instead of asking again")
	cmd.Flags().StringVar(&workDir, "narrate-workdir", "",
		"where narration prompt/answer files are written (default: a temp dir)")
	cmd.Flags().DurationVar(&narrateTimeout, "narrate-timeout", 10*time.Minute,
		"per-request budget for narration")
	cmd.Flags().BoolVar(&fullNarration, "full-narration", false,
		"also summarise the busiest files for the explorer (implies --narrate; hours on a large repo, cached by content hash)")
	cmd.Flags().IntVar(&narrateFiles, "narrate-files", 250,
		"how many files --full-narration describes, busiest first")
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
	// Always report narration when it was asked for. Reporting it only on
	// success made a total failure look like an ordinary draft — the summary
	// contradicted the warning above it.
	if res.NarrateRequested {
		fmt.Fprintf(out, "  narrated:    %d of %d stops\n", res.Narrated, res.Stops)
	}
	fmt.Fprintf(out, "  wrote:       %s\n", res.Path)
	if res.Narrated > 0 {
		fmt.Fprintf(out, "\nNext: review the generated prose, then `tds build %s`\n", res.Path)
	} else {
		fmt.Fprintf(out, "\nNext: curate the prose, then `tds build %s`\n", res.Path)
	}
}
