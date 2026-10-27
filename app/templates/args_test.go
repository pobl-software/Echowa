package templates

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wantTLSSetup = `set -e; (umask 077; mkdir -p /run/echowa-tls; printf '%s\n' "$ECHOWA_TLS_CERT" > /run/echowa-tls/server.crt; printf '%s\n' "$ECHOWA_TLS_KEY" > /run/echowa-tls/server.key); chown -R `

// Apps made before args were built from parts stored these on their rows,
// and redeploying them must start the same server.
func TestArgsMatchWhatAppsWereSeededWith(t *testing.T) {
	tests := []struct {
		slug string
		opts Options
		want []string
	}{
		{"postgres", Options{}, nil},
		{"postgres", Options{TLS: true}, []string{"sh", "-c", wantTLSSetup + "postgres /run/echowa-tls; exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/run/echowa-tls/server.crt -c ssl_key_file=/run/echowa-tls/server.key"}},
		{"mysql", Options{}, nil},
		{"mysql", Options{TLS: true}, []string{"sh", "-c", wantTLSSetup + "mysql /run/echowa-tls; exec docker-entrypoint.sh mysqld --ssl-cert=/run/echowa-tls/server.crt --ssl-key=/run/echowa-tls/server.key"}},
		{"redis", Options{}, []string{"sh", "-c", `exec docker-entrypoint.sh redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"`}},
		{"redis", Options{TLS: true}, []string{"sh", "-c", wantTLSSetup + `redis /run/echowa-tls; exec docker-entrypoint.sh redis-server --appendonly yes --requirepass "$REDIS_PASSWORD" --tls-port 6380 --tls-cert-file /run/echowa-tls/server.crt --tls-key-file /run/echowa-tls/server.key --tls-auth-clients no`}},
	}
	for _, tt := range tests {
		tmpl, ok := Find(tt.slug)
		require.True(t, ok, tt.slug)
		assert.Equal(t, tt.want, tmpl.Args(tt.opts), "%s with %+v", tt.slug, tt.opts)
	}
}

func TestArgsAddEachOptionsFlags(t *testing.T) {
	tmpl := Template{
		Server:  "docker-entrypoint.sh server",
		TLS:     &TLS{Owner: "server", Flags: "--tls"},
		Inspect: &Inspect{SlowQueryFlags: "--slow"},
	}

	assert.Nil(t, tmpl.Args(Options{}), "with nothing on, the image's CMD runs")
	got := tmpl.Args(Options{TLS: true})
	require.Len(t, got, 3)
	assert.Equal(t, wantTLSSetup+"server /run/echowa-tls; exec docker-entrypoint.sh server --tls", got[2])
	assert.Equal(t, []string{"sh", "-c", "exec docker-entrypoint.sh server --slow"}, tmpl.Args(Options{SlowQueries: true}))
	got = tmpl.Args(Options{TLS: true, SlowQueries: true})
	assert.Equal(t, wantTLSSetup+"server /run/echowa-tls; exec docker-entrypoint.sh server --tls --slow", got[2])

	tmpl.ReplaceCMD = true
	assert.Equal(t, []string{"sh", "-c", "exec docker-entrypoint.sh server"}, tmpl.Args(Options{}))

	tmpl.TLS = nil
	assert.Equal(t, []string{"sh", "-c", "exec docker-entrypoint.sh server"}, tmpl.Args(Options{TLS: true}), "a template without TLS ignores the option")
}
