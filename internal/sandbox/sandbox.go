// Package sandbox manages the AlgoKit LocalNet docker compose project, equivalent to algokit.core.sandbox.
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/joe-p/algokit-cli-go/internal/conf"
	"github.com/joe-p/algokit-cli-go/internal/engine"
	"github.com/joe-p/algokit-cli-go/internal/httpx"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/proc"
)

var logger = log.New("algokit.core.sandbox")

const (
	DockerComposeMinimumVersion = "2.5.0"
	PodmanComposeMinimumVersion = "1.0.6"

	BaseName = "sandbox"

	DefaultAlgodServer   = "http://localhost"
	DefaultIndexerServer = "http://localhost"
	DefaultIndexerPort   = 8980

	ImageVersionCheckInterval = 7 * 24 * time.Hour
)

var (
	DefaultAlgodToken   = strings.Repeat("a", 64)
	DefaultIndexerToken = strings.Repeat("a", 64)
	AlgodHealthURL      = fmt.Sprintf("%s:%d/v2/status", DefaultAlgodServer, DefaultAlgodPort)
	IndexerHealthURL    = fmt.Sprintf("%s:%d/health", DefaultIndexerServer, DefaultIndexerPort)
)

// HealthCheckTimings controls how long to wait for LocalNet to become healthy; variable so tests can shorten them.
var HealthCheckTimings = struct {
	AlgodWait     time.Duration
	IndexerWait   time.Duration
	HealthTimeout time.Duration
}{
	AlgodWait:     60 * time.Second,
	IndexerWait:   60 * time.Second,
	HealthTimeout: 1 * time.Second,
}

type ComposeFileStatus int

const (
	Missing ComposeFileStatus = iota
	UpToDate
	OutOfDate
)

// MinComposeVersion returns the minimum supported compose version for the active container engine.
func MinComposeVersion() string {
	if engine.Get() == engine.Docker {
		return DockerComposeMinimumVersion
	}
	return PodmanComposeMinimumVersion
}

// ComposeVersionCommand returns the command used to check the compose version.
func ComposeVersionCommand() []string {
	return []string{engine.Get(), "compose", "version", "--format", "json"}
}

var ansiColorPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// splitLines splits text into lines like Python's str.splitlines.
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// extractJSONLines returns the lines of output which are valid JSON (after stripping ANSI colour codes).
func extractJSONLines(output string) []string {
	var valid []string
	for _, line := range splitLines(output) {
		parsed := ansiColorPattern.ReplaceAllString(line, "")
		if json.Valid([]byte(parsed)) {
			valid = append(valid, parsed)
		}
	}
	return valid
}

// ServiceInfo is an entry in the output of `docker compose ps --format json`.
type ServiceInfo map[string]any

func (s ServiceInfo) str(key string) string {
	value, _ := s[key].(string)
	return value
}

func (s ServiceInfo) Service() string { return s.str("Service") }
func (s ServiceInfo) State() string   { return s.str("State") }

// ComposeSandbox is a LocalNet docker compose project in a directory.
type ComposeSandbox struct {
	Name      string
	Directory string
}

