package project

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// maxEditableBytes bounds the file the editor reads, the same budget the model gives every project file.
const maxEditableBytes = 1 << 20

// Editor implements application.DeclarationEditor over the YAML composition root. It cuts one declaration by
// its line span and keeps every other byte, re-loads the result in memory and writes only a result that
// loads, through the project's os.Root and an atomic rename.
type Editor struct {
	getwd func() (string, error)
}

// NewEditor returns an Editor that resolves relative paths against getwd().
func NewEditor(getwd func() (string, error)) *Editor { return &Editor{getwd: getwd} }

// Remove cuts the declaration ref names from the YAML file that declares it.
func (e *Editor) Remove(ctx context.Context, sel application.Selection, ref model.Ref) (application.Removed, error) {
	if err := ctx.Err(); err != nil {
		return application.Removed{}, fmt.Errorf("removing %s: %w", ref, err)
	}
	cwd, err := e.getwd()
	if err != nil {
		return application.Removed{}, application.Unexpected("project", err)
	}
	dir, name, err := locate(cwd, sel)
	if err != nil {
		return application.Removed{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return application.Removed{}, application.Unexpected("project", err)
	}
	defer root.Close()
	loaded, diags := model.Load(root.FS(), name, nil)
	if len(diags) > 0 {
		return application.Removed{}, application.NewError(application.ClassValidation, "project.invalid",
			"the project is invalid; run check for the findings").WithFindings(diagnostics.FromModel(diags.Sorted()))
	}
	file, line, col, err := declaration(loaded, ref)
	if err != nil {
		return application.Removed{}, err
	}
	original, mode, err := readEditable(root, file)
	if err != nil {
		return application.Removed{}, err
	}
	edited, err := cutDeclaration(original, line, col)
	if err != nil {
		return application.Removed{}, application.NewError(application.ClassValidation, "remove.not_editable",
			fmt.Sprintf("%s in %s cannot be removed automatically (%v); delete its lines by hand", ref, file, err))
	}
	if err := verifyRemoval(root, name, file, edited, ref); err != nil {
		return application.Removed{}, err
	}
	if err := writeAtomic(root, file, edited, mode); err != nil {
		return application.Removed{}, application.Unexpected("remove", err)
	}
	return application.Removed{Ref: ref, File: file}, nil
}

// declaration finds where ref was written. A declaration an SDK produced is refused with the edit to make.
func declaration(p model.Project, ref model.Ref) (file string, line, col int, err error) {
	prov, ok := p.Provenance(ref)
	if !ok {
		return "", 0, 0, application.NewError(application.ClassNotFound, "project.unknown_object", fmt.Sprintf("%s is not declared", ref))
	}
	origin := prov.Declared()
	file, hasFile := origin.File()
	if origin.Kind() == model.OriginSDK {
		return "", 0, 0, application.NewError(application.ClassValidation, "remove.sdk_root", fmt.Sprintf(
			"%s was declared by an SDK; SDK source is not round-trippable, so remove edits YAML roots only. "+
				"Delete the declaration of %s in your SDK source and regenerate the Model file", ref, ref))
	}
	line, col, hasPos := origin.Position()
	if !hasFile || !hasPos {
		return "", 0, 0, application.NewError(application.ClassValidation, "remove.not_editable",
			fmt.Sprintf("%s has no source position; delete it by hand", ref))
	}
	return file, line, col, nil
}

// readEditable reads file through root, refusing a symlink and an oversized file.
func readEditable(root *os.Root, file string) ([]byte, fs.FileMode, error) {
	info, err := root.Lstat(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, application.NewError(application.ClassValidation, "remove.not_editable",
			file+" declares it but is not in the project directory; delete the declaration where it is written")
	}
	if err != nil {
		return nil, 0, application.Unexpected("remove", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, application.NewError(application.ClassValidation, "remove.not_editable",
			file+" is not a regular file (a symlink is never edited); delete the declaration by hand")
	}
	f, err := root.Open(file)
	if err != nil {
		return nil, 0, application.Unexpected("remove", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxEditableBytes+1))
	if err != nil || len(data) > maxEditableBytes {
		return nil, 0, application.NewError(application.ClassValidation, "remove.not_editable", file+" cannot be read within the file limit")
	}
	return data, info.Mode().Perm(), nil
}

// cutDeclaration removes the block-mapping entry whose key sits at line:col. The entry runs from its key line
// to the last more-indented line before the next entry of the same or an outer mapping; blank and comment
// lines after it stay with what follows. When the entry is the only one of its mapping, the mapping's own key
// goes with it, so removing the last module leaves no empty "modules:". Every other byte is kept.
func cutDeclaration(src []byte, line, col int) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("the file does not parse: %w", err)
	}
	chain := keyChain(&doc, line, col)
	if len(chain) == 0 {
		return nil, errors.New("no mapping key at the declared position")
	}
	for _, link := range chain {
		if link.mapping.Style&yaml.FlowStyle != 0 {
			return nil, errors.New("the declaration is in a flow-style mapping")
		}
	}
	// Climb while the entry is its mapping's only one and that mapping is itself a value of a key.
	last := len(chain) - 1
	for last > 0 && len(chain[last].mapping.Content) == 2 {
		last--
	}
	key := chain[last].key
	return cutLines(src, key.Line, key.Column)
}

