package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// options holds the values of the persistent flags of one root command. It is created per
// NewRootCommand call, so commands share no global state.
type options struct {
	file           string
	env            string
	output         string
	nonInteractive bool
	version        bool
	timeout        time.Duration
}

func (o *options) bind(cmd *cobra.Command) {
	flags := cmd.PersistentFlags()
	flags.StringVar(&o.file, "file", "", "composition root file or directory (default: stackpuddle.yaml in the working directory)")
	flags.StringVar(&o.env, "env", "", "declared environment to use")
	flags.StringVar(&o.output, "output", "text", "output format: text or json")
	flags.BoolVar(&o.nonInteractive, "non-interactive", false, "never prompt; missing inputs fail")
	flags.DurationVar(&o.timeout, "timeout", 0,
		"how long to wait for a busy environment or for --wait and --follow; it never cancels accepted work (default: fail at once)")
}

// waitBound returns --timeout; a negative value is a usage error.
func (o *options) waitBound() (time.Duration, error) {
	if o.timeout < 0 {
		return 0, application.NewError(application.ClassUsage, "usage.invalid_timeout", "--timeout must not be negative")
	}
	return o.timeout, nil
}

// selection builds the application selection from the flags the user set.
func (o *options) selection() (application.Selection, error) {
	var sel application.Selection
	if o.file != "" {
		sel.File, sel.HasFile = o.file, true
	}
	if o.env != "" {
		name, err := model.ParseEnvironmentName(o.env)
		if err != nil {
			return application.Selection{}, application.NewError(application.ClassUsage, "usage.invalid_environment",
				"--env is not a valid environment name").WithCause(err)
		}
		sel.Environment, sel.HasEnvironment = name, true
	}
	return sel, nil
}

// format parses --output; any other value is a usage error.
func (o *options) format() (output.Format, error) {
	f, ok := output.ParseFormat(o.output)
	if !ok {
		return 0, application.NewError(application.ClassUsage, "usage.invalid_output", "--output must be text or json")
	}
	return f, nil
}
