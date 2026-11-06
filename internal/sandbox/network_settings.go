package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NetworkSettings are persisted independently of the algod image selection.
// Nil fields leave the existing network template setting unchanged.
type NetworkSettings struct {
	DevMode *bool `json:"devMode,omitempty"`
	VFuture *bool `json:"vFuture,omitempty"`
}

func (s *ComposeSandbox) networkSettingsPath() string {
	return filepath.Join(s.Directory, "localnet-settings.json")
}

func (s *ComposeSandbox) loadNetworkSettings() error {
	data, err := os.ReadFile(s.networkSettingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.Settings); err != nil {
		return fmt.Errorf("invalid saved LocalNet settings: %w", err)
	}
	return nil
}

func networkTemplateWithSettings(content string, settings NetworkSettings) (string, error) {
	if settings.DevMode != nil {
		if !devModePattern.MatchString(content) {
			return "", fmt.Errorf("network template is missing Genesis.DevMode")
		}
		content = devModePattern.ReplaceAllLiteralString(content, fmt.Sprintf(`"DevMode": %t`, *settings.DevMode))
	}
	if settings.VFuture == nil {
		return content, nil
	}
	// The upstream template contains an unquoted NUM_ROUNDS placeholder.
	const roundsPlaceholder = "__ALGOKIT_NUM_ROUNDS__"
	var template map[string]any
	decoder := json.NewDecoder(strings.NewReader(strings.ReplaceAll(content, "NUM_ROUNDS", `"`+roundsPlaceholder+`"`)))
	decoder.UseNumber()
	if err := decoder.Decode(&template); err != nil {
		return "", err
	}
	genesis, ok := template["Genesis"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("network template is missing Genesis")
	}
	protocol, exists := genesis["ConsensusProtocol"]
	if *settings.VFuture {
		if protocol == "future" {
			return content, nil
		}
		genesis["ConsensusProtocol"] = "future"
	} else {
		if !exists {
			return content, nil
		}
		delete(genesis, "ConsensusProtocol")
	}
	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), `"`+roundsPlaceholder+`"`, "NUM_ROUNDS") + "\n", nil
}

// NetworkSettingsChanged checks whether applying settings changes genesis.
func (s *ComposeSandbox) NetworkSettingsChanged(settings NetworkSettings) (bool, error) {
	content, err := os.ReadFile(s.AlgodNetworkTemplateFilePath())
	if err != nil {
		return false, err
	}
	updated, err := networkTemplateWithSettings(string(content), settings)
	return updated != string(content), err
}

// SetNetworkSettings updates the network template and saves the selected toggles
// so starts and regenerated configurations retain them.
func (s *ComposeSandbox) SetNetworkSettings(settings NetworkSettings) error {
	content, err := os.ReadFile(s.AlgodNetworkTemplateFilePath())
	if err != nil {
		return err
	}
	updated, err := networkTemplateWithSettings(string(content), settings)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.AlgodNetworkTemplateFilePath(), []byte(updated), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(s.networkSettingsPath(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.Settings = settings
	return nil
}
