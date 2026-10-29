package distribution_test

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/distribution"
	"github.com/TomazVidovic533/stackpuddle/artifacts"
)

func publish(t *testing.T) (*server, archive, archive) {
	t.Helper()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	module := buildArchive(t, artifacts.Module, moduleFiles(moduleName, "v1"))
	srv := newServer(t, map[string][]byte{"/bundle.tar.gz": bundle.Data, "/audit-store.tar.gz": module.Data})
	return srv, bundle, module
}

func TestResolveVerifiesStoresAndReturnsTheDefinition(t *testing.T) {
	t.Parallel()
	srv, bundle, module := publish(t)
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store.tar.gz", module.Digest}})
	cache := t.TempDir()
	defs, err := resolver(cache).Resolve(context.Background(), lock)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(defs) != 1 || defs[0].Name().String() != moduleName {
		t.Fatalf("definitions = %v, want audit-store", defs)
	}
	for _, digest := range []string{bundle.Digest, module.Digest} {
		path := cacheFile(cache, digest)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("the verified archive is not cached at %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("cache file mode = %o, want 0600", info.Mode().Perm())
		}
	}
	for dir := filepath.Dir(cacheFile(cache, module.Digest)); dir != cache; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("cache directory %s mode = %v (%v), want 0700", dir, info.Mode().Perm(), err)
		}
	}
}

func TestResolveUsesTheCacheWithoutTheNetwork(t *testing.T) {
	t.Parallel()
	srv, bundle, module := publish(t)
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store.tar.gz", module.Digest}})
	cache := t.TempDir()
	if _, err := resolver(cache).Resolve(context.Background(), lock); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	srv.Close()
	if _, err := resolver(cache).Resolve(context.Background(), lock); err != nil {
		t.Fatalf("a verified cache copy must resolve with the source down: %v", err)
	}
}

func TestADigestMismatchIsCompatibilityNamesTheModuleAndKeepsThePreviousCopy(t *testing.T) {
	t.Parallel()
	srv, bundle, module := publish(t)
	good := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store.tar.gz", module.Digest}})
	cache := t.TempDir()
	if _, err := resolver(cache).Resolve(context.Background(), good); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// A newer module is pinned by its real digest, but the server now serves other bytes under that name.
	newer := buildArchive(t, artifacts.Module, moduleFiles(moduleName, "v2"))
	srv.set("/audit-store-2.tar.gz", module.Data)
	bad := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store-2.tar.gz", newer.Digest}})
	_, err := resolver(cache).Resolve(context.Background(), bad)
	appErr := wantError(t, err, application.ClassCompatibility, "compatibility.digest_mismatch")
	if !strings.Contains(appErr.Error(), "module "+moduleName) {
		t.Errorf("the message must name the module: %s", appErr.Error())
	}
	if exists(cacheFile(cache, newer.Digest)) {
		t.Error("bytes that failed verification were stored in the cache")
	}
	srv.Close()
	if _, err := resolver(cache).Resolve(context.Background(), good); err != nil {
		t.Fatalf("the previously verified copy must still work: %v", err)
	}
}

func TestATamperedCacheFileIsAMissAndIsFetchedAgain(t *testing.T) {
	t.Parallel()
	srv, bundle, module := publish(t)
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store.tar.gz", module.Digest}})
	cache := t.TempDir()
	if _, err := resolver(cache).Resolve(context.Background(), lock); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	path := cacheFile(cache, module.Digest)
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := srv.count("/audit-store.tar.gz")
	if _, err := resolver(cache).Resolve(context.Background(), lock); err != nil {
		t.Fatalf("resolve over a tampered cache: %v", err)
	}
	if srv.count("/audit-store.tar.gz") != before+1 {
		t.Error("the tampered cache file was used instead of being fetched again")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, module.Data) {
		t.Errorf("the cache entry was not repaired: %v", err)
	}
}

func TestAModuleOutsideTheBundleFailsBeforeItIsFetched(t *testing.T) {
	t.Parallel()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	extra := buildArchive(t, artifacts.Module, moduleFiles("extra-store", "x"))
	srv := newServer(t, map[string][]byte{"/bundle.tar.gz": bundle.Data, "/extra.tar.gz": extra.Data})
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{"extra-store": {"0.1.0", srv.URL + "/extra.tar.gz", extra.Digest}})
	_, err := resolver(t.TempDir()).Resolve(context.Background(), lock)
	wantError(t, err, application.ClassCompatibility, "compatibility.module_not_in_bundle")
	if srv.count("/extra.tar.gz") != 0 {
		t.Error("the module outside the bundle was fetched; the check must come first")
	}
}

func TestAnArchiveWithAnotherDefinitionIsRefused(t *testing.T) {
	t.Parallel()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	other := buildArchive(t, artifacts.Module, moduleFiles("other-store", "x"))
	srv := newServer(t, map[string][]byte{"/bundle.tar.gz": bundle.Data, "/m.tar.gz": other.Data})
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/m.tar.gz", other.Digest}})
	_, err := resolver(t.TempDir()).Resolve(context.Background(), lock)
	wantError(t, err, application.ClassCompatibility, "compatibility.module_name_mismatch")
}

