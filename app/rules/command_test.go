package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitCommand(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"php artisan horizon", []string{"php", "artisan", "horizon"}},
		{"  php   artisan\tqueue:work  ", []string{"php", "artisan", "queue:work"}},
		{`sh -c 'echo $HOME && sleep 1'`, []string{"sh", "-c", "echo $HOME && sleep 1"}},
		{`echo "a \"b\" c\\d" e`, []string{"echo", `a "b" c\d`, "e"}},
		{`echo "it's"`, []string{"echo", "it's"}},
		{`echo 'it'\''s'`, []string{"echo", "it's"}},
		{`a\ b c`, []string{"a b", "c"}},
		{`--flag=''`, []string{"--flag="}},
		{`''`, []string{""}},
	} {
		got, err := SplitCommand(tt.in)
		require.NoError(t, err, tt.in)
		assert.Equal(t, tt.want, got, tt.in)
	}

	for _, bad := range []string{`echo 'open`, `echo "open`, `echo "a\"`} {
		_, err := SplitCommand(bad)
		assert.Error(t, err, bad)
		assert.False(t, IsCommand(bad), bad)
	}
}
