package commands_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joe-p/algokit-cli-go/internal/sandbox"
	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

func setupLocalnet(t *testing.T) *testsupport.Env {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5.0")
	return env
}

func assertContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Errorf("output doesn't contain %q:\n%s", want, output)
	}
}

func assertNotContains(t *testing.T, output, unwanted string) {
	t.Helper()
	if strings.Contains(output, unwanted) {
		t.Errorf("output contains %q:\n%s", unwanted, output)
	}
}

func assertRan(t *testing.T, env *testsupport.Env, command string, want bool) {
	t.Helper()
	if slices.Contains(env.Proc.Commands(), command) != want {
		t.Errorf("ran %q = %v, want %v; commands: %v", command, !want, want, env.Proc.Commands())
	}
}

func readCache(t *testing.T, env *testsupport.Env) map[string]bool {
	t.Helper()
	var cache map[string]bool
	content := env.ReadFile(filepath.Join(env.AppStateDir(), "last-localnet-version-check"))
	if err := json.Unmarshal([]byte(content), &cache); err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestLocalnetGroupChecksFail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(env *testsupport.Env)
		message string
	}{
		{"missing compose", func(env *testsupport.Env) { env.Proc.BadExit("docker compose version --format json") },
			"Container engine compose not found"},
		{"engine not running", func(env *testsupport.Env) { env.Proc.BadExit("docker version") },
			"Container engine isn't running"},
		{"old compose", func(env *testsupport.Env) { env.MockComposeVersion("v2.2.1") },
			"Minimum compose version supported: v2.5.0, installed = v2.2.1\nPlease update your compose install"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupLocalnet(t)
			tc.setup(env)
			result := env.Invoke("", "localnet", "start")
			assertExitCode(t, result, 1)
			assertContains(t, result.Output, "Error: "+tc.message)
		})
	}
}

func TestLocalnetAcceptsGitpodComposeVersion(t *testing.T) {
	env := setupLocalnet(t)
	env.MockComposeVersion("v2.10.0-gitpod.0")
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()
	assertExitCode(t, env.Invoke("", "localnet", "start"), 0)
}

func TestLocalnetPodmanMinimumComposeVersion(t *testing.T) {
	env := setupLocalnet(t)
	env.WriteFile(filepath.Join(env.AppConfigDir(), "active-container-engine"), "podman")
	env.Proc.Set("podman compose version --format json", []string{`{"version": "1.0.5"}`}, 0)
	result := env.Invoke("", "localnet", "start")
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Minimum compose version supported: v1.0.6, installed = v1.0.5")
}

func TestLocalnetConfigSkipsEngineRunningCheck(t *testing.T) {
	env := setupLocalnet(t)
	env.Proc.BadExit("docker version")
	env.MockNoRunningLocalnet()
	assertExitCode(t, env.Invoke("", "localnet", "config", "podman"), 0)
	if got := env.ReadFile(filepath.Join(env.AppConfigDir(), "active-container-engine")); got != "podman" {
		t.Errorf("active-container-engine = %q", got)
	}
}

func TestLocalnetHelpDoesntNeedContainerEngine(t *testing.T) {
	env := setupLocalnet(t)
	env.Proc.NotFound("docker")
	result := env.Invoke("", "localnet", "start", "--help")
	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Usage: algokit localnet start [OPTIONS]")
	if len(env.Proc.Calls) != 0 {
		t.Errorf("unexpected commands: %v", env.Proc.Commands())
	}
}

func TestLocalnetStartWritesComposeFiles(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()

	assertExitCode(t, env.Invoke("", "localnet", "start"), 0)

	// goal_mount is created before `compose up`, so Docker doesn't create it owned by root
	for _, file := range []string{"docker-compose.yml", "algod_config.json", "algod_network_template.json", "conduit.yml", "goal_mount"} {
		if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox", file)); err != nil {
			t.Error(err)
		}
	}
	assertRan(t, env, "docker compose up --detach --quiet-pull --wait", true)
}

func TestLocalnetStartOmitsWaitForPodman(t *testing.T) {
	env := setupLocalnet(t)
	env.WriteFile(filepath.Join(env.AppConfigDir(), "active-container-engine"), "podman")
	env.Proc.Set("podman compose version --format json", []string{`{"version": "1.0.6"}`}, 0)
	env.Proc.Set("podman compose ls", []string{"[]"}, 0)
	env.MockHealthSuccess()

	assertExitCode(t, env.Invoke("", "localnet", "start"), 0)
	assertRan(t, env, "podman compose up --detach --quiet-pull", true)
}

