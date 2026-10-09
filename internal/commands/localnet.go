package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joe-p/algokit-cli-go/internal/cli"
	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/engine"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/proc"
	"github.com/joe-p/algokit-cli-go/internal/prompt"
	"github.com/joe-p/algokit-cli-go/internal/sandbox"
	"github.com/joe-p/algokit-cli-go/internal/versions"
)

var localnetLogger = log.New("algokit.cli.localnet")

// ServiceNames are the LocalNet compose services, in the order they're reported by `localnet status`.
var ServiceNames = []string{"algod", "conduit", "indexer-db", "indexer"}

// checkContainerEngine runs before every localnet subcommand.
func checkContainerEngine(_ *cli.Context, subcommand string) error {
	composeVersionResult, err := proc.Run(sandbox.ComposeVersionCommand(), proc.Options{})
	var notFound *clierr.CommandNotFound
	if errors.As(err, &notFound) {
		// this will only occur if the container engine isn't an executable in the user's path
		return clierr.New("Container engine not found; please install Docker or Podman and add to path.")
	} else if err != nil {
		return err
	}
	if composeVersionResult.ExitCode != 0 {
		return clierr.New("Container engine compose not found; please install Docker Compose or Podman Compose and add to path.")
	}

	minimumVersion := sandbox.MinComposeVersion()
	versionStr, err := versions.ExtractTriple(composeVersionResult.Output)
	versionOK := true
	if err == nil {
		versionOK, err = versions.IsMinimum(versionStr, minimumVersion)
	}
	if err != nil {
		localnetLogger.Warning(
			"Unable to extract compose version from output: \n"+
				strings.TrimSuffix(composeVersionResult.Output, "\n")+
				"\nPlease ensure a minimum of compose v"+minimumVersion+" is used",
			log.WithError(err),
		)
	} else if !versionOK {
		return clierr.New("Minimum compose version supported: v%s, installed = v%s\nPlease update your compose install",
			minimumVersion, versionStr)
	}

	if subcommand == "config" {
		return nil
	}
	_, err = proc.Run([]string{engine.Get(), "version"}, proc.Options{
		BadReturnCodeErrorMessage: "Container engine isn't running; please start it.",
	})
	return err
}

func configDirOption(help string) *cli.Option {
	return &cli.Option{
		Key:     "config_dir",
		Opts:    []string{"--config-dir", "-P"},
		Kind:    cli.Value,
		Metavar: "DIRECTORY",
		Help:    help,
		DefaultFunc: func() (any, bool) {
			value := os.Getenv("ALGOKIT_LOCALNET_CONFIG_DIR")
			return value, value != ""
		},
		Convert: func(value string) (string, error) {
			resolved, err := filepath.Abs(value)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return "", fmt.Errorf("Directory '%s' does not exist.", value) //nolint:staticcheck // matches click
			}
			if !info.IsDir() {
				return "", fmt.Errorf("Directory '%s' is a file.", value) //nolint:staticcheck // matches click
			}
			if real, err := filepath.EvalSymlinks(resolved); err == nil {
				resolved = real
			}
			return resolved, nil
		},
	}
}

func checkOption() *cli.Option {
	return &cli.Option{
		Key:  "check",
		Opts: []string{"--check"},
		Kind: cli.Flag,
		Help: "Force check the Docker registry for new LocalNet image versions, ignoring the version check cache.",
	}
}

