package commands_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalnetConfigName(t *testing.T) {
	t.Run("uses config name", func(t *testing.T) {
		env := setupLocalnet(t)
		env.MockNoRunningLocalnet()
		env.MockHealthSuccess()
		path := localnetConfigFile(env, `{"name":"from-config"}`)
		assertExitCode(t, env.Invoke("", "localnet", "start", "--config", path), 0)
		if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox_from-config")); err != nil {
			t.Errorf("named LocalNet directory wasn't created: %v", err)
		}
		if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox")); !os.IsNotExist(err) {
			t.Error("config name started the default LocalNet")
		}
	})

	t.Run("name flag overrides config", func(t *testing.T) {
		env := setupLocalnet(t)
		env.MockNoRunningLocalnet()
		env.MockHealthSuccess()
		path := localnetConfigFile(env, `{"name":"from-config"}`)
		assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "from-flag", "--config", path), 0)
		if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox_from-flag")); err != nil {
			t.Errorf("name flag directory wasn't created: %v", err)
		}
		if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox_from-config")); !os.IsNotExist(err) {
			t.Error("config name wasn't overridden by the name flag")
		}
	})
}

func TestLocalnetConfigRejectsInvalidName(t *testing.T) {
	for _, content := range []string{
		`{"name":""}`, `{"name":"   "}`, `{"name":123}`, `{"name":null}`,
		`{"name":[]}`, `{"name":true}`,
	} {
		t.Run(content, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockRunningLocalnet("sandbox")
			path := localnetConfigFile(env, content)
			result := env.Invoke("", "localnet", "start", "--config", path)
			assertExitCode(t, result, 1)
			assertContains(t, result.Output, "invalid LocalNet config")
			assertRan(t, env, "docker compose stop", false)
			if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox_from-config")); !os.IsNotExist(err) {
				t.Error("invalid name created a LocalNet directory")
			}
		})
	}
}
