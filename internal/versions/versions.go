// Package versions parses and compares version numbers, equivalent to the helpers in algokit.core.utils.
package versions

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var triplePattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// ExtractTriple returns the first `major.minor.patch` version in s.
func ExtractTriple(s string) (string, error) {
	match := triplePattern.FindString(s)
	if match == "" {
		return "", errors.New("unable to parse version number")
	}
	return match, nil
}

func parts(version string) ([]int, error) {
	var result []int
	for part := range strings.SplitSeq(version, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid version: %s", version)
		}
		result = append(result, n)
	}
	return result, nil
}

// IsMinimum reports whether system >= minimum, comparing dot-separated integers like Python tuples.
func IsMinimum(system, minimum string) (bool, error) {
	s, err := parts(system)
	if err != nil {
		return false, err
	}
	m, err := parts(minimum)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(s) && i < len(m); i++ {
		if s[i] != m[i] {
			return s[i] > m[i], nil
		}
	}
	// shorter tuples compare lower when all shared parts are equal
	return len(s) >= len(m), nil
}
