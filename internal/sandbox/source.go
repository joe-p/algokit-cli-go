package sandbox

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joe-p/algokit-cli-go/internal/engine"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/proc"
)

const DefaultAlgodRepo = "https://github.com/algorand/go-algorand.git"
const DefaultAlgodRef = "master"

//go:embed templates/Dockerfile.algod
var algodDockerfile string

// AlgodSource records the remote source used by this LocalNet. A nil source
// retains the normal published-image workflow.
type AlgodSource struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref"`
}

func (s *ComposeSandbox) sourcePath() string {
	return filepath.Join(s.Directory, "algod-source.json")
}

func (s *ComposeSandbox) loadSource() error {
	data, err := os.ReadFile(s.sourcePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var source AlgodSource
	if err := json.Unmarshal(data, &source); err != nil {
		return fmt.Errorf("invalid algod source configuration: %w", err)
	}
	if strings.TrimSpace(source.Repo) == "" || strings.TrimSpace(source.Ref) == "" {
		return fmt.Errorf("algod source configuration requires repo and ref")
	}
	s.Source = &source
	return nil
}

func (source *AlgodSource) image() string {
	hash := sha256.Sum256([]byte(source.Repo + "\x00" + source.Ref))
	return fmt.Sprintf("algokit-algod:%x", hash[:12])
}

func (s *ComposeSandbox) algodImageDefinition() string {
	if s.Source == nil {
		return "    image: " + AlgorandImage
	}
	return "    image: " + s.Source.image() + "\n    pull_policy: never"
}

// ConfigureAlgodSource builds before updating any existing configuration. Omitted
// fields retain their saved values, or default to upstream/master on first use.
func (s *ComposeSandbox) ConfigureAlgodSource(repo, ref string) error {
	source := AlgodSource{Repo: DefaultAlgodRepo, Ref: DefaultAlgodRef}
	if s.Source != nil {
		source = *s.Source
	}
	if repo != "" {
		source.Repo = repo
	}
	if ref != "" {
		source.Ref = ref
	}
	if strings.TrimSpace(source.Repo) == "" || strings.TrimSpace(source.Ref) == "" {
		return fmt.Errorf("algod repo and ref must not be blank")
	}
	compose, err := os.ReadFile(s.ComposeFilePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldDefinition := s.algodImageDefinition()
	if err == nil && !strings.Contains(string(compose), oldDefinition+"\n") {
		return fmt.Errorf("cannot configure algod source: the algod image definition has been customized in %s", s.ComposeFilePath())
	}
	if err := s.buildAlgodSource(&source); err != nil {
		return err
	}
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.sourcePath(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.Source = &source
	if compose != nil {
		updated := strings.Replace(string(compose), oldDefinition+"\n", s.algodImageDefinition()+"\n", 1)
		return os.WriteFile(s.ComposeFilePath(), []byte(updated), 0o644)
	}
	return nil
}

// RebuildAlgod refreshes moving refs as well as immutable commits. A nonce
// invalidates the clone layer, while allowing base dependencies to stay cached.
func (s *ComposeSandbox) RebuildAlgod() error {
	if s.Source == nil {
		return nil
	}
	return s.buildAlgodSource(s.Source)
}

func (s *ComposeSandbox) buildAlgodSource(source *AlgodSource) error {
	dir, err := os.MkdirTemp("", "algokit-algod-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(algodDockerfile), 0o600); err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Building algod from %s at %s (this may take several minutes)...", source.Repo, source.Ref))
	_, err = proc.Run([]string{engine.Get(), "build", "--tag", source.image(),
		"--build-arg", "ALGOD_REPO=" + source.Repo,
		"--build-arg", "ALGOD_REF=" + source.Ref,
		"--build-arg", fmt.Sprintf("BUILD_NONCE=%d", time.Now().UnixNano()), dir}, proc.Options{
		StdoutLogLevel: log.Info, BadReturnCodeErrorMessage: "Failed to build algod from source",
	})
	return err
}
