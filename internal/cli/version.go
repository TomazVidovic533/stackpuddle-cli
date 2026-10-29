package cli

import (
	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
)

// runVersion prints the version document in the selected format. It is the root flag's behaviour rather
// than Cobra's version template, which cannot follow --output.
func runVersion(cmd *cobra.Command, deps Dependencies, opts *options) error {
	if deps.Version == nil {
		return notWired("version")
	}
	format, err := opts.format()
	if err != nil {
		return err
	}
	info, err := deps.Version()
	if err != nil {
		return application.Unexpected("version", err)
	}
	if err := renderer(format).Version(cmd.OutOrStdout(), info); err != nil {
		return application.Unexpected("version", err)
	}
	return nil
}
