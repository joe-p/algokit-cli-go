package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/sandbox"
	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

func sourceBuilds(env *testsupport.Env) []testsupport.Call {
	var builds []testsupport.Call
	for _, call := range env.Proc.Calls {
		if len(call.Command) > 1 && call.Command[1] == "build" {
			builds = append(builds, call)
		}
	}
	return builds
}

func localnetConfigFile(env *testsupport.Env, content string) string {
	path := filepath.Join(env.Root, "localnet config.json")
	env.WriteFile(path, content)
	return path
}

func TestLocalnetAlgodSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		repo   string
		ref    string
	}{
		{"branch", `{"algodSource":{"ref":"feature/test"}}`, sandbox.DefaultAlgodRepo, "feature/test"},
		{"tag", `{"algodSource":{"ref":"v4.0.0-stable"}}`, sandbox.DefaultAlgodRepo, "v4.0.0-stable"},
		{"commit", `{"algodSource":{"ref":"0123456789abcdef0123456789abcdef01234567"}}`, sandbox.DefaultAlgodRepo, "0123456789abcdef0123456789abcdef01234567"},
		{"fork", `{"algodSource":{"repo":"https://example.com/fork.git"}}`, "https://example.com/fork.git", sandbox.DefaultAlgodRef},
		{"both", `{"algodSource":{"repo":"https://example.com/fork.git","ref":"feature/test"}}`, "https://example.com/fork.git", "feature/test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.MockHealthSuccess()
			var buildContext string
			env.Proc.Set("docker build", nil, 0, func() {
				build := sourceBuilds(env)[0]
				buildContext = build.Command[len(build.Command)-1]
				dockerfile := env.ReadFile(filepath.Join(buildContext, "Dockerfile"))
				assertContains(t, dockerfile, "git -C /src fetch --depth=1")
				assertContains(t, dockerfile, "CMD [\"/node/run/run.sh\"]")
			})
			configPath := localnetConfigFile(env, tc.config)
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "source", "--config", configPath), 0)
			builds := sourceBuilds(env)
			if len(builds) != 1 {
				t.Fatalf("builds = %v", builds)
			}
			for _, arg := range []string{"ALGOD_REPO=" + tc.repo, "ALGOD_REF=" + tc.ref} {
				if !slices.Contains(builds[0].Command, arg) {
					t.Errorf("build args missing %q: %v", arg, builds[0].Command)
				}
			}
			if _, err := os.Stat(buildContext); !os.IsNotExist(err) {
				t.Errorf("temporary build context wasn't removed: %v", err)
			}
			dir := env.SandboxDir("sandbox_source")
			var source sandbox.AlgodSource
			if err := json.Unmarshal([]byte(env.ReadFile(filepath.Join(dir, "algod-source.json"))), &source); err != nil {
				t.Fatal(err)
			}
			if source.Repo != tc.repo || source.Ref != tc.ref {
				t.Errorf("saved source = %+v", source)
			}
			compose := env.ReadFile(filepath.Join(dir, "docker-compose.yml"))
			assertContains(t, compose, "image: algokit-algod:")
			assertContains(t, compose, "pull_policy: never")
			assertNotContains(t, compose, "image: algorand/algod:latest")
			assertRan(t, env, "docker compose up --detach --quiet-pull --wait", true)
			sb, err := sandbox.New("source", env.AppConfigDir())
			if err != nil {
				t.Fatal(err)
			}
			status, err := sb.ComposeFileStatus()
			if err != nil || status != sandbox.UpToDate {
				t.Errorf("source definition status = %v, %v", status, err)
			}
			if err := os.Remove(configPath); err != nil {
				t.Fatal(err)
			}
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "source"), 0)
			if len(sourceBuilds(env)) != 1 {
				t.Error("start without config rebuilt algod")
			}
			for _, request := range env.HTTP.Requests {
				if strings.Contains(request.URL.String(), "hub.docker.com") {
					t.Errorf("unexpected image version check: %s", request.URL)
				}
			}
		})
	}
}

