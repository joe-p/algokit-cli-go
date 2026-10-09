package sandbox

import (
	_ "embed"
	"strconv"
	"strings"
)

// The templates are generated from the Python CLI's algokit.core.sandbox (get_config_json etc), and must match it
// byte for byte: the Python CLI resets LocalNet if the files on disk differ from what it would generate.

//go:embed templates/algod_config.json
var configJSON string

//go:embed templates/algod_network_template.json
var algodNetworkTemplate string

//go:embed templates/conduit.yml
var conduitYAML string

//go:embed templates/docker-compose.yml
var dockerComposeTemplate string

const (
	DefaultAlgodPort   = 4001
	DefaultKMDPort     = 4002
	DefaultTealdbgPort = 9392
	IndexerImage       = "algorand/indexer:latest"
	AlgorandImage      = "algorand/algod:latest"
	ConduitImage       = "algorandfoundation/conduit-localnet:latest"
)

// ConfigJSON returns the algod config.json content.
func ConfigJSON() string { return configJSON }

// AlgodNetworkTemplate returns the algod network template content.
func AlgodNetworkTemplate() string { return algodNetworkTemplate }

// ConduitYAML returns the conduit config content.
func ConduitYAML() string { return conduitYAML }

// DockerComposeYAML returns the docker compose definition for a LocalNet with the given compose project name
// (e.g. algokit_sandbox).
func DockerComposeYAML(name string) string {
	return dockerComposeYAMLWithPorts(name, DefaultAlgodPort, DefaultKMDPort, DefaultTealdbgPort)
}

func dockerComposeYAMLWithPorts(name string, algodPort, kmdPort, tealdbgPort int) string {
	return strings.NewReplacer(
		"{{NAME}}", name,
		"{{ALGOD_PORT}}", strconv.Itoa(algodPort),
		"{{KMD_PORT}}", strconv.Itoa(kmdPort),
		"{{TEALDBG_PORT}}", strconv.Itoa(tealdbgPort),
	).Replace(dockerComposeTemplate)
}
