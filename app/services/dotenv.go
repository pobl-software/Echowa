package services

import (
	"fmt"
	"strings"

	"goravel/app/rules"
)

// EnvPair is one parsed KEY=value line.
type EnvPair struct {
	Key   string
	Value string
}

// ParseDotEnv reads .env text, as pasted into the Environment tab:
//
//   - blank lines and lines starting with # are skipped;
//   - an optional "export " prefix is allowed;
//   - double-quoted values may span lines and understand \n, \r, \t, \" and \\;
//   - single-quoted values are taken literally;
//   - unquoted values are trimmed, and " #" starts a comment.
//
// Errors name the line. Later duplicates win, as in a shell.
func ParseDotEnv(text string) ([]EnvPair, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var pairs []EnvPair

	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, rest, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found {
			return nil, fmt.Errorf("line %d: expected KEY=value", lineNo)
		}
		if !rules.IsEnvKey(key) {
			return nil, fmt.Errorf("line %d: %q isn't a valid variable name", lineNo, key)
		}
		rest = strings.TrimLeft(rest, " \t")

		var value string
		switch {
		case strings.HasPrefix(rest, `"`):
			// Join following lines until the closing quote.
			body := rest[1:]
			for {
				if v, ok := closeDoubleQuoted(body); ok {
					value = v
					break
				}
				if i+1 >= len(lines) {
					return nil, fmt.Errorf("line %d: unterminated double quote", lineNo)
				}
				i++
				body += "\n" + lines[i]
			}
		case strings.HasPrefix(rest, `'`):
			end := strings.Index(rest[1:], `'`)
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated single quote", lineNo)
			}
			value = rest[1 : end+1]
		default:
			if j := strings.Index(rest, " #"); j >= 0 {
				rest = rest[:j]
			}
			value = strings.TrimSpace(rest)
		}

		pairs = append(pairs, EnvPair{Key: key, Value: value})
	}

	return pairs, nil
}

// closeDoubleQuoted unescapes s up to its first unescaped quote. ok is
// false when there's no closing quote yet.
func closeDoubleQuoted(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return b.String(), true
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}

	return "", false
}