func TestLocalnetStartNamed(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()

	result := env.Invoke("", "localnet", "start", "--name", "test")

	assertExitCode(t, result, 0)
	composeYML := env.ReadFile(filepath.Join(env.AppConfigDir(), "sandbox_test", "docker-compose.yml"))
	if composeYML != sandbox.DockerComposeYAML("algokit_sandbox_test") {
		t.Error("unexpected compose file for named LocalNet")
	}
	assertContains(t, result.Output, "The named LocalNet configuration has been created in {app_config}/sandbox_test.")
	assertContains(t, result.Output, "A named LocalNet is running, update checks are disabled.")
}

func TestLocalnetStartStopsDifferentRunningLocalnet(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()

	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "other"), 0)

	want := testsupport.ConfirmCall{Message: "This will stop any running AlgoKit LocalNet instance. Are you sure?", Default: true}
	if !slices.Contains(env.ConfirmCalls, want) {
		t.Errorf("confirm calls = %v", env.ConfirmCalls)
	}
	stopped := false
	for _, call := range env.Proc.Calls {
		if call.String() == "docker compose stop" && call.Dir == filepath.Join(env.AppConfigDir(), "sandbox") {
			stopped = true
		}
	}
	if !stopped {
		t.Errorf("default sandbox wasn't stopped: %v", env.Proc.Calls)
	}
}

func TestLocalnetStartDecliningToStopErrors(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	env.ConfirmAnswer = false

	result := env.Invoke("", "localnet", "start", "--name", "other")

	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: LocalNet is already running. Please stop it first")
}

func TestLocalnetStartWarnsWhenOutOfDate(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()
	dir := env.SandboxDir("sandbox")
	for _, file := range []string{"docker-compose.yml", "algod_config.json", "algod_network_template.json"} {
		env.WriteFile(filepath.Join(dir, file), "out of date config")
	}

	result := env.Invoke("", "localnet", "start")

	assertContains(t, result.Output, "WARNING: LocalNet definition is out of date; please run `algokit localnet reset`")
}

func TestLocalnetStartComposeWithoutAlgodConfigIsOutOfDate(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml"), "out of date config")

	result := env.Invoke("", "localnet", "start")

	assertContains(t, result.Output, "LocalNet definition is out of date")
}

func TestLocalnetStartWarnsAboutNewImages(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetOutOfDate()
	env.MockHealthSuccess()

	result := env.Invoke("", "localnet", "start")

	assertContains(t, result.Output, "WARNING: indexer has a new version available")
	assertContains(t, result.Output, "WARNING: algod has a new version available")
	if cache := readCache(t, env); !cache["indexer_outdated"] || !cache["algod_outdated"] {
		t.Errorf("cache = %v", cache)
	}
	// the cache file is formatted like Python's json.dumps, since it's shared with the Python CLI
	if got := env.ReadFile(filepath.Join(env.AppStateDir(), "last-localnet-version-check")); got != `{"indexer_outdated": true, "algod_outdated": true}` {
		t.Errorf("cache file = %s", got)
	}
}

func TestLocalnetStartUsesFreshImageVersionCache(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	env.WriteFile(filepath.Join(env.AppStateDir(), "last-localnet-version-check"), `{"indexer_outdated": true, "algod_outdated": false}`)
	inspected := func() bool {
		return slices.ContainsFunc(env.Proc.Commands(), func(c string) bool { return strings.HasPrefix(c, "docker image inspect") })
	}

	cached := env.Invoke("", "localnet", "start")
	assertContains(t, cached.Output, "Skipping image version check")
	assertContains(t, cached.Output, "indexer has a new version available")
	if inspected() {
		t.Error("images were inspected despite a fresh cache")
	}

	env.MockLocalnetUpToDate()
	checked := env.Invoke("", "localnet", "start", "--check")
	assertNotContains(t, checked.Output, "new version available")
	if !inspected() {
		t.Error("images weren't inspected with --check")
	}
}

func TestLocalnetStartRechecksExpiredImageVersionCache(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	env.MockLocalnetUpToDate()
	cachePath := filepath.Join(env.AppStateDir(), "last-localnet-version-check")
	env.WriteFile(cachePath, `{"indexer_outdated": true, "algod_outdated": true}`)
	eightDaysAgo := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(cachePath, eightDaysAgo, eightDaysAgo); err != nil {
		t.Fatal(err)
	}

	result := env.Invoke("", "localnet", "start")

	assertContains(t, result.Output, "Image version cache expired, will check for updates")
	assertNotContains(t, result.Output, "new version available")
}

