// Package testsupport provides mocks and helpers for testing the CLI without Docker or network access.
package testsupport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joe-p/algokit-cli-go/internal/commands"
	"github.com/joe-p/algokit-cli-go/internal/conf"
	"github.com/joe-p/algokit-cli-go/internal/httpx"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/proc"
	"github.com/joe-p/algokit-cli-go/internal/prompt"
	"github.com/joe-p/algokit-cli-go/internal/sandbox"
)

// CommandMock is the mocked behaviour of a command.
type CommandMock struct {
	ExitCode   int
	Output     []string
	NotFound   bool
	SideEffect func()
}

// Call is a recorded process execution.
type Call struct {
	Command     []string
	Dir         string
	Interactive bool
}

// String returns the command line.
func (c Call) String() string { return strings.Join(c.Command, " ") }

// ProcMock replaces process execution; commands are matched by (longest) prefix, like the Python test-suite's
// proc_mock. Unmatched commands succeed with the output "STDOUT", "STDERR".
type ProcMock struct {
	mu    sync.Mutex
	mocks map[string]CommandMock
	Calls []Call
}

var defaultOutput = []string{"STDOUT", "STDERR"}

// Set mocks the output and exit code of commands starting with prefix.
func (m *ProcMock) Set(prefix string, output []string, exitCode int, sideEffect ...func()) {
	mock := CommandMock{ExitCode: exitCode, Output: output}
	if len(sideEffect) > 0 {
		mock.SideEffect = sideEffect[0]
	}
	m.mocks[prefix] = mock
}

// BadExit mocks commands starting with prefix to exit with code 1 (and the default output).
func (m *ProcMock) BadExit(prefix string) { m.Set(prefix, defaultOutput, 1) }

// NotFound mocks commands starting with prefix to fail to start (as if the executable doesn't exist).
func (m *ProcMock) NotFound(prefix string) {
	m.mocks[prefix] = CommandMock{ExitCode: -1, NotFound: true}
}

func (m *ProcMock) lookup(command string) CommandMock {
	best, found := "", false
	for prefix := range m.mocks {
		if (command == prefix || strings.HasPrefix(command, prefix+" ")) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	if !found {
		return CommandMock{Output: defaultOutput}
	}
	return m.mocks[best]
}

func (m *ProcMock) execute(spec proc.Spec) (int, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Command: spec.Args, Dir: spec.Dir, Interactive: spec.Interactive})
	mock := m.lookup(strings.Join(spec.Args, " "))
	m.mu.Unlock()
	if mock.NotFound {
		return -1, &exec.Error{Name: spec.Args[0], Err: exec.ErrNotFound}
	}
	if mock.SideEffect != nil {
		mock.SideEffect()
	}
	if !spec.Interactive {
		for _, line := range mock.Output {
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			_, _ = io.WriteString(spec.Stdout, line)
		}
	}
	return mock.ExitCode, nil
}

// Commands returns the command lines executed so far.
func (m *ProcMock) Commands() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var commands []string
	for _, call := range m.Calls {
		commands = append(commands, call.String())
	}
	return commands
}

// LastCall returns the most recent process execution.
func (m *ProcMock) LastCall() Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Calls[len(m.Calls)-1]
}

type httpResponse struct {
	status int
	body   any
	err    error
}

// HTTPMock replaces HTTP requests; responses are matched by exact URL and consumed in order (the last one is
// reused). Unmatched requests fail with a connection error.
type HTTPMock struct {
	mu        sync.Mutex
	responses map[string][]httpResponse
	Requests  []*http.Request
}

// Add mocks a JSON response for url.
func (m *HTTPMock) Add(url string, body any, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[url] = append(m.responses[url], httpResponse{status: status, body: body})
}

// AddError mocks a connection error for url.
func (m *HTTPMock) AddError(url string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[url] = append(m.responses[url], httpResponse{err: err})
}

