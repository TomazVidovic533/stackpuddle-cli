package cli

import (
	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
)

func newRunCommand(deps Dependencies, opts *options) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "run <job>",
		Short: "Invoke one job as its own operation",
		Long: "Run invokes the job once, as a new operation with its own invocation ID (<operation>/<step>) and the job's\n" +
			"native exit code. The job runs even when its spec is unchanged. A non-zero exit fails the operation\n" +
			"with job.exited_nonzero (exit 10). --dry-run shows the invocation and runs nothing.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.RunJob == nil {
				return notWired("run")
			}
			format, err := opts.format()
			if err != nil {
				return err
			}
			sel, err := opts.selection()
			if err != nil {
				return err
			}
			wait, err := opts.waitBound()
			if err != nil {
				return err
			}
			req := application.RunJobRequest{Selection: sel, Job: args[0], DryRun: dryRun, Wait: wait}
			if format == output.FormatText {
				req.Progress = progressPrinter(cmd.ErrOrStderr())
			}
			result, err := deps.RunJob(cmd.Context(), req)
			if err != nil {
				return err
			}
			if err := renderer(format).Invocation(cmd.OutOrStdout(), result); err != nil {
				return application.Unexpected("output", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the invocation and run nothing; take no lock and write nothing")
	return cmd
}
