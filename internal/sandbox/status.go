package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/joe-p/algokit-cli-go/internal/httpx"
	"github.com/joe-p/algokit-cli-go/internal/log"
	"github.com/joe-p/algokit-cli-go/internal/pyrepr"
)

// StatusEntry is a key/value line in the `localnet status` output.
type StatusEntry struct {
	Key   string
	Value string
}

var statusError = []StatusEntry{{"Status", "Error"}}

func hasPublishedPort(info ServiceInfo, port int) (bool, error) {
	publishers, ok := info["Publishers"].([]any)
	if !ok {
		return false, errors.New("'Publishers'")
	}
	for _, item := range publishers {
		publisher, ok := item.(map[string]any)
		if !ok {
			return false, errors.New("'PublishedPort'")
		}
		published, ok := publisher["PublishedPort"]
		if !ok {
			return false, errors.New("'PublishedPort'")
		}
		if n, ok := published.(float64); ok && n == float64(port) {
			return true, nil
		}
	}
	return false, nil
}

// lookup returns obj[key] formatted as Python would print it, or a KeyError-like error.
func lookup(obj map[string]any, key string) (string, error) {
	value, ok := obj[key]
	if !ok {
		return "", fmt.Errorf("'%s'", key)
	}
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case nil:
		return "None", nil
	}
	return fmt.Sprint(value), nil
}

// FetchAlgodStatus returns the algod status details for `localnet status`.
func FetchAlgodStatus(info ServiceInfo) []StatusEntry {
	results, err := fetchAlgodStatus(info)
	if err != nil {
		logger.Debug("Error checking algod status: "+err.Error(), log.WithError(err))
		return statusError
	}
	return results
}

func fetchAlgodStatus(info ServiceInfo) ([]StatusEntry, error) {
	// Search for DefaultAlgodPort in ports, if found use it, if not found this is an error
	if found, err := hasPublishedPort(info, DefaultAlgodPort); err != nil || !found {
		return statusError, err
	}
	results := []StatusEntry{{"Port", strconv.Itoa(DefaultAlgodPort)}}
	headers := map[string]string{"X-Algo-API-Token": DefaultAlgodToken}
	base := fmt.Sprintf("%s:%d", DefaultAlgodServer, DefaultAlgodPort)
	statusResponse, err := httpx.Get(base+"/v2/status", headers, 3*time.Second)
	if err != nil {
		return nil, err
	}
	versionsResponse, err := httpx.Get(base+"/versions", headers, 3*time.Second)
	if err != nil {
		return nil, err
	}
	if statusResponse.StatusCode != 200 || versionsResponse.StatusCode != 200 {
		return statusError, nil
	}

	var status map[string]any
	if err := statusResponse.JSON(&status); err != nil {
		return nil, err
	}
	lastRound, err := lookup(status, "last-round")
	if err != nil {
		return nil, err
	}
	results = append(results, StatusEntry{"Last round", lastRound})
	timeSinceLastRound, ok := status["time-since-last-round"].(json.Number)
	if !ok {
		return nil, errors.New("'time-since-last-round'")
	}
	nanoseconds, err := timeSinceLastRound.Float64()
	if err != nil {
		return nil, err
	}
	results = append(results, StatusEntry{"Time since last round", fmt.Sprintf("%.1fs", nanoseconds/1e9)})

	var versions map[string]any
	if err := versionsResponse.JSON(&versions); err != nil {
		return nil, err
	}
	for _, field := range []struct{ key, label string }{{"genesis_id", "Genesis ID"}, {"genesis_hash_b64", "Genesis hash"}} {
		value, err := lookup(versions, field.key)
		if err != nil {
			return nil, err
		}
		results = append(results, StatusEntry{field.label, value})
	}
	build, ok := versions["build"].(map[string]any)
	if !ok {
		return nil, errors.New("'build'")
	}
	var version [3]string
	for i, key := range []string{"major", "minor", "build_number"} {
		if version[i], err = lookup(build, key); err != nil {
			return nil, err
		}
	}
	results = append(results, StatusEntry{"Version", fmt.Sprintf("%s.%s.%s", version[0], version[1], version[2])})
	return results, nil
}

// FetchIndexerStatus returns the indexer status details for `localnet status`.
func FetchIndexerStatus(info ServiceInfo) []StatusEntry {
	results, err := fetchIndexerStatus(info)
	if err != nil {
		logger.Debug("Error checking indexer status: "+err.Error(), log.WithError(err))
		return statusError
	}
	return results
}

func fetchIndexerStatus(info ServiceInfo) ([]StatusEntry, error) {
	if found, err := hasPublishedPort(info, DefaultIndexerPort); err != nil || !found {
		return statusError, err
	}
	results := []StatusEntry{{"Port", strconv.Itoa(DefaultIndexerPort)}}
	healthURL := fmt.Sprintf("%s:%d/health", DefaultAlgodServer, DefaultIndexerPort)
	response, err := httpx.Get(healthURL, nil, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		return statusError, nil
	}
	var health map[string]any
	if err := response.JSON(&health); err != nil {
		return nil, err
	}
	repr, err := pyrepr.FromJSON(response.Body)
	if err != nil {
		return nil, err
	}
	logger.Debug(fmt.Sprintf("%s response: %s", healthURL, repr))
	for _, field := range []struct{ key, label string }{{"round", "Last round"}, {"version", "Version"}} {
		value, err := lookup(health, field.key)
		if err != nil {
			return nil, err
		}
		results = append(results, StatusEntry{field.label, value})
	}
	return results, nil
}
