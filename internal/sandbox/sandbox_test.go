package sandbox_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/sandbox"
	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

func newSandbox(t *testing.T) *sandbox.ComposeSandbox {
	t.Helper()
	sb, err := sandbox.New(sandbox.BaseName, "")
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func assertStatus(t *testing.T, sb *sandbox.ComposeSandbox, want sandbox.ComposeFileStatus) {
	t.Helper()
	if got, err := sb.ComposeFileStatus(); err != nil || got != want {
		t.Errorf("status = %v, %v; want %v", got, err, want)
	}
}

func TestComposeFileStatusMissing(t *testing.T) {
	testsupport.Setup(t)
	assertStatus(t, newSandbox(t), sandbox.Missing)
}

func TestComposeFileStatusIgnoresDevMode(t *testing.T) {
	env := testsupport.Setup(t)
	env.WriteLatestCompose("sandbox")
	sb := newSandbox(t)
	assertStatus(t, sb, sandbox.UpToDate)

	if err := sb.SetAlgodDevMode(false); err != nil {
		t.Fatal(err)
	}
	if devMode, err := sb.IsAlgodDevMode(); err != nil || devMode {
		t.Errorf("dev mode = %v, %v", devMode, err)
	}
	assertStatus(t, sb, sandbox.UpToDate)
}

func TestComposeFileStatusOutOfDate(t *testing.T) {
	env := testsupport.Setup(t)
	env.WriteLatestCompose("sandbox")
	templatePath := filepath.Join(env.SandboxDir("sandbox"), "algod_network_template.json")
	sb := newSandbox(t)

	env.WriteFile(templatePath, strings.Replace(sandbox.AlgodNetworkTemplate(), `"Stake": 20`, `"Stake": 21`, 1))
	assertStatus(t, sb, sandbox.OutOfDate)

	env.WriteFile(templatePath, "{not json")
	assertStatus(t, sb, sandbox.OutOfDate)

	env.WriteFile(templatePath, strings.Replace(sandbox.AlgodNetworkTemplate(), `"DevMode": true`, `"Dev": true`, 1))
	assertStatus(t, sb, sandbox.OutOfDate)
}

func TestComposeFileStatusNamed(t *testing.T) {
	env := testsupport.Setup(t)
	env.WriteLatestCompose("sandbox_test")
	sb, err := sandbox.New("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if sb.Name != "sandbox_test" || sb.Directory != filepath.Join(env.AppConfigDir(), "sandbox_test") {
		t.Errorf("unexpected sandbox %+v", sb)
	}
	assertStatus(t, sb, sandbox.UpToDate)
}

func TestDockerComposeYAMLName(t *testing.T) {
	yaml := sandbox.DockerComposeYAML("algokit_sandbox_test")
	for _, want := range []string{`name: "algokit_sandbox_test"`, `container_name: "algokit_sandbox_test_algod"`, "- 4001:8080"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("compose yaml doesn't contain %q", want)
		}
	}
	if strings.Contains(yaml, "{{") {
		t.Error("compose yaml contains unreplaced placeholders")
	}
}
