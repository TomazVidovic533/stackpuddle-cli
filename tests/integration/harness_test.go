//go:build integration

// Package integration_test runs the real stackpuddle binary against a real Docker daemon, in disposable
// Compose projects named sp-m01-045-<random>. Every project is cleaned up with `docker compose -p <project>
// down -v` only, and the suite fails by name when Docker is missing.
package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

const (
	namePrefix = "sp-m01-045-"
	// busybox is the pinned image the tests run: small, pulled through the mirror by digest.
	busybox      = "mirror.gcr.io/library/busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"
	buildBudget  = 5 * time.Minute
	stepBudget   = 90 * time.Second
	slowJobSleep = "8"
)

// binary is the stackpuddle binary TestMain builds once.
var binary string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: Docker is required and `docker info` failed: %v\n%s\n", err, out)
		return 1
	}
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: Docker Compose is required and `docker compose version` failed: %v\n%s\n", err, out)
		return 1
	}
	if out, err := exec.Command("docker", "pull", "--quiet", busybox).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: the pinned image %s could not be pulled: %v\n%s\n", busybox, err, out)
		return 1
	}
	dir, err := os.MkdirTemp("", "sp-m01-045-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	binary = filepath.Join(dir, "stackpuddle")
	ctx, cancel := context.WithTimeout(context.Background(), buildBudget)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "./cmd/stackpuddle")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: go build failed: %v\n%s\n", err, out)
		return 1
	}
	return m.Run()
}

// stack is one disposable project with its own state root, cache and Compose projects.
type stack struct {
	t       *testing.T
	name    string
	dir     string
	state   string
	cache   string
	vars    []string
	mu      sync.Mutex
	outputs []string
}

type options struct {
	// slowJob makes the migrate job sleep, so an operation stays open long enough to signal.
	slowJob bool
}

func randomName(t testing.TB) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return namePrefix + hex.EncodeToString(b)
}

// projectFiles is the sample project: two services (one with a volume), a job the api requires, a
// project-local module instance with a volume, a secret input, and two environments.
func projectFiles(name string, o options) map[string]string {
	job := `["true"]`
	if o.slowJob {
		job = `["sleep", "` + slowJobSleep + `"]`
	}
	env := func(n string) string {
		return `    ` + n + `:
      runtime:
        type: compose
        context: default
      secrets:
        provider: local
      inputs:
        api-token:
          process: SP_API_TOKEN
`
	}
	yaml := `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: ` + name + `
spec:
  inputs:
    api-token:
      sensitivity: secret
  definitions:
    kv-store:
      path: definitions/kv-store
  modules:
    primary:
      definition: kv-store
      options:
        note: primary
  workloads:
    api:
      type: service
      image: ` + busybox + `
      command: [sleep, "3600"]
      volumes:
        uploads:
          path: /data
      prerequisites:
        - job: migrate
    worker:
      type: service
      image: ` + busybox + `
      command: [sleep, "3600"]
    migrate:
      type: job
      image: ` + busybox + `
      command: ` + job + `
  applications:
    shop:
      workloads: [api, worker]
      default: true
  environments:
` + env("local") + env("other")
	return map[string]string{
		"stackpuddle.yaml":                                 yaml,
		"definitions/kv-store/module.yaml":                 kvModule,
		"definitions/kv-store/content/options.schema.json": kvSchema,
	}
}

const kvSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:stackpuddle:module:kv-store:configuration",
  "type": "object",
  "additionalProperties": false,
  "properties": { "note": { "type": "string" } }
}
`

const kvModule = `apiVersion: stackpuddle/v1
kind: Module
name: kv-store
version: 0.1.0
requires:
  capabilities: [runtime.compose/1]
configuration:
  schema: content/options.schema.json
workloads:
  store:
    type: service
    image: ` + busybox + `
    command: [sleep, "3600"]
    volumes:
      data:
        path: /var/lib/kv
