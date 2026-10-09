// Package clierr defines the errors that control how the CLI exits, mirroring click's exception types.
package clierr

import "fmt"

// Error is a user-facing error; printed as `Error: <message>` with exit code 1 (equivalent to click.ClickException).
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

// New returns a user-facing error.
func New(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// UsageError is a command line usage error; printed with the command's usage and exits with code 2
// (equivalent to click.UsageError).
type UsageError struct {
	Message string
	// Usage is the usage line of the command the error relates to, e.g. `algokit localnet start [OPTIONS]`.
	Usage string
	// HelpHint is the command to run for help, e.g. `algokit localnet start -h`.
	HelpHint string
}

func (e *UsageError) Error() string { return e.Message }

// Exit ends the CLI with the given code without printing anything further (equivalent to click.exceptions.Exit).
type Exit struct {
	Code int
}

func (e *Exit) Error() string { return fmt.Sprintf("exit with code %d", e.Code) }

// Abort ends the CLI with `Aborted!` and exit code 1 (equivalent to click.Abort).
type Abort struct{}

func (e *Abort) Error() string { return "Aborted!" }

// CommandNotFound is returned when an executable can't be found or started (equivalent to Python's OSError from Popen).
type CommandNotFound struct {
	Command string
	Err     error
}

func (e *CommandNotFound) Error() string {
	return fmt.Sprintf("command not found or not executable: %s: %v", e.Command, e.Err)
}

func (e *CommandNotFound) Unwrap() error { return e.Err }

// FileNotFound mirrors Python's FileNotFoundError, which is reported by name when unhandled.
type FileNotFound struct {
	Message string
}

func (e *FileNotFound) Error() string     { return e.Message }
func (e *FileNotFound) ErrorName() string { return "FileNotFoundError" }