// link is one step from the document root to a key: a mapping and the key inside it.
type link struct {
	mapping *yaml.Node
	key     *yaml.Node
}

// keyChain returns the mappings and keys from the document root down to the key at line:col, or nil.
func keyChain(n *yaml.Node, line, col int) []link {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			here := link{mapping: n, key: k}
			if k.Line == line && k.Column == col {
				return []link{here}
			}
			if below := keyChain(v, line, col); below != nil {
				return append([]link{here}, below...)
			}
		}
		return nil
	}
	for _, child := range n.Content {
		if below := keyChain(child, line, col); below != nil {
			return below
		}
	}
	return nil
}

// cutLines removes the lines of the block entry whose key is at line:col.
func cutLines(src []byte, line, col int) ([]byte, error) {
	lines := bytes.SplitAfter(src, []byte("\n"))
	if line < 1 || line > len(lines) {
		return nil, errors.New("the declared line is outside the file")
	}
	start := line - 1
	indent := col - 1
	if leadingSpaces(lines[start]) != indent {
		return nil, errors.New("the key shares its line with other syntax")
	}
	end := start + 1
	for i := start + 1; i < len(lines); i++ {
		text := strings.TrimSpace(string(lines[i]))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if leadingSpaces(lines[i]) <= indent {
			break
		}
		end = i + 1
	}
	out := bytes.Join(lines[:start], nil)
	return append(out, bytes.Join(lines[end:], nil)...), nil
}

func leadingSpaces(line []byte) int {
	return len(line) - len(bytes.TrimLeft(line, " "))
}

// verifyRemoval loads the edited project in memory. A result that no longer loads, or that still declares
// ref, is refused with its findings, and nothing is written.
func verifyRemoval(root *os.Root, rootFile, file string, data []byte, ref model.Ref) error {
	after, diags := model.Load(overlay{base: root.FS(), name: file, data: data}, rootFile, nil)
	if len(diags) > 0 {
		return application.NewError(application.ClassValidation, "remove.leaves_invalid_project",
			fmt.Sprintf("removing %s leaves the project invalid; nothing was written", ref)).
			WithFindings(diagnostics.FromModel(diags.Sorted()))
	}
	if _, still := after.Provenance(ref); still {
		return application.NewError(application.ClassValidation, "remove.not_editable",
			fmt.Sprintf("%s is declared in more than one place; delete the declarations by hand", ref))
	}
	return nil
}

// writeAtomic replaces file with data: a temporary file in the same directory with the original mode, synced,
// then renamed, all through root so a path or symlink cannot leave the project.
func writeAtomic(root *os.Root, file string, data []byte, mode fs.FileMode) error {
	tmp := path.Join(path.Dir(file), ".stackpuddle-edit-"+rand.Text())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("creating the temporary file: %w", err)
	}
	_, writeErr := f.Write(data)
	chmodErr := f.Chmod(mode) // the process umask must not narrow the original mode
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("writing the temporary file: %w", err)
	}
	if err := root.Rename(tmp, file); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", file, err)
	}
	return nil
}

// overlay serves base with one file replaced by data, so the edit is loaded without touching the disk.
type overlay struct {
	base fs.FS
	name string
	data []byte
}

func (o overlay) Open(name string) (fs.File, error) {
	if name != o.name {
		return o.base.Open(name)
	}
	return &overlayFile{Reader: bytes.NewReader(o.data), name: path.Base(name), size: int64(len(o.data))}, nil
}

type overlayFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *overlayFile) Close() error               { return nil }
func (f *overlayFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *overlayFile) Name() string               { return f.name }
func (f *overlayFile) Size() int64                { return f.size }
func (*overlayFile) Mode() fs.FileMode            { return 0o444 }
func (*overlayFile) ModTime() time.Time           { return time.Time{} }
func (*overlayFile) IsDir() bool                  { return false }
func (*overlayFile) Sys() any                     { return nil }