func TestLocalnetAlgodSourceUpdatesKeepCustomConfiguration(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	configPath := localnetConfigFile(env, `{"algodSource":{"repo":"https://example.com/fork.git","ref":"first"}}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "source", "--config", configPath), 0)
	path := filepath.Join(env.SandboxDir("sandbox_source"), "docker-compose.yml")
	env.WriteFile(path, env.ReadFile(path)+"\n# custom configuration\n")
	configPath = localnetConfigFile(env, `{"algodSource":{"ref":"second"}}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "source", "--config", configPath), 0)
	assertContains(t, env.ReadFile(path), "# custom configuration")
	builds := sourceBuilds(env)
	if len(builds) != 2 || !slices.Contains(builds[1].Command, "ALGOD_REF=second") || !slices.Contains(builds[1].Command, "ALGOD_REPO=https://example.com/fork.git") {
		t.Fatalf("unexpected builds: %v", builds)
	}
	// An explicit repeat refreshes moving branches too.
	assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "source", "--config", configPath), 0)
	if len(sourceBuilds(env)) != 3 {
		t.Fatal("explicit config did not rebuild")
	}
}

func TestLocalnetAlgodSourceBuildFailurePreservesConfiguration(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	env.WriteLatestCompose("sandbox")
	env.Proc.BadExit("docker build")
	configPath := localnetConfigFile(env, `{"algodSource":{"ref":"missing"}}`)
	result := env.Invoke("", "localnet", "start", "--config", configPath)
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "Failed to build algod from source")
	assertRan(t, env, "docker compose up --detach --quiet-pull --wait", false)
	if got := env.ReadFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml")); got != sandbox.DockerComposeYAML("algokit_sandbox") {
		t.Error("failed build modified compose configuration")
	}
	if _, err := os.Stat(filepath.Join(env.SandboxDir("sandbox"), "algod-source.json")); !os.IsNotExist(err) {
		t.Error("failed build saved source configuration")
	}
}

func TestLocalnetAlgodSourceReset(t *testing.T) {
	for _, name := range []string{"sandbox", "source"} {
		for _, update := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/reset", true: "/update"}[update], func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				configPath := localnetConfigFile(env, `{"algodSource":{"ref":"feature/test"}}`)
				args := []string{"localnet", "start", "--config", configPath}
				fullName := "sandbox"
				if name != "sandbox" {
					args = append(args, "--name", name)
					fullName += "_" + name
				}
				assertExitCode(t, env.Invoke("", args...), 0)
				env.MockRunningLocalnet(fullName)
				args = []string{"localnet", "reset"}
				if update {
					args = append(args, "--update")
				}
				assertExitCode(t, env.Invoke("", args...), 0)
				wantBuilds := 1
				if update {
					wantBuilds++
				}
				if len(sourceBuilds(env)) != wantBuilds {
					t.Errorf("build count = %d, want %d", len(sourceBuilds(env)), wantBuilds)
				}
				assertContains(t, env.ReadFile(filepath.Join(env.SandboxDir(fullName), "docker-compose.yml")), "image: algokit-algod:")
				assertRan(t, env, "docker compose pull --ignore-pull-failures --quiet", false)
				assertRan(t, env, "docker compose pull --ignore-pull-failures --quiet conduit indexer-db indexer", update)
			})
		}
	}
}

