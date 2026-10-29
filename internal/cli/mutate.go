package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// verbSpec describes one of up, stop and destroy: its command, its help and the flags it offers.
type verbSpec struct {
	command application.Command
	use     string
	short   string
	long    string
	flags   mutateFlags
}

// mutateFlags are the flags of up, stop and destroy.
type mutateFlags struct {
	all         bool
	dryRun      bool
	expandScope bool
	deleteData  bool
	// hasAllFlag and hasDeleteFlag say which of the verb-specific flags the verb offers.
	hasAllFlag    bool
	hasDeleteFlag bool
}

// parseRefs turns positional arguments into references; a bad one is a usage error.
func parseRefs(args []string) ([]model.Ref, error) {
	refs := make([]model.Ref, 0, len(args))
	for _, arg := range args {
		ref, err := model.ParseRef(arg)
		if err != nil {
			return nil, application.NewError(application.ClassUsage, "reference.invalid_ref",
				fmt.Sprintf("%q is not a valid reference; use app/<key>, workload/<key>, module/<key> or env/<name>", arg)).WithCause(err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// newMutateCommand builds up, stop or destroy: three verbs that differ in their help, their flags and their
// command, and share one run path.
func newMutateCommand(deps Dependencies, opts *options, spec verbSpec) *cobra.Command {
	command, flags := spec.command, spec.flags
	cmd := &cobra.Command{Use: spec.use, Short: spec.short, Long: spec.long, Args: usageArgs(cobra.ArbitraryArgs)}
	if flags.hasAllFlag {
		cmd.Flags().BoolVar(&flags.all, "all", false, "select everything; it excludes explicit references")
	}
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false,
		"print the planned actions, unknowns and the generation they are based on; take no lock and write nothing")
	cmd.Flags().BoolVar(&flags.expandScope, "expand-scope", false,
		"permit a change to a shared resource that reaches consumers the selection does not name")
	if flags.hasDeleteFlag {
		cmd.Flags().BoolVar(&flags.deleteData, "delete-data", false,
			"also delete the persistent data (volumes) of the selected objects; without it destroy keeps them")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if deps.Mutate == nil {
			return notWired(string(command))
		}
		format, err := opts.format()
		if err != nil {
			return err
		}
		sel, err := opts.selection()
		if err != nil {
			return err
		}
		refs, err := parseRefs(args)
		if err != nil {
			return err
		}
		wait, err := opts.waitBound()
		if err != nil {
			return err
		}
		req := application.MutateRequest{
			Selection: sel, Targets: application.TargetRequest{Refs: refs, All: flags.all, Command: command},
			DryRun: flags.dryRun, ExpandedScope: flags.expandScope, DeleteData: flags.deleteData, Wait: wait,
		}
		if format == output.FormatText {
			req.Progress = progressPrinter(cmd.ErrOrStderr())
		}
		result, err := deps.Mutate(cmd.Context(), req)
		if err != nil {
			return err
		}
		if err := renderer(format).Mutation(cmd.OutOrStdout(), result); err != nil {
			return application.Unexpected("output", err)
		}
		return nil
	}
	return cmd
}

// progressPrinter returns the callback that tells a person which operation was accepted and which step moved,
// on w while the command runs. The final result still prints on standard output.
func progressPrinter(w io.Writer) func(application.OperationRecord) {
	announced := false
	seen := map[string]application.StepStatus{}
	return func(r application.OperationRecord) {
		if !announced {
			announced = true
			fmt.Fprintf(w, "accepted %s; follow it from another terminal with: stackpuddle inspect %s --follow\n", r.ID, r.ID.Ref())
		}
		for _, s := range r.Steps {
			if seen[s.ID] == s.Status {
				continue
			}
			seen[s.ID] = s.Status
			fmt.Fprintf(w, "  %s %s %s: %s\n", s.ID, s.Action, s.Object, s.Status)
		}
	}
}

func newUpCommand(deps Dependencies, opts *options) *cobra.Command {
	return newMutateCommand(deps, opts, verbSpec{command: application.CommandUp, use: "up [refs...]",
		short: "Bring the selected workloads and modules of an environment up",
		long: "Up prints the selection and the accepted operation ID, then runs the operation.\n" +
			"Without references it selects the default applications. References are app/<key>, workload/<key>,\n" +
			"module/<key> and env/<name>; groups expand and deduplicate, a module selection does not select its\n" +
			"consumers, and a job never runs because its group was selected.\n" +
			"--dry-run takes no lock and writes nothing. A busy environment fails with exit 6 unless --timeout waits.\n" +
			"An interrupted operation is continued by running up again, under a new operation that names the old one.",
		flags: mutateFlags{hasAllFlag: true}})
}

func newStopCommand(deps Dependencies, opts *options) *cobra.Command {
	return newMutateCommand(deps, opts, verbSpec{command: application.CommandStop, use: "stop <refs...>|--all",
		short: "Stop the selected workloads and keep their data",
		long: "Stop records the stopped intent and keeps every volume. Name references or pass --all; a later up\n" +
			"without that selection does not restart the stopped workloads.",
		flags: mutateFlags{hasAllFlag: true}})
}

func newDestroyCommand(deps Dependencies, opts *options) *cobra.Command {
	return newMutateCommand(deps, opts, verbSpec{command: application.CommandDestroy, use: "destroy <refs...>",
		short: "Delete the runtime resources stackpuddle owns for the selection",
		long: "Destroy deletes only the owned runtime resources of the selected environment; env/<name> selects the\n" +
			"whole environment. Persistent data (volumes) stays unless --delete-data names that intent.\n" +
			"Another environment's resources are never touched.",
		flags: mutateFlags{hasDeleteFlag: true}})
}

func newRemoveCommand(deps Dependencies, opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <ref>",
		Short: "Delete a declaration from the YAML composition root; nothing physical changes",
		Long: "Remove cuts workload/<key>, module/<key>, client/<key> or app/<key> out of the YAML file that declares it\n" +
			"and changes nothing else; the next up shows its realization as retained until destroy.\n" +
			"A root written by an SDK is refused with the exact edit to make, because SDK source is not round-trippable.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.Remove == nil {
				return notWired("remove")
			}
			format, err := opts.format()
			if err != nil {
				return err
			}
			sel, err := opts.selection()
			if err != nil {
				return err
			}
			refs, err := parseRefs(args)
			if err != nil {
				return err
			}
			removed, err := deps.Remove(cmd.Context(), application.RemoveRequest{Selection: sel, Ref: refs[0]})
			if err != nil {
				return err
			}
			if err := renderer(format).Removed(cmd.OutOrStdout(), removed); err != nil {
				return application.Unexpected("output", err)
			}
			return nil
		},
	}
}

