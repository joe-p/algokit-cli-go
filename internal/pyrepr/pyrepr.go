// Package pyrepr formats JSON the way Python prints the equivalent decoded value (repr of dicts/lists), so log
// messages match the Python CLI.
package pyrepr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// FromJSON returns the Python repr of the JSON document in data, preserving key order.
func FromJSON(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var b strings.Builder
	if err := writeValue(&b, decoder); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeValue(b *strings.Builder, decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch t := token.(type) {
	case json.Delim:
		closing, open := "]", "["
		if t == '{' {
			closing, open = "}", "{"
		}
		b.WriteString(open)
		for i := 0; decoder.More(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if t == '{' {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				b.WriteString(String(fmt.Sprint(key)))
				b.WriteString(": ")
			}
			if err := writeValue(b, decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil && err != io.EOF {
			return err
		}
		b.WriteString(closing)
	case string:
		b.WriteString(String(t))
	case json.Number:
		b.WriteString(t.String())
	case bool:
		if t {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case nil:
		b.WriteString("None")
	}
	return nil
}

// String returns the Python repr of a str.
func String(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case !unicode.IsPrint(r) && r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case !unicode.IsPrint(r) && r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		case !unicode.IsPrint(r):
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}