func (m *HTTPMock) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Requests = append(m.Requests, req)
	url := req.URL.String()
	list := m.responses[url]
	if len(list) == 0 {
		return nil, fmt.Errorf("connection refused: no mock response registered for %s", url)
	}
	response := list[0]
	if len(list) > 1 {
		m.responses[url] = list[1:]
	}
	if response.err != nil {
		return nil, response.err
	}
	body, err := json.Marshal(response.body)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: response.status,
		Status:     fmt.Sprintf("%d %s", response.status, http.StatusText(response.status)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

// ConfirmCall is a recorded confirmation prompt.
type ConfirmCall struct {
	Message string
	Default bool
}

// Env is an isolated test environment: temporary config/state directories and mocked processes, HTTP and prompts.
type Env struct {
	t    *testing.T
	Root string
	Proc *ProcMock
	HTTP *HTTPMock
	// ConfirmAnswer is the answer given to confirmation prompts.
	ConfirmAnswer bool
	ConfirmCalls  []ConfirmCall
}

// Setup creates an isolated test environment, restored when the test completes.
func Setup(t *testing.T) *Env {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := &Env{
		t:             t,
		Root:          root,
		Proc:          &ProcMock{mocks: map[string]CommandMock{}},
		HTTP:          &HTTPMock{responses: map[string][]httpResponse{}},
		ConfirmAnswer: true,
	}
	home, configHome, stateHome := filepath.Join(root, "home"), filepath.Join(root, "config"), filepath.Join(root, "state")
	for _, dir := range []string{home, configHome, stateHome} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME": configHome, "APPDATA": configHome,
		"XDG_STATE_HOME": stateHome, "LOCALAPPDATA": stateHome,
		"NO_COLOR": "", "ALGOKIT_LOCALNET_CONFIG_DIR": "", "COLUMNS": "80",
	} {
		t.Setenv(name, value)
	}

	previousExecutor, previousClient := proc.Executor, httpx.Client
	previousConfirm, previousTimings := prompt.Confirm, sandbox.HealthCheckTimings
	proc.Executor = env.Proc.execute
	httpx.Client = &http.Client{Transport: env.HTTP}
	prompt.Confirm = func(message string, defaultValue bool) (bool, error) {
		env.ConfirmCalls = append(env.ConfirmCalls, ConfirmCall{message, defaultValue})
		return env.ConfirmAnswer, nil
	}
	sandbox.HealthCheckTimings.AlgodWait = 100 * time.Millisecond
	sandbox.HealthCheckTimings.IndexerWait = 100 * time.Millisecond
	sandbox.HealthCheckTimings.HealthTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		proc.Executor, httpx.Client = previousExecutor, previousClient
		prompt.Confirm, sandbox.HealthCheckTimings = previousConfirm, previousTimings
		log.Reset()
	})
	return env
}

// AppConfigDir returns the (test) app config directory.
func (e *Env) AppConfigDir() string { return conf.AppConfigDir() }

// AppStateDir returns the (test) app state directory.
func (e *Env) AppStateDir() string { return conf.AppStateDir() }

