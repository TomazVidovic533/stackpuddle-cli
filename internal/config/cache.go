package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// CacheEnv is the one variable that overrides the artifact cache root.
const CacheEnv = "STACKPUDDLE_CACHE_DIR"

// CacheRoot returns the directory the artifact cache lives under: STACKPUDDLE_CACHE_DIR when it is set and
// absolute, else the user cache directory. Both inputs are passed in, so the environment is read only at
// the composition root. A set but relative or empty override is an error rather than a silent fallback, so a
// typo never moves the cache somewhere unexpected.
func CacheRoot(lookup func(string) (string, bool), userCacheDir func() (string, error)) (string, error) {
	if value, ok := lookup(CacheEnv); ok {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path", CacheEnv)
		}
		return filepath.Clean(value), nil
	}
	dir, err := userCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache directory: set %s or fix the user cache directory: %w", CacheEnv, err)
	}
	if dir == "" {
		return "", errors.New("the user cache directory is empty; set " + CacheEnv)
	}
	return dir, nil
}
