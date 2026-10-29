package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/application/inprocess"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/distribution"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// run is the composition root. It builds every dependency once, runs the command tree for args and
// prints a failure to stderr. The process environment arrives as lookupEnv and the working directory as
// getwd, so the whole tree runs in-process under test with no global state. A panic below becomes an
// unexpected error instead of a crash.
func run(ctx context.Context, args []string, lookupEnv func(string) (string, bool),
	getwd func() (string, error), stdout, stderr io.Writer,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = application.Unexpected("internal", fmt.Errorf("panic: %v", r))
			_ = cli.WriteFailure(cli.NewRootCommand(cli.Dependencies{}), stderr, err)
		}
	}()
	source := project.NewSource(getwd)
	// The one in-process operations adapter; it composes the engine on first use, so check, inspect --view
	// config and --version never construct it and read no state root.
	operations := inprocess.New(lookupEnv, time.Now)
	modules := distribution.NewResolver(distribution.Options{
		CacheRoot: func() (string, error) { return config.CacheRoot(lookupEnv, os.UserCacheDir) },
	})
	// One line per checker.
	checks := []application.Checker{
		application.StructureChecker(),
		application.ConfigurationChecker(lookupEnv),
		application.SemanticChecker(),
		application.LiveChecker(operations, lookupEnv, time.Now),
	}
	root := cli.NewRootCommand(cli.Dependencies{
		Check:   application.NewResolvingCheckProject(source, modules, checks),
		Inspect: application.NewInspectConfig(source, lookupEnv),
		Version: func() (model.VersionInfo, error) {
			info, _ := debug.ReadBuildInfo()
			return application.VersionInfoFrom(info), nil
		},
		Mutate:           application.NewMutate(source, modules, operations, lookupEnv),
		InspectOperation: application.NewInspectOperation(source, operations, application.Sleep),
		InspectState:     application.NewInspectState(source, operations),
		Cancel:           application.NewCancel(source, operations),
		List:             application.NewList(source, operations),
		Remove:           application.NewRemove(project.NewEditor(getwd)),
		RunJob:           application.NewRunJob(source, modules, operations, lookupEnv),
	})
	root.SetArgs(append([]string{}, args...)) // never nil: Cobra reads os.Args for a nil slice
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(strings.NewReader(""))
	if err = root.ExecuteContext(ctx); err != nil {
		if writeErr := cli.WriteFailure(root, stderr, err); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}
