package draft

// The sidecar pass writes everything narration produces that is not tour prose:
// subsystem names and descriptions, and — under --full-narration — per-file
// summaries. It exists because those things describe the codebase rather than
// the walk through it, so they have no place in the .tour.md a human curates.

import (
	"context"
	"fmt"

	"github.com/charlesharris/tourdesource/internal/narration"
	"github.com/charlesharris/tourdesource/internal/site"
	"github.com/charlesharris/tourdesource/internal/store"
)

// narrateSidecar derives subsystems, has the assistant describe them, and
// optionally summarises the busiest files. Results are written to the narration
// sidecar in mapDir, which `tds build` reads.
func narrateSidecar(
	ctx context.Context,
	st *store.Store,
	dctx *Context,
	mapDir string,
	opts NarrateOptions,
	res *Result,
	logf, warnf func(string, ...any),
) error {
	path := narration.Path(mapDir)
	doc, err := narration.Load(path)
	if err != nil {
		return err
	}
	doc.Commit = dctx.Commit
	save := func() error { return doc.Save(path) }

	files, err := st.Files()
	if err != nil {
		return err
	}
	symbols, err := st.Symbols()
	if err != nil {
		return err
	}
	imports, err := st.Imports()
	if err != nil {
		return err
	}
	signals, err := st.GitSignals()
	if err != nil {
		return err
	}
	entrypoints, err := st.Entrypoints()
	if err != nil {
		return err
	}

	// Derived exactly as `tds build` will derive it, so the ids line up. If the
	// grouping ever changed between the two, the sidecar would key on subsystem
	// ids that no longer exist and silently render nothing.
	subs, _, _ := site.DeriveSubsystems(files, symbols, imports, signals, entrypoints)

	n, err := narrateSubsystems(ctx, subs, doc, opts.Assistant, opts, dctx.ProjectName, save, logf, warnf)
	if err != nil {
		return fmt.Errorf("narrating subsystems: %w", err)
	}
	res.Subsystems = n

	if opts.FullNarration {
		m, err := narrateFiles(ctx, files, signals, doc, opts.Assistant, opts,
			dctx.Root, dctx.ProjectName, save, logf, warnf)
		if err != nil {
			return fmt.Errorf("summarising files: %w", err)
		}
		res.Summaries = m
	}

	return save()
}