// SandboxDir returns (creating it if needed) the directory for the named sandbox, e.g. "sandbox" or "sandbox_test".
func (e *Env) SandboxDir(name string) string {
	dir := filepath.Join(e.AppConfigDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

// WriteFile writes a file, failing the test on error.
func (e *Env) WriteFile(path, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// ReadFile reads a file, failing the test on error.
func (e *Env) ReadFile(path string) string {
	e.t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(content)
}

// WriteLatestCompose writes the latest LocalNet definition for the named sandbox.
func (e *Env) WriteLatestCompose(name string) {
	dir := e.SandboxDir(name)
	e.WriteFile(filepath.Join(dir, "docker-compose.yml"), sandbox.DockerComposeYAML("algokit_"+name))
	e.WriteFile(filepath.Join(dir, "algod_config.json"), sandbox.ConfigJSON())
	e.WriteFile(filepath.Join(dir, "algod_network_template.json"), sandbox.AlgodNetworkTemplate())
}

const digestArg = "{{range .RepoDigests}}{{println .}}{{end}}"

var (
	algodDigest   = "sha256:" + strings.Repeat("a", 64)
	indexerDigest = "sha256:" + strings.Repeat("b", 64)
)

// MockComposeVersion mocks `docker compose version` (equivalent to the Python conftest proc_mock defaults).
func (e *Env) MockComposeVersion(version string) {
	e.Proc.Set("docker compose version --format json", []string{fmt.Sprintf(`{"version": "%s"}`, version)}, 0)
}

// MockHealthSuccess mocks successful LocalNet health checks.
func (e *Env) MockHealthSuccess() {
	e.HTTP.Add(sandbox.AlgodHealthURL, map[string]any{}, 200)
	e.HTTP.Add(sandbox.IndexerHealthURL, map[string]any{}, 200)
}

func (e *Env) mockImageVersions(latestAlgod, latestIndexer string) {
	e.Proc.Set("docker image inspect "+sandbox.AlgorandImage+" --format "+digestArg, []string{"tag@" + algodDigest}, 0)
	e.Proc.Set("docker image inspect "+sandbox.IndexerImage+" --format "+digestArg, []string{"tag@" + indexerDigest}, 0)
	e.HTTP.Add("https://registry.hub.docker.com/v2/repositories/algorand/indexer/tags/latest", map[string]any{"digest": latestIndexer}, 200)
	e.HTTP.Add("https://registry.hub.docker.com/v2/repositories/algorand/algod/tags/latest", map[string]any{"digest": latestAlgod}, 200)
}

// MockLocalnetUpToDate mocks the local LocalNet images matching the latest on Docker Hub.
func (e *Env) MockLocalnetUpToDate() { e.mockImageVersions(algodDigest, indexerDigest) }

// MockLocalnetOutOfDate mocks newer LocalNet images being available on Docker Hub.
func (e *Env) MockLocalnetOutOfDate() {
	e.mockImageVersions("sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("c", 64))
}

// MockRunningLocalnet mocks `docker compose ls` reporting the named sandbox (in the app config dir) as running.
func (e *Env) MockRunningLocalnet(name string) {
	composeFile, _ := json.Marshal(filepath.Join(e.AppConfigDir(), name, "docker-compose.yml"))
	e.Proc.Set("docker compose ls --format json --filter name=algokit_sandbox*", []string{
		fmt.Sprintf(`[{"Name": "algokit_%s", "Status": "running", "ConfigFiles": %s}]`, name, composeFile),
	}, 0)
}

// MockNoRunningLocalnet mocks `docker compose ls` reporting no running LocalNet.
func (e *Env) MockNoRunningLocalnet() {
	e.Proc.Set("docker compose ls --format json --filter name=algokit_sandbox*", []string{"[]"}, 0)
}

// MockAlgodRunning mocks `docker compose ps algod` reporting algod as running.
func (e *Env) MockAlgodRunning(name string) {
	e.Proc.Set("docker compose ps algod --format json", []string{
		fmt.Sprintf(`[{"Name": "algokit_%s_algod", "State": "running"}]`, name),
	}, 0)
}

// Result is the outcome of invoking the CLI.
type Result struct {
	ExitCode int
	Output   string
}

// Invoke runs the CLI (with -v --no-color, as the Python test-suite does) in cwd (if not empty), capturing stdout
// and stderr, and normalising paths in the output to {app_config} and {current_working_directory}.
func (e *Env) Invoke(cwd string, args ...string) Result {
	e.t.Helper()
	return e.InvokeRaw(cwd, append([]string{"-v", "--no-color"}, args...)...)
}

// InvokeRaw is like Invoke, but without adding any arguments.
func (e *Env) InvokeRaw(cwd string, args ...string) Result {
	e.t.Helper()
	if cwd != "" {
		e.t.Chdir(cwd)
	}
	var buf syncBuffer
	restore := log.SetOutput(&buf, &buf, false)
	defer restore()
	code := commands.Main(args)
	actualCwd, _ := os.Getwd()
	output := strings.ReplaceAll(buf.String(), e.AppConfigDir(), "{app_config}")
	output = strings.ReplaceAll(output, actualCwd, "{current_working_directory}")
	if runtime.GOOS == "windows" {
		output = strings.ReplaceAll(output, `\`, "/")
	}
	return Result{ExitCode: code, Output: output}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// PythonFixture returns an approved output file from the Python AlgoKit CLI's test-suite.
func PythonFixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	content, err := os.ReadFile(filepath.Join(filepath.Dir(file), "testdata", "python", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
