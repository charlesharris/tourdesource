package cli

// `tds run` is the on-ramp: one command from a repository with nothing in it to
// a site you can serve.
//
// It is not simply "run every stage". The stages are not equally safe to repeat
// — `tds draft` overwrites the tour file, and the tour is the one artifact a
// human is expected to curate. A wrapper that re-drafted on every run would
// quietly destroy that work, which is the opposite of what a convenience command
// should do. So run converges rather than replays: it always refreshes the map,
// the findings and the site, and it creates the tour only when there is not one
// already. That makes it as useful on day 50 (refresh the site after a week of
// commits) as on day 1.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/analyzer"
	"github.com/charlesharris/tourdesource/internal/draft"
	"github.com/charlesharris/tourdesource/internal/mapper"
)

func newRunCmd() *cobra.Command {
	var repo, outDir, audience, tourPath, narrateWorkdir string
	var noNarrate, redraft, serve bool
	var narrateFiles, landmarks, port int
	var narrateTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Map, analyze, draft and build in one command",
		Long: `Take a repository from nothing to a servable site in one command.

It runs the whole pipeline: ` + "`map`" + ` to index the code, ` + "`analyze`" + ` to collect
findings from whatever language tooling is installed, ` + "`draft`" + ` to plan and
narrate a tour, and ` + "`build`" + ` to render the site.

Re-running is safe. The map, the findings and the site are always refreshed, but
the tour is written only if one does not exist yet — your curation of it is never
overwritten. Use --redraft when you do want it regenerated.

Narration is on by default and needs tmux and Claude Code on PATH; it runs on
your own subscription. It writes the tour's prose, names the subsystems on the
architecture map, and summarises the busiest files for the explorer. On a large
repository expect a handful of minutes. Use --no-narrate to skip it entirely and
get a site whose stops all read TODO.

Requires Hugo extended >= 0.128 on PATH for the build stage.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				repo = args[0]
			}
			return runPipeline(cmd, runOptions{
				Repo:           repo,
				OutDir:         outDir,
				Audience:       audience,
				TourPath:       tourPath,
				Narrate:        !noNarrate,
				NarrateFiles:   narrateFiles,
				NarrateWorkdir: narrateWorkdir,
				NarrateTimeout: narrateTimeout,
				Landmarks:      landmarks,
				Redraft:        redraft,
				Serve:          serve,
				Port:           port,
			})
		},
	}

	cmd.Flags().StringVar(&repo, "repo", ".", "repository root")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory for the site (default <repo>/.tds/site)")
	cmd.Flags().StringVar(&tourPath, "tour", "",
		"tour file to use or create (default <repo>/.tds/<project>.tour.md)")
	cmd.Flags().BoolVar(&noNarrate, "no-narrate", false,
		"skip narration: every stop keeps its TODO and subsystems stay as measured")
	cmd.Flags().IntVar(&narrateFiles, "narrate-files", 0,
		"how many files to summarise, busiest first (default 200; 0 disables file summaries)")
	cmd.Flags().StringVar(&narrateWorkdir, "narrate-workdir", "",
		"keep narration prompts and responses here (default: a temp dir, deleted on exit)")
	cmd.Flags().DurationVar(&narrateTimeout, "narrate-timeout", 10*time.Minute,
		"per-request budget for narration")
	cmd.Flags().IntVar(&landmarks, "landmarks", 6, "how many landmark stops to propose")
	cmd.Flags().StringVar(&audience, "audience", "", "who the tour is for (frontmatter)")
	cmd.Flags().BoolVar(&redraft, "redraft", false,
		"regenerate the tour even if one exists — DISCARDS any curation of it")
	cmd.Flags().BoolVar(&serve, "serve", false, "serve the finished site over HTTP")
	cmd.Flags().IntVar(&port, "port", 8000, "port for --serve")
	return cmd
}

type runOptions struct {
	Repo           string
	OutDir         string
	Audience       string
	TourPath       string
	Narrate        bool
	NarrateFiles   int
	NarrateWorkdir string
	NarrateTimeout time.Duration
	Landmarks      int
	Redraft        bool
	Serve          bool
	Port           int
}

// defaultRunNarrateFiles matches `tds draft --full-narration`. At roughly 40
// files per request this is a handful of round trips rather than the hours an
// uncapped pass would take on a large repository.
const defaultRunNarrateFiles = 200

func runPipeline(cmd *cobra.Command, opts runOptions) error {
	out := cmd.OutOrStdout()
	warnf := func(format string, a ...any) {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
	}
	logf := func(format string, a ...any) {
		fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
	}

	root, err := filepath.Abs(orDefaultStr(opts.Repo, "."))
	if err != nil {
		return err
	}
	mapDir := filepath.Join(root, ".tds")

	// Decide up front whether a tour will be written, because that determines
	// both whether narration will happen and which tools must be present.
	tourPath, existing, err := resolveTour(opts.TourPath, mapDir)
	if err != nil {
		return err
	}
	willDraft := !existing || opts.Redraft
	willNarrate := opts.Narrate && willDraft

	if existing && !opts.Redraft {
		logf("using the existing tour at %s (pass --redraft to regenerate it)", tourPath)
	}
	if existing && opts.Redraft {
		warnf("--redraft will overwrite %s, discarding any edits you have made to it", tourPath)
	}
	if opts.Narrate && !willDraft {
		logf("narration skipped: the tour already has its prose")
	}

	// Preflight before anything expensive. Discovering that tmux is missing
	// after a five-minute map is a waste of the user's time, and narration is
	// the stage most likely to be unavailable.
	if willNarrate {
		if err := checkNarrationTools(); err != nil {
			return err
		}
	}

	// 1. Map. Always: the whole pipeline downstream is only as current as this.
	fmt.Fprintf(out, "== Mapping %s\n", root)
	mapRes, err := mapper.Build(cmd.Context(), mapper.Options{Root: root, Warnf: warnf})
	if err != nil {
		return fmt.Errorf("mapping: %w", err)
	}
	printMapSummary(cmd, mapRes)

	// 2. Analyze. Genuinely optional: the site renders fine with no findings,
	// and a repo with no language tooling installed is an ordinary case rather
	// than a failure.
	fmt.Fprintf(out, "\n== Analyzing\n")
	anRes, err := analyzer.Run(cmd.Context(), analyzer.Options{Root: root, Warnf: warnf})
	if err != nil {
		warnf("analyze failed, continuing without findings: %v", err)
	} else {
		printAnalyzeSummary(cmd, anRes)
	}

	// 3. Draft — only when there is no tour to protect.
	if willDraft {
		fmt.Fprintf(out, "\n== Drafting\n")
		var narrateOpts *draft.NarrateOptions
		if opts.Narrate {
			files := opts.NarrateFiles
			if files == 0 && !cmd.Flags().Changed("narrate-files") {
				files = defaultRunNarrateFiles
			}
			narrateOpts = &draft.NarrateOptions{
				Root:    root,
				WorkDir: opts.NarrateWorkdir,
				Timeout: opts.NarrateTimeout,
				// File summaries are part of what makes the one-command result
				// worth looking at, so run opts into them where `tds draft`
				// requires --full-narration.
				FullNarration: files > 0,
				MaxFiles:      files,
			}
		}
		dRes, err := draft.Generate(cmd.Context(), draft.Options{
			Root:     root,
			Out:      tourPath,
			Audience: opts.Audience,
			Assemble: draft.AssembleOptions{MaxLandmarks: opts.Landmarks},
			Narrate:  narrateOpts,
			Warnf:    warnf,
			Logf:     logf,
		})
		if err != nil {
			return fmt.Errorf("drafting: %w", err)
		}
		printDraftSummary(cmd, dRes)

		// Same rule as `tds draft`: asking for prose and getting none is a
		// failed run, not a quiet fallback. Stop here rather than building a
		// site full of TODOs that looks like it worked.
		if dRes.NarrateRequested && dRes.Narrated == 0 && dRes.Stops > 0 {
			return fmt.Errorf("narration produced nothing: %d stops still have TODO prose "+
				"(see the warnings above; %s was still written, so `tds build` it "+
				"or re-run with --no-narrate if that is what you want)", dRes.Stops, tourPath)
		}
		tourPath = dRes.Path
	}

	// 4. Build.
	fmt.Fprintf(out, "\n== Building the site\n")
	if err := buildSite(cmd, tourPath, root, "", opts.OutDir, "", false, warnf, logf); err != nil {
		return fmt.Errorf("building: %w", err)
	}

	siteDir := opts.OutDir
	if siteDir == "" {
		siteDir = filepath.Join(root, ".tds", "site")
	}

	fmt.Fprintf(out, "\nCurate the tour by editing:\n  %s\n", tourPath)
	fmt.Fprintf(out, "Then re-run `tds run` to rebuild the site from it.\n")

	if opts.Serve {
		return serveSite(cmd.Context(), cmd, siteDir, opts.Port)
	}
	// The site uses absolute paths and fetches data/*.json, so opening
	// index.html from the filesystem does not work — it has to be served.
	fmt.Fprintf(out, "\nServe it:\n  tds run --serve\n  (or: cd %s && python3 -m http.server %d)\n",
		siteDir, opts.Port)
	return nil
}

// resolveTour decides which tour file to use. An explicit --tour wins. Otherwise
// it looks for one already in the map directory rather than predicting the name
// the drafter would choose, so a tour named by an earlier run — or renamed by
// hand — is still found and protected.
func resolveTour(explicit, mapDir string) (path string, exists bool, err error) {
	if explicit != "" {
		return explicit, fileExists(explicit), nil
	}

	found, err := filepath.Glob(filepath.Join(mapDir, "*.tour.md"))
	if err != nil {
		return "", false, err
	}
	sort.Strings(found)
	switch len(found) {
	case 0:
		// Let the drafter name it; Out is left empty so it uses its own default.
		return "", false, nil
	case 1:
		return found[0], true, nil
	default:
		return "", false, fmt.Errorf(
			"found %d tours in %s (%s): pass --tour to say which one to build",
			len(found), mapDir, filepath.Base(found[0])+", …")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkNarrationTools fails early and actionably. Narration is the one stage
// with external dependencies, and finding out after the map has run is worse
// than finding out immediately.
func checkNarrationTools() error {
	var missing []string
	for _, bin := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("narration needs tmux and Claude Code on PATH; not found: %s.\n"+
		"Install them, or re-run with --no-narrate to build a site whose stops read TODO",
		joinComma(missing))
}

// serveSite serves the finished site until interrupted. The site fetches
// data/*.json, so file:// does not work and a server is not a nicety.
func serveSite(ctx context.Context, cmd *cobra.Command, dir string, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("serving on port %d: %w (try --port)", port, err)
	}

	srv := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	fmt.Fprintf(cmd.OutOrStdout(), "\nServing %s\n  http://%s\n\nPress Ctrl-C to stop.\n",
		dir, ln.Addr().String())

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
