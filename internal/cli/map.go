package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/charlesharris/tourdesource/internal/mapper"
)

func newMapCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "map [path]",
		Short: "Build the structural index of a repository",
		Long: `Build the structural index (the "map") of a repository: its files,
git signals, and the symbols/imports/entrypoints extracted by the language
providers. Writes map.sqlite and map.json under <repo>/.tds (or --out).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			res, err := mapper.Build(cmd.Context(), mapper.Options{
				Root:   root,
				OutDir: outDir,
				Warnf: func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
				},
			})
			if err != nil {
				return err
			}
			printMapSummary(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "output directory for the map (default <repo>/.tds)")
	return cmd
}

func printMapSummary(cmd *cobra.Command, res *mapper.Result) {
	out := cmd.OutOrStdout()
	commit := res.Commit
	if commit == "" {
		commit = "(no git commit)"
	} else if len(commit) > 12 {
		commit = commit[:12]
	}

	providers := "(none — structure limited to line ranges)"
	if len(res.Providers) > 0 {
		providers = joinComma(res.Providers)
	}

	fmt.Fprintf(out, "Mapped %s @ %s\n", res.Root, commit)
	fmt.Fprintf(out, "  providers:   %s\n", providers)
	fmt.Fprintf(out, "  files:       %d (%s)\n", res.Files, languageBreakdown(res.Languages))
	fmt.Fprintf(out, "  symbols:     %d\n", res.Symbols)
	fmt.Fprintf(out, "  imports:     %d\n", res.Imports)
	fmt.Fprintf(out, "  entrypoints: %d\n", res.Entrypoints)
	fmt.Fprintf(out, "  wrote:       %s\n               %s\n", res.SQLitePath, res.JSONPath)
}

// languageBreakdown renders "ruby 30, markdown 5, …" sorted by count desc.
func languageBreakdown(langs map[string]int) string {
	type lc struct {
		lang  string
		count int
	}
	items := make([]lc, 0, len(langs))
	for l, c := range langs {
		if l == "" {
			l = "other"
		}
		items = append(items, lc{l, c})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].lang < items[j].lang
	})
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = fmt.Sprintf("%s %d", it.lang, it.count)
	}
	return joinComma(parts)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
