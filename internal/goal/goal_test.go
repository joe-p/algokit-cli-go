package goal_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/goal"
	"github.com/joe-p/algokit-cli-go/internal/testsupport"
)

func TestIsPathOrFilename(t *testing.T) {
	for arg, want := range map[string]bool{
		"approval.teal":     true,
		"./approval.teal":   true,
		"dir/file":          true,
		"/abs/path":         true,
		"~/file.txt":        true,
		"my-file_1.v2.json": true,
		"ünïcødé.teal":      true,
		"-o":                false,
		"account":           false,
		"1000":              false,
		"--out=file":        false,
		"noext.":            false,
		".hidden":           false,
		".":                 false,
		"":                  false,
		"/":                 false,
	} {
		if got := goal.IsPathOrFilename(arg); got != want {
			t.Errorf("IsPathOrFilename(%q) = %v, want %v", arg, got, want)
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestOutputFlagsAreOutputsEvenIfTheFileExists(t *testing.T) {
	env := testsupport.Setup(t)
	cwd, mount := filepath.Join(env.Root, "cwd"), filepath.Join(env.Root, "mount")
	env.WriteFile(filepath.Join(cwd, "existing.out"), "old")
	env.WriteFile(filepath.Join(cwd, "in.teal"), "in")

	inputs, outputs, command, err := goal.PreprocessCommandArgs(
		[]string{"clerk", "compile", filepath.Join(cwd, "in.teal"), "--outfile", filepath.Join(cwd, "existing.out")},
		mount, goal.VolumeMountPathDocker)

	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"clerk", "compile", "/root/goal_mount/in.teal", "--outfile", "/root/goal_mount/existing.out"}; !slices.Equal(command, want) {
		t.Errorf("command = %v", command)
	}
	if !slices.Equal(inputs, []string{filepath.Join(cwd, "in.teal")}) || !slices.Equal(outputs, []string{filepath.Join(cwd, "existing.out")}) {
		t.Errorf("inputs = %v, outputs = %v", inputs, outputs)
	}
	if got := env.ReadFile(filepath.Join(mount, "in.teal")); got != "in" {
		t.Errorf("copied input = %q", got)
	}

	env.WriteFile(filepath.Join(mount, "existing.out"), "new")
	if err := goal.PostProcess(inputs, outputs, mount); err != nil {
		t.Fatal(err)
	}
	if got := env.ReadFile(filepath.Join(cwd, "existing.out")); got != "new" {
		t.Errorf("output = %q", got)
	}
	if exists(filepath.Join(mount, "in.teal")) {
		t.Error("input wasn't removed from the mount")
	}
}

func TestPostProcessWithoutExtensionAndRegexCharacters(t *testing.T) {
	env := testsupport.Setup(t)
	cwd, mount := filepath.Join(env.Root, "cwd"), filepath.Join(env.Root, "mount")
	env.WriteFile(filepath.Join(mount, "out+file"), "a")
	env.WriteFile(filepath.Join(mount, "out+file-2"), "b")
	env.WriteFile(filepath.Join(mount, "outtfile"), "c")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := goal.PostProcess(nil, []string{filepath.Join(cwd, "out+file")}, mount); err != nil {
		t.Fatal(err)
	}

	if env.ReadFile(filepath.Join(cwd, "out+file")) != "a" || env.ReadFile(filepath.Join(cwd, "out+file-2")) != "b" {
		t.Error("outputs weren't copied")
	}
	if exists(filepath.Join(cwd, "outtfile")) {
		t.Error("outtfile shouldn't match")
	}
}

func TestPreprocessMissingInput(t *testing.T) {
	env := testsupport.Setup(t)
	t.Chdir(env.Root)
	_, _, _, err := goal.PreprocessCommandArgs([]string{"clerk", "compile", "missing.teal"}, filepath.Join(env.Root, "mount"), goal.VolumeMountPathDocker)
	if err == nil || err.Error() != "missing.teal does not exist." {
		t.Errorf("err = %v", err)
	}
}