// New returns the sandbox with the given name (BaseName for the default) in configPath (default: the app config
// directory), creating its directory if needed.
func New(name, configPath string) (*ComposeSandbox, error) {
	fullName := BaseName
	if name != BaseName {
		fullName = BaseName + "_" + name
	}
	if configPath == "" {
		configPath = conf.AppConfigDir()
	}
	s := &ComposeSandbox{Name: fullName, Directory: filepath.Join(configPath, fullName)}
	if _, err := os.Stat(s.Directory); errors.Is(err, fs.ErrNotExist) {
		logger.Debug(fmt.Sprintf("The %s directory does not exist yet; creating it", s.Name))
		if err := os.Mkdir(s.Directory, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *ComposeSandbox) ComposeFilePath() string {
	return filepath.Join(s.Directory, "docker-compose.yml")
}
func (s *ComposeSandbox) ConduitFilePath() string { return filepath.Join(s.Directory, "conduit.yml") }
func (s *ComposeSandbox) AlgodConfigFilePath() string {
	return filepath.Join(s.Directory, "algod_config.json")
}
func (s *ComposeSandbox) AlgodNetworkTemplateFilePath() string {
	return filepath.Join(s.Directory, "algod_network_template.json")
}

func (s *ComposeSandbox) latestYAML() string { return DockerComposeYAML("algokit_" + s.Name) }

// FromEnvironment finds the currently running AlgoKit LocalNet (if any).
func FromEnvironment() (*ComposeSandbox, error) {
	result, err := proc.Run(
		[]string{engine.Get(), "compose", "ls", "--format", "json", "--filter", "name=algokit_sandbox*"},
		proc.Options{BadReturnCodeErrorMessage: "Failed to list running LocalNet"},
	)
	if err != nil {
		logger.Debug("Error checking for existing sandbox: "+err.Error(), log.WithError(err))
		return nil, nil
	}

	jsonLines := extractJSONLines(result.Output)
	if len(jsonLines) == 0 {
		return nil, nil
	}
	var data []map[string]any
	if err := json.Unmarshal([]byte(jsonLines[0]), &data); err != nil {
		logger.Info("Error checking config file: "+err.Error(), log.WithError(err))
		return nil, nil
	}
	if len(data) == 0 {
		return nil, nil
	}
	configFiles, _ := data[0]["ConfigFiles"].(string)
	configFile, _, _ := strings.Cut(configFiles, ",")
	configDir := filepath.Dir(configFile)
	fullName := filepath.Base(configDir)
	name := fullName
	if strings.HasPrefix(fullName, BaseName+"_") {
		name = strings.ReplaceAll(fullName, BaseName+"_", "")
	}
	return New(name, filepath.Dir(configDir))
}

var devModePattern = regexp.MustCompile(`"DevMode":\s*(true|false)`)

// SetAlgodDevMode sets the DevMode flag in the algod network template.
func (s *ComposeSandbox) SetAlgodDevMode(devMode bool) error {
	content, err := os.ReadFile(s.AlgodNetworkTemplateFilePath())
	if err != nil {
		return err
	}
	updated := devModePattern.ReplaceAllLiteralString(string(content), fmt.Sprintf(`"DevMode": %t`, devMode))
	return os.WriteFile(s.AlgodNetworkTemplateFilePath(), []byte(updated), 0o644)
}

// IsAlgodDevMode reports whether DevMode is enabled in the algod network template.
func (s *ComposeSandbox) IsAlgodDevMode() (bool, error) {
	content, err := os.ReadFile(s.AlgodNetworkTemplateFilePath())
	if err != nil {
		return false, err
	}
	match := devModePattern.FindStringSubmatch(string(content))
	return match != nil && match[1] == "true", nil
}

func parseNetworkTemplate(content string) (map[string]any, error) {
	// Ensure the NUM_ROUNDS placeholder is the same in both files
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.ReplaceAll(content, "NUM_ROUNDS", `"NUM_ROUNDS"`)), &parsed); err != nil {
		return nil, err
	}
	genesis, ok := parsed["Genesis"].(map[string]any)
	if !ok {
		return nil, errors.New("missing Genesis")
	}
	if _, ok := genesis["DevMode"]; !ok {
		return nil, errors.New("missing Genesis.DevMode")
	}
	// Remove DevMode from comparison as the value is configurable via the `--dev` option
	delete(genesis, "DevMode")
	return parsed, nil
}

// ComposeFileStatus reports whether the LocalNet definition on disk is missing, or matches the latest definition.
func (s *ComposeSandbox) ComposeFileStatus() (ComposeFileStatus, error) {
	var contents [3]string
	for i, path := range []string{s.ComposeFilePath(), s.AlgodConfigFilePath(), s.AlgodNetworkTemplateFilePath()} {
		content, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			// treat as out of date if compose file exists but algod config doesn't
			// so that existing setups aren't suddenly reset
			if _, err := os.Stat(s.ComposeFilePath()); err == nil {
				return OutOfDate, nil
			}
			return Missing, nil
		} else if err != nil {
			return Missing, err
		}
		contents[i] = string(content)
	}

	current, err := parseNetworkTemplate(contents[2])
	if err != nil {
		// If config files are corrupted or malformed, treat as out of date
		return OutOfDate, nil
	}
	latest, err := parseNetworkTemplate(AlgodNetworkTemplate())
	if err != nil {
		return OutOfDate, err
	}
	if contents[0] == s.latestYAML() && contents[1] == ConfigJSON() && reflect.DeepEqual(current, latest) {
		return UpToDate, nil
	}
	return OutOfDate, nil
}

