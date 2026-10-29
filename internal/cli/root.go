// Package cli is the Cobra edge of the command line: flags, argument checks, exit codes and the choice of
// renderer. Behaviour lives in internal/application; this package turns arguments into requests and
// results into output.
package cli

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
)

// Dependencies are the use cases a root command runs, built once at the composition root.
type Dependencies struct {
	Check   application.CheckProject
	Inspect application.InspectConfig
	Version application.VersionReport

	Mutate           application.Mutate
	InspectOperation application.InspectOperation
	InspectState     application.InspectState
	Cancel           application.Cancel
	List             application.List
	Remove           application.Remove
	RunJob           application.RunJob
}

// NewRootCommand builds the whole command tree from deps. The tree holds no global state, so any number
// of roots can run in one process. Unknown arguments fail as usage errors.
func NewRootCommand(deps Dependencies) *cobra.Command {
	opts := &options{}
	root := &cobra.Command{
		Use:           "stackpuddle",
		Short:         "stackpuddle is the command-line interface for StackPuddle",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          usageArgs(cobra.NoArgs),
		PersistentPreRunE: func(*cobra.Command, []string) error {
			_, err := opts.format()
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.version {
				return runVersion(cmd, deps, opts)
			}
			return cmd.Help()
		},
	}
	opts.bind(root)
	root.Flags().BoolVar(&opts.version, "version", false, "print the build, the contract versions it reads and exit")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })
	root.AddCommand(newCheckCommand(deps, opts), newInspectCommand(deps, opts),
		newUpCommand(deps, opts), newStopCommand(deps, opts), newDestroyCommand(deps, opts),
		newRemoveCommand(deps, opts), newCancelCommand(deps, opts), newListCommand(deps, opts),
		newRunCommand(deps, opts))
	return root
}

// WriteFailure prints err to w in the format the root command selected, or as text when the format is not
// known. A check outcome prints nothing: its report is already written.
func WriteFailure(root *cobra.Command, w io.Writer, err error) error {
	failure, ok := failureOf(err)
	if !ok {
		return nil
	}
	format := output.FormatText
	if flag := root.PersistentFlags().Lookup("output"); flag != nil {
		if parsed, valid := output.ParseFormat(flag.Value.String()); valid {
			format = parsed
		}
	}
	return renderer(format).Failure(w, failure)
}

func renderer(f output.Format) output.Renderer { return output.NewRenderer(f) }

// usageArgs wraps an argument validator so its failure is a usage error with exit code 2.
func usageArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return usageError(err)
		}
		return nil
	}
}

func notWired(name string) error {
	return application.Unexpected(name, errors.New("the command is not wired at the composition root"))
}
