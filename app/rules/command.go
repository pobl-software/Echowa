package rules

import (
	"errors"
	"strings"
)

// SplitCommand splits a command line into the arguments that replace an
// image's CMD, as a shell would split it: at spaces, with '...' taken as
// written, "..." allowing \" and \\, and \ escaping the next character.
// Nothing is expanded, since no shell runs it. Empty is no arguments.
func SplitCommand(s string) ([]string, error) {
	var args []string
	var arg strings.Builder
	inArg := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inArg {
				args = append(args, arg.String())
				arg.Reset()
				inArg = false
			}
		case c == '\'':
			inArg = true
			end := i + 1
			for end < len(runes) && runes[end] != '\'' {
				end++
			}
			if end == len(runes) {
				return nil, errors.New("a ' isn't closed")
			}
			arg.WriteString(string(runes[i+1 : end]))
			i = end
		case c == '"':
			inArg = true
			i++
			for ; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					i++
				}
				arg.WriteRune(runes[i])
			}
			if i == len(runes) {
				return nil, errors.New(`a " isn't closed`)
			}
		case c == '\\':
			inArg = true
			if i+1 < len(runes) {
				i++
				arg.WriteRune(runes[i])
			}
		default:
			inArg = true
			arg.WriteRune(c)
		}
	}
	if inArg {
		args = append(args, arg.String())
	}

	return args, nil
}

// IsCommand: a command line SplitCommand can split, its quotes closed.
func IsCommand(s string) bool {
	_, err := SplitCommand(s)
	return err == nil
}
