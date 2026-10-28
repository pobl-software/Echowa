package templates

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerVersionReadsWhatTheVersionCommandsPrint(t *testing.T) {
	for out, want := range map[string]string{
		"postgres (PostgreSQL) 18.0 (Debian 18.0-1.pgdg13+3)\n":                     "18.0",
		"postgres (PostgreSQL) 18beta1\n":                                           "18",
		"/usr/sbin/mysqld  Ver 9.7.0 for Linux on aarch64 (MySQL Community Server)": "9.7.0",
	} {
		got, err := ServerVersion([]byte(out))
		require.NoError(t, err, out)
		assert.Equal(t, want, got, out)
	}

	_, err := ServerVersion([]byte("command not found"))
	assert.Error(t, err)
}

func TestBackupsRestoreIntoTheSameOrANewerServerOnly(t *testing.T) {
	cases := []struct {
		slug, from, to string
		want           bool
	}{
		{"postgres", "18.0", "18.0", true},
		{"postgres", "18.0", "18.3", true},
		{"postgres", "18.3", "18.0", true},
		{"postgres", "17.6", "18.0", true},
		{"postgres", "18.0", "17.6", false},
		{"postgres", "9.6", "10", true},
		{"postgres", "18", "18.1", true},
		{"mysql", "9.7.0", "9.7.2", true},
		{"mysql", "9.7.2", "9.7.0", true},
		{"mysql", "8.4.6", "9.7.0", true},
		{"mysql", "9.7.0", "9.6.0", false},
		{"mysql", "9.7.0", "8.4.6", false},
		{"mysql", "9.10.0", "9.9.0", false},
		{"postgres", "", "18.0", false},
		{"postgres", "18.0", "eighteen", false},
	}
	for _, c := range cases {
		tmpl, ok := Find(c.slug)
		require.True(t, ok)
		assert.Equal(t, c.want, tmpl.Backup.Compatible(c.from, c.to), "%s %s into %s", c.slug, c.from, c.to)
	}
}

func TestBackupCommandsKeepSecretsOutOfTheirArguments(t *testing.T) {
	for _, tmpl := range All() {
		if tmpl.Backup == nil {
			continue
		}
		t.Run(tmpl.Slug, func(t *testing.T) {
			b := tmpl.Backup
			assert.NotEmpty(t, b.Version)
			assert.NotEmpty(t, b.Extension)
			assert.NotNil(t, b.Compatible)
			for _, cmd := range [][]string{b.Dump, b.Restore} {
				require.Equal(t, []string{"sh", "-c"}, cmd[:2], "the env is read inside sh -c")
				require.Len(t, cmd, 3)
				assert.True(t, strings.HasPrefix(cmd[2], "exec ") || strings.Contains(cmd[2], "; exec "),
					"sh execs the client, so stdin and signals reach it: %s", cmd[2])
				assert.NotRegexp(t, `(^|\s)(-p|--password)`, cmd[2], "no password argument")
			}
		})
	}
	redis, _ := Find("redis")
	assert.Nil(t, redis.Backup, "Redis isn't backed up")
}
