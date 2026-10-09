package commands_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

const goalPrefix = "docker exec --interactive --workdir /root algokit_sandbox_algod goal"

func setupGoal(t *testing.T) (*testsupport.Env, string) {
	env := testsupport.Setup(t)
	env.MockNoRunningLocalnet()
	env.WriteLatestCompose("sandbox")
	cwd := filepath.Join(env.Root, "cwd")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return env, cwd
}

func goalCalls(env *testsupport.Env) []testsupport.Call {
	var calls []testsupport.Call
	for _, call := range env.Proc.Calls {
		if slices.Contains(call.Command, "goal") {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestGoalPassesArgumentsThrough(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")
	env.Proc.Set(goalPrefix, []string{"  indented\tline one  \n", "line two\n"}, 0)

	result := env.Invoke(cwd, "goal", "account", "list", "-w", "unencrypted-default-wallet")

	assertExitCode(t, result, 0)
	if got := goalCalls(env)[0].Command[7:]; !slices.Equal(got, []string{"account", "list", "-w", "unencrypted-default-wallet"}) {
		t.Errorf("goal args = %v", got)
	}
	// goal output is passed through unchanged (not trimmed, prefixed or duplicated as a debug log line)
	assertContains(t, result.Output, "'\n  indented\tline one  \nline two\n")
	assertNotContains(t, result.Output, "docker: line two")
}

func TestGoalOptionParsing(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		goalArgs   []string
		console    bool
		tty        bool
		consoleArg bool
	}{
		// known options are recognised anywhere, like click
		{args: []string{"node", "status", "--interactive"}, goalArgs: []string{"node", "status"}, tty: true},
		// "--" ends option parsing (and isn't passed through)
		{args: []string{"--", "--interactive", "-v"}, goalArgs: []string{"--interactive", "-v"}},
		// unknown long options, with or without values, are passed through
		{args: []string{"clerk", "--bogus=1", "-x"}, goalArgs: []string{"clerk", "--bogus=1", "-x"}},
		// unknown characters in a short option cluster are passed through
		{args: []string{"node", "-dw"}, goalArgs: []string{"node", "-dw"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			env, cwd := setupGoal(t)
			env.MockAlgodRunning("sandbox")

			assertExitCode(t, env.Invoke(cwd, append([]string{"goal"}, tc.args...)...), 0)

			call := goalCalls(env)[0]
			index := slices.Index(call.Command, "goal")
			if got := call.Command[index+1:]; !slices.Equal(got, tc.goalArgs) {
				t.Errorf("goal args = %v, want %v", got, tc.goalArgs)
			}
			if slices.Contains(call.Command, "--tty") != tc.tty {
				t.Errorf("tty = %v, want %v", !tc.tty, tc.tty)
			}
		})
	}
}

func TestGoalHelp(t *testing.T) {
	env, cwd := setupGoal(t)
	result := env.Invoke(cwd, "goal", "clerk", "--help")
	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "Usage: algokit goal [OPTIONS] [GOAL_ARGS]...")
	if len(env.Proc.Calls) != 0 {
		t.Errorf("unexpected commands: %v", env.Proc.Commands())
	}
}

func TestGoalPropagatesExitCode(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")
	env.Proc.Set(goalPrefix, []string{"boom"}, 3)

	result := env.Invoke(cwd, "goal", "wallet", "badcmd")

	assertExitCode(t, result, 3)
	assertNotContains(t, result.Output, "Error:")
}

func TestGoalStartsLocalnet(t *testing.T) {
	env, cwd := setupGoal(t)
	env.Proc.Set("docker compose ps algod --format json", []string{"[]"}, 0)
	env.MockHealthSuccess()

	result := env.Invoke(cwd, "goal", "node", "status")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "LocalNet isn't running\nStarting AlgoKit LocalNet now...")
}

func TestGoalInteractive(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")

	assertExitCode(t, env.Invoke(cwd, "goal", "--interactive", "account", "new"), 0)

	call := goalCalls(env)[0]
	want := "docker exec --tty --interactive --workdir /root algokit_sandbox_algod goal account new"
	if !call.Interactive || call.String() != want {
		t.Errorf("unexpected goal call: %+v", call)
	}
}

