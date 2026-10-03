package command

import (
	"fmt"
	"strings"
)

// QuoteJQL returns a double-quoted JQL literal, escaping backslashes and
// double quotes. C0 and DEL controls are refused rather than rewritten.
func QuoteJQL(v string) (string, error) {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("value %q contains a control character, which cannot be safely represented in a JQL string literal", v)
		}
	}
	var b strings.Builder
	b.Grow(len(v) + 2)
	b.WriteByte('"')
	for _, r := range v {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String(), nil
}
