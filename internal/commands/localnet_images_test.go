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

var imageServices = []struct{ name, key, repo string }{
	{"algod", "algodSource", sandbox.DefaultAlgodRepo},
	{"indexer", "indexerSource", sandbox.DefaultIndexerRepo},
	{"conduit", "conduitSource", sandbox.DefaultConduitRepo},
}

func TestLocalnetPrebuiltImages(t *testing.T) {
	for _, service := range imageServices {
		for _, image := range []string{"custom:dev", "localhost:5000/team/service:test", "registry.example/service@sha256:" + strings.Repeat("a", 64)} {
			t.Run(service.name+"/"+image, func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				data, _ := json.Marshal(map[string]any{service.key: map[string]string{"image": image}})
				path := localnetConfigFile(env, string(data))
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--config", path), 0)
				if len(sourceBuilds(env)) != 0 {
					t.Fatal("image selection triggered a source build")
				}
				dir := env.SandboxDir("sandbox_test")
				compose := env.ReadFile(filepath.Join(dir, "docker-compose.yml"))
				assertContains(t, compose, fmt.Sprintf("image: %q", image))
				assertNotContains(t, compose, "pull_policy: never")
				var saved sandbox.ServiceSource
				if err := json.Unmarshal([]byte(env.ReadFile(filepath.Join(dir, service.name+"-source.json"))), &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Image != image || saved.Repo != "" || saved.Ref != "" {
					t.Errorf("incorrect saved selection: %+v", saved)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test"), 0)
				sb, err := sandbox.New("test", env.AppConfigDir())
				if err != nil {
					t.Fatal(err)
				}
				if status, err := sb.ComposeFileStatus(); err != nil || status != sandbox.UpToDate {
					t.Errorf("custom image is considered outdated: %v, %v", status, err)
				}
			})
		}
	}
}

func TestLocalnetSwitchBetweenImageAndSource(t *testing.T) {
	for _, service := range imageServices {
		t.Run(service.name, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.MockHealthSuccess()
			path := localnetConfigFile(env, fmt.Sprintf(`{"%s":{"repo":"https://example.com/fork.git","ref":"branch"}}`, service.key))
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--config", path), 0)
			for _, image := range []string{"custom:first", "custom:second"} {
				path = localnetConfigFile(env, fmt.Sprintf(`{"%s":{"image":%q}}`, service.key, image))
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--config", path), 0)
				compose := env.ReadFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"))
				assertContains(t, compose, fmt.Sprintf("image: %q", image))
				assertNotContains(t, compose, "pull_policy: never")
			}
			if len(sourceBuilds(env)) != 1 {
				t.Fatal("switching to prebuilt images built a source")
			}
			path = localnetConfigFile(env, fmt.Sprintf(`{"%s":{"ref":"next"}}`, service.key))
			assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--config", path), 0)
			builds := sourceBuilds(env)
			if len(builds) != 2 {
				t.Fatalf("switching back to source did not build: %v", builds)
			}
			prefix := "SOURCE"
			if service.name == "algod" {
				prefix = "ALGOD"
			}
			for _, arg := range []string{prefix + "_REPO=" + service.repo, prefix + "_REF=next"} {
				if !slices.Contains(builds[1].Command, arg) {
					t.Errorf("switching back used stale source settings, missing %q", arg)
				}
			}
			compose := env.ReadFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"))
			assertContains(t, compose, "image: algokit-"+service.name+":")
			assertContains(t, compose, "pull_policy: never")
			assertNotContains(t, compose, "custom:second")
		})
	}
}

