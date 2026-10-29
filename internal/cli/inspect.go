package cli

import (
	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// The views `inspect` serves; definition and graph are accepted names that fail as usage until m02.
const (
	viewConfig = "config"
	viewState  = "state"
)

// inspectFlags are the flags of inspect that shape what is read and how it waits.
type inspectFlags struct {
	explain bool
	wait    bool
	follow  bool
}

// inspectCall carries what one inspect invocation resolved, so the per-view functions take one value.
type inspectCall struct {
	cmd    *cobra.Command
	deps   Dependencies
	opts   *options
	format output.Format
	sel    application.Selection
	ref    model.Ref
	flags  inspectFlags
}

func newInspectCommand(deps Dependencies, opts *options) *cobra.Command {
	var view string
	var flags inspectFlags
	cmd := &cobra.Command{
		Use:   "inspect <ref>",
		Short: "Show the effective configuration or state of a reference, or an operation",
		Long: "Inspect --view config shows each effective value of workload/<key> or module/<key> in one\n" +
			"environment. --explain adds the source of each value and the sources it shadowed. Secret values\n" +
			"print as [redacted].\n" +
			"Inspect --view state shows the accepted intent, observed facts, ownership evidence and freshness of\n" +
			"workload/<key>, module/<key> or env/<name>, without secret values.\n" +
			"Inspect operation/<id> reads an open or archived operation without the lock; --wait polls until it\n" +
			"ends and exits with its outcome, --follow also prints each step as it changes, and --timeout bounds both.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format()
			if err != nil {
				return err
			}
			ref, err := model.ParseRef(args[0])
			if err != nil {
				return application.NewError(application.ClassUsage, "reference.invalid_ref",
					"the reference is not valid; use workload/<key>, module/<key>, env/<name> or operation/<id>").WithCause(err)
			}
			sel, err := opts.selection()
			if err != nil {
				return err
			}
			call := inspectCall{cmd: cmd, deps: deps, opts: opts, format: format, sel: sel, ref: ref, flags: flags}
			if ref.Kind() == model.RefOperation {
				return inspectOperation(call)
			}
			if flags.wait || flags.follow {
				return application.NewError(application.ClassUsage, "inspect.wait_needs_operation",
					"--wait and --follow apply to operation/<id> only")
			}
			if err := checkView(view); err != nil {
				return err
			}
			if view == viewState {
				return inspectState(call)
			}
			return inspectConfig(call)
		},
	}
	cmd.Flags().StringVar(&view, "view", viewConfig, "view to show: config or state")
	cmd.Flags().BoolVar(&flags.explain, "explain", false, "show the source of each value and the sources it shadowed")
	cmd.Flags().BoolVar(&flags.wait, "wait", false, "operation/<id> only: wait until the operation ends and exit with its outcome")
	cmd.Flags().BoolVar(&flags.follow, "follow", false, "operation/<id> only: like --wait, and print each step as it changes")
	return cmd
}

func inspectConfig(c inspectCall) error {
	if c.deps.Inspect == nil {
		return notWired("inspect")
	}
	result, err := c.deps.Inspect(c.cmd.Context(), application.InspectRequest{Selection: c.sel, Ref: c.ref})
	if err != nil {
		return err
	}
	if err := renderer(c.format).View(c.cmd.OutOrStdout(), result, output.ViewOptions{Explain: c.flags.explain}); err != nil {
		return application.Unexpected("output", err)
	}
	return nil
}

func inspectState(c inspectCall) error {
	if c.deps.InspectState == nil {
		return notWired("inspect")
	}
	view, err := c.deps.InspectState(c.cmd.Context(), application.InspectStateRequest{Selection: c.sel, Ref: c.ref})
	if err != nil {
		return err
	}
	if err := renderer(c.format).State(c.cmd.OutOrStdout(), view); err != nil {
		return application.Unexpected("output", err)
	}
	return nil
}

func inspectOperation(c inspectCall) error {
	if c.deps.InspectOperation == nil {
		return notWired("inspect")
	}
	id, err := application.OperationIDFromRef(c.ref)
	if err != nil {
		return err
	}
	timeout, err := c.opts.waitBound()
	if err != nil {
		return err
	}
	req := application.InspectOperationRequest{Selection: c.sel, Operation: id, Wait: c.flags.wait, Follow: c.flags.follow, Timeout: timeout}
	if c.flags.follow && c.format == output.FormatText {
		req.Progress = progressPrinter(c.cmd.ErrOrStderr())
	}
	record, err := c.deps.InspectOperation(c.cmd.Context(), req)
	if err != nil {
		return err
	}
	if err := renderer(c.format).Operation(c.cmd.OutOrStdout(), record); err != nil {
		return application.Unexpected("output", err)
	}
	return nil
}

func checkView(view string) error {
	switch view {
	case viewConfig, viewState:
		return nil
	case "definition", "graph":
		return application.NewError(application.ClassUsage, "inspect.unsupported_view",
			"only --view config and --view state are supported")
	}
	return application.NewError(application.ClassUsage, "inspect.unknown_view",
		"--view must be one of definition, config, state or graph; only config and state are supported")
}
