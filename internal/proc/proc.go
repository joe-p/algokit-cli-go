// Package proc runs external processes with logging, equivalent to algokit.core.proc.
package proc

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/log"
)

var logger = log.New("algokit.core.proc")

// Spec describes a process to execute.
type Spec struct {
	Args []string
	Dir  string
	Env  []string
	// Interactive attaches the process to the current terminal (stdin/stdout/stderr); Stdout/Stderr are ignored.
	Interactive bool
	// PassStdin connects our stdin to the process (non-interactive mode only).
	PassStdin bool
	Stdout    io.Writer
	Stderr    io.Writer
}

// Executor runs a process to completion and returns its exit code. It returns an error only if the process could
// not be started. Replaceable for testing.
var Executor = execute

func execute(spec Spec) (int, error) {
	cmd := exec.Command(spec.Args[0], spec.Args[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	if spec.Interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	} else {
		if spec.PassStdin {
			cmd.Stdin = os.Stdin
		}
		cmd.Stdout, cmd.Stderr = spec.Stdout, spec.Stderr
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return -1, err
	}
	return exitCode(cmd.ProcessState), nil
}

// exitCode mirrors Python's subprocess returncode, which is the negated signal number if the process was killed.
func exitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}

// Result is the outcome of running a process.
type Result struct {
	Command  string
	ExitCode int
	// Output is the process's stdout and stderr, interleaved, with line endings normalised to "\n".
	Output string
}

// Options configures Run.
type Options struct {
	Dir string
	Env []string
	// BadReturnCodeErrorMessage, if set, is returned as a user-facing error when the process exits non-zero.
	BadReturnCodeErrorMessage string
	// Passthrough writes the process's stdout/stderr through to ours unchanged (still captured, and logged to the
	// log file only), rather than logging each line.
	Passthrough bool
	// StdoutLogLevel is the level each output line is logged at (when not passing through). Defaults to Debug.
	StdoutLogLevel log.Level
	PassStdin      bool
}

func cwdOr(dir string) string {
	if dir != "" {
		return dir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// lineSplitter splits written bytes into lines using Python's universal newlines ("\n", "\r\n" and "\r" all end a
// line, and are normalised to "\n").
type lineSplitter struct {
	buf       strings.Builder
	pendingCR bool
	onLine    func(line string)
}

func (s *lineSplitter) Write(p []byte) (int, error) {
	for _, b := range p {
		if s.pendingCR {
			s.pendingCR = false
			if b == '\n' {
				continue
			}
		}
		switch b {
		case '\r':
			s.pendingCR = true
			s.flushLine()
		case '\n':
			s.flushLine()
		default:
			s.buf.WriteByte(b)
		}
	}
	return len(p), nil
}

func (s *lineSplitter) flushLine() {
	s.buf.WriteByte('\n')
	s.onLine(s.buf.String())
	s.buf.Reset()
}

func (s *lineSplitter) Close() {
	if s.buf.Len() > 0 {
		s.onLine(s.buf.String())
		s.buf.Reset()
	}
}

// teeWriter writes to w and then feeds the splitter, under a shared lock so stdout/stderr lines don't interleave.
type teeWriter struct {
	mu       *sync.Mutex
	w        func(string)
	splitter *lineSplitter
}

func (t *teeWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.w != nil {
		t.w(string(p))
	}
	return t.splitter.Write(p)
}

// Run runs a process, capturing stdout and stderr (interleaved) and logging each line as it is received.
// Equivalent to algokit.core.proc.run.
func Run(command []string, options Options) (Result, error) {
	commandStr := strings.Join(command, " ")
	logger.Debug("Running '" + commandStr + "' in '" + cwdOr(options.Dir) + "'")
	level := options.StdoutLogLevel
	if level == 0 {
		level = log.Debug
	}

	var lines []string
	onLine := func(line string) {
		lines = append(lines, line)
		message := log.Bold(command[0]+":") + " " + strings.TrimSpace(line)
		if options.Passthrough {
			logger.Debug(message, log.ExcludeFromConsole)
		} else {
			logger.Log(level, message)
		}
	}
	var mu sync.Mutex
	var splitters []*lineSplitter
	newWriter := func(w func(string)) *teeWriter {
		splitter := &lineSplitter{onLine: onLine}
		splitters = append(splitters, splitter)
		return &teeWriter{mu: &mu, w: w, splitter: splitter}
	}
	spec := Spec{Args: command, Dir: options.Dir, Env: options.Env, PassStdin: options.PassStdin}
	if options.Passthrough {
		spec.Stdout, spec.Stderr = newWriter(log.WriteStdout), newWriter(log.WriteStderr)
	} else {
		// the same writer for both, so the output is interleaved in the correct order (like stderr=STDOUT)
		w := newWriter(nil)
		spec.Stdout, spec.Stderr = w, w
	}

	code, err := Executor(spec)
	if err != nil {
		return Result{}, &clierr.CommandNotFound{Command: command[0], Err: err}
	}
	mu.Lock()
	for _, splitter := range splitters {
		splitter.Close()
	}
	mu.Unlock()
	return finish(commandStr, code, strings.Join(lines, ""), options.BadReturnCodeErrorMessage)
}

func finish(commandStr string, code int, output, badReturnCodeErrorMessage string) (Result, error) {
	result := Result{Command: commandStr, ExitCode: code, Output: output}
	if code == 0 {
		logger.Debug("'"+commandStr+"' completed successfully", log.ExcludeFromConsole)
		return result, nil
	}
	logger.Debug("'"+commandStr+"' failed, exited with code = "+strconv.Itoa(code), log.ExcludeFromConsole)
	if badReturnCodeErrorMessage != "" {
		return result, clierr.New("%s", badReturnCodeErrorMessage)
	}
	return result, nil
}

// InteractiveOptions configures RunInteractive.
type InteractiveOptions struct {
	Dir                       string
	Env                       []string
	BadReturnCodeErrorMessage string
}

// RunInteractive runs a process attached to the current terminal, logging the command being executed but not its
// output. Equivalent to algokit.core.proc.run_interactive.
func RunInteractive(command []string, options InteractiveOptions) (Result, error) {
	commandStr := strings.Join(command, " ")
	logger.Debug("Running '" + commandStr + "' in '" + cwdOr(options.Dir) + "'")

	// Like a shell, let the foreground child process handle Ctrl+C rather than exiting ourselves.
	// (Notify rather than Ignore, since ignored signals are inherited by the child.)
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	code, err := Executor(Spec{Args: command, Dir: options.Dir, Env: options.Env, Interactive: true})
	if err != nil {
		return Result{}, &clierr.CommandNotFound{Command: command[0], Err: err}
	}
	return finish(commandStr, code, "", options.BadReturnCodeErrorMessage)
}
