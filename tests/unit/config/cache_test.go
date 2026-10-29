package config_test

import (
	"errors"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

func TestCacheRoot(t *testing.T) {
	t.Parallel()
	userDir := func() (string, error) { return "/home/u/.cache", nil }
	failing := func() (string, error) { return "", errors.New("no HOME") }
	tests := []struct {
		name    string
		vars    map[string]string
		userDir func() (string, error)
		want    string
		wantErr bool
	}{
		{"override wins", map[string]string{"STACKPUDDLE_CACHE_DIR": "/srv/cache/"}, userDir, "/srv/cache", false},
		{"user cache directory", nil, userDir, "/home/u/.cache", false},
		{"relative override is refused", map[string]string{"STACKPUDDLE_CACHE_DIR": "cache"}, userDir, "", true},
		{"empty override is refused", map[string]string{"STACKPUDDLE_CACHE_DIR": ""}, userDir, "", true},
		{"no user cache directory", nil, failing, "", true},
		{"override needs no user cache directory", map[string]string{"STACKPUDDLE_CACHE_DIR": "/srv/cache"}, failing, "/srv/cache", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.CacheRoot(fixture.Lookup(tt.vars), tt.userDir)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("CacheRoot = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
