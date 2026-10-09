// Package log provides console output and logging equivalent to the Python CLI's click/logging setup:
// INFO and above (or DEBUG with -v) is echoed to the console, and everything is written to a rotating cli.log
// in the app state directory (shared with the Python CLI).
package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/joe-p/algokit-cli-go/internal/conf"
)

type Level int

const (
	Debug    Level = 10
	Info     Level = 20
	Warning  Level = 30
	Error    Level = 40
	Critical Level = 50
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warning:
		return "WARNING"
	case Error:
		return "ERROR"
	case Critical:
		return "CRITICAL"
	}
	return fmt.Sprintf("Level %d", int(l))
}

type Color int

const (
	NoColor Color = 0
	Red     Color = 31
	Green   Color = 32
	Yellow  Color = 33
	Cyan    Color = 36
)

// Style wraps text in ANSI styling codes (equivalent to click.style). Codes are stripped on output when colour is off.
func Style(text string, fg Color, bold bool) string {
	codes := ""
	if fg != NoColor {
		codes += fmt.Sprintf("\x1b[%dm", int(fg))
	}
	if bold {
		codes += "\x1b[1m"
	}
	if codes == "" {
		return text
	}
	return codes + text + "\x1b[0m"
}

// Bold is shorthand for Style(text, NoColor, true).
func Bold(text string) string { return Style(text, NoColor, true) }

var ansiPattern = regexp.MustCompile(`\x1b\[[;?0-9]*[a-zA-Z]`)

// StripANSI removes ANSI styling codes (equivalent to click's strip_ansi).
func StripANSI(text string) string { return ansiPattern.ReplaceAllString(text, "") }

type levelStyle struct {
	fg   Color
	bold bool
}

var levelStyles = map[Level]levelStyle{
	Critical: {Red, true},
	Error:    {Red, false},
	Warning:  {Yellow, false},
	Debug:    {Cyan, false},
}

type outputState struct {
	mu sync.Mutex
	// color is true to force colour, false to disable colour, and nil to auto-detect based on whether output is a TTY
	color        *bool
	consoleLevel Level
	logFile      bool
	stdout       io.Writer
	stderr       io.Writer
	stdoutTTY    bool
	stderrTTY    bool
}

var state = newState()

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func newState() *outputState {
	return &outputState{
		consoleLevel: Info,
		logFile:      true,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		stdoutTTY:    isTerminal(os.Stdout),
		stderrTTY:    isTerminal(os.Stderr),
	}
}

// SetColor sets the explicit colour preference (nil to auto-detect).
func SetColor(color *bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.color = color
}

// ColorPreference returns the explicit colour preference, if any (equivalent to click's ctx.color).
func ColorPreference() *bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.color
}

// SetVerbose enables or disables DEBUG output to the console.
func SetVerbose(verbose bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if verbose {
		state.consoleLevel = Debug
	} else {
		state.consoleLevel = Info
	}
}

// SetLogFileEnabled enables or disables writing to cli.log.
func SetLogFileEnabled(enabled bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.logFile = enabled
}

// SetOutput redirects console output, primarily for testing. Returns a function that restores the previous state.
func SetOutput(stdout, stderr io.Writer, tty bool) (restore func()) {
	state.mu.Lock()
	defer state.mu.Unlock()
	prevStdout, prevStderr, prevStdoutTTY, prevStderrTTY := state.stdout, state.stderr, state.stdoutTTY, state.stderrTTY
	state.stdout, state.stderr = stdout, stderr
	state.stdoutTTY, state.stderrTTY = tty, tty
	return func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.stdout, state.stderr = prevStdout, prevStderr
		state.stdoutTTY, state.stderrTTY = prevStdoutTTY, prevStderrTTY
	}
}

// Reset restores the default verbosity and colour settings.
func Reset() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.color = nil
	state.consoleLevel = Info
}

// Stdout returns the current stdout writer.
func Stdout() io.Writer {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.stdout
}

// Stderr returns the current stderr writer.
func Stderr() io.Writer {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.stderr
}

func (s *outputState) shouldStrip(tty bool) bool {
	if s.color != nil {
		return !*s.color
	}
	return !tty
}

// WriteStdout writes text to stdout exactly as given (no newline, styling or stripping).
func WriteStdout(text string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, _ = io.WriteString(state.stdout, text)
}

