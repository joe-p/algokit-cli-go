// Package engine stores the active container engine (docker or podman), shared with the Python CLI.
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joe-p/algokit-cli-go/internal/conf"
)

const (
	Docker = "docker"
	Podman = "podman"
)

// All is the list of supported container engines.
var All = []string{Docker, Podman}

func configFile() string {
	return filepath.Join(conf.AppConfigDir(), "active-container-engine")
}

// Get returns the active container engine, defaulting to docker.
func Get() string {
	content, err := os.ReadFile(configFile())
	if err != nil {
		return Docker
	}
	return strings.TrimSpace(string(content))
}

// Save sets the active container engine.
func Save(engine string) error {
	if !slices.Contains(All, engine) {
		return fmt.Errorf("invalid container engine: %s", engine)
	}
	return os.WriteFile(configFile(), []byte(engine), 0o644)
}
