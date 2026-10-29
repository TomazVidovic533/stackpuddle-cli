package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
)

// rootNames are the default composition root names, in the order they are reported.
func rootNames() []string { return []string{"stackpuddle.yaml", "stackpuddle.yml"} }

// locate resolves a selection to the directory that holds the root file and the root file's name inside
// it. --file names a file, or a directory searched like the working directory. More than one candidate
// fails instead of guessing.
func locate(cwd string, sel application.Selection) (dir, name string, err error) {
	if !sel.HasFile {
		return pick(cwd)
	}
	path := sel.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	info, statErr := os.Stat(path)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		return "", "", application.NewError(application.ClassNotFound, "project.root_not_found",
			fmt.Sprintf("--file %q does not exist", sel.File))
	case statErr != nil:
		return "", "", application.Unexpected("project", statErr)
	case info.IsDir():
		return pick(path)
	}
	return filepath.Dir(path), filepath.Base(path), nil
}

// pick finds the single default root file in dir through os.Root, so a symlink cannot lead out of it.
func pick(dir string) (string, string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", "", application.NewError(application.ClassNotFound, "project.root_not_found",
			"the project directory cannot be opened").WithCause(err)
	}
	defer root.Close()
	var found []string
	for _, name := range rootNames() {
		if _, statErr := root.Stat(name); statErr == nil {
			found = append(found, name)
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return "", "", application.Unexpected("project", statErr)
		}
	}
	switch len(found) {
	case 0:
		return "", "", application.NewError(application.ClassNotFound, "project.root_not_found",
			"no "+strings.Join(rootNames(), " or ")+" in the project directory; select a root with --file")
	case 1:
		return dir, found[0], nil
	}
	return "", "", application.NewError(application.ClassValidation, "project.root_ambiguous",
		"found "+strings.Join(found, " and ")+"; select one with --file")
}