func TestLocalnetStartIgnoresImageCheckErrors(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	env.Proc.Set("docker image inspect", []string{"STDOUT", "STDERR"}, -1)
	assertExitCode(t, env.Invoke("", "localnet", "start"), 0)
}

func TestLocalnetStartNoDevRestarts(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()
	env.WriteLatestCompose("sandbox")

	result := env.Invoke("", "localnet", "start", "--no-dev")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Refreshed 'DevMode' flag to 'False'")
	template := env.ReadFile(filepath.Join(env.AppConfigDir(), "sandbox", "algod_network_template.json"))
	assertContains(t, template, `"DevMode": false`)
	assertRan(t, env, "docker compose down", true)
	assertRan(t, env, "docker compose up --detach --quiet-pull --wait", true)
}

func TestLocalnetStartDevForceDoesntPromptOrRestart(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.WriteLatestCompose("sandbox")
	templatePath := filepath.Join(env.SandboxDir("sandbox"), "algod_network_template.json")
	env.WriteFile(templatePath, strings.Replace(env.ReadFile(templatePath), `"DevMode": true`, `"DevMode": false`, 1))

	result := env.Invoke("", "localnet", "start", "--dev", "--force")

	assertExitCode(t, result, 0)
	if len(env.ConfirmCalls) != 0 {
		t.Errorf("unexpected prompts: %v", env.ConfirmCalls)
	}
	assertRan(t, env, "docker compose down", false)
	assertContains(t, env.ReadFile(templatePath), `"DevMode": true`)
}

func TestLocalnetStartCustomConfigDir(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()
	customDir := filepath.Join(env.Root, "custom")
	if err := os.Mkdir(customDir, 0o755); err != nil {
		t.Fatal(err)
	}

	assertExitCode(t, env.Invoke("", "localnet", "start", "--config-dir", customDir), 0)
	if _, err := os.Stat(filepath.Join(customDir, "sandbox", "docker-compose.yml")); err != nil {
		t.Error(err)
	}

	envDir := filepath.Join(env.Root, "from-env")
	if err := os.Mkdir(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALGOKIT_LOCALNET_CONFIG_DIR", envDir)
	assertExitCode(t, env.Invoke("", "localnet", "start"), 0)
	if _, err := os.Stat(filepath.Join(envDir, "sandbox", "docker-compose.yml")); err != nil {
		t.Error(err)
	}
}

func TestLocalnetStartRejectsMissingConfigDir(t *testing.T) {
	env := setupLocalnet(t)
	result := env.Invoke("", "localnet", "start", "--config-dir", "/does/not/exist")
	assertExitCode(t, result, 2)
	assertContains(t, result.Output, "Error: Invalid value for '--config-dir' / '-P': Directory '/does/not/exist' does not exist.")
}

func TestLocalnetStartComposeUpFails(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.Proc.BadExit("docker compose up")

	result := env.Invoke("", "localnet", "start")

	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: Failed to start LocalNet")
}

func TestLocalnetStartHealthCheckFails(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockLocalnetUpToDate()
	env.HTTP.Add(sandbox.AlgodHealthURL, map[string]any{}, 500)

	result := env.Invoke("", "localnet", "start")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "AlgoKit LocalNet health check returned 500, waiting")
	assertContains(t, result.Output, "WARNING: AlgoKit LocalNet failed to return a successful health check")
}

func TestLocalnetStopNotRunning(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	result := env.Invoke("", "localnet", "stop")
	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "LocalNet is not running; run `algokit localnet start` to start the AlgoKit LocalNet")
}

func TestLocalnetStopNamed(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox_test")
	env.WriteLatestCompose("sandbox_test")
	assertExitCode(t, env.Invoke("", "localnet", "stop"), 0)
	if dir := env.Proc.LastCall().Dir; dir != filepath.Join(env.AppConfigDir(), "sandbox_test") {
		t.Errorf("stopped in %s", dir)
	}
}

func TestLocalnetStopFails(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	env.WriteLatestCompose("sandbox")
	env.Proc.BadExit("docker compose stop")
	result := env.Invoke("", "localnet", "stop")
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: Failed to stop LocalNet")
}

func TestLocalnetResetFromScratch(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	result := env.Invoke("", "localnet", "reset")
	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Existing LocalNet not found; creating from scratch...")
	assertRan(t, env, "docker compose down", false)
}

