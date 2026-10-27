package setup

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// token is a well-formed (but not real) Cloudflare Tunnel token.
var token = base64.StdEncoding.EncodeToString([]byte(`{"a":"account","t":"tunnel-id","s":"c2VjcmV0"}`))

func TestParseFlags(t *testing.T) {
	opts, err := parse([]string{"--with-backups", "--access", "https", "--domain", "Echowa.Example.com"}, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, Options{WithBackups: true, Access: AccessHTTPS, Domain: "echowa.example.com"}, opts)

	opts, err = parse([]string{"--tunnel-token", token}, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, AccessTunnel, opts.Access, "a token implies the tunnel")

	for args, want := range map[string]string{
		"--access ftp":                     "must be http, https or tunnel",
		"--access http --tunnel-token abc": "--tunnel-token is for --access tunnel",
		"--url https://echowa.example.com": "not defined",
		"--acme-email a@b.c":               "not defined",
		"extra":                            "unexpected argument",
	} {
		_, err := parse(strings.Fields(args), io.Discard)
		assert.ErrorContains(t, err, want, args)
	}
}

func TestCheckAccess(t *testing.T) {
	check := func(opts Options) error {
		return (&steps{opts: opts}).checkAccess()
	}
	assert.NoError(t, check(Options{Access: AccessHTTP}))
	assert.NoError(t, check(Options{Access: AccessHTTPS, Domain: "echowa.example.com"}))
	assert.ErrorContains(t, check(Options{Access: AccessHTTPS}), "needs --domain")
	assert.ErrorContains(t, check(Options{Access: AccessHTTPS, Domain: "https://echowa.example.com"}), "isn't a domain")
	assert.NoError(t, check(Options{Access: AccessTunnel, TunnelToken: token}))
	assert.ErrorContains(t, check(Options{Access: AccessTunnel}), "needs its token")
	assert.ErrorContains(t, check(Options{Access: AccessTunnel, TunnelToken: "sudo cloudflared service install"}), "isn't a Cloudflare Tunnel token")
}

func TestEveryFileSetupWritesIsEmbedded(t *testing.T) {
	for _, name := range []string{"echowa.service", "echowa-update.path", "echowa-update.service", "echowa-backup.service", "echowa-backup.timer", "echowa.env", "echowa-artisan", "traefik.yml", "traefik-ui.yml"} {
		data, err := files.ReadFile("files/" + name)
		require.NoError(t, err, name)
		assert.NotEmpty(t, data, name)
	}
}

func TestRenderEnv(t *testing.T) {
	template, err := files.ReadFile("files/echowa.env")
	require.NoError(t, err)
	key, err := newAppKey()
	require.NoError(t, err)

	env := parseEnv(renderEnv(string(template), map[string]string{
		"APP_KEY":                 key,
		"APP_URL":                 "https://echowa.example.com",
		"APP_HOST":                "172.18.0.1",
		"ECHOWA_CLIENT_IP_HEADER": "X-Real-IP",
		"ECHOWA_NEW":              "added",
	}))
	assert.Equal(t, key, env["APP_KEY"])
	assert.Equal(t, "https://echowa.example.com", env["APP_URL"])
	assert.Equal(t, "172.18.0.1", env["APP_HOST"])
	assert.Equal(t, "3000", env["APP_PORT"], "the template's, untouched")
	assert.Equal(t, "X-Real-IP", env["ECHOWA_CLIENT_IP_HEADER"], "a commented-out line is filled in")
	assert.Equal(t, "added", env["ECHOWA_NEW"])
	assert.Equal(t, "production", env["APP_ENV"])
	assert.Equal(t, "/var/lib/echowa/echowa.sqlite", env["DB_DATABASE"])
	assert.NotContains(t, env, "ECHOWA_TRUSTED_PROXIES", "still commented out")
}

func TestTheUIRouteIsTraefikConfig(t *testing.T) {
	route := renderRoute("echowa.example.com", "http://172.18.0.1:3000")
	assert.Contains(t, route, "rule: Host(`echowa.example.com`)")
	assert.Contains(t, route, "entryPoints: [echowa]\n", "port 8192 only, never the apps' 443")
	assert.Contains(t, route, "certResolver: le")
	assert.Contains(t, route, "- url: http://172.18.0.1:3000\n")

	stack, err := files.ReadFile("files/traefik.yml")
	require.NoError(t, err)
	assert.Contains(t, string(stack), TraefikDynamic+":/etc/traefik/dynamic:ro")
	assert.Contains(t, string(stack), "--providers.file.directory=/etc/traefik/dynamic")
	assert.Contains(t, string(stack), "--entrypoints.echowa.address=:"+UIPort+"\n")
	assert.NotContains(t, string(stack), "published: "+UIPort, "published by traefik-ui.yml only, as Echowa holds it with HTTP access")

	ui, err := files.ReadFile("files/traefik-ui.yml")
	require.NoError(t, err)
	assert.Contains(t, string(ui), "target: "+UIPort+"\n        published: "+UIPort+"\n")
}