func boolLabel(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

// sandboxOrDefault returns the running sandbox if there is one, otherwise the default sandbox in configPath.
func sandboxOrDefault(configPath string) (*sandbox.ComposeSandbox, error) {
	sb, err := sandbox.FromEnvironment()
	if err != nil || sb != nil {
		return sb, err
	}
	return sandbox.New(sandbox.BaseName, configPath)
}

func runConfig(engineName string, force bool) error {
	if engineName == "" {
		current := engine.Get()
		choices := []string{"Docker", "Podman"}
		for i, choice := range choices {
			if strings.ToLower(choice) == current {
				choices[i] += " (Active)"
			}
		}
		selected := prompt.Select("Which container engine do you prefer?", choices)
		if selected < 0 {
			return clierr.New("No valid container engine selected. Aborting...")
		}
		engineName = engine.All[selected]
	}

	sb, err := sandbox.FromEnvironment()
	if err != nil {
		return err
	}
	hasActiveInstance := sb != nil && force
	if sb != nil && !force {
		hasActiveInstance, err = prompt.Confirm(
			fmt.Sprintf("Detected active localnet instance, would you like to restart it with '%s'?", engineName), true)
		if err != nil {
			return err
		}
	}
	if hasActiveInstance {
		if err := sb.Down(); err != nil {
			return err
		}
		if err := engine.Save(engineName); err != nil {
			return err
		}
		if err := sb.WriteComposeFile(); err != nil {
			return err
		}
		if err := sb.Up(); err != nil {
			return err
		}
	} else if err := engine.Save(engineName); err != nil {
		return err
	}
	localnetLogger.Info("Container engine set to `" + engineName + "`")
	return nil
}

func runStart(name string, hasName bool, configPath string, devMode, force, check bool) error {
	sb, err := sandbox.FromEnvironment()
	if err != nil {
		return err
	}
	fullName := sandbox.BaseName
	if hasName {
		fullName = sandbox.BaseName + "_" + name
	}
	if sb != nil && fullName != sb.Name {
		localnetLogger.Debug("LocalNet is already running.")
		stop, err := prompt.Confirm("This will stop any running AlgoKit LocalNet instance. Are you sure?", true)
		if err != nil {
			return err
		}
		if !stop {
			return clierr.New("LocalNet is already running. Please stop it first")
		}
		if err := sb.Stop(); err != nil {
			return err
		}
	}
	if !hasName {
		name = sandbox.BaseName
	}
	sb, err = sandbox.New(name, configPath)
	if err != nil {
		return err
	}
	status, err := sb.ComposeFileStatus()
	if err != nil {
		return err
	}
	sb.CheckForNewImageVersions(check)
	switch {
	case status == sandbox.Missing:
		localnetLogger.Debug("LocalNet compose file does not exist yet; writing it out for the first time")
		if err := sb.WriteComposeFile(); err != nil {
			return err
		}
		if hasName {
			localnetLogger.Info(fmt.Sprintf("The named LocalNet configuration has been created in %s. \n"+
				"You can edit the configuration by changing those files. "+
				"Running `algokit localnet reset` will ensure the configuration is applied", sb.Directory))
		}
	case status == sandbox.UpToDate:
		localnetLogger.Debug("LocalNet compose file does not require updating")
	case status == sandbox.OutOfDate && !hasName:
		localnetLogger.Warning("LocalNet definition is out of date; please run `algokit localnet reset`")
	}
	if hasName {
		localnetLogger.Info("A named LocalNet is running, update checks are disabled. If you wish to synchronize " +
			"with the latest version, run `algokit localnet reset --update`")
	}

	currentDevMode, err := sb.IsAlgodDevMode()
	if err != nil {
		return err
	}
	if currentDevMode == devMode {
		return sb.Up()
	}
	if err := sb.SetAlgodDevMode(devMode); err != nil {
		return err
	}
	localnetLogger.Info(fmt.Sprintf("Refreshed 'DevMode' flag to '%s'", boolLabel(devMode)))
	if force {
		return nil
	}
	restart, err := prompt.Confirm(fmt.Sprintf("Would you like to restart 'LocalNet' to apply 'DevMode' flag set to '%s'? "+
		"Otherwise, the next `algokit localnet reset` will restart with the new flag", boolLabel(devMode)), true)
	if err != nil || !restart {
		return err
	}
	if err := sb.Down(); err != nil {
		return err
	}
	return sb.Up()
}

func runStop() error {
	sb, err := sandbox.FromEnvironment()
	if err != nil {
		return err
	}
	if sb == nil {
		localnetLogger.Debug("LocalNet is not running; run `algokit localnet start` to start the AlgoKit LocalNet")
		return nil
	}
	status, err := sb.ComposeFileStatus()
	if err != nil {
		return err
	}
	if status != sandbox.Missing {
		return sb.Stop()
	}
	return nil
}

func runReset(update bool, configPath string, check bool) error {
	sb, err := sandboxOrDefault(configPath)
	if err != nil {
		return err
	}
	status, err := sb.ComposeFileStatus()
	if err != nil {
		return err
	}
	switch {
	case status == sandbox.Missing:
		localnetLogger.Debug("Existing LocalNet not found; creating from scratch...")
		if err := sb.WriteComposeFile(); err != nil {
			return err
		}
	case sb.Name == sandbox.BaseName:
		if err := sb.Down(); err != nil {
			return err
		}
		if status != sandbox.UpToDate {
			localnetLogger.Info("Syncing LocalNet configuration")
			if err := sb.WriteComposeFile(); err != nil {
				return err
			}
		}
		if update {
			if err := sb.Pull(); err != nil {
				return err
			}
		} else {
			sb.CheckForNewImageVersions(check)
		}
	case update:
		confirmed, err := prompt.Confirm(fmt.Sprintf("A named LocalNet is running, are you sure you want to reset "+
			"the LocalNet configuration in %s?\nThis will stop the running LocalNet and overwrite any changes "+
			"you've made to the configuration", sb.Directory), true)
		if err != nil {
			return err
		}
		if !confirmed {
			return clierr.New("LocalNet configuration has not been reset")
		}
		if err := sb.Down(); err != nil {
			return err
		}
		if err := sb.WriteComposeFile(); err != nil {
			return err
		}
		if err := sb.Pull(); err != nil {
			return err
		}
	default:
		if err := sb.Down(); err != nil {
			return err
		}
	}
	return sb.Up()
}

func runStatus(check bool) error {
	sb, err := sandboxOrDefault("")
	if err != nil {
		return err
	}
	sb.CheckForNewImageVersions(check)

	localnetLogger.Info("# container engine")
	localnetLogger.Info("Name: " + log.Bold(engine.Get()) + " (change with `algokit localnet config`)")

	ps, err := sb.PS("")
	if err != nil {
		return err
	}
	psByName := map[string]sandbox.ServiceInfo{}
	for _, stats := range ps {
		psByName[stats.Service()] = stats
	}
	// if any of the required containers does not exist (ie it's not just stopped but hasn't even been created),
	// then they will be missing from the output
	if len(psByName) != len(ServiceNames) || slices.ContainsFunc(ServiceNames, func(name string) bool {
		_, ok := psByName[name]
		return !ok
	}) {
		return clierr.New("LocalNet has not been initialized yet, please run 'algokit localnet start'")
	}

	outputByName := map[string][]sandbox.StatusEntry{}
	for _, name := range ServiceNames {
		status := "Not running"
		if psByName[name].State() == "running" {
			status = "Running"
		}
		outputByName[name] = []sandbox.StatusEntry{{Key: "Status", Value: status}}
	}
	// fill out the remaining algod and indexer values
	for _, service := range []struct {
		name  string
		fetch func(sandbox.ServiceInfo) []sandbox.StatusEntry
	}{{"algod", sandbox.FetchAlgodStatus}, {"indexer", sandbox.FetchIndexerStatus}} {
		if outputByName[service.name][0].Value == "Running" {
			outputByName[service.name] = updateEntries(outputByName[service.name], service.fetch(psByName[service.name]))
		}
	}

	allRunning := true
	for _, name := range ServiceNames {
		localnetLogger.Info(log.Bold("# " + name + " status"))
		for _, entry := range outputByName[name] {
			localnetLogger.Info(log.Bold(entry.Key+":") + " " + entry.Value)
		}
		allRunning = allRunning && outputByName[name][0].Value == "Running"
	}

	// return non-zero if any container is not running
	if !allRunning {
		return clierr.New("At least one container isn't running; execute `algokit localnet start` to start the LocalNet")
	}
	return nil
}

// updateEntries merges updates into entries like Python's dict.update: existing keys keep their position.
func updateEntries(entries, updates []sandbox.StatusEntry) []sandbox.StatusEntry {
	for _, update := range updates {
		i := slices.IndexFunc(entries, func(entry sandbox.StatusEntry) bool { return entry.Key == update.Key })
		if i >= 0 {
			entries[i].Value = update.Value
		} else {
			entries = append(entries, update)
		}
	}
	return entries
}

func runLogs(follow bool, tail string) error {
	sb, err := sandbox.New(sandbox.BaseName, "")
	if err != nil {
		return err
	}
	color := log.ColorPreference()
	return sb.Logs(follow, color != nil && !*color, tail)
}

func localnetCommand() *cli.Command {
	return &cli.Command{
		Name:      "localnet",
		ShortHelp: "Manage the AlgoKit LocalNet.",
		Before:    checkContainerEngine,
		Commands: []*cli.Command{
			{
				Name:      "config",
				ShortHelp: "Configure the container engine for AlgoKit LocalNet.",
				Help:      "Set the default container engine for use by AlgoKit CLI to run LocalNet images.",
				Options: []*cli.Option{{
					Key:  "force",
					Opts: []string{"--force", "-f"},
					Kind: cli.Flag,
					Help: "Skip confirmation prompts. Defaults to 'yes' to all prompts.",
				}},
				Arguments: []*cli.Argument{{Key: "engine", Metavar: "[docker|podman]", Choices: engine.All}},
				Run: func(ctx *cli.Context) error {
					engineName, _ := ctx.String("engine")
					return runConfig(engineName, ctx.Bool("force"))
				},
			},
			{
				Name:      "start",
				ShortHelp: "Start the AlgoKit LocalNet.",
				Options: []*cli.Option{
					{
						Key:  "name",
						Opts: []string{"--name", "-n"},
						Kind: cli.Value,
						Help: "Specify a name for a custom LocalNet instance. " +
							"AlgoKit will not manage the configuration of named LocalNet instances, " +
							"allowing developers to configure it in any way they need. Defaults to 'sandbox'.",
					},
					configDirOption("Specify the custom localnet configuration directory. Defaults to '~/.config' " +
						`on UNIX and 'C:\\Users\\USERNAME\\AppData\\Roaming' on Windows.`),
					{
						Key:           "dev",
						Opts:          []string{"--dev", "-d"},
						SecondaryOpts: []string{"--no-dev"},
						Kind:          cli.BoolPair,
						Default:       true,
						Help:          "Control whether to launch 'algod' in developer mode or not. Defaults to 'yes'.",
					},
					{
						Key:  "force",
						Opts: []string{"--force"},
						Kind: cli.Flag,
						Help: "Ignore the prompt to stop the LocalNet if it's already running.",
					},
					checkOption(),
				},
				Run: func(ctx *cli.Context) error {
					name, hasName := ctx.String("name")
					configPath, _ := ctx.String("config_dir")
					return runStart(name, hasName, configPath, ctx.Bool("dev"), ctx.Bool("force"), ctx.Bool("check"))
				},
			},
			{
				Name:      "stop",
				ShortHelp: "Stop the AlgoKit LocalNet.",
				Run:       func(*cli.Context) error { return runStop() },
			},
			{
				Name:      "reset",
				ShortHelp: "Reset the AlgoKit LocalNet.",
				Options: []*cli.Option{
					{
						Key:           "update",
						Opts:          []string{"--update"},
						SecondaryOpts: []string{"--no-update"},
						Kind:          cli.BoolPair,
						Help:          "Enable or disable updating to the latest available LocalNet version, default: don't update",
					},
					configDirOption("Specify the custom localnet configuration directory."),
					checkOption(),
				},
				Run: func(ctx *cli.Context) error {
					configPath, _ := ctx.String("config_dir")
					return runReset(ctx.Bool("update"), configPath, ctx.Bool("check"))
				},
			},
			{
				Name:      "status",
				ShortHelp: "Check the status of the AlgoKit LocalNet.",
				Options:   []*cli.Option{checkOption()},
				Run:       func(ctx *cli.Context) error { return runStatus(ctx.Bool("check")) },
			},
			{
				Name: "console",
				ShortHelp: "Run the Algorand goal CLI against the AlgoKit LocalNet via a Bash console so you can " +
					"execute multiple goal commands and/or interact with a filesystem.",
				Run: func(*cli.Context) error { return runGoal(true, false, nil) },
			},
			{
				Name:      "logs",
				ShortHelp: "See the output of the Docker containers.",
				Options: []*cli.Option{
					{
						Key:  "follow",
						Opts: []string{"--follow", "-f"},
						Kind: cli.Flag,
						Help: "Follow log output.",
					},
					{
						Key:         "tail",
						Opts:        []string{"--tail"},
						Kind:        cli.Value,
						Default:     "all",
						ShowDefault: true,
						Help:        "Number of lines to show from the end of the logs for each container.",
					},
				},
				Run: func(ctx *cli.Context) error {
					tail, _ := ctx.String("tail")
					return runLogs(ctx.Bool("follow"), tail)
				},
			},
		},
	}
}