func TestLocalnetResetSyncsAndUpdates(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	env.MockHealthSuccess()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml"), "out of date")

	result := env.Invoke("", "localnet", "reset", "--update")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Syncing LocalNet configuration")
	assertRan(t, env, "docker compose pull --ignore-pull-failures --quiet", true)
	if env.ReadFile(filepath.Join(env.AppConfigDir(), "sandbox", "docker-compose.yml")) != sandbox.DockerComposeYAML("algokit_sandbox") {
		t.Error("compose file wasn't synced")
	}
	if cache := readCache(t, env); cache["indexer_outdated"] || cache["algod_outdated"] {
		t.Errorf("cache = %v", cache)
	}
}

func TestLocalnetResetNamedWithUpdate(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox_test")
	env.MockHealthSuccess()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"), "custom")

	assertExitCode(t, env.Invoke("", "localnet", "reset", "--update"), 0)

	if len(env.ConfirmCalls) != 1 {
		t.Errorf("confirm calls = %v", env.ConfirmCalls)
	}
	if env.ReadFile(filepath.Join(env.AppConfigDir(), "sandbox_test", "docker-compose.yml")) != sandbox.DockerComposeYAML("algokit_sandbox_test") {
		t.Error("named compose file wasn't reset")
	}
}

func TestLocalnetResetNamedDeclined(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox_test")
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"), "custom")
	env.ConfirmAnswer = false

	result := env.Invoke("", "localnet", "reset", "--update")

	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: LocalNet configuration has not been reset")
}

func TestLocalnetResetNamedKeepsConfig(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox_test")
	env.MockHealthSuccess()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"), "custom")

	assertExitCode(t, env.Invoke("", "localnet", "reset"), 0)

	if got := env.ReadFile(filepath.Join(env.AppConfigDir(), "sandbox_test", "docker-compose.yml")); got != "custom" {
		t.Errorf("compose file = %q", got)
	}
	assertRan(t, env, "docker compose down", true)
}

func service(name, state string, ports ...int) string {
	publishers := make([]map[string]int, len(ports))
	for i, port := range ports {
		publishers[i] = map[string]int{"PublishedPort": port}
	}
	encoded, _ := json.Marshal(map[string]any{"Service": name, "State": state, "Publishers": publishers})
	return string(encoded)
}

func setupStatus(t *testing.T) *testsupport.Env {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.WriteLatestCompose("sandbox")
	return env
}

func TestLocalnetStatusStoppedServices(t *testing.T) {
	env := setupStatus(t)
	// newline delimited `compose ps` output (compose >= 2.21.0)
	env.Proc.Set("docker compose ps --format json", []string{
		service("algod", "exited"), service("conduit", "running"),
		service("indexer-db", "running"), service("indexer", "exited"),
	}, 0)

	result := env.Invoke("", "localnet", "status")

	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "# algod status\nStatus: Not running\n# conduit status\nStatus: Running")
	assertContains(t, result.Output, "Error: At least one container isn't running")
}

func TestLocalnetStatusUnexpectedPort(t *testing.T) {
	env := setupStatus(t)
	env.Proc.Set("docker compose ps --format json", []string{fmt.Sprintf("[%s, %s, %s, %s]",
		service("algod", "running", 1234), service("conduit", "running"),
		service("indexer-db", "running"), service("indexer", "running", 5678))}, 0)

	result := env.Invoke("", "localnet", "status")

	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "# algod status\nStatus: Error\n")
	assertContains(t, result.Output, "# indexer status\nStatus: Error\n")
}

func TestLocalnetStatusHTTPFailure(t *testing.T) {
	env := setupStatus(t)
	env.Proc.Set("docker compose ps --format json", []string{fmt.Sprintf("[%s, %s, %s, %s]",
		service("algod", "running", 4001), service("conduit", "running"),
		service("indexer-db", "running"), service("indexer", "running", 8980))}, 0)
	env.HTTP.Add("http://localhost:4001/v2/status", map[string]any{"last-round": 1, "time-since-last-round": 1e9}, 200)
	env.HTTP.Add("http://localhost:4001/versions", map[string]any{
		"genesis_id": "id", "genesis_hash_b64": "hash", "build": map[string]any{"major": 1, "minor": 2, "build_number": 3},
	}, 200)
	env.HTTP.AddError("http://localhost:8980/health", errors.New("unable to read within timeout"))

	result := env.Invoke("", "localnet", "status")

	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Time since last round: 1.0s\nGenesis ID: id\nGenesis hash: hash\nVersion: 1.2.3")
	assertContains(t, result.Output, "# indexer status\nStatus: Error\n")
}

