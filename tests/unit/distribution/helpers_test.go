package distribution_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/distribution"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/artifacts"
	"github.com/TomazVidovic533/stackpuddle/model"
)

const moduleName = fixture.ModuleName

// archive is one built archive and its digest.
type archive = fixture.Archive

func buildArchive(t *testing.T, kind artifacts.Kind, files map[string]string) archive {
	t.Helper()
	return fixture.BuildArchive(t, kind, files)
}

func moduleFiles(name, note string) map[string]string { return fixture.ModuleFiles(name, note) }

func bundleFiles(modules ...string) map[string]string { return fixture.BundleFiles(modules...) }

// server serves archives by path and counts the requests per path.
type server struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
}

func newServer(t *testing.T, files map[string][]byte) *server {
	t.Helper()
	s := &server{files: files, hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		data, ok := s.files[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) set(path string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = data
}

func (s *server) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// lockFor writes a Lock for the given sources and digests and loads it through the model.
func lockFor(t *testing.T, bundleSource, bundleDigest string, modules map[string][3]string) model.Lock {
	t.Helper()
	locked := map[string]fixture.LockedModule{}
	for name, m := range modules { // m = version, source, digest
		locked[name] = fixture.LockedModule{Version: m[0], Source: m[1], Digest: m[2]}
	}
	text := fixture.LockYAML(bundleSource, bundleDigest, locked)
	lock, diags := model.LoadLock(fixture.MapFS(map[string]string{"stackpuddle.lock.yaml": text}), "stackpuddle.lock.yaml")
	if len(diags) > 0 {
		t.Fatalf("the test Lock is invalid: %v\n%s", diags, text)
	}
	return lock
}

func resolver(cache string) *distribution.Resolver {
	return distribution.NewResolver(distribution.Options{CacheRoot: func() (string, error) { return cache, nil }})
}

func wantError(t *testing.T, err error, class application.Class, code string) *application.Error {
	t.Helper()
	appErr, ok := errors.AsType[*application.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *application.Error %s/%s", err, class, code)
	}
	if appErr.Class() != class || appErr.Code() != code {
		t.Fatalf("err = %s/%s (%v), want %s/%s", appErr.Class(), appErr.Code(), appErr, class, code)
	}
	return appErr
}

func cacheFile(cache, digest string) string {
	return filepath.Join(cache, "stackpuddle", "artifacts", "sha256", strings.TrimPrefix(digest, "sha256:")+".tar.gz")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// unfetched is a module entry no test ever fetches: the bundle fails first.
func unfetched() map[string][3]string {
	return map[string][3]string{moduleName: {"0.1.0", "https://127.0.0.1:1/unfetched.tar.gz", "sha256:" + strings.Repeat("b", 64)}}
}