// WriteStderr writes text to stderr exactly as given (no newline, styling or stripping).
func WriteStderr(text string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, _ = io.WriteString(state.stderr, text)
}

func (s *outputState) echo(message string, toStderr bool) {
	w, tty := s.stdout, s.stdoutTTY
	if toStderr {
		w, tty = s.stderr, s.stderrTTY
	}
	if s.shouldStrip(tty) {
		message = StripANSI(message)
	}
	_, _ = io.WriteString(w, message+"\n")
}

// Echo prints a line to stdout (equivalent to click.echo).
func Echo(message string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.echo(message, false)
}

// EchoErr prints a line to stderr (equivalent to click.echo(err=True)).
func EchoErr(message string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.echo(message, true)
}

const (
	logFileMaxBytes    = 1 * 1024 * 1024
	logFileBackupCount = 5
)

// rotateLogFile mirrors logging.handlers.RotatingFileHandler.doRollover.
func rotateLogFile(logFile string) {
	for i := logFileBackupCount - 1; i >= 1; i-- {
		source := fmt.Sprintf("%s.%d", logFile, i)
		if _, err := os.Stat(source); err == nil {
			dest := fmt.Sprintf("%s.%d", logFile, i+1)
			_ = os.Remove(dest)
			_ = os.Rename(source, dest)
		}
	}
	first := logFile + ".1"
	_ = os.Remove(first)
	_ = os.Rename(logFile, first)
}

func (s *outputState) writeToLogFile(name string, level Level, message string, err error) {
	if !s.logFile {
		return
	}
	logFile := filepath.Join(conf.AppStateDir(), "cli.log")
	now := time.Now()
	line := fmt.Sprintf("%s.%03d %s %s %s\n",
		now.Format("2006-01-02T15:04:05"), now.Nanosecond()/int(time.Millisecond), name, level, StripANSI(message))
	if err != nil {
		line += fmt.Sprintf("%s: %v\n", ErrorName(err), err)
	}
	if info, statErr := os.Stat(logFile); statErr == nil && info.Size()+int64(len(line)) >= logFileMaxBytes {
		rotateLogFile(logFile)
	}
	// never let log file failures interrupt the CLI
	f, openErr := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if openErr != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// ErrorName returns a Python-style exception name for err, used when reporting unhandled errors.
func ErrorName(err error) string {
	if named, ok := err.(interface{ ErrorName() string }); ok {
		return named.ErrorName()
	}
	name := fmt.Sprintf("%T", err)
	name = strings.TrimPrefix(name, "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

type record struct {
	excludeConsole bool
	excludeLogFile bool
	err            error
}

// Option customises a single log record.
type Option func(*record)

// ExcludeFromConsole writes the record to the log file only.
func ExcludeFromConsole(r *record) { r.excludeConsole = true }

// ExcludeFromLogFile writes the record to the console only.
func ExcludeFromLogFile(r *record) { r.excludeLogFile = true }

// WithError attaches error details, written to the log file only (equivalent to exc_info=...).
func WithError(err error) Option { return func(r *record) { r.err = err } }

// Logger writes records under a name, like a Python logging.Logger.
type Logger struct {
	Name string
}

// New returns a logger with the given (Python module style) name.
func New(name string) Logger { return Logger{Name: name} }

func (l Logger) Log(level Level, message string, options ...Option) {
	var r record
	for _, option := range options {
		option(&r)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !r.excludeLogFile {
		state.writeToLogFile(l.Name, level, message, r.err)
	}
	if r.excludeConsole || level < state.consoleLevel {
		return
	}
	if style, ok := levelStyles[level]; ok {
		// if the user hasn't disabled colours/styling use that, otherwise prefix the level name
		if state.color == nil || *state.color {
			message = Style(message, style.fg, style.bold)
		} else {
			message = level.String() + ": " + message
		}
	}
	state.echo(message, false)
}

func (l Logger) Debug(message string, options ...Option)    { l.Log(Debug, message, options...) }
func (l Logger) Info(message string, options ...Option)     { l.Log(Info, message, options...) }
func (l Logger) Warning(message string, options ...Option)  { l.Log(Warning, message, options...) }
func (l Logger) Error(message string, options ...Option)    { l.Log(Error, message, options...) }
func (l Logger) Critical(message string, options ...Option) { l.Log(Critical, message, options...) }