// WriteComposeFile writes out the latest LocalNet definition.
func (s *ComposeSandbox) WriteComposeFile() error {
	files := []struct{ path, content string }{
		{s.ConduitFilePath(), ConduitYAML()},
		{s.ComposeFilePath(), s.latestYAML()},
		{s.AlgodConfigFilePath(), ConfigJSON()},
		{s.AlgodNetworkTemplateFilePath(), AlgodNetworkTemplate()},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *ComposeSandbox) runComposeCommand(args string, level log.Level, badReturnCodeErrorMessage string) (proc.Result, error) {
	return proc.Run(append([]string{engine.Get(), "compose"}, strings.Fields(args)...), proc.Options{
		Dir:                       s.Directory,
		StdoutLogLevel:            level,
		BadReturnCodeErrorMessage: badReturnCodeErrorMessage,
	})
}

// Up starts LocalNet and waits for it to become healthy.
func (s *ComposeSandbox) Up() error {
	logger.Info("Starting AlgoKit LocalNet now...")
	// Create the goal_mount bind mount source ourselves, otherwise Docker (on Linux) creates it owned by root and
	// `algokit goal` can't copy files into it. (The Python CLI doesn't do this.)
	if err := os.MkdirAll(filepath.Join(s.Directory, "goal_mount"), 0o755); err != nil {
		logger.Debug("Failed to create goal_mount directory: "+err.Error(), log.WithError(err))
	}
	wait := ""
	if engine.Get() == engine.Docker {
		wait = " --wait"
	}
	if _, err := s.runComposeCommand("up --detach --quiet-pull"+wait, log.Info, "Failed to start LocalNet"); err != nil {
		return err
	}
	logger.Debug("AlgoKit LocalNet started, waiting for health check")
	if waitForAlgod() && waitForIndexer() {
		logger.Info("Started; execute `algokit goal` or `algokit localnet console` to interact with LocalNet.")
	} else {
		logger.Warning("AlgoKit LocalNet failed to return a successful health check")
	}
	return nil
}

// Stop stops LocalNet's containers.
func (s *ComposeSandbox) Stop() error {
	logger.Info("Stopping AlgoKit LocalNet now...")
	if _, err := s.runComposeCommand("stop", log.Info, "Failed to stop LocalNet"); err != nil {
		return err
	}
	logger.Info("LocalNet Stopped; execute `algokit localnet start` to start it again.")
	return nil
}

// Down removes LocalNet's containers.
func (s *ComposeSandbox) Down() error {
	logger.Info("Cleaning up the running AlgoKit LocalNet...")
	_, err := s.runComposeCommand("down", log.Debug, "")
	return err
}

// Pull fetches the latest LocalNet images.
func (s *ComposeSandbox) Pull() error {
	logger.Info("Fetching any container updates from DockerHub...")
	if _, err := s.runComposeCommand("pull --ignore-pull-failures --quiet", log.Info, ""); err != nil {
		return err
	}
	logger.Debug("Image version cache reset")
	updateImageVersionCache(imageVersionCache{})
	return nil
}

// Logs shows the output of LocalNet's containers.
func (s *ComposeSandbox) Logs(follow, noColor bool, tail string) error {
	args := []string{engine.Get(), "compose", "logs"}
	if follow {
		args = append(args, "--follow")
	}
	if noColor {
		args = append(args, "--no-color")
	}
	if tail != "" {
		args = append(args, "--tail", tail)
	}
	_, err := proc.RunInteractive(args, proc.InteractiveOptions{
		Dir:                       s.Directory,
		BadReturnCodeErrorMessage: "Failed to get logs, are the containers running?",
	})
	return err
}

// PS returns the status of LocalNet's containers (optionally just the named service).
func (s *ComposeSandbox) PS(serviceName string) ([]ServiceInfo, error) {
	result, err := s.runComposeCommand("ps "+serviceName+" --format json", log.Debug, "")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, nil
	}
	var data []ServiceInfo
	if strings.HasPrefix(result.Output, "[") {
		// `docker compose ps --format json` on version < 2.21.0 outputs a JSON array
		if err := json.Unmarshal([]byte(result.Output), &data); err != nil {
			return nil, err
		}
		return data, nil
	}
	// `docker compose ps --format json` on version >= 2.21.0 outputs separate JSON objects, each on a new line
	for _, line := range extractJSONLines(result.Output) {
		var item ServiceInfo
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		data = append(data, item)
	}
	return data, nil
}

// localImageVersions returns the local versions of an image. Note that a single image may be pulled from multiple
// repo digests.
func localImageVersions(imageName string) []string {
	arg := "{{range .RepoDigests}}{{println .}}{{end}}"
	result, err := proc.Run([]string{engine.Get(), "image", "inspect", imageName, "--format", arg}, proc.Options{})
	if err != nil {
		logger.Debug("Failed to get local image versions: "+err.Error(), log.WithError(err))
		return nil
	}
	var versions []string
	for _, line := range splitLines(result.Output) {
		if parts := strings.Split(line, "@"); len(parts) > 1 {
			line = parts[1]
		}
		versions = append(versions, line)
	}
	return versions
}

