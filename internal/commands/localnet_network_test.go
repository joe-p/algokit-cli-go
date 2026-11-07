package commands_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/sandbox"
	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

func assertNetworkSettings(t *testing.T, env *testsupport.Env, name string, devMode, future bool) {
	t.Helper()
	content := env.ReadFile(filepath.Join(env.AppConfigDir(), name, "algod_network_template.json"))
	var network struct {
		Genesis struct {
			DevMode           bool
			ConsensusProtocol string
		}
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(content, "NUM_ROUNDS", "30000")), &network); err != nil {
		t.Fatal(err)
	}
	if network.Genesis.DevMode != devMode || (network.Genesis.ConsensusProtocol == "future") != future {
		t.Errorf("unexpected Genesis settings: %+v", network.Genesis)
	}
	if !future && network.Genesis.ConsensusProtocol != "" {
		t.Errorf("disabled vFuture must use the default protocol, got %q", network.Genesis.ConsensusProtocol)
	}
	if !strings.Contains(content, "NUM_ROUNDS") {
		t.Error("network template lost the NUM_ROUNDS placeholder")
	}
}

func TestLocalnetNetworkSettings(t *testing.T) {
	for _, devMode := range []bool{false, true} {
		for _, future := range []bool{false, true} {
			t.Run(fmt.Sprintf("dev=%t/future=%t", devMode, future), func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				path := localnetConfigFile(env, fmt.Sprintf(`{"devMode":%t,"vFuture":%t}`, devMode, future))
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
				assertNetworkSettings(t, env, "sandbox_foo", devMode, future)
				assertRan(t, env, "docker compose down", false)
				assertRan(t, env, "docker compose up --detach --quiet-pull --wait", true)
				if len(sourceBuilds(env)) != 0 || len(env.ConfirmCalls) != 0 {
					t.Error("new network settings must not build an image or prompt to clear state")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo"), 0)
				assertNetworkSettings(t, env, "sandbox_foo", devMode, future)
				sb, err := sandbox.New("foo", env.AppConfigDir())
				if err != nil {
					t.Fatal(err)
				}
				status, err := sb.ComposeFileStatus()
				if err != nil || status != sandbox.UpToDate {
					t.Errorf("network settings cause an outdated definition: %v, %v", status, err)
				}
			})
		}
	}
}

func TestLocalnetSingleNetworkSetting(t *testing.T) {
	for _, tc := range []struct {
		config string
		dev    bool
		future bool
	}{
		{`{"devMode":false}`, false, false},
		{`{"vFuture":true}`, true, true},
		{`{"vFuture":false}`, true, false},
	} {
		t.Run(tc.config, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.MockHealthSuccess()
			path := localnetConfigFile(env, tc.config)
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
			assertNetworkSettings(t, env, "sandbox_foo", tc.dev, tc.future)
			if len(sourceBuilds(env)) != 0 {
				t.Error("network-only config built an image")
			}
		})
	}
}

func TestLocalnetNetworkSettingsToggleAndPreserveOmitted(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	path := localnetConfigFile(env, `{"devMode":false,"vFuture":true}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
	templatePath := filepath.Join(env.AppConfigDir(), "sandbox_foo", "algod_network_template.json")
	env.WriteFile(templatePath, strings.Replace(env.ReadFile(templatePath), `"RewardsPoolBalance": 0`, `"RewardsPoolBalance": 9007199254740993`, 1))
	path = localnetConfigFile(env, `{"vFuture":false}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
	assertNetworkSettings(t, env, "sandbox_foo", false, false)
	assertContains(t, env.ReadFile(templatePath), `"RewardsPoolBalance": 9007199254740993`)
	assertRan(t, env, "docker compose down", true)
	if len(env.ConfirmCalls) != 1 {
		t.Fatalf("expected one destructive-change confirmation: %v", env.ConfirmCalls)
	}
	assertContains(t, env.ConfirmCalls[0].Message, "clearing all chain state")
	// Reapplying the same settings must not ask to clear state again.
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
	if len(env.ConfirmCalls) != 1 {
		t.Error("identical settings prompted to recreate the network")
	}
}

func TestLocalnetNetworkSettingsDeclined(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	env.WriteLatestCompose("sandbox_foo")
	env.ConfirmAnswer = false
	path := localnetConfigFile(env, `{"devMode":false,"vFuture":true}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
	assertNetworkSettings(t, env, "sandbox_foo", true, false)
	if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox_foo", "localnet-settings.json")); !os.IsNotExist(err) {
		t.Error("declined settings were persisted")
	}
	assertRan(t, env, "docker compose down", false)
	assertRan(t, env, "docker compose up --detach --quiet-pull --wait", false)
}

func TestLocalnetNetworkSettingsForce(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	env.WriteLatestCompose("sandbox_foo")
	path := localnetConfigFile(env, `{"devMode":false,"vFuture":true}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path, "--force"), 0)
	assertNetworkSettings(t, env, "sandbox_foo", false, true)
	assertRan(t, env, "docker compose down", true)
	assertRan(t, env, "docker compose up --detach --quiet-pull --wait", true)
	if len(env.ConfirmCalls) != 0 {
		t.Error("--force prompted for network settings")
	}
}

func TestLocalnetNetworkSettingsCLIOverride(t *testing.T) {
	for _, tc := range []struct {
		flag string
		json bool
		want bool
	}{
		{"--dev", false, true},
		{"--no-dev", true, false},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.MockHealthSuccess()
			path := localnetConfigFile(env, fmt.Sprintf(`{"devMode":%t,"vFuture":true}`, tc.json))
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path, tc.flag), 0)
			assertNetworkSettings(t, env, "sandbox_foo", tc.want, true)
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo"), 0)
			assertNetworkSettings(t, env, "sandbox_foo", tc.want, true)
		})
	}
}

func TestLocalnetNetworkSettingsSurviveResetAndRegeneration(t *testing.T) {
	for _, name := range []string{"sandbox", "foo"} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update=%t", name, update), func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				path := localnetConfigFile(env, `{"devMode":false,"vFuture":true}`)
				args := []string{"localnet", "start", "--config", path}
				fullName := "sandbox"
				if name != "sandbox" {
					args = append(args, "--name", name)
					fullName += "_" + name
				}
				assertExitCode(t, env.Invoke("", args...), 0)
				env.MockRunningLocalnet(fullName)
				// Force regeneration where reset manages the Compose definition.
				if name == "sandbox" || update {
					env.WriteFile(filepath.Join(env.AppConfigDir(), fullName, "docker-compose.yml"), "outdated")
				}
				args = []string{"localnet", "reset"}
				if update {
					args = append(args, "--update")
				}
				assertExitCode(t, env.Invoke("", args...), 0)
				assertNetworkSettings(t, env, fullName, false, true)
				if len(sourceBuilds(env)) != 0 {
					t.Error("network-only settings triggered a source build")
				}
			})
		}
	}
}

func TestLocalnetNetworkSettingsWithSource(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	path := localnetConfigFile(env, `{"algodSource":{"ref":"feature/test"},"devMode":false,"vFuture":true}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "foo", "--config", path), 0)
	assertNetworkSettings(t, env, "sandbox_foo", false, true)
	if len(sourceBuilds(env)) != 1 {
		t.Error("combined config did not build algod")
	}
}
