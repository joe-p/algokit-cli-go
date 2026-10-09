package commands

import (
	"errors"
	"slices"
	"strings"

	"github.com/joe-p/algokit-cli-go/internal/cli"
	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/engine"
	"github.com/joe-p/algokit-cli-go/internal/goal"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/proc"
	"github.com/joe-p/algokit-cli-go/internal/sandbox"
)

var goalLogger = log.New("algokit.cli.goal")

func goalCommand() *cli.Command {
	return &cli.Command{
		Name:      "goal",
		ShortHelp: "Run the Algorand goal CLI against the AlgoKit LocalNet.",
		Help: "Run the Algorand goal CLI against the AlgoKit LocalNet.\n\n" +
			"Look at https://dev.algorand.co/algokit/algokit-cli/goal for more information.",
		Options: []*cli.Option{
			{
				Key:  "console",
				Opts: []string{"--console"},
				Kind: cli.Flag,
				Help: "Open a Bash console so you can execute multiple goal commands and/or interact with a filesystem.",
			},
			{
				Key:  "interactive",
				Opts: []string{"--interactive"},
				Kind: cli.Flag,
				Help: "Force running the goal command in interactive mode.",
			},
		},
		Arguments:            []*cli.Argument{{Key: "goal_args", Metavar: "GOAL_ARGS", Variadic: true}},
		IgnoreUnknownOptions: true,
		Run: func(ctx *cli.Context) error {
			return runGoal(ctx.Bool("console"), ctx.Bool("interactive"), ctx.Args("goal_args"))
		},
	}
}

func runGoal(console, interactive bool, goalArgs []string) error {
	containerEngine := engine.Get()
	_, err := proc.Run([]string{containerEngine, "version"}, proc.Options{
		BadReturnCodeErrorMessage: containerEngine + " engine isn't running; please start it.",
	})
	var notFound *clierr.CommandNotFound
	if errors.As(err, &notFound) {
		// this will only occur if the container engine isn't an executable in the user's path
		docsURL := "https://podman.io/get-started"
		if containerEngine == engine.Docker {
			docsURL = "https://www.docker.com/get-started/"
		}
		return clierr.New("%s not found; please install %s and add to path.\nSee %s for more information.",
			containerEngine, containerEngine, docsURL)
	} else if err != nil {
		return err
	}

	sb, err := sandbox.FromEnvironment()
	if err == nil && sb == nil {
		sb, err = sandbox.New(sandbox.BaseName, "")
	}
	if err != nil {
		return err
	}
	if sb.Name != sandbox.BaseName {
		goalLogger.Info("A named LocalNet is running, goal command will be executed against the named LocalNet")
	}

	volumeMountPathLocal := goal.VolumeMountPathLocal(sb.Name)

	status, err := sb.ComposeFileStatus()
	if err != nil {
		return err
	}
	if status != sandbox.UpToDate && sb.Name == sandbox.BaseName {
		return clierr.New("LocalNet definition is out of date; please run `algokit localnet reset` first!")
	}
	ps, err := sb.PS("algod")
	if err != nil {
		return err
	}
	if !(len(ps) == 1 && ps[0].State() == "running") {
		goalLogger.Info("LocalNet isn't running")
		if err := sb.Up(); err != nil {
			return err
		}
	}

	var result proc.Result
	containerName := "algokit_" + sb.Name + "_algod"
	if console {
		if len(goalArgs) > 0 {
			goalLogger.Warning("--console opens an interactive shell, remaining arguments are being ignored")
		}
		goalLogger.Info("Opening Bash console on the algod node; execute `exit` to return to original console")
		result, err = proc.RunInteractive(
			[]string{containerEngine, "exec", "-it", "-w", "/root", containerName, "bash"}, proc.InteractiveOptions{})
		if err != nil {
			return err
		}
	} else {
		cmd := []string{containerEngine, "exec"}
		if interactive {
			cmd = append(cmd, "--tty")
		}
		cmd = append(cmd, "--interactive", "--workdir", "/root", containerName, "goal")
		inputFiles, outputFiles, args, err := goal.PreprocessCommandArgs(goalArgs, volumeMountPathLocal, goal.VolumeMountPathDocker)
		if err != nil {
			return err
		}
		cmd = append(cmd, args...)

		if interactive {
			result, err = proc.RunInteractive(cmd, proc.InteractiveOptions{})
		} else {
			// Try non-interactive first, fallback to interactive if it fails with input-related error
			result, err = proc.Run(cmd, proc.Options{Passthrough: true, PassStdin: true})
			if err == nil && result.ExitCode != 0 && strings.Contains(result.Output, "inappropriate ioctl") {
				// Fallback to interactive mode if we detect TTY-related errors
				goalLogger.Debug("Command failed with TTY error, retrying in interactive mode")
				result, err = proc.RunInteractive(slices.Insert(cmd, 2, "--tty"), proc.InteractiveOptions{})
			}
		}
		if err != nil {
			return err
		}
		if err := goal.PostProcess(inputFiles, outputFiles, volumeMountPathLocal); err != nil {
			return err
		}
	}

	if result.ExitCode != 0 {
		return &clierr.Exit{Code: result.ExitCode}
	}
	return nil
}