func TestLocalnetStatusLargeRoundNumbers(t *testing.T) {
	env := setupStatus(t)
	env.Proc.Set("docker compose ps --format json", []string{fmt.Sprintf("[%s, %s, %s, %s]",
		service("algod", "running", 4001), service("conduit", "running"),
		service("indexer-db", "running"), service("indexer", "running", 8980))}, 0)
	env.HTTP.Add("http://localhost:4001/v2/status", json.RawMessage(`{"last-round": 123456789, "time-since-last-round": 2500000000}`), 200)
	env.HTTP.Add("http://localhost:4001/versions", map[string]any{
		"genesis_id": "id", "genesis_hash_b64": "hash", "build": map[string]any{"major": 4, "minor": 0, "build_number": 12},
	}, 200)
	env.HTTP.Add("http://localhost:8980/health", json.RawMessage(`{"round": 123456789, "version": "3.10.0"}`), 200)

	result := env.Invoke("", "localnet", "status")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Last round: 123456789\nTime since last round: 2.5s")
	assertContains(t, result.Output, "Version: 4.0.12")
	assertContains(t, result.Output, "Port: 8980\nLast round: 123456789\nVersion: 3.10.0")
}

func TestLocalnetStatusMissingService(t *testing.T) {
	env := setupStatus(t)
	env.Proc.Set("docker compose ps --format json", []string{fmt.Sprintf("[%s, %s, %s]",
		service("algod", "running"), service("conduit", "running"), service("indexer-db", "running"))}, 0)
	result := env.Invoke("", "localnet", "status")
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: LocalNet has not been initialized yet, please run 'algokit localnet start'")
}

func TestLocalnetConfigRestartsActiveLocalnet(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	env.MockHealthSuccess()
	env.WriteLatestCompose("sandbox")

	result := env.Invoke("", "localnet", "config", "podman", "--force")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Container engine set to `podman`")
	assertRan(t, env, "docker compose down", true)
	assertRan(t, env, "podman compose up --detach --quiet-pull", true)
}

func TestLocalnetConfigSwitchesBackToDocker(t *testing.T) {
	env := setupLocalnet(t)
	env.WriteFile(filepath.Join(env.AppConfigDir(), "active-container-engine"), "podman")
	env.Proc.Set("podman compose version --format json", []string{`{"version": "1.0.6"}`}, 0)
	env.Proc.Set("podman compose ls", []string{"[]"}, 0)

	assertExitCode(t, env.Invoke("", "localnet", "config", "docker"), 0)

	if len(env.ConfirmCalls) != 0 {
		t.Errorf("unexpected prompts: %v", env.ConfirmCalls)
	}
	if got := env.ReadFile(filepath.Join(env.AppConfigDir(), "active-container-engine")); got != "docker" {
		t.Errorf("active-container-engine = %q", got)
	}
}

func TestLocalnetConfigRejectsUnknownEngine(t *testing.T) {
	env := setupLocalnet(t)
	result := env.Invoke("", "localnet", "config", "containerd")
	assertExitCode(t, result, 2)
	assertContains(t, result.Output, "Error: Invalid value for '[[docker|podman]]': 'containerd' is not one of 'docker', 'podman'.")
}

func TestLocalnetLogs(t *testing.T) {
	env := setupLocalnet(t)
	assertExitCode(t, env.Invoke("", "localnet", "logs", "--follow", "--tail", "10"), 0)
	call := env.Proc.LastCall()
	if call.String() != "docker compose logs --follow --no-color --tail 10" || !call.Interactive ||
		call.Dir != filepath.Join(env.AppConfigDir(), "sandbox") {
		t.Errorf("unexpected logs call: %+v", call)
	}

	env.InvokeRaw("", "localnet", "logs", "-f")
	if got := env.Proc.LastCall().String(); got != "docker compose logs --follow --tail all" {
		t.Errorf("logs call = %s", got)
	}
}

func TestLocalnetLogsFails(t *testing.T) {
	env := setupLocalnet(t)
	env.Proc.BadExit("docker compose logs")
	result := env.Invoke("", "localnet", "logs")
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: Failed to get logs, are the containers running?")
}

func TestLocalnetConsole(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.Proc.Set("docker compose ps algod --format json", []string{`[{"State": "running"}]`}, 0)
	env.WriteLatestCompose("sandbox")

	assertExitCode(t, env.Invoke("", "localnet", "console"), 0)

	if got := env.Proc.LastCall().String(); got != "docker exec -it -w /root algokit_sandbox_algod bash" {
		t.Errorf("console call = %s", got)
	}
}
