package distribution

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/TomazVidovic533/stackpuddle/artifacts"
)

// archiveFS is a read-only fs.FS over an archive that artifacts.Inspect already accepted. Nothing is extracted
// to disk: entries are read into memory within the inspection limits, so no path from the archive ever
// touches the filesystem.
type archiveFS struct {
	files map[string][]byte
	dirs  map[string]bool
}

// newArchiveFS reads the entries of a gzip-compressed tar stream. It re-applies the file and total size
// limits while reading, so it never trusts the inspection's numbers alone.
func newArchiveFS(r io.Reader, limits artifacts.Limits) (*archiveFS, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("opening the archive: %w", err)
	}
	defer gz.Close()
	out := &archiveFS{files: map[string][]byte{}, dirs: map[string]bool{".": true}}
	reader := tar.NewReader(gz)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the archive: %w", err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !fs.ValidPath(name) {
			return nil, fmt.Errorf("the archive holds an unsafe path %q", name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			out.addDirs(name)
		case tar.TypeReg:
			data, err := io.ReadAll(io.LimitReader(reader, limits.FileBytes+1))
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", name, err)
			}
			total += int64(len(data))
			if int64(len(data)) > limits.FileBytes || total > limits.TotalBytes {
				return nil, fmt.Errorf("the archive exceeds its size limits at %s", name)
			}
			out.addDirs(path.Dir(name))
			out.files[name] = data
		default:
			return nil, fmt.Errorf("the archive holds %s of an unsupported type", name)
		}
	}
}

// addDirs records name and its parents as directories.
func (a *archiveFS) addDirs(name string) {
	for name != "." && name != "" {
		a.dirs[name] = true
		name = path.Dir(name)
	}
}

// Open implements fs.FS.
func (a *archiveFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if data, ok := a.files[name]; ok {
		return &memFile{info: memInfo{name: path.Base(name), size: int64(len(data))}, data: data}, nil
	}
	if a.dirs[name] {
		return &memDir{info: memInfo{name: path.Base(name), dir: true}, entries: a.list(name)}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ReadFile implements fs.ReadFileFS.
func (a *archiveFS) ReadFile(name string) ([]byte, error) {
	if data, ok := a.files[name]; ok && fs.ValidPath(name) {
		return append([]byte(nil), data...), nil
	}
	return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrNotExist}
}

// list returns the direct children of dir, sorted by name.
func (a *archiveFS) list(dir string) []fs.DirEntry {
	seen := map[string]fs.DirEntry{}
	for name, data := range a.files {
		if path.Dir(name) == dir {
			seen[path.Base(name)] = memInfo{name: path.Base(name), size: int64(len(data))}
		}
	}
	for name := range a.dirs {
		if name != "." && path.Dir(name) == dir {
			seen[path.Base(name)] = memInfo{name: path.Base(name), dir: true}
		}
	}
	entries := make([]fs.DirEntry, 0, len(seen))
	for _, e := range seen {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries
}

// memInfo is both the fs.FileInfo and the fs.DirEntry of one in-memory entry.
type memInfo struct {
	name string
	size int64
	dir  bool
}

func (i memInfo) Name() string { return i.name }
func (i memInfo) Size() int64  { return i.size }
func (i memInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (memInfo) ModTime() time.Time           { return time.Time{} }
func (i memInfo) IsDir() bool                { return i.dir }
func (memInfo) Sys() any                     { return nil }
func (i memInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i memInfo) Info() (fs.FileInfo, error) { return i, nil }

type memFile struct {
	info   memInfo
	data   []byte
	offset int
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Close() error               { return nil }
func (f *memFile) Read(p []byte) (int, error) {
	if f.offset >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.offset:])
	f.offset += n
	return n, nil
}

type memDir struct {
	info    memInfo
	entries []fs.DirEntry
	offset  int
}

func (d *memDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *memDir) Close() error               { return nil }
func (d *memDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: errors.New("is a directory")}
}

// ReadDir implements fs.ReadDirFile.
func (d *memDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.offset:]
	if n <= 0 {
		d.offset = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.offset += n
	return rest[:n], nil
}
