package distribution

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	cacheDirMode  = 0o700
	cacheFileMode = 0o600
	digestPrefix  = "sha256:"
)

// cache stores verified archives under <root>/stackpuddle/artifacts/sha256/<hex>.tar.gz. The key is the
// content digest, never the version, so a name can never point at different bytes. Directories are 0700 and
// files 0600; a write is temp, fsync, rename, so a reader never sees half an archive.
type cache struct {
	root *os.Root
}

// openCache creates the cache directories under base and opens them as a confined root.
func openCache(base string) (*cache, error) {
	dir := filepath.Join(base, "stackpuddle", "artifacts", "sha256")
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return nil, fmt.Errorf("creating the cache directory %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening the cache directory %s: %w", dir, err)
	}
	return &cache{root: root}, nil
}

func (c *cache) close() { _ = c.root.Close() }

// fileName returns the cache entry name for a digest, or false when the digest is not "sha256:<64 hex>".
func fileName(digest string) (string, bool) {
	hex, ok := strings.CutPrefix(digest, digestPrefix)
	if !ok || len(hex) != 64 || strings.Trim(hex, "0123456789abcdef") != "" {
		return "", false
	}
	return hex + ".tar.gz", true
}

// get returns the cached bytes of digest; ok is false for a miss. The caller re-hashes them.
func (c *cache) get(digest string) ([]byte, bool) {
	name, ok := fileName(digest)
	if !ok {
		return nil, false
	}
	f, err := c.root.Open(name)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxArchiveBytes+1))
	if err != nil || len(data) > maxArchiveBytes {
		return nil, false
	}
	return data, true
}

// put stores data under digest, replacing a corrupt entry.
func (c *cache) put(digest string, data []byte) error {
	name, ok := fileName(digest)
	if !ok {
		return fmt.Errorf("the digest %q is not a sha256 digest", digest)
	}
	tmp := ".tmp-" + rand.Text()
	f, err := c.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, cacheFileMode)
	if err != nil {
		return fmt.Errorf("writing the cache: %w", err)
	}
	if err := writeSynced(f, data); err != nil {
		_ = c.root.Remove(tmp)
		return fmt.Errorf("writing the cache: %w", err)
	}
	if err := c.root.Rename(tmp, name); err != nil {
		_ = c.root.Remove(tmp)
		return fmt.Errorf("storing %s: %w", name, err)
	}
	return nil
}

func writeSynced(f *os.File, data []byte) error {
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}