func TestBytesThatAreNotAnArchiveAreRefusedEvenWithAMatchingDigest(t *testing.T) {
	t.Parallel()
	garbage := []byte("not an archive")
	digest, err := artifacts.Digest(bytes.NewReader(garbage))
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(t, map[string][]byte{"/bundle.tar.gz": garbage})
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", digest, unfetched())
	cache := t.TempDir()
	_, err = resolver(cache).Resolve(context.Background(), lock)
	wantError(t, err, application.ClassCompatibility, "compatibility.archive_invalid")
	if exists(cacheFile(cache, digest)) {
		t.Error("an archive that failed inspection was cached")
	}
}

func TestAnUnreachableSourceIsUnavailableAndNamesTheModule(t *testing.T) {
	t.Parallel()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	srv := newServer(t, map[string][]byte{"/bundle.tar.gz": bundle.Data})
	module := buildArchive(t, artifacts.Module, moduleFiles(moduleName, "v1"))
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", "https://127.0.0.1:1/audit-store.tar.gz", module.Digest}})
	_, err := resolver(t.TempDir()).Resolve(context.Background(), lock)
	appErr := wantError(t, err, application.ClassUnavailable, "distribution.source_unreachable")
	if !strings.Contains(appErr.Error(), "module "+moduleName) {
		t.Errorf("the message must name the module: %s", appErr.Error())
	}
}

func TestAMissingArchiveIsUnavailable(t *testing.T) {
	t.Parallel()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	srv := newServer(t, map[string][]byte{})
	lock := lockFor(t, srv.URL+"/missing.tar.gz", bundle.Digest, unfetched())
	_, err := resolver(t.TempDir()).Resolve(context.Background(), lock)
	wantError(t, err, application.ClassUnavailable, "distribution.source_unreachable")
}

func TestARedirectOffTheLockedHostIsRefused(t *testing.T) {
	t.Parallel()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	elsewhere := newServer(t, map[string][]byte{"/bundle.tar.gz": bundle.Data})
	srv := newServer(t, nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/bundle.tar.gz", http.StatusFound)
	})
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, unfetched())
	_, err := resolver(t.TempDir()).Resolve(context.Background(), lock)
	wantError(t, err, application.ClassForbidden, "distribution.redirect_refused")
	if elsewhere.count("/bundle.tar.gz") != 0 {
		t.Error("the off-host redirect was followed")
	}
}

func TestARedirectOnTheSameHostIsFollowed(t *testing.T) {
	t.Parallel()
	srv, bundle, module := publish(t)
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/bundle.tar.gz" {
			http.Redirect(w, r, "/bundle.tar.gz", http.StatusFound)
			return
		}
		inner.ServeHTTP(w, r)
	})
	lock := lockFor(t, srv.URL+"/latest/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store.tar.gz", module.Digest}})
	if _, err := resolver(t.TempDir()).Resolve(context.Background(), lock); err != nil {
		t.Fatalf("resolve through a same-host redirect: %v", err)
	}
}

func TestAnOversizedDownloadIsRefused(t *testing.T) {
	t.Parallel()
	bundle := buildArchive(t, artifacts.Bundle, bundleFiles(moduleName))
	srv := newServer(t, map[string][]byte{"/bundle.tar.gz": make([]byte, 64<<20+16)})
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, unfetched())
	_, err := resolver(t.TempDir()).Resolve(context.Background(), lock)
	wantError(t, err, application.ClassCompatibility, "compatibility.artifact_too_large")
}

func TestCheckSourceAcceptsOnlyHTTPSAndLoopbackHTTP(t *testing.T) {
	t.Parallel()
	allowed := []string{"https://artifacts.example.test/a.tar.gz", "https://host:8443/a", "http://127.0.0.1:8080/a.tar.gz", "http://127.0.0.1/a"}
	refused := []string{
		"http://artifacts.example.test/a", "http://localhost:8080/a", "http://127.0.0.2/a", "http://[::1]:8080/a",
		"ftp://example.test/a", "file:///etc/passwd", "https://user:pass@example.test/a", "https://", "", "//example.test/a",
		"https://example.test/a#frag",
	}
	for _, raw := range allowed {
		if _, err := distribution.CheckSource(raw); err != nil {
			t.Errorf("CheckSource(%q) = %v, want it accepted", raw, err)
		}
	}
	for _, raw := range refused {
		if _, err := distribution.CheckSource(raw); err == nil {
			t.Errorf("CheckSource(%q) accepted a source the policy forbids", raw)
		}
	}
}

func TestNothingIsWrittenOutsideTheCacheTree(t *testing.T) {
	t.Parallel()
	srv, bundle, module := publish(t)
	lock := lockFor(t, srv.URL+"/bundle.tar.gz", bundle.Digest, map[string][3]string{moduleName: {"0.1.0", srv.URL + "/audit-store.tar.gz", module.Digest}})
	cache := t.TempDir()
	if _, err := resolver(cache).Resolve(context.Background(), lock); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var files []string
	_ = filepath.WalkDir(cache, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(cache, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(files) != 2 {
		t.Fatalf("cache files = %v, want the bundle and the module archive only (no temp file left behind)", files)
	}
	for _, f := range files {
		if !strings.HasPrefix(f, "stackpuddle/artifacts/sha256/") || !strings.HasSuffix(f, ".tar.gz") {
			t.Errorf("unexpected cache path %s", f)
		}
	}
}
