package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// LocalnetConfig contains user-supplied LocalNet settings. The selected source
// is saved with the instance, so the input file is not needed on later starts.
type LocalnetConfig struct {
	Algod *AlgodSource `json:"algod"`
}

// ReadLocalnetConfig validates the entire file before any LocalNet is changed.
// Relative paths are resolved against the CLI's working directory.
func ReadLocalnetConfig(path string) (*LocalnetConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read LocalNet config %q: %w", path, err)
	}
	defer file.Close()

	// Pointers distinguish omitted fields from explicitly blank values.
	var input struct {
		Algod *struct {
			Repo *string `json:"repo"`
			Ref  *string `json:"ref"`
		} `json:"algod"`
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
	if input.Algod == nil || (input.Algod.Repo == nil && input.Algod.Ref == nil) {
		return invalid(fmt.Errorf("algod must contain repo or ref"))
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
				return invalid(fmt.Errorf("algod.%s must not be blank", field.name))
			}
			*field.target = *field.value
		}
	}
	return &LocalnetConfig{Algod: source}, nil
}
