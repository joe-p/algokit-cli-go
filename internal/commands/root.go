// Package commands implements the algokit CLI's commands, equivalent to algokit.cli.
package commands

import (
	"errors"
	"os"
	"runtime/debug"

	"github.com/joe-p/algokit-cli-go/internal/cli"
	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/log"
)

// Version is the CLI version, set at build time with -ldflags "-X github.com/joe-p/algokit-cli-go/internal/commands.Version=...".
var Version = ""

func version() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Root returns the root `algokit` command.
func Root() *cli.Command {
	return &cli.Command{
		Name: "algokit",
		Help: "AlgoKit is your one-stop shop to develop applications on the Algorand blockchain.\n\n" +
			"If you are getting started, please see the quick start tutorial: " +
			"https://dev.algorand.co/getting-started/algokit-quick-start/.",
		Options: []*cli.Option{
			{
				Key:  "version",
				Opts: []string{"--version"},
				Kind: cli.Flag,
				Help: "Show the version and exit.",
				Eager: func(ctx *cli.Context) error {
					log.Echo("algokit, version " + version())
					return &clierr.Exit{Code: 0}
				},
			},
			{
				Key:  "verbose",
				Opts: []string{"-v", "--verbose"},
				Kind: cli.Flag,
				Help: "Enable logging of DEBUG messages to the console.",
			},
			{
				Key:           "color",
				Opts:          []string{"--color"},
				SecondaryOpts: []string{"--no-color"},
				Kind:          cli.BoolPair,
				Help:          "Force enable or disable of console output styling.",
				// support NO_COLOR (ref: https://no-color.org) env var as default value
				DefaultFunc: func() (any, bool) { return false, os.Getenv("NO_COLOR") != "" },
			},
			{
				// accepted for compatibility with the Python CLI; this CLI has no version check
				Key:    "skip_version_check",
				Opts:   []string{"--skip-version-check"},
				Kind:   cli.Flag,
				Help:   "Skip version checking and prompting.",
				Hidden: true,
			},
		},
		OnParsed: func(ctx *cli.Context) error {
			log.SetVerbose(ctx.Bool("verbose"))
			if value, ok := ctx.Value("color"); ok {
				color := value.(bool)
				log.SetColor(&color)
			}
			return nil
		},
		Commands: []*cli.Command{goalCommand(), localnetCommand()},
	}
}

// Main runs the CLI with the given arguments (excluding the program name), returning the process exit code.
func Main(args []string) int {
	log.Reset()
	return handleError(cli.Execute(Root(), args))
}

func handleError(err error) int {
	if err == nil {
		return 0
	}
	var exit *clierr.Exit
	var usage *clierr.UsageError
	var abort *clierr.Abort
	var cliErr *clierr.Error
	switch {
	case errors.As(err, &exit):
		return exit.Code
	case errors.As(err, &usage):
		if usage.Usage != "" {
			log.EchoErr(usage.Usage + "\nTry '" + usage.HelpHint + "' for help.\n")
		}
		log.EchoErr("Error: " + usage.Message)
		return 2
	case errors.As(err, &abort):
		log.EchoErr("Aborted!")
		return 1
	case errors.As(err, &cliErr):
		log.EchoErr("Error: " + cliErr.Message)
		return 1
	}
	// Like the Python CLI's uncaught exception handler, which runs outside of the click context (so styling is
	// always applied, and stripped if output isn't a terminal).
	log.SetColor(nil)
	log.New("root").Critical("Unhandled "+log.ErrorName(err)+": "+err.Error(), log.WithError(err))
	return 1
}
