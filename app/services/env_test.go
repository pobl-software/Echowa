package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDotEnv(t *testing.T) {
	text := "# a comment\n" +
		"\n" +
		"PLAIN=value\n" +
		"export EXPORTED=yes\n" +
		"SPACED = padded value   \n" +
		"INLINE=value # comment\n" +
		"HASH=abc#def\n" +
		"EMPTY=\n" +
		"EQUALS=a=b=c\n" +
		`DOUBLE="line one\nline two \"quoted\" \\ done"` + "\n" +
		`SINGLE='literal \n $HOME # not a comment'` + "\n" +
		"MULTI=\"-----BEGIN KEY-----\n" +
		"abc\n" +
		"-----END KEY-----\"\n" +
		"PLAIN=overridden later\r\n"

	pairs, err := ParseDotEnv(text)
	require.NoError(t, err)

	assert.Equal(t, []EnvPair{
		{"PLAIN", "value"},
		{"EXPORTED", "yes"},
		{"SPACED", "padded value"},
		{"INLINE", "value"},
		{"HASH", "abc#def"},
		{"EMPTY", ""},
		{"EQUALS", "a=b=c"},
		{"DOUBLE", "line one\nline two \"quoted\" \\ done"},
		{"SINGLE", `literal \n $HOME # not a comment`},
		{"MULTI", "-----BEGIN KEY-----\nabc\n-----END KEY-----"},
		{"PLAIN", "overridden later"},
	}, pairs)
}

func TestParseDotEnvErrorsNameTheLine(t *testing.T) {
	tests := map[string]string{
		"A=1\nNOT A PAIR\n": "line 2: expected KEY=value",
		"A=1\n\n1BAD=x\n":   `line 3: "1BAD" isn't a valid variable name`,
		"MY-VAR=x":          `line 1: "MY-VAR" isn't a valid variable name`,
		"A=\"open\nstill\n": "line 1: unterminated double quote",
		"A='open":           "line 1: unterminated single quote",
	}
	for text, want := range tests {
		_, err := ParseDotEnv(text)
		assert.EqualError(t, err, want, "input %q", text)
	}
}

func TestApplyEnvEdits(t *testing.T) {
	str := func(s string) *string { return &s }
	current := map[string]string{"KEEP": "kept", "RENAME": "moved", "CHANGE": "old", "DROP": "gone"}

	env, err := ApplyEnvEdits(current, []EnvEdit{
		{Key: "KEEP", OriginalKey: str("KEEP")},
		{Key: "RENAMED", OriginalKey: str("RENAME")},
		{Key: "CHANGE", OriginalKey: str("CHANGE"), Value: str("new")},
		{Key: "ADDED", Value: str("")},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"KEEP": "kept", "RENAMED": "moved", "CHANGE": "new", "ADDED": ""}, env)

	_, err = ApplyEnvEdits(current, []EnvEdit{{Key: "A", Value: str("1")}, {Key: "B"}})
	assert.Equal(t, &EnvEditError{Row: 1, Message: "enter a value"}, err, "a new row needs a value")

	_, err = ApplyEnvEdits(current, []EnvEdit{{Key: "X", OriginalKey: str("MISSING")}})
	assert.Equal(t, &EnvEditError{Row: 0, Message: "enter a value"}, err, "an unchanged row must exist")

	_, err = ApplyEnvEdits(current, []EnvEdit{{Key: "A", Value: str("1")}, {Key: "A", Value: str("2")}})
	assert.Equal(t, &EnvEditError{Row: 1, Message: "this key is already used above"}, err)

	env, err = ApplyEnvEdits(current, nil)
	require.NoError(t, err)
	assert.Empty(t, env, "no rows clears the environment")
}

func TestShortDigest(t *testing.T) {
	assert.Equal(t, "0123456789ab", ShortDigest("ghcr.io/o/a@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	assert.Equal(t, "0123456789ab", ShortDigest("sha256:0123456789abcdef"))
	assert.Equal(t, "", ShortDigest("nginx:latest"))
	assert.Equal(t, "", ShortDigest(""))
}
