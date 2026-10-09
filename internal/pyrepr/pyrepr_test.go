package pyrepr_test

import (
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/pyrepr"
)

func TestFromJSON(t *testing.T) {
	for input, want := range map[string]string{
		`{"round": 1, "errors": ["error"], "version": "v1.0"}`:  `{'round': 1, 'errors': ['error'], 'version': 'v1.0'}`,
		`{"b": true, "a": false, "n": null, "f": 1.5, "e": {}}`: `{'b': True, 'a': False, 'n': None, 'f': 1.5, 'e': {}}`,
		`[]`:                   `[]`,
		`"it's"`:               `"it's"`,
		`"both ' and \""`:      `'both \' and "'`,
		`"line\nbreak\\slash"`: `'line\nbreak\\slash'`,
		`"ünïcødé"`:            `'ünïcødé'`,
		`"\u0007"`:             `'\x07'`,
	} {
		if got, err := pyrepr.FromJSON([]byte(input)); err != nil || got != want {
			t.Errorf("FromJSON(%s) = %s, %v; want %s", input, got, err, want)
		}
	}
}
