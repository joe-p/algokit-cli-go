// Compares the output of this CLI against the approved output of the Python AlgoKit CLI test-suite
// (internal/testsupport/testdata/python) for equivalent scenarios.
package commands_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/sandbox"
	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

// pythonFixture loads an approval file, adjusting it for this CLI's intentional differences.
func pythonFixture(t *testing.T, name string) string {
	t.Helper()
	fixture := testsupport.PythonFixture(t, name)
	// `explore` isn't part of this CLI
	fixture = strings.ReplaceAll(fixture,
		"Started; execute `algokit explore` to explore LocalNet in a web user interface.",
		"Started; execute `algokit goal` or `algokit localnet console` to interact with LocalNet.")
	// HTTP request logging is debug level only
	fixture = regexp.MustCompile(`(?m)^HTTP Request:`).ReplaceAllString(fixture, "DEBUG: HTTP Request:")
	// `algokit config container-engine` isn't part of this CLI
	return strings.ReplaceAll(fixture, "`algokit config container-engine`", "`algokit localnet config`")
}

// normalise drops trailing whitespace (the approval files have no trailing newline), and the debug line explaining
// the image version check (the Python suite patches _should_check_image_versions to always return True).
func normalise(text string) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasSuffix(line, "inaccessible, will check for image updates") {
			lines = append(lines, line)
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \n")
}

func assertOutput(t *testing.T, got, want string) {
	t.Helper()
	if normalise(got) != normalise(want) {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", normalise(got), normalise(want))
	}
}

func assertExitCode(t *testing.T, result testsupport.Result, want int) {
	t.Helper()
	if result.ExitCode != want {
		t.Fatalf("exit code = %d, want %d; output:\n%s", result.ExitCode, want, result.Output)
	}
}

func TestTemplatesMatchPython(t *testing.T) {
	var configJSON bytes.Buffer
	if err := json.Indent(&configJSON, []byte(sandbox.ConfigJSON()), "", "  "); err != nil {
		t.Fatal(err)
	}
	assertOutput(t, configJSON.String(), pythonFixture(t, "config_json.txt"))
	assertOutput(t, sandbox.ConduitYAML(), pythonFixture(t, "conduit_yaml.txt"))
	assertOutput(t, sandbox.DockerComposeYAML("algokit_sandbox"), pythonFixture(t, "docker_compose_yml.txt"))
	assertOutput(t, sandbox.AlgodNetworkTemplate(), pythonFixture(t, "algod_network_template.txt"))
}

func TestParityLocalnetStart(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5.0")
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()

	result := env.Invoke("", "localnet", "start")

	assertExitCode(t, result, 0)
	composeFile := env.ReadFile(filepath.Join(env.AppConfigDir(), "sandbox", "docker-compose.yml"))
	output := result.Output + "----\n{app_config}/sandbox/docker-compose.yml:\n----\n" + composeFile
	assertOutput(t, output, pythonFixture(t, "localnet_start.txt"))
}

func TestParityLocalnetStartWithoutDocker(t *testing.T) {
	env := testsupport.Setup(t)
	env.Proc.NotFound("docker compose version")

	result := env.Invoke("", "localnet", "start")

	assertExitCode(t, result, 1)
	assertOutput(t, result.Output, pythonFixture(t, "localnet_start_without_docker.txt"))
}

func TestParityLocalnetStartUnparseableComposeVersion(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5-dev123")
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()

	result := env.Invoke("", "localnet", "start")

	assertExitCode(t, result, 0)
	expected, _, _ := strings.Cut(pythonFixture(t, "localnet_start_unparseable_compose.txt"), "\n----\n")
	assertOutput(t, result.Output, expected)
}

func TestParityLocalnetStop(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5.0")
	env.MockRunningLocalnet("sandbox")
	dir := env.SandboxDir("sandbox")
	env.WriteFile(filepath.Join(dir, "docker-compose.yml"), "existing")
	env.WriteFile(filepath.Join(dir, "algod_config.json"), "existing")

	result := env.Invoke("", "localnet", "stop")

	assertExitCode(t, result, 0)
	assertOutput(t, result.Output, pythonFixture(t, "localnet_stop.txt"))
}

func TestParityLocalnetResetUpToDate(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5.0")
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.MockHealthSuccess()
	env.WriteLatestCompose("sandbox")

	result := env.Invoke("", "localnet", "reset")

	assertExitCode(t, result, 0)
	assertOutput(t, result.Output, pythonFixture(t, "localnet_reset_up_to_date.txt"))
}