func TestLocalnetMixedImageSourceUpdates(t *testing.T) {
	// -1 selects all prebuilt images; otherwise one service is source-built.
	for build := -1; build < len(imageServices); build++ {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("build=%d/update=%t", build, update), func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockNoRunningLocalnet()
				env.MockHealthSuccess()
				config := map[string]any{}
				for i, service := range imageServices {
					if i == build {
						config[service.key] = map[string]string{"ref": "feature/test"}
					} else {
						config[service.key] = map[string]string{"image": "custom/" + service.name + ":dev"}
					}
				}
				data, _ := json.Marshal(config)
				path := localnetConfigFile(env, string(data))
				assertExitCode(t, env.Invoke("", "localnet", "start", "--name", "test", "--config", path), 0)
				env.MockRunningLocalnet("sandbox_test")
				args := []string{"localnet", "reset"}
				if update {
					args = append(args, "--update")
				}
				assertExitCode(t, env.Invoke("", args...), 0)
				wantBuilds := 0
				if build >= 0 {
					wantBuilds = 1
					if update {
						wantBuilds++
					}
				}
				if len(sourceBuilds(env)) != wantBuilds {
					t.Errorf("build count = %d, want %d", len(sourceBuilds(env)), wantBuilds)
				}
				pull := "docker compose pull --ignore-pull-failures --quiet"
				for _, name := range []string{"algod", "conduit", "indexer-db", "indexer"} {
					if build < 0 || name != imageServices[build].name {
						pull += " " + name
					}
				}
				assertRan(t, env, pull, update)
				compose := env.ReadFile(filepath.Join(env.SandboxDir("sandbox_test"), "docker-compose.yml"))
				for i, service := range imageServices {
					if i != build {
						assertContains(t, compose, fmt.Sprintf("image: %q", "custom/"+service.name+":dev"))
					}
				}
			})
		}
	}
}

func TestLocalnetPrebuiltImagesDefaultReset(t *testing.T) {
	env := setupLocalnet(t)
	env.MockNoRunningLocalnet()
	env.MockHealthSuccess()
	path := localnetConfigFile(env, `{"algodSource":{"image":"custom/algod:dev"},"indexerSource":{"image":"custom/indexer:dev"},"conduitSource":{"image":"custom/conduit:dev"}}`)
	assertExitCode(t, env.Invoke("", "localnet", "start", "--config", path), 0)
	env.MockRunningLocalnet("sandbox")
	composePath := filepath.Join(env.AppConfigDir(), "sandbox", "docker-compose.yml")
	env.WriteFile(composePath, "outdated")
	assertExitCode(t, env.Invoke("", "localnet", "reset", "--update"), 0)
	for _, service := range imageServices {
		assertContains(t, env.ReadFile(composePath), fmt.Sprintf("image: %q", "custom/"+service.name+":dev"))
	}
	if len(sourceBuilds(env)) != 0 {
		t.Error("regenerating an image-based LocalNet triggered builds")
	}
	assertRan(t, env, "docker compose pull --ignore-pull-failures --quiet algod conduit indexer-db indexer", true)
}

func TestLocalnetRejectsInvalidSavedImageSelection(t *testing.T) {
	for _, service := range imageServices {
		t.Run(service.name, func(t *testing.T) {
			env := setupLocalnet(t)
			env.MockNoRunningLocalnet()
			env.WriteFile(filepath.Join(env.SandboxDir("sandbox"), service.name+"-source.json"), `{"image":"custom:tag","ref":"main"}`)
			result := env.Invoke("", "localnet", "start")
			assertExitCode(t, result, 1)
			assertContains(t, result.Output, "image cannot be combined with repo or ref")
			assertRan(t, env, "docker compose up --detach --quiet-pull --wait", false)
		})
	}
}

func TestLocalnetRejectsInvalidImageSelection(t *testing.T) {
	for _, service := range imageServices {
		for _, value := range []string{
			`{"image":""}`, `{"image":" "}`, `{"image":null}`, `{"image":123}`, `{"image":{}}`,
			`{"image":"custom:tag","repo":"https://example.com/fork.git"}`,
			`{"image":"custom:tag","ref":"main"}`, `{"image":"custom:tag","ref":""}`,
			`{"image":"custom:tag\nother: true"}`, `{"image":"${IMAGE}"}`,
		} {
			t.Run(service.name+value, func(t *testing.T) {
				env := setupLocalnet(t)
				env.MockRunningLocalnet("sandbox")
				path := localnetConfigFile(env, fmt.Sprintf(`{"%s":%s}`, service.key, value))
				result := env.Invoke("", "localnet", "start", "--name", "test", "--config", path)
				assertExitCode(t, result, 1)
				assertContains(t, result.Output, "invalid LocalNet config")
				if len(sourceBuilds(env)) != 0 || len(env.ConfirmCalls) != 0 {
					t.Error("invalid image selection caused a build or restart prompt")
				}
				assertRan(t, env, "docker compose stop", false)
			})
		}
	}
}
