// Package conf locates the AlgoKit config and state directories, which are shared with the Python CLI.
package conf

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const PackageName = "algokit"

// ExpandUser expands a leading `~` to the user's home directory (equivalent to Path.expanduser).
func ExpandUser(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !(runtime.GOOS == "windows" && strings.HasPrefix(p, `~\`)) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

func relativeAppPath(baseDir string) string {
	result := filepath.Join(ExpandUser(baseDir), PackageName)
	_ = os.MkdirAll(result, 0o755)
	// resolve path in case of UWP sandbox redirection
	if resolved, err := filepath.EvalSymlinks(result); err == nil {
		result = resolved
	}
	if abs, err := filepath.Abs(result); err == nil {
		result = abs
	}
	return result
}

// lookupEnv returns the named environment variable, or fallback if it's unset or empty (per the XDG spec).
func lookupEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// AppConfigDir is the application config files location - things that should persist, and potentially follow a user.
func AppConfigDir() string {
	if runtime.GOOS == "windows" {
		return relativeAppPath(lookupEnv("APPDATA", "~/.config"))
	}
	return relativeAppPath(lookupEnv("XDG_CONFIG_HOME", "~/.config"))
}

// AppStateDir is the application state files location - things the user wouldn't normally interact with directly.
func AppStateDir() string {
	switch runtime.GOOS {
	case "windows":
		return relativeAppPath(lookupEnv("LOCALAPPDATA", "~/.local/state"))
	case "darwin":
		return relativeAppPath("~/Library/Application Support")
	default:
		return relativeAppPath(lookupEnv("XDG_STATE_HOME", "~/.local/state"))
	}
}
