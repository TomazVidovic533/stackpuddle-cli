// Package distribution is the adapter that turns a project Lock into verified module definitions: it
// fetches each locked archive, checks the bytes against the Lock digest before anything reads them, keeps
// them in a cache keyed by digest and loads the definitions from memory. No path reads the stackpuddle
// source tree. It implements application.ModuleResolver.
package distribution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/artifacts"
	"github.com/TomazVidovic533/stackpuddle/model"
)

const bundleDescriptor = "bundle.yaml"

// Options configure NewResolver.
type Options struct {
	// CacheRoot returns the directory the cache lives under. It is called only when an archive is needed,
	// so a command that resolves nothing never reads the environment or creates a directory.
	CacheRoot func() (string, error)
	// Client is the HTTP client to copy; nil uses a default. Its redirect policy is always replaced.
	Client *http.Client
}

// Resolver implements application.ModuleResolver.
type Resolver struct {
	cacheRoot func() (string, error)
	client    *http.Client
}

// NewResolver returns a Resolver.
func NewResolver(opts Options) *Resolver {
	return &Resolver{cacheRoot: opts.CacheRoot, client: newClient(opts.Client)}
}

// Resolve verifies the bundle and every module of lock and returns their definitions. The bundle comes first
// and a module outside its tested set fails before any module is fetched, so nothing else runs on an
// unsupported combination.
func (r *Resolver) Resolve(ctx context.Context, lock model.Lock) ([]model.ModuleDefinition, error) {
	store, err := r.openCache()
	if err != nil {
		return nil, err
	}
	defer store.close()
	bundleBytes, err := r.obtain(ctx, store, artifacts.Bundle, "the bundle", lock.Bundle())
	if err != nil {
		return nil, err
	}
	bundleFS, err := readArchive(bundleBytes, "the bundle")
	if err != nil {
		return nil, err
	}
	bundle, diags := model.LoadBundle(bundleFS, bundleDescriptor)
	if len(diags) > 0 {
		return nil, compatibility("compatibility.bundle_invalid", "the bundle is not valid", diags)
	}
	if diags := model.CheckLock(lock, bundle); len(diags) > 0 {
		return nil, compatibility("compatibility.module_not_in_bundle", "the Lock leaves the bundle's tested set: "+messages(diags), diags)
	}
	defs := make([]model.ModuleDefinition, 0, len(lock.Modules()))
	for _, locked := range lock.Modules() {
		def, err := r.module(ctx, store, locked)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

func (r *Resolver) openCache() (*cache, error) {
	if r.cacheRoot == nil {
		return nil, application.Unexpected("distribution", fmt.Errorf("no cache root is wired"))
	}
	base, err := r.cacheRoot()
	if err != nil {
		return nil, application.NewError(application.ClassValidation, "config.invalid_setting", err.Error()).WithCause(err)
	}
	store, err := openCache(base)
	if err != nil {
		return nil, application.Unexpected("distribution", err)
	}
	return store, nil
}

func (r *Resolver) module(ctx context.Context, store *cache, locked model.LockedModule) (model.ModuleDefinition, error) {
	label := "module " + locked.Name.String()
	data, err := r.obtain(ctx, store, artifacts.Module, label, locked.LockedArtifact)
	if err != nil {
		return model.ModuleDefinition{}, err
	}
	archive, err := readArchive(data, label)
	if err != nil {
		return model.ModuleDefinition{}, err
	}
	def, diags := model.LoadModuleDefinition(archive)
	if len(diags) > 0 {
		return model.ModuleDefinition{}, compatibility("compatibility.module_invalid", label+" has no valid definition", diags)
	}
	if def.Name().String() != locked.Name.String() {
		return model.ModuleDefinition{}, application.NewError(application.ClassCompatibility, "compatibility.module_name_mismatch",
			fmt.Sprintf("%s: the archive holds the definition of %s", label, def.Name()))
	}
	return def, nil
}

// obtain returns the verified bytes of one locked archive. A cached copy is re-hashed on every use and a copy
// that fails is a miss. Fetched bytes are hashed against the Lock digest before inspection or storage; a
// mismatch is a compatibility failure that never touches the cache, which is keyed by the digest anyway.
func (r *Resolver) obtain(ctx context.Context, store *cache, kind artifacts.Kind, label string, locked model.LockedArtifact) ([]byte, error) {
	if data, ok := store.get(locked.Digest); ok && digestOf(data) == locked.Digest {
		return data, nil
	}
	data, err := fetch(ctx, r.client, locked.Source)
	if err != nil {
		if appErr, ok := errors.AsType[*application.Error](err); ok {
			return nil, application.NewError(appErr.Class(), appErr.Code(), label+": "+appErr.Error()).WithCause(err)
		}
		return nil, application.Unexpected("distribution", err)
	}
	if got := digestOf(data); got != locked.Digest {
		return nil, application.NewError(application.ClassCompatibility, "compatibility.digest_mismatch",
			fmt.Sprintf("%s: the archive from %s hashes to %s, not the %s the Lock pins", label, locked.Source, got, locked.Digest))
	}
	if err := artifacts.Inspect(kind, bytes.NewReader(data), artifacts.DefaultLimits()); err != nil {
		return nil, application.NewError(application.ClassCompatibility, "compatibility.archive_invalid",
			fmt.Sprintf("%s: the archive is not accepted: %v", label, err)).WithCause(err)
	}
	if err := store.put(locked.Digest, data); err != nil {
		return nil, application.Unexpected("distribution", err)
	}
	return data, nil
}

// digestOf returns the sha256 digest of data, or "" when hashing fails, which no in-memory reader does.
func digestOf(data []byte) string {
	digest, err := artifacts.Digest(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	return digest
}

func readArchive(data []byte, label string) (*archiveFS, error) {
	archive, err := newArchiveFS(bytes.NewReader(data), artifacts.DefaultLimits())
	if err != nil {
		return nil, application.NewError(application.ClassCompatibility, "compatibility.archive_invalid",
			label+": "+err.Error()).WithCause(err)
	}
	return archive, nil
}

func compatibility(code, message string, diags model.Diagnostics) *application.Error {
	return application.NewError(application.ClassCompatibility, code, message).
		WithFindings(diagnostics.FromModel(slices.Clone(diags).Sorted()))
}

func messages(diags model.Diagnostics) string {
	parts := make([]string, 0, len(diags))
	for _, d := range diags.Sorted() {
		parts = append(parts, d.Message())
	}
	return strings.Join(parts, "; ")
}
