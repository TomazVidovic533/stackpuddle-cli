package binary_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/artifacts"
)

// These tests run the real binary on a PATH that holds no docker, so a Docker call is observable as exit 12
// and a refusal that comes first is observable as any other exit. The engine validates packaged definitions
// only for modules it has a validator for, so a project whose module is verified and loaded but not checkable
// ends at the engine's own refusal, compatibility.project_unresolved: proof that the definitions were resolved.

type published struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
}

func publish(t *testing.T, files map[string][]byte) *published {
	t.Helper()
	p := &published{hits: map[string]int{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits[r.URL.Path]++
		p.mu.Unlock()
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *published) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits[path]
}

// runWithoutDocker runs bin in dir on a PATH with no docker and returns its exit code and output.
func runWithoutDocker(t *testing.T, bin, dir, cache string, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + t.TempDir(), "HOME=" + t.TempDir(),
		"STACKPUDDLE_STATE_DIR=" + t.TempDir(), "STACKPUDDLE_CACHE_DIR=" + cache,
		"STACKPUDDLE_SECRETS_KEY=" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), out.String(), errOut.String()
	}
	if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return 0, out.String(), errOut.String()
}

func TestAGoodLockResolvesTheModulesAndTheEngineIsReachedBeforeAnyDockerCall(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	bundle := fixture.BuildArchive(t, artifacts.Bundle, fixture.BundleFiles(fixture.ModuleName))
	module := fixture.BuildArchive(t, artifacts.Module, fixture.ModuleFiles(fixture.ModuleName, "v1"))
	srv := publish(t, map[string][]byte{"/bundle.tar.gz": bundle.Data, "/audit-store.tar.gz": module.Data})
	lock := fixture.LockYAML(srv.URL+"/bundle.tar.gz", bundle.Digest, map[string]fixture.LockedModule{
		fixture.ModuleName: {Version: "0.1.0", Source: srv.URL + "/audit-store.tar.gz", Digest: module.Digest},
	})
	dir := fixture.Dir(t, fixture.PackagedProject(lock))
	cache := t.TempDir()
	exit, _, stderr := runWithoutDocker(t, bin, dir, cache, "up", "--all")
	if exit != cli.ExitCompatibility || !strings.Contains(stderr, "compatibility.project_unresolved") {
		t.Fatalf("exit = %d, want %d from the engine after the modules resolved\n%s", exit, cli.ExitCompatibility, stderr)
	}
	// The second run needs no network: the verified copy is in the cache.
	srv.Close()
	exit, _, stderr = runWithoutDocker(t, bin, dir, cache, "up", "--all")
	if exit != cli.ExitCompatibility || !strings.Contains(stderr, "compatibility.project_unresolved") {
		t.Fatalf("second run exit = %d, want the engine's refusal again, from the cache\n%s", exit, stderr)
	}
}

func TestADigestMismatchExitsCompatibilityNamingTheModuleBeforeAnyDockerCall(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	bundle := fixture.BuildArchive(t, artifacts.Bundle, fixture.BundleFiles(fixture.ModuleName))
	module := fixture.BuildArchive(t, artifacts.Module, fixture.ModuleFiles(fixture.ModuleName, "v1"))
	other := fixture.BuildArchive(t, artifacts.Module, fixture.ModuleFiles(fixture.ModuleName, "tampered"))
	srv := publish(t, map[string][]byte{"/bundle.tar.gz": bundle.Data, "/audit-store.tar.gz": other.Data})
	lock := fixture.LockYAML(srv.URL+"/bundle.tar.gz", bundle.Digest, map[string]fixture.LockedModule{
		fixture.ModuleName: {Version: "0.1.0", Source: srv.URL + "/audit-store.tar.gz", Digest: module.Digest},
	})
	dir := fixture.Dir(t, fixture.PackagedProject(lock))
	exit, _, stderr := runWithoutDocker(t, bin, dir, t.TempDir(), "up", "--all")
	if exit != cli.ExitCompatibility || !strings.Contains(stderr, "compatibility.digest_mismatch") || !strings.Contains(stderr, "module "+fixture.ModuleName) {
		t.Fatalf("exit = %d, want %d naming the module\n%s", exit, cli.ExitCompatibility, stderr)
	}
}

func TestAModuleOutsideTheBundleFailsBeforeAnyModuleIsFetchedOrDockerIsCalled(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	bundle := fixture.BuildArchive(t, artifacts.Bundle, fixture.BundleFiles("something-else"))
	module := fixture.BuildArchive(t, artifacts.Module, fixture.ModuleFiles(fixture.ModuleName, "v1"))
	srv := publish(t, map[string][]byte{"/bundle.tar.gz": bundle.Data, "/audit-store.tar.gz": module.Data})
	lock := fixture.LockYAML(srv.URL+"/bundle.tar.gz", bundle.Digest, map[string]fixture.LockedModule{
		fixture.ModuleName: {Version: "0.1.0", Source: srv.URL + "/audit-store.tar.gz", Digest: module.Digest},
	})
	dir := fixture.Dir(t, fixture.PackagedProject(lock))
	exit, _, stderr := runWithoutDocker(t, bin, dir, t.TempDir(), "up", "--all")
	if exit != cli.ExitCompatibility || !strings.Contains(stderr, "compatibility.module_not_in_bundle") {
		t.Fatalf("exit = %d, want %d\n%s", exit, cli.ExitCompatibility, stderr)
	}
	if srv.count("/audit-store.tar.gz") != 0 {
		t.Error("the module outside the bundle was fetched")
	}
}

func TestCheckAndInspectConfigNeedNeitherTheCacheNorTheStateRoot(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	dir := fixture.Dir(t, fixture.ShopFiles(nil))
	cmd := exec.CommandContext(t.Context(), bin, "check")
	cmd.Dir = dir
	// No HOME, no cache variable, no state variable: only the lifecycle verbs may need them.
	cmd.Env = []string{"PATH=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() != cli.ExitIncomplete {
		t.Fatalf("check failed: %v\n%s", err, out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		t.Fatal("the project directory vanished")
	}
}

func TestUpWithoutDockerExitsUnavailable(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	exit, _, stderr := runWithoutDocker(t, bin, dir, t.TempDir(), "up", "--all")
	if exit != cli.ExitUnavailable || !strings.Contains(stderr, "runtime.target_unreachable") {
		t.Fatalf("up: exit = %d, want %d\n%s", exit, cli.ExitUnavailable, stderr)
	}
}
