package commands_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/sandbox"
)

func TestLocalnetAdditionalServiceSources(t *testing.T) {
	for _, service := range []struct {
		name, key, repo, ref, binary, dataEnv string
	}{
		{"indexer", "indexerSource", sandbox.DefaultIndexerRepo, sandbox.DefaultIndexerRef, "algorand-indexer", "INDEXER_DATA"},
		{"conduit", "conduitSource", sandbox.DefaultConduitRepo, sandbox.DefaultConduitRef, "conduit", "CONDUIT_DATA_DIR"},
	} {
		for _, fork := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fork=%t", service.name, fork), func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				repo, ref := service.repo, "feature/test"
				source := map[string]string{"ref": ref}
				if fork {
					repo, ref = "https://example.com/fork.git", service.ref
					source = map[string]string{"repo": repo}
				}
				data, _ := json.Marshal(map[string]any{service.key: source})
				path := localnetConfigFile(env, string(data))
				var context string
				env.Proc.Set("docker build", nil, 0, func() {
					builds := sourceBuilds(env)
					build := builds[len(builds)-1]
					context = build.Command[len(build.Command)-1]
					dockerfile := env.ReadFile(filepath.Join(context, "Dockerfile"))
					assertContains(t, dockerfile, "./cmd/"+service.binary)
					assertContains(t, dockerfile, "ENV "+service.dataEnv+"=/data")
					assertContains(t, dockerfile, "ENTRYPOINT [\"docker-entrypoint.sh\"]")
					assertNotContains(t, dockerfile, "{{")
					if service.name == "conduit" {
						assertContains(t, dockerfile, "grep -q localnet_algod")
					}
				})
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--localnet-config", path), 0)
				builds := sourceBuilds(env)
				if len(builds) != 1 {
					t.Fatalf("unexpected builds: %v", builds)
				}
				for _, arg := range []string{"SOURCE_REPO=" + repo, "SOURCE_REF=" + ref} {
					if !slices.Contains(builds[0].Command, arg) {
						t.Errorf("missing build arg %q: %v", arg, builds[0].Command)
					}
				}
				if !strings.HasPrefix(builds[0].Command[3], "algokit-"+service.name+":") {
					t.Errorf("incorrect image tag: %v", builds[0].Command)
				}
				if _, err := os.Stat(context); !os.IsNotExist(err) {
					t.Error("temporary build context was not removed")
				}
				dir := env.SandboxDir("sandbox_test")
				var saved sandbox.ServiceSource
				if err := json.Unmarshal([]byte(env.ReadFile(filepath.Join(dir, service.name+"-source.json"))), &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Repo != repo || saved.Ref != ref {
					t.Errorf("unexpected saved source: %+v", saved)
				}
				assertContains(t, env.ReadFile(filepath.Join(dir, "docker-compose.yml")), "image: algokit-"+service.name+":")
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test"), 0)
				if len(sourceBuilds(env)) != 1 {
					t.Error("start without source config rebuilt an image")
				}
				// Updating only ref keeps the saved repository and other services.
				data, _ = json.Marshal(map[string]any{service.key: map[string]string{"ref": "next"}})
				path = localnetConfigFile(env, string(data))
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--localnet-config", path), 0)
				builds = sourceBuilds(env)
				if len(builds) != 2 || !slices.Contains(builds[1].Command, "SOURCE_REPO="+repo) || !slices.Contains(builds[1].Command, "SOURCE_REF=next") {
					t.Errorf("source update lost settings: %v", builds)
				}
			})
		}
	}
}

func TestLocalnetServiceSourceCombinations(t *testing.T) {
	services := []struct{ name, key string }{{"algod", "algodSource"}, {"indexer", "indexerSource"}, {"conduit", "conduitSource"}}
	for mask := 1; mask < 8; mask++ {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("mask=%d/update=%t", mask, update), func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				config := map[string]any{}
				selected := map[string]bool{}
				for i, service := range services {
					if mask&(1<<i) != 0 {
						config[service.key] = map[string]string{"ref": "feature/test"}
						selected[service.name] = true
					}
				}
				data, _ := json.Marshal(config)
				path := localnetConfigFile(env, string(data))
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--localnet-config", path), 0)
				if len(sourceBuilds(env)) != len(selected) {
					t.Fatalf("incorrect build count: %v", sourceBuilds(env))
				}
				sb, err := sandbox.New("test", env.AppConfigDir())
				if err != nil {
					t.Fatal(err)
				}
				if status, err := sb.ComposeFileStatus(); err != nil || status != sandbox.UpToDate {
					t.Errorf("source Compose definition not up to date: %v, %v", status, err)
				}
				env.MockRunningLocalnet("sandbox_test")
				args := []string{"localnet", "reset"}
				if update {
					args = append(args, "--update")
				}
				assertExitCode(t, env.Invoke("", args...), 0)
				wantBuilds := len(selected)
				if update {
					wantBuilds *= 2
				}
				if len(sourceBuilds(env)) != wantBuilds {
					t.Errorf("reset builds = %d, want %d", len(sourceBuilds(env)), wantBuilds)
				}
				compose := env.ReadFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"))
				for name := range selected {
					assertContains(t, compose, "image: algokit-"+name+":")
				}
				pull := "docker compose pull --ignore-pull-failures --quiet"
				for _, name := range []string{"algod", "conduit", "indexer-db", "indexer"} {
					if !selected[name] {
						pull += " " + name
					}
				}
				assertRan(t, env, pull, update)
				for _, request := range env.HTTP.Requests {
					if strings.Contains(request.URL.String(), "hub.docker.com") {
						t.Errorf("unexpected published-image check: %s", request.URL)
					}
				}
			})
		}
	}
}

func TestLocalnetAdditionalSourcesInvalidConfig(t *testing.T) {
	for _, key := range []string{"indexerSource", "conduitSource"} {
		for _, value := range []string{`{}`, `null`, `"main"`, `{"ref":""}`, `{"repo":" "}`, `{"ref":123}`, `{"reff":"main"}`} {
			t.Run(key+value, func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockRunningLocalnet("sandbox")
				path := localnetConfigFile(env, fmt.Sprintf(`{"%s":%s}`, key, value))
				result := env.Invoke("", "localnet", "start", "--name", "test", "--localnet-config", path)
				assertExitCode(t, result, 1)
				assertContains(t, result.Output, "invalid LocalNet config")
				if len(sourceBuilds(env)) != 0 || len(env.ConfirmCalls) != 0 {
					t.Error("invalid config caused a build or restart prompt")
				}
			})
		}
	}
}

func TestLocalnetAdditionalSourceBuildFailure(t *testing.T) {
	for _, service := range []string{"indexer", "conduit"} {
		t.Run(service, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.WriteLatestCompose("sandbox")
			env.Proc.BadExit("docker build")
			path := localnetConfigFile(env, fmt.Sprintf(`{"%sSource":{"ref":"missing"}}`, service))
			result := env.Invoke("", "localnet", "start", "--localnet-config", path)
			assertExitCode(t, result, 1)
			assertContains(t, result.Output, "Failed to build "+service+" from source")
			if got := env.ReadFile(filepath.Join(env.SandboxDir("sandbox"), "docker-compose.yml")); got != sandbox.DockerComposeYAML("algokit_sandbox") {
				t.Error("failed build changed Compose configuration")
			}
			if _, err := os.Stat(filepath.Join(env.SandboxDir("sandbox"), service+"-source.json")); !os.IsNotExist(err) {
				t.Error("failed build saved source selection")
			}
			assertRan(t, env, "docker compose up --detach --quiet-pull --wait", false)
		})
	}
}
