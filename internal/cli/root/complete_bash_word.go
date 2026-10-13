package root

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gechr/x/shell"
)

// bashCompletionQuotes converts the ANSI-C quoting emitted by Bash printf %q
// into literal POSIX quoting for shell.Split. It never evaluates expansions.
// Unsupported escapes are refused rather than interpreted as filename bytes.
func bashCompletionQuotes(raw string, partial bool) (string, bool) {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(raw); {
		char := raw[i]
		if quote != 0 || char == '\\' {
			out.WriteByte(char)
			i++
			if char == '\\' && quote != '\'' && i < len(raw) {
				out.WriteByte(raw[i])
				i++
			} else if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			out.WriteByte(char)
			i++
			continue
		}
		if char != '$' || i+1 == len(raw) || raw[i+1] != '\'' {
			out.WriteByte(char)
			i++
			continue
		}
		i += 2
		var value strings.Builder
		for i < len(raw) && raw[i] != '\'' {
			if strings.HasPrefix(raw[i:], `\e`) || strings.HasPrefix(raw[i:], `\E`) {
				value.WriteByte(27) // Bash's escape spelling is absent from Go string literals.
				i += 2
				continue
			}
			runeValue, multibyte, tail, err := strconv.UnquoteChar(raw[i:], '\'')
			if err != nil {
				return "", false
			}
			i = len(raw) - len(tail)
			if multibyte {
				value.WriteRune(runeValue)
			} else {
				// Octal UTF-8 escapes encode individual bytes, not runes.
				value.WriteByte(byte(runeValue))
			}
		}
		if i == len(raw) {
			if !partial {
				return "", false
			}
		} else {
			i++
		}
		if !utf8.ValidString(value.String()) {
			return "", false
		}
		out.WriteString(shell.Quote(value.String()))
	}
	return out.String(), true
}