func newCancelCommand(deps Dependencies, opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <op-id>",
		Short: "Ask a running operation to stop at its next step",
		Long: "Cancel writes the cancel request of the operation and takes no lock. The running operation stops at its\n" +
			"next step with partial outcomes kept, and is never continued automatically. " + output.CancelNote + ".",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.Cancel == nil {
				return notWired("cancel")
			}
			format, err := opts.format()
			if err != nil {
				return err
			}
			sel, err := opts.selection()
			if err != nil {
				return err
			}
			id, err := application.ParseOperationID(args[0])
			if err != nil {
				return application.NewError(application.ClassUsage, "reference.invalid_ref",
					"the operation ID is not valid; use op-<26 characters>").WithCause(err)
			}
			if err := deps.Cancel(cmd.Context(), application.CancelRequest{Selection: sel, Operation: id}); err != nil {
				return err
			}
			if err := renderer(format).CancelRequested(cmd.OutOrStdout(), id); err != nil {
				return application.Unexpected("output", err)
			}
			return nil
		},
	}
}

func newListCommand(deps Dependencies, opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "list <collection>",
		Short: "List applications, workloads, modules, resources, bindings or environments",
		Long: "List prints one collection of the project. resources reads the environment's state; the rest read the\n" +
			"project. operations and sessions are not listable yet and fail as unsupported.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.List == nil {
				return notWired("list")
			}
			format, err := opts.format()
			if err != nil {
				return err
			}
			sel, err := opts.selection()
			if err != nil {
				return err
			}
			collection, err := collectionOf(args[0])
			if err != nil {
				return err
			}
			result, err := deps.List(cmd.Context(), application.ListRequest{Selection: sel, Collection: collection})
			if err != nil {
				return err
			}
			if err := renderer(format).List(cmd.OutOrStdout(), result); err != nil {
				return application.Unexpected("output", err)
			}
			return nil
		},
	}
}

func collectionOf(name string) (application.Collection, error) {
	if c, ok := application.ParseCollection(name); ok {
		return c, nil
	}
	switch name {
	case "operations":
		return "", application.NewError(application.ClassUsage, "list.unsupported_collection",
			"list operations is not available: the engine publishes no operation listing yet; read one with inspect operation/<id>")
	case "sessions":
		return "", application.NewError(application.ClassUsage, "list.unsupported_collection",
			"list sessions is not available yet; sessions arrive with the interactive shell")
	}
	return "", application.NewError(application.ClassUsage, "list.unknown_collection",
		"the collection must be one of applications, workloads, modules, resources, bindings or environments")
}
