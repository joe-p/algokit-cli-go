package proc

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/log"
)

func TestLineSplitterUniversalNewlines(t *testing.T) {
	var lines []string
	s := &lineSplitter{onLine: func(line string) { lines = append(lines, line) }}
	// split across writes, including a "\r\n" split between writes
	for _, chunk := range []string{"one\r", "\ntwo\rthr", "ee\nfour"} {
		_, _ = s.Write([]byte(chunk))
	}
	s.Close()
	if want := []string{"one\n", "two\n", "three\n", "four"}; !slices.Equal(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}

func captureLog(t *testing.T, verbose bool) *bytes.Buffer {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var buf bytes.Buffer
	t.Cleanup(log.SetOutput(&buf, &buf, false))
	no := false
	log.SetColor(&no)
	log.SetVerbose(verbose)
	t.Cleanup(log.Reset)
	return &buf
}

func TestRunRealProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	buf := captureLog(t, true)

	result, err := Run([]string{"sh", "-c", "echo out; echo err >&2; exit 3"}, Options{})

	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 || result.Output != "out\nerr\n" {
		t.Errorf("result = %+v", result)
	}
	if want := "DEBUG: sh: out\nDEBUG: sh: err\n"; !bytes.Contains(buf.Bytes(), []byte(want)) {
		t.Errorf("log output = %q", buf.String())
	}
}

func TestRunPassthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	buf := captureLog(t, true)

	result, err := Run([]string{"sh", "-c", "printf 'prompt: '"}, Options{Passthrough: true})

	if err != nil {
		t.Fatal(err)
	}
	// output without a trailing newline is passed through immediately and unchanged
	if result.Output != "prompt: " || !bytes.HasSuffix(buf.Bytes(), []byte("prompt: ")) {
		t.Errorf("result = %+v, output = %q", result, buf.String())
	}
}

func TestRunSignalledExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	captureLog(t, false)
	result, err := Run([]string{"sh", "-c", "kill -TERM $$"}, Options{})
	if err != nil || result.ExitCode != -15 {
		t.Errorf("result = %+v, %v", result, err)
	}
}

func TestRunBadReturnCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	captureLog(t, false)
	_, err := Run([]string{"sh", "-c", "exit 1"}, Options{BadReturnCodeErrorMessage: "it failed"})
	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) || cliErr.Message != "it failed" {
		t.Errorf("err = %v", err)
	}
}

func TestRunNotFound(t *testing.T) {
	captureLog(t, false)
	_, err := Run([]string{"definitely-not-a-real-command-algokit"}, Options{})
	var notFound *clierr.CommandNotFound
	if !errors.As(err, &notFound) || !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