// latestImageVersion returns the latest version of an image from Docker Hub.
func latestImageVersion(imageName string) (string, bool) {
	parts := strings.Split(imageName, ":")
	name, tag := parts[0], "latest"
	if len(parts) > 1 {
		tag = parts[1]
	}
	url := fmt.Sprintf("https://registry.hub.docker.com/v2/repositories/%s/tags/%s", name, tag)
	response, err := httpx.Get(url, nil, 0)
	if err == nil {
		var data map[string]any
		if err = response.JSON(&data); err == nil {
			if digest, ok := data["digest"]; ok {
				return fmt.Sprint(digest), true
			}
			err = errors.New("'digest'")
		}
	}
	logger.Debug("Error checking image status: "+err.Error(), log.WithError(err))
	return "", false
}

// IsImageUpToDate reports whether the local image matches the latest version on Docker Hub (true if that can't be
// determined).
func IsImageUpToDate(imageName string) bool {
	local := localImageVersions(imageName)
	latest, ok := latestImageVersion(imageName)
	return !ok || slices.Contains(local, latest)
}

// CheckForNewImageVersions warns if newer LocalNet images are available, checking Docker Hub at most once a week
// unless force is set.
func (s *ComposeSandbox) CheckForNewImageVersions(force bool) {
	var state imageVersionCache
	if force || shouldCheckImageVersions() {
		// Check Docker registry for new versions
		state.IndexerOutdated = !IsImageUpToDate(IndexerImage)
		state.AlgodOutdated = !IsImageUpToDate(AlgorandImage)
		updateImageVersionCache(state)
	} else {
		// Use cached state
		cached, ok := getImageVersionCache()
		if !ok {
			return
		}
		state = cached
	}
	if state.IndexerOutdated {
		logger.Warning("indexer has a new version available, run `algokit localnet reset --update` to get the latest version")
	}
	if state.AlgodOutdated {
		logger.Warning("algod has a new version available, run `algokit localnet reset --update` to get the latest version")
	}
}

type imageVersionCache struct {
	IndexerOutdated bool
	AlgodOutdated   bool
}

func imageVersionCachePath() string {
	return filepath.Join(conf.AppStateDir(), "last-localnet-version-check")
}

func getImageVersionCache() (imageVersionCache, bool) {
	content, err := os.ReadFile(imageVersionCachePath())
	if err != nil {
		return imageVersionCache{}, false
	}
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return imageVersionCache{}, false
	}
	return imageVersionCache{IndexerOutdated: truthy(data["indexer_outdated"]), AlgodOutdated: truthy(data["algod_outdated"])}, true
}

// truthy mirrors Python's truthiness for decoded JSON values.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func shouldCheckImageVersions() bool {
	cachePath := imageVersionCachePath()
	info, err := os.Stat(cachePath)
	if err != nil {
		logger.Debug(cachePath + " inaccessible, will check for image updates")
		return true
	}
	elapsed := time.Since(info.ModTime())
	if elapsed > ImageVersionCheckInterval {
		logger.Debug("Image version cache expired, will check for updates")
		return true
	}
	logger.Debug(fmt.Sprintf("Skipping image version check, last checked %.1fh ago", elapsed.Hours()))
	return false
}

func updateImageVersionCache(state imageVersionCache) {
	// formatted like Python's json.dumps, since the file is shared with the Python CLI
	content := fmt.Sprintf(`{"indexer_outdated": %t, "algod_outdated": %t}`, state.IndexerOutdated, state.AlgodOutdated)
	if err := os.WriteFile(imageVersionCachePath(), []byte(content), 0o644); err != nil {
		logger.Debug("Failed to update image version cache: " + err.Error())
	}
}

// waitForService waits for a service to become ready via its health check endpoint.
func waitForService(url, token, headerName, serviceName string, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(end) {
		health, err := httpx.Get(url, map[string]string{headerName: token}, HealthCheckTimings.HealthTimeout)
		if err != nil {
			lastErr = err
		} else if health.OK() {
			logger.Debug(fmt.Sprintf("AlgoKit LocalNet health check successful, %s is ready", serviceName))
			return true
		} else {
			logger.Debug(fmt.Sprintf("AlgoKit LocalNet health check returned %d, waiting", health.StatusCode))
		}
		time.Sleep(HealthCheckTimings.HealthTimeout)
	}
	if lastErr != nil {
		logger.Debug("AlgoKit LocalNet health request failed for "+serviceName, log.WithError(lastErr))
	}
	return false
}

func waitForAlgod() bool {
	return waitForService(AlgodHealthURL, DefaultAlgodToken, "X-Algo-API-Token", "algod", HealthCheckTimings.AlgodWait)
}

func waitForIndexer() bool {
	return waitForService(IndexerHealthURL, DefaultIndexerToken, "X-Indexer-API-Token", "indexer", HealthCheckTimings.IndexerWait)
}
