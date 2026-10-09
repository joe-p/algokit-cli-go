package log

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	var buf bytes.Buffer
	t.Cleanup(SetOutput(&buf, &buf, false))
	t.Cleanup(Reset)
	return &buf, filepath.Join(stateDir, "algokit", "cli.log")
}

func TestConsoleLevelsAndStyling(t *testing.T) {
	buf, _ := setup(t)
	logger := New("test")

	logger.Debug("hidden")
	logger.Info("info")
	no := false
	SetColor(&no)
	logger.Warning("warn")
	yes := true
	SetColor(&yes)
	SetVerbose(true)
	logger.Debug("shown")

	want := "info\nWARNING: warn\n\x1b[36mshown\x1b[0m\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestAutoColorStripsWhenNotATerminal(t *testing.T) {
	buf, _ := setup(t)
	New("test").Error("bad " + Bold("thing"))
	if buf.String() != "bad thing\n" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestLogFile(t *testing.T) {
	_, logFile := setup(t)
	New("algokit.test").Info("to " + Bold("file"))
	New("algokit.test").Debug("console only", ExcludeFromLogFile)

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(content))
	if !strings.HasSuffix(line, " algokit.test INFO to file") || strings.Count(string(content), "\n") != 1 {
		t.Errorf("log file = %q", content)
	}
}

func TestLogFileRotation(t *testing.T) {
	_, logFile := setup(t)
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= logFileBackupCount; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", logFile, i), fmt.Append(nil, i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(logFile, bytes.Repeat([]byte("x"), logFileMaxBytes), 0o644); err != nil {
		t.Fatal(err)
	}

	New("test").Info("new")

	read := func(path string) string { content, _ := os.ReadFile(path); return string(content) }
	if got := read(logFile + ".2"); got != "1" {
		t.Errorf("cli.log.2 = %q", got)
	}
	if got := read(logFile + ".5"); got != "4" {
		t.Errorf("cli.log.5 = %q", got)
	}
	if got := len(read(logFile + ".1")); got != logFileMaxBytes {
		t.Errorf("cli.log.1 has %d bytes", got)
	}
	if got := read(logFile); !strings.HasSuffix(got, "test INFO new\n") {
		t.Errorf("cli.log = %q", got)
	}
}
