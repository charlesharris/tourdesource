package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// errNotImplemented is returned by pipeline-stage stubs until the stage is built
// out in later milestones (see docs/implementation-plan.md).
var errNotImplemented = errors.New("not implemented yet")

// newStageCmd builds a placeholder command for a pipeline stage. Each stage is
// fleshed out in its own task; for now they exist so `tds --help` documents the
// intended surface and the command tree is stable.
func newStageCmd(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented
		},
	}
}