func TestHTTPSServesTheUIOnItsOwnPort(t *testing.T) {
	s := &steps{opts: Options{Access: AccessHTTPS, Domain: "echowa.example.com"}, fresh: true}
	assert.True(t, s.traefikServesUI())
	s.opts.Access = AccessHTTP
	assert.False(t, s.traefikServesUI(), "Echowa itself listens on 8192")
	s.opts.Access = AccessTunnel
	assert.False(t, s.traefikServesUI(), "no open port")

	s = &steps{opts: Options{Access: AccessHTTPS, Domain: "echowa.example.com"}}
	assert.False(t, s.traefikServesUI(), "a rerun follows the route on disk, never the flags")
}

func TestNewAppKey(t *testing.T) {
	a, err := newAppKey()
	require.NoError(t, err)
	b, err := newAppKey()
	require.NoError(t, err)

	assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9]{32}$`), a)
	assert.NotEqual(t, a, b)
}

func TestParseOSReleaseAndDockerSources(t *testing.T) {
	info := parseOSRelease(`PRETTY_NAME="Ubuntu 24.04.3 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION_CODENAME=noble
ID=ubuntu
UBUNTU_CODENAME=noble
`)
	assert.Equal(t, "ubuntu", info["ID"])
	assert.Equal(t, "24.04", info["VERSION_ID"])
	assert.Contains(t, dockerSources(info), "Suites: noble\n")
	assert.Contains(t, dockerSources(map[string]string{"VERSION_CODENAME": "jammy"}), "Suites: jammy\n")
}

func TestRouteSource(t *testing.T) {
	assert.Equal(t, "203.0.113.10", routeSource("1.1.1.1 via 203.0.113.1 dev eth0 src 203.0.113.10 uid 0\n    cache"))
	assert.Empty(t, routeSource("RTNETLINK answers: Network is unreachable"))
}

func TestInterrupted(t *testing.T) {
	ctx := context.Background()
	err := exec.Command("sh", "-c", "kill -INT $$").Run()
	assert.True(t, interrupted(ctx, fmt.Errorf("apt-get update: %w", err)), "a command Ctrl-C stopped")
	assert.False(t, interrupted(ctx, exec.Command("false").Run()), "a command that failed")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	assert.True(t, interrupted(cancelled, errors.New("interrupted")))
}

func TestPlainOutput(t *testing.T) {
	var out bytes.Buffer
	u := newUI(&out)
	u.begin("Docker Engine")
	u.doing("installing")
	u.Write([]byte("E: no space left"))
	u.ok("29.9.2")
	u.say("Backed up the database")
	u.begin("Traefik")
	u.failed(errors.New("port 80 is taken\nby python3"), false)

	assert.Equal(t, "· Docker Engine: installing\n"+
		"E: no space left\n"+
		"✓ Docker Engine  29.9.2\n"+
		"· Backed up the database\n"+
		"✗ Traefik        failed\n\n"+
		"✗ Setup stopped: port 80 is taken\n  by python3\n\n"+
		"Every step checks before it changes anything, so fix that, then run the same command again.\n", out.String())
	assert.NotContains(t, out.String(), "\033", "no escapes off a terminal")
}

func TestRestoreScriptReplacesTheDatabaseAndItsLog(t *testing.T) {
	dir := t.TempDir()
	backup, database := filepath.Join(dir, "backup.sqlite"), filepath.Join(dir, "echowa.sqlite")
	require.NoError(t, os.WriteFile(backup, []byte("before"), 0o600))
	for _, name := range []string{database, database + "-wal", database + "-shm"} {
		require.NoError(t, os.WriteFile(name, []byte("after"), 0o600))
	}

	out, err := exec.Command("/bin/sh", "-c", restoreScript, "sh", backup, database).CombinedOutput()
	require.NoError(t, err, string(out))

	data, err := os.ReadFile(database)
	require.NoError(t, err)
	assert.Equal(t, "before", string(data))
	for _, name := range []string{database + "-wal", database + "-shm", database + ".restore"} {
		assert.NoFileExists(t, name, "SQLite would replay the old log into the restored copy")
	}
	assert.FileExists(t, backup, "the backup is kept")
}