func TestGoalFallsBackToInteractiveOnTTYErrors(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")
	env.Proc.Set(goalPrefix, []string{"inappropriate ioctl for device"}, 1)

	env.Invoke(cwd, "goal", "account", "new")

	calls := goalCalls(env)
	if len(calls) != 2 || !calls[1].Interactive || !strings.HasPrefix(calls[1].String(), "docker exec --tty --interactive") {
		t.Errorf("unexpected goal calls: %+v", calls)
	}
}

func TestGoalConsoleIgnoresArguments(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")
	result := env.Invoke(cwd, "goal", "--console", "account", "list")
	assertContains(t, result.Output, "WARNING: --console opens an interactive shell, remaining arguments are being ignored")
}

func TestGoalMissingInputFile(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")

	result := env.Invoke(cwd, "goal", "clerk", "compile", "approval.teal")

	assertExitCode(t, result, 1)
	// matches the Python CLI, which logs the error and then reports the unhandled exception
	assertContains(t, result.Output, "ERROR: approval.teal does not exist.\nUnhandled FileNotFoundError: approval.teal does not exist.\n")
	if len(goalCalls(env)) != 0 {
		t.Error("goal shouldn't run")
	}
}

func TestGoalCopiesSplitOutputFiles(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockAlgodRunning("sandbox")
	env.WriteFile(filepath.Join(cwd, "input.gtxn"), "group")
	goalMount := filepath.Join(env.SandboxDir("sandbox"), "goal_mount")
	env.Proc.Set(goalPrefix, []string{"done"}, 0, func() {
		env.WriteFile(filepath.Join(goalMount, "output-0.txn"), "0")
		env.WriteFile(filepath.Join(goalMount, "output-1.txn"), "1")
		env.WriteFile(filepath.Join(goalMount, "unrelated.txn"), "x")
	})

	result := env.Invoke(cwd, "goal", "clerk", "split", "-i", "input.gtxn", "-o", "./output.txn")

	assertExitCode(t, result, 0)
	command := goalCalls(env)[0].Command
	if got := command[len(command)-4:]; !slices.Equal(got, []string{"-i", "/root/goal_mount/input.gtxn", "-o", "/root/goal_mount/output.txn"}) {
		t.Errorf("goal args = %v", got)
	}
	for name, content := range map[string]string{"output-0.txn": "0", "output-1.txn": "1"} {
		if got := env.ReadFile(filepath.Join(cwd, name)); got != content {
			t.Errorf("%s = %q", name, got)
		}
	}
	exists := func(path string) bool { _, err := os.Stat(path); return err == nil }
	if exists(filepath.Join(cwd, "unrelated.txn")) {
		t.Error("unrelated.txn shouldn't be copied")
	}
	// input and matched output files are cleaned out of the mount
	if exists(filepath.Join(goalMount, "input.gtxn")) || exists(filepath.Join(goalMount, "output-0.txn")) {
		t.Error("goal_mount wasn't cleaned up")
	}
	if !exists(filepath.Join(goalMount, "unrelated.txn")) {
		t.Error("unrelated.txn should be left in goal_mount")
	}
}

func TestGoalNamedLocalnet(t *testing.T) {
	env, cwd := setupGoal(t)
	env.MockRunningLocalnet("sandbox_test")
	env.MockAlgodRunning("sandbox_test")
	env.WriteFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"), "custom config is fine")

	result := env.Invoke(cwd, "goal", "node", "status")

	assertExitCode(t, result, 0)
	assertContains(t, result.Output, "A named LocalNet is running, goal command will be executed against the named LocalNet")
	if !slices.Contains(goalCalls(env)[0].Command, "algokit_sandbox_test_algod") {
		t.Errorf("unexpected goal call: %v", goalCalls(env)[0])
	}
}

func TestGoalDockerNotInstalled(t *testing.T) {
	env, cwd := setupGoal(t)
	env.Proc.NotFound("docker version")
	result := env.Invoke(cwd, "goal", "node", "status")
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: docker not found; please install docker and add to path.\n"+
		"See https://www.docker.com/get-started/ for more information.")
}

func TestGoalEngineNotRunning(t *testing.T) {
	env, cwd := setupGoal(t)
	env.Proc.BadExit("docker version")
	result := env.Invoke(cwd, "goal", "node", "status")
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Error: docker engine isn't running; please start it.")
}