func TestLocalnetAlgodSourceUsesPodman(t *testing.T) {
	env := setupLocalnet(t)
	env.WriteFile(filepath.Join(env.AppConfigDir(), "active-container-engine"), "podman")
	env.Proc.Set("podman compose version --format json", []string{`{"version": "1.0.6"}`}, 0)
	env.Proc.Set("podman compose ls", []string{"[]"}, 0)
	env.MockHealthSuccess()
	configPath := localnetConfigFile(env, `{"algodSource":{"ref":"master"}}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--config", configPath), 0)
	builds := sourceBuilds(env)
	if len(builds) != 1 || builds[0].Command[0] != "podman" {
		t.Fatalf("unexpected builds: %v", builds)
	}
}

func TestLocalnetAlgodSourceRejectsInvalidSavedConfiguration(t *testing.T) {
	for _, content := range []string{`{`, `{}`, `{"repo":"https://example.com/fork.git","ref":" "}`} {
		t.Run(content, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), "algod-source.json"), content)
			assertExitCode(t, env.Invoke("", "localnet", "start"), 1)
			assertRan(t, env, "docker compose up --detach --quiet-pull --wait", false)
		})
	}
}

func TestLocalnetAlgodSourceRejectsCustomizedImage(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.WriteLatestCompose("sandbox")
	path := filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml")
	custom := strings.Replace(env.ReadFile(path), "image: "+sandbox.AlgorandImage, "image: custom:latest", 1)
	env.WriteFile(path, custom)
	configPath := localnetConfigFile(env, `{"algodSource":{"ref":"master"}}`)
	result := env.Invoke("", "localnet", "start", "--config", configPath)
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "algod image definition has been customized")
	if env.ReadFile(path) != custom || len(sourceBuilds(env)) != 0 {
		t.Error("customized image was overwritten or a build was started")
	}
}

func TestLocalnetConfigRelativePath(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	path := localnetConfigFile(env, `{"algodSource":{"ref":"feature/test"}}`)
	assertExitCode(t, env.Invoke(env.Root, "localnet", "start", "--name", "foo", "--config", filepath.Base(path)), 0)
	if len(sourceBuilds(env)) != 1 {
		t.Fatal("relative config path did not trigger a build")
	}
}

func TestLocalnetConfigValidationBeforeChanges(t *testing.T) {
	for _, content := range []string{
		`{`, `null`, `[]`, `{}`, `{"algodSource":null}`, `{"algodSource":{}}`,
		`{"algodSource":{"repo":""}}`, `{"algodSource":{"ref":" "}}`,
		`{"algodSource":{"repo":"https://example.com/fork.git","ref":""}}`,
		`{"algodSource":{"ref":123}}`, `{"algodSource":"master"}`,
		`{"algod":{"ref":"master"}}`, `{"algod_source":{"ref":"master"}}`,
		`{"indexer_source":{"ref":"main"}}`, `{"conduit_source":{"ref":"main"}}`,
		`{"devMode":"false"}`, `{"devMode":0}`, `{"devMode":null}`,
		`{"vFuture":"true"}`, `{"vFuture":1}`, `{"vFuture":null}`,
		`{"algodSource":{"ref":"master"},"devMode":null}`,
		`{"devMode":true,"vFuture":[]}`,
		`{"algodSource":{"reff":"master"}}`, `{"algodSource":{"ref":"master"},"unknown":true}`,
		`{"algodSource":{"ref":"master"}} {}`, `{"algodSource":{"ref":"master"}} trailing`,
	} {
		t.Run(content, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockRunningLocalnet("sandbox")
			path := localnetConfigFile(env, content)
			result := env.Invoke("", "localnet", "start", "--name", "foo", "--config", path)
			assertExitCode(t, result, 1)
			assertContains(t, result.Output, "invalid LocalNet config")
			if len(sourceBuilds(env)) != 0 || len(env.ConfirmCalls) != 0 {
				t.Error("invalid config triggered a build or restart confirmation")
			}
			assertRan(t, env, "docker compose stop", false)
			if _, err := os.Stat(filepath.Join(env.AppConfigDir(), "sandbox_foo")); !os.IsNotExist(err) {
				t.Error("invalid config created a LocalNet directory")
			}
		})
	}
}

func TestLocalnetConfigMissingFile(t *testing.T) {
	env := setupLocalnet(t)
	env.MockRunningLocalnet("sandbox")
	result := env.Invoke("", "localnet", "start", "--name", "foo", "--config", filepath.Join(env.Root, "missing.json"))
	assertExitCode(t, result, 1)
	assertContains(t, result.Output, "cannot read LocalNet config")
	assertRan(t, env, "docker compose stop", false)
	if len(sourceBuilds(env)) != 0 {
		t.Error("missing config triggered a build")
	}
}

func TestLocalnetConfigRejectsBlankPath(t *testing.T) {
	for _, value := range []string{"", "   "} {
		t.Run(value, func(t *testing.T) {
			env := setupLocalnet(t)
			result := env.Invoke("", "localnet", "start", "--config", value)
			assertExitCode(t, result, 1)
			assertContains(t, result.Output, "must not be blank")
			if len(sourceBuilds(env)) != 0 {
				t.Error("blank option triggered a build")
			}
		})
	}
}
