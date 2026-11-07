package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// LocalnetConfig contains user-supplied LocalNet settings. Settings are saved
// with the instance, so the input file is not needed on later starts.
type LocalnetConfig struct {
	Name          *string        `json:"name,omitempty"`
	AlgodSource   *AlgodSource   `json:"algodSource,omitempty"`
	IndexerSource *ServiceSource `json:"indexerSource,omitempty"`
	ConduitSource *ServiceSource `json:"conduitSource,omitempty"`
	DevMode       *bool          `json:"devMode,omitempty"`
	VFuture       *bool          `json:"vFuture,omitempty"`
}

// ReadLocalnetConfig validates the entire file before any LocalNet is changed.
// Relative paths are resolved against the CLI's working directory.
func ReadLocalnetConfig(path string) (*LocalnetConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read LocalNet config %q: %w", path, err)
	}
	defer file.Close()

	// Pointers distinguish omitted fields from explicitly blank values. Raw
	// booleans let us reject null as well as strings and numbers.
	type sourceConfig struct {
		Repo  *string         `json:"repo"`
		Ref   *string         `json:"ref"`
		Image json.RawMessage `json:"image"`
	}
	var input struct {
		Name    json.RawMessage `json:"name"`
		Algod   *sourceConfig   `json:"algodSource"`
		Indexer *sourceConfig   `json:"indexerSource"`
		Conduit *sourceConfig   `json:"conduitSource"`
		DevMode json.RawMessage `json:"devMode"`
		VFuture json.RawMessage `json:"vFuture"`
	}
	invalid := func(err error) (*LocalnetConfig, error) {
		return nil, fmt.Errorf("invalid LocalNet config %q: %w", path, err)
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return invalid(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return invalid(fmt.Errorf("expected a single JSON object"))
	}
	config := &LocalnetConfig{}
	if len(input.Name) != 0 {
		var value string
		if string(input.Name) == "null" || json.Unmarshal(input.Name, &value) != nil {
			return invalid(fmt.Errorf("name must be a string"))
		}
		if strings.TrimSpace(value) == "" {
			return invalid(fmt.Errorf("name must not be blank"))
		}
		config.Name = &value
	}
	for _, field := range []struct {
		name   string
		value  json.RawMessage
		target **bool
	}{
		{"devMode", input.DevMode, &config.DevMode},
		{"vFuture", input.VFuture, &config.VFuture},
	} {
		if len(field.value) != 0 {
			var value bool
			if string(field.value) == "null" || json.Unmarshal(field.value, &value) != nil {
				return invalid(fmt.Errorf("%s must be a boolean", field.name))
			}
			*field.target = &value
		}
	}
	for _, service := range []struct {
		name   string
		input  *sourceConfig
		target **ServiceSource
	}{
		{"algodSource", input.Algod, &config.AlgodSource},
		{"indexerSource", input.Indexer, &config.IndexerSource},
		{"conduitSource", input.Conduit, &config.ConduitSource},
	} {
		if service.input == nil {
			continue
		}
		if len(service.input.Image) != 0 {
			if service.input.Repo != nil || service.input.Ref != nil {
				return invalid(fmt.Errorf("%s.image cannot be combined with repo or ref", service.name))
			}
			var image string
			if err := json.Unmarshal(service.input.Image, &image); err != nil {
				return invalid(fmt.Errorf("%s.image must be a string", service.name))
			}
			if err := validateServiceImage(image); err != nil {
				return invalid(fmt.Errorf("%s: %w", service.name, err))
			}
			*service.target = &ServiceSource{Image: image}
			continue
		}
		if service.input.Repo == nil && service.input.Ref == nil {
			return invalid(fmt.Errorf("%s must contain repo or ref, or image", service.name))
		}
		source := &ServiceSource{}
		for _, field := range []struct {
			name   string
			value  *string
			target *string
		}{
			{"repo", service.input.Repo, &source.Repo},
			{"ref", service.input.Ref, &source.Ref},
		} {
			if field.value != nil {
				if strings.TrimSpace(*field.value) == "" {
					return invalid(fmt.Errorf("%s.%s must not be blank", service.name, field.name))
				}
				*field.target = *field.value
			}
		}
		*service.target = source
	}
	if config.Name == nil && config.AlgodSource == nil && config.IndexerSource == nil && config.ConduitSource == nil && config.DevMode == nil && config.VFuture == nil {
		return invalid(fmt.Errorf("config must contain name, algodSource, indexerSource, conduitSource, devMode, or vFuture"))
	}
	return config, nil
}
