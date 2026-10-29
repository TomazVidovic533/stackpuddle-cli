package cli

import "github.com/spf13/cobra"

func NewRootCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "stackpuddle",
		Short:         "stackpuddle is the command-line interface for StackPuddle",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
}