`

func newStack(t *testing.T, o options) *stack {
	t.Helper()
	s := &stack{t: t, name: randomName(t), state: t.TempDir(), cache: t.TempDir()}
	s.dir = fixture.Dir(t, projectFiles(s.name, o))
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	s.vars = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "DOCKER_HOST=" + os.Getenv("DOCKER_HOST"),
		"STACKPUDDLE_STATE_DIR=" + s.state, "STACKPUDDLE_CACHE_DIR=" + s.cache, "STACKPUDDLE_SECRETS_KEY=" + key,
		"SP_API_TOKEN=" + fixture.Canary,
	}
	t.Cleanup(s.cleanup)
	return s
}

// result is one finished command.
type result struct {
	exit           int
	stdout, stderr string
}

func (s *stack) record(r result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs = append(s.outputs, r.stdout, r.stderr)
}

// command builds the binary's command in the project directory. The project declares two environments, so
// every command selects local unless the test names another with --env.
func (s *stack) command(args ...string) *exec.Cmd {
	if !slices.Contains(args, "--env") {
		args = append(slices.Clone(args), "--env", "local")
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = s.dir
	cmd.Env = s.vars
	return cmd
}

// run runs the binary to its end.
func (s *stack) run(args ...string) result {
	s.t.Helper()
	p := s.start(false, args...)
	return p.wait(s.t, stepBudget*2)
}

func (s *stack) mustRun(args ...string) result {
	s.t.Helper()
	r := s.run(args...)
	if r.exit != 0 {
		s.t.Fatalf("stackpuddle %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.exit, r.stdout, r.stderr)
	}
	return r
}

func (s *stack) json(args ...string) map[string]any {
	s.t.Helper()
	r := s.mustRun(append(args, "--output", "json")...)
	return parseJSON(s.t, r.stdout)
}

func parseJSON(t testing.TB, text string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("not a JSON document: %v\n%s", err, text)
	}
	return doc
}

// proc is a running command with its output captured as it arrives.
type proc struct {
	s      *stack
	cmd    *exec.Cmd
	mu     sync.Mutex
	stdout bytes.Buffer
	stderr bytes.Buffer
	done   chan struct{}
	exit   int
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// start launches the binary. With group set it runs in its own process group, so a test can signal the
// whole group as a terminal's Ctrl-C does.
func (s *stack) start(group bool, args ...string) *proc {
	s.t.Helper()
	p := &proc{s: s, cmd: s.command(args...), done: make(chan struct{})}
	p.cmd.Stdout = lockedWriter{&p.mu, &p.stdout}
	p.cmd.Stderr = lockedWriter{&p.mu, &p.stderr}
	if group {
		p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := p.cmd.Start(); err != nil {
		s.t.Fatalf("start %v: %v", args, err)
	}
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		defer p.mu.Unlock()
		p.exit = 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			p.exit = exitErr.ExitCode()
			if p.exit == -1 {
				p.exit = 128 + int(exitErr.ProcessState.Sys().(syscall.WaitStatus).Signal())
			}
		} else if err != nil {
			p.exit = 127
		}
		close(p.done)
	}()
	s.t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		<-p.done
	})
	return p
}

func (p *proc) wait(t testing.TB, budget time.Duration) result {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(budget):
		t.Fatalf("the command did not finish within %s\nstdout: %s\nstderr: %s", budget, p.out(), p.err())
	}
	p.mu.Lock()
	r := result{exit: p.exit, stdout: p.stdout.String(), stderr: p.stderr.String()}
	p.mu.Unlock()
	p.s.record(r)
	return r
}

func (p *proc) out() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdout.String()
}

func (p *proc) err() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

// waitStderr waits until the command's stderr holds substr and returns the text after it up to the next
// whitespace, which is how a test reads the operation ID off "accepted op-…".
func (p *proc) waitStderr(t testing.TB, substr string) string {
	t.Helper()
	deadline := time.Now().Add(stepBudget)
	for time.Now().Before(deadline) {
		text := p.err()
		if i := strings.Index(text, substr); i >= 0 {
			rest := text[i+len(substr):]
			if end := strings.IndexAny(rest, " ;\n"); end >= 0 {
				return rest[:end]
			}
		}
		select {
		case <-p.done:
			t.Fatalf("the command ended before printing %q\nstdout: %s\nstderr: %s", substr, p.out(), p.err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("%q never appeared on stderr:\n%s", substr, p.err())
	return ""
}

// docker runs the docker CLI and returns its trimmed output.
func docker(t testing.TB, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	sort.Strings(out)
	return out
}

// containers lists this project's containers by name, running ones only when running is set.
func (s *stack) containers(running bool) []string {
	s.t.Helper()
	args := []string{"ps", "-a", "--filter", "name=" + s.name, "--format", "{{.Names}}"}
	if running {
		args = []string{"ps", "--filter", "name=" + s.name, "--filter", "status=running", "--format", "{{.Names}}"}
	}
	return lines(docker(s.t, args...))
}

func (s *stack) volumes() []string {
	s.t.Helper()
	return lines(docker(s.t, "volume", "ls", "--filter", "name="+s.name, "--format", "{{.Name}}"))
}

func (s *stack) composeProjects() string {
	s.t.Helper()
	return docker(s.t, "compose", "ls", "-a", "--filter", "name="+s.name, "--format", "json")
}

// containsName reports whether any name holds the fragment, such as the compose service "migrate".
func containsName(names []string, fragment string) bool {
	for _, n := range names {
		if strings.Contains(n, fragment) {
			return true
		}
	}
	return false
}

// waitRunning waits until a container whose name holds fragment is running.
func (s *stack) waitRunning(fragment string) {
	s.t.Helper()
	deadline := time.Now().Add(stepBudget)
	for time.Now().Before(deadline) {
		if containsName(s.containers(true), fragment) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("no running container named like %q: %v", fragment, s.containers(false))
}

// snapshot is everything a dry run must leave unchanged: the state bytes, `docker compose ls` and the
// volume and container lists of the project.
type snapshot struct {
	state      string
	compose    string
	volumes    string
	containers string
}

func (s *stack) snapshot() snapshot {
	s.t.Helper()
	return snapshot{
		state: hashTree(s.t, s.state), compose: s.composeProjects(),
		volumes: strings.Join(s.volumes(), ","), containers: strings.Join(s.containers(false), ","),
	}
}

func hashTree(t testing.TB, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s|%v|", rel, info.Mode())
		if !d.IsDir() {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", root, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// composeProject returns the Compose project of an environment, read from `inspect --view state`.
func (s *stack) composeProject(env string) string {
	out, err := s.command("inspect", "env/"+env, "--view", "state", "--output", "json", "--env", env).Output()
	if err != nil {
		return ""
	}
	var doc struct {
		ComposeProject string `json:"composeProject"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return ""
	}
	return doc.ComposeProject
}

