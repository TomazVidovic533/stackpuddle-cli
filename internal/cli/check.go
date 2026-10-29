package cli

import (
	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
)

func newCheckCommand(deps Dependencies, opts *options) *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate the declared inputs and configuration of a project",
		Long: "Check loads the project, runs every checker and prints one result per checker and environment.\n" +
			"It takes no lock, reads no state and needs no Docker or control plane.\n" +
			"--live also asks the running system what an up would meet (a lost data object, an unreachable Docker\n" +
			"daemon): it takes no lock and changes nothing, and a daemon it cannot reach is unknown, never pass.\n" +
			"Exit codes: 0 pass, 3 fail, 5 incomplete.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Check == nil {
				return notWired("check")
			}
			format, err := opts.format()
			if err != nil {
				return err
			}
			sel, err := opts.selection()
			if err != nil {
				return err
			}
			report, err := deps.Check(cmd.Context(), application.CheckRequest{Selection: sel, Live: live})
			if err != nil {
				return err
			}
			if err := renderer(format).Report(cmd.OutOrStdout(), report); err != nil {
				return application.Unexpected("output", err)
			}
			if outcome := report.Outcome(); outcome != diagnostics.StatePass {
				return outcomeError{state: outcome}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "also check the running system: what an up would meet; no lock, no change")
	return cmd
}
