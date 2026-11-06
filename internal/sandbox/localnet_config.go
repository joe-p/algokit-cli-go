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
	AlgodSource *AlgodSource `json:"algodSource,omitempty"`
	DevMode     *bool        `json:"devMode,omitempty"`
	VFuture     *bool        `json:"vFuture,omitempty"`
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
	var input struct {
		Algod *struct {
			Repo *string `json:"repo"`
			Ref  *string `json:"ref"`
		} `json:"algodSource"`
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
	if input.Algod != nil {
		if input.Algod.Repo == nil && input.Algod.Ref == nil {
			return invalid(fmt.Errorf("algodSource must contain repo or ref"))
		}
		source := &AlgodSource{}
		for _, field := range []struct {
			name   string
			value  *string
			target *string
		}{
			{"repo", input.Algod.Repo, &source.Repo},
			{"ref", input.Algod.Ref, &source.Ref},
		} {
			if field.value != nil {
				if strings.TrimSpace(*field.value) == "" {
					return invalid(fmt.Errorf("algodSource.%s must not be blank", field.name))
				}
				*field.target = *field.value
			}
		}
		config.AlgodSource = source
	}
	if config.AlgodSource == nil && config.DevMode == nil && config.VFuture == nil {
		return invalid(fmt.Errorf("config must contain algodSource, devMode, or vFuture"))
	}
	return config, nil
}
