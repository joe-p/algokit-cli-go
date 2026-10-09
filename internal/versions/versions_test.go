package versions_test

import (
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/versions"
)

func TestExtractTriple(t *testing.T) {
	if got, err := versions.ExtractTriple(`{"version": "v2.10.0-gitpod.0"}`); err != nil || got != "2.10.0" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := versions.ExtractTriple("v2.5-dev"); err == nil {
		t.Error("expected an error")
	}
}

func TestIsMinimum(t *testing.T) {
	for _, tc := range []struct {
		system, minimum string
		want            bool
	}{
		{"2.5.0", "2.5.0", true},
		{"2.10.0", "2.5.0", true},
		{"2.4.9", "2.5.0", false},
		{"3.0.0", "2.5.0", true},
		{"1.0.6", "1.0.6", true},
		{"1.0", "1.0.0", false},
		{"1.0.0.1", "1.0.0", true},
	} {
		if got, err := versions.IsMinimum(tc.system, tc.minimum); err != nil || got != tc.want {
			t.Errorf("IsMinimum(%s, %s) = %v, %v; want %v", tc.system, tc.minimum, got, err, tc.want)
		}
	}
}