// cleanup removes this project's runtime objects with `docker compose -p <project> down -v` and nothing
// else, then fails the test if anything carrying the project name is left or a secret leaked.
func (s *stack) cleanup() {
	for _, env := range []string{"local", "other"} {
		project := s.composeProject(env)
		if project == "" {
			continue
		}
		down := exec.Command("docker", "compose", "-p", project, "down", "-v", "--timeout", "1")
		if out, err := down.CombinedOutput(); err != nil {
			s.t.Errorf("cleanup: docker compose -p %s down -v: %v\n%s", project, err, out)
		}
	}
	if left := s.containers(false); len(left) > 0 {
		s.t.Errorf("cleanup left containers: %v", left)
	}
	if left := s.volumes(); len(left) > 0 {
		s.t.Errorf("cleanup left volumes: %v", left)
	}
	s.mu.Lock()
	outputs := append([]string(nil), s.outputs...)
	s.mu.Unlock()
	for i, out := range outputs {
		if strings.Contains(out, fixture.Canary) {
			s.t.Errorf("a canary secret value appeared in output %d:\n%s", i, out)
		}
	}
	_ = filepath.WalkDir(s.state, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if data, readErr := os.ReadFile(path); readErr == nil && bytes.Contains(data, []byte(fixture.Canary)) {
			s.t.Errorf("the canary secret value is stored in plaintext in %s", path)
		}
		return nil
	})
}