// composePSOutput returns the `docker compose ps` output used in the Python status test.
func composePSOutput(t *testing.T) string {
	t.Helper()
	for line := range strings.SplitSeq(testsupport.PythonFixture(t, "localnet_status_successful.txt"), "\n") {
		if strings.HasPrefix(line, `DEBUG: docker: [{"ID"`) {
			return strings.TrimPrefix(line, "DEBUG: docker: ")
		}
	}
	t.Fatal("compose ps output not found in fixture")
	return ""
}

func TestParityLocalnetStatus(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5.0")
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml"), "existing")
	env.HTTP.Add("http://localhost:4001/v2/status", map[string]any{"last-round": 1, "time-since-last-round": 15.3e9}, 200)
	env.HTTP.Add("http://localhost:4001/versions", map[string]any{
		"genesis_id":       "{genesis_id}",
		"genesis_hash_b64": "{genesis_hash_b64}",
		"build":            map[string]any{"major": 1, "minor": 2, "build_number": 1},
	}, 200)
	env.HTTP.Add("http://localhost:8980/health", json.RawMessage(`{"round": 1, "errors": ["error"], "version": "v1.0"}`), 200)
	env.Proc.Set("docker compose ps --format json", []string{composePSOutput(t)}, 0)

	result := env.Invoke("", "localnet", "status")

	assertExitCode(t, result, 0)
	assertOutput(t, result.Output, pythonFixture(t, "localnet_status_successful.txt"))
}

func TestParityLocalnetStatusNotInitialised(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockComposeVersion("v2.5.0")
	env.MockRunningLocalnet("sandbox")
	env.MockLocalnetUpToDate()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml"), "existing")
	env.Proc.Set("docker compose ps --format json", []string{"[]"}, 0)

	result := env.Invoke("", "localnet", "status")

	assertExitCode(t, result, 1)
	assertOutput(t, result.Output, pythonFixture(t, "localnet_status_failure.txt"))
}

func TestParityGoalInputOutputFiles(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockNoRunningLocalnet()
	env.MockAlgodRunning("sandbox")
	env.WriteLatestCompose("sandbox")
	cwd := filepath.Join(env.Root, "cwd")
	env.WriteFile(filepath.Join(cwd, "approval.teal"), "\n#pragma version 8\nint 1\nreturn\n")
	goalMount := filepath.Join(env.SandboxDir("sandbox"), "goal_mount")
	env.Proc.Set("docker exec --interactive --workdir /root algokit_sandbox_algod goal clerk compile",
		[]string{"File compiled"}, 0, func() {
			env.WriteFile(filepath.Join(goalMount, "approval.compiled"), "compiled")
		})

	result := env.Invoke(cwd, "goal", "clerk", "compile", "approval.teal", "-o", "approval.compiled")

	assertExitCode(t, result, 0)
	goalCall := env.Proc.LastCall().Command
	if goalCall[9] != "/root/goal_mount/approval.teal" || goalCall[11] != "/root/goal_mount/approval.compiled" {
		t.Errorf("unexpected goal command: %v", goalCall)
	}
	if got := env.ReadFile(filepath.Join(cwd, "approval.compiled")); got != "compiled" {
		t.Errorf("approval.compiled = %q", got)
	}
	// intentional difference: goal's output is passed through unchanged, rather than trimmed and prefixed with a space
	expected := strings.Replace(pythonFixture(t, "goal_input_output_files.txt"), "\n File compiled", "\nFile compiled", 1)
	assertOutput(t, strings.ReplaceAll(result.Output, "docker", "{container_engine}"), expected)
}

func TestParityGoalComposeOutdated(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockNoRunningLocalnet()
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml"), "outdated")

	result := env.Invoke("", "goal", "account", "list")

	assertExitCode(t, result, 1)
	assertOutput(t, strings.ReplaceAll(result.Output, "docker", "{container_engine}"),
		pythonFixture(t, "goal_compose_outdated.txt"))
}

func TestParityGoalConsole(t *testing.T) {
	env := testsupport.Setup(t)
	env.MockNoRunningLocalnet()
	env.MockAlgodRunning("sandbox")
	env.WriteLatestCompose("sandbox")

	result := env.Invoke("", "goal", "--console")

	assertExitCode(t, result, 0)
	if !env.Proc.LastCall().Interactive {
		t.Error("console should run interactively")
	}
	assertOutput(t, strings.ReplaceAll(result.Output, "docker", "{container_engine}"), pythonFixture(t, "goal_console.txt"))
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
