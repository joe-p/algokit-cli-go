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
	"unicode"

	"github.com/joe-p/algokit-cli-go/internal/engine"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/proc"
)

const (
	DefaultAlgodRepo   = "https://github.com/algorand/go-algorand.git"
	DefaultAlgodRef    = "master"
	DefaultIndexerRepo = "https://github.com/algorand/indexer.git"
	DefaultIndexerRef  = "main"
	// LocalNet needs the localnet_algod importer, absent from plain Conduit.
	DefaultConduitRepo = "https://github.com/algorandfoundation/algokit-conduit-localnet-importer.git"
	DefaultConduitRef  = "main"
)

//go:embed templates/Dockerfile.algod
var algodDockerfile string

//go:embed templates/Dockerfile.service
var serviceDockerfile string

// ServiceSource selects either a remote source build or an existing container
// image. A nil selection retains the normal published-image workflow.
type ServiceSource struct {
	Repo  string `json:"repo,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Image string `json:"image,omitempty"`
}

func validateServiceImage(image string) error {
	if image == "" || strings.ContainsRune(image, '$') || strings.IndexFunc(image, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("image must be a nonblank reference without whitespace or '$'")
	}
	return nil
}

func (source ServiceSource) validate() error {
	if source.Image != "" {
		if source.Repo != "" || source.Ref != "" {
			return fmt.Errorf("image cannot be combined with repo or ref")
		}
		return validateServiceImage(source.Image)
	}
	if strings.TrimSpace(source.Repo) == "" || strings.TrimSpace(source.Ref) == "" {
		return fmt.Errorf("source configuration requires repo and ref, or image")
	}
	return nil
}

// AlgodSource is retained as an alias for existing source configuration users.
type AlgodSource = ServiceSource

type sourceSpec struct {
	name, repo, ref, image, dockerfile string
	source                             **ServiceSource
}

func (s *ComposeSandbox) sourceSpecs() []sourceSpec {
	indexerDockerfile := strings.NewReplacer(
		"{{BINARY}}", "algorand-indexer", "{{DATA_ENV}}", "INDEXER_DATA",
		"{{GENERATE}}", "go generate ./idb/postgres/internal/schema", "{{VALIDATE}}", "",
	).Replace(serviceDockerfile)
	conduitDockerfile := strings.NewReplacer(
		"{{BINARY}}", "conduit", "{{DATA_ENV}}", "CONDUIT_DATA_DIR", "{{GENERATE}}", "true",
		"{{VALIDATE}}", "RUN /out/conduit list | grep -q localnet_algod || { echo 'Conduit source must include the localnet_algod importer' >&2; exit 1; }",
	).Replace(serviceDockerfile)
	return []sourceSpec{
		{"algod", DefaultAlgodRepo, DefaultAlgodRef, AlgorandImage, algodDockerfile, &s.Source},
		{"indexer", DefaultIndexerRepo, DefaultIndexerRef, IndexerImage, indexerDockerfile, &s.IndexerSource},
		{"conduit", DefaultConduitRepo, DefaultConduitRef, ConduitImage, conduitDockerfile, &s.ConduitSource},
	}
}

func (s *ComposeSandbox) sourcePath(service string) string {
	return filepath.Join(s.Directory, service+"-source.json")
}

func (s *ComposeSandbox) loadSources() error {
	for _, spec := range s.sourceSpecs() {
		data, err := os.ReadFile(s.sourcePath(spec.name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var source ServiceSource
		if err := json.Unmarshal(data, &source); err != nil {
			return fmt.Errorf("invalid %s source configuration: %w", spec.name, err)
		}
		if err := source.validate(); err != nil {
			return fmt.Errorf("invalid %s source configuration: %w", spec.name, err)
		}
		*spec.source = &source
	}
	return nil
}

func (source *ServiceSource) image(service string) string {
	hash := sha256.Sum256([]byte(source.Repo + "\x00" + source.Ref))
	return fmt.Sprintf("algokit-%s:%x", service, hash[:12])
}

func (spec sourceSpec) imageDefinition() string {
	if *spec.source == nil {
		return "    image: " + spec.image
	}
	if (*spec.source).Image != "" {
		// JSON strings are valid YAML strings and prevent YAML interpretation
		// of tags, digests, and other punctuation in an image reference.
		image, _ := json.Marshal((*spec.source).Image)
		return "    image: " + string(image)
	}
	return "    image: " + (*spec.source).image(spec.name) + "\n    pull_policy: never"
}

func (spec sourceSpec) buildsImage() bool {
	return *spec.source != nil && (*spec.source).Image == ""
}

func (s *ComposeSandbox) hasSources() bool {
	return s.Source != nil || s.IndexerSource != nil || s.ConduitSource != nil
}

// ConfigureAlgodSource builds before updating existing configuration. Omitted
// fields retain saved values, or default to upstream/master on first use.
func (s *ComposeSandbox) ConfigureAlgodSource(repo, ref string) error {
	return s.ConfigureServiceSource("algod", repo, ref)
}

// ConfigureServiceSource builds a service and saves its effective source.
func (s *ComposeSandbox) ConfigureServiceSource(service, repo, ref string) error {
	return s.ConfigureService(service, ServiceSource{Repo: repo, Ref: ref})
}

// ConfigureService selects a prebuilt image or source build. Other services and
// user edits to the Compose configuration are left untouched.
func (s *ComposeSandbox) ConfigureService(service string, selection ServiceSource) error {
	for _, spec := range s.sourceSpecs() {
		if spec.name == service {
			return s.configureSource(spec, selection)
		}
	}
	return fmt.Errorf("unknown source service %q", service)
}

func (s *ComposeSandbox) configureSource(spec sourceSpec, selection ServiceSource) error {
	source := ServiceSource{Repo: spec.repo, Ref: spec.ref}
	if selection.Image != "" {
		// Image selection replaces saved source settings, not merges with them.
		source = selection
	} else {
		// Switching back from an image starts with source defaults. Partial
		// updates to an existing source build retain its other saved fields.
		if *spec.source != nil && (*spec.source).Image == "" {
			source = **spec.source
		}
		if selection.Repo != "" {
			source.Repo = selection.Repo
		}
		if selection.Ref != "" {
			source.Ref = selection.Ref
		}
	}
	if err := source.validate(); err != nil {
		return fmt.Errorf("invalid %s selection: %w", spec.name, err)
	}
	compose, err := os.ReadFile(s.ComposeFilePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldDefinition := spec.imageDefinition()
	if err == nil && !strings.Contains(string(compose), oldDefinition+"\n") {
		return fmt.Errorf("cannot configure %s source: the %s image definition has been customized in %s", spec.name, spec.name, s.ComposeFilePath())
	}
	if source.Image == "" {
		if err := buildServiceSource(spec, &source); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.sourcePath(spec.name), append(data, '\n'), 0o600); err != nil {
		return err
	}
	*spec.source = &source
	if compose != nil {
		updated := strings.Replace(string(compose), oldDefinition+"\n", spec.imageDefinition()+"\n", 1)
		return os.WriteFile(s.ComposeFilePath(), []byte(updated), 0o644)
	}
	return nil
}

// RebuildSources refreshes all saved source refs. A nonce invalidates the clone
// layer so moving refs are fetched again, while base dependencies stay cached.
func (s *ComposeSandbox) RebuildSources() error {
	for _, spec := range s.sourceSpecs() {
		if spec.buildsImage() {
			if err := buildServiceSource(spec, *spec.source); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildServiceSource(spec sourceSpec, source *ServiceSource) error {
	dir, err := os.MkdirTemp("", "algokit-"+spec.name+"-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(spec.dockerfile), 0o600); err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Building %s from %s at %s (this may take several minutes)...", spec.name, source.Repo, source.Ref))
	prefix := "SOURCE"
	if spec.name == "algod" {
		prefix = "ALGOD"
	}
	_, err = proc.Run([]string{engine.Get(), "build", "--tag", source.image(spec.name),
		"--build-arg", prefix + "_REPO=" + source.Repo,
		"--build-arg", prefix + "_REF=" + source.Ref,
		"--build-arg", fmt.Sprintf("BUILD_NONCE=%d", time.Now().UnixNano()), dir}, proc.Options{
		StdoutLogLevel: log.Info, BadReturnCodeErrorMessage: "Failed to build " + spec.name + " from source",
	})
	return err
}
