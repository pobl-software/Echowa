package templates

import (
	"crypto/tls"
	"crypto/x509"
	"maps"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/rules"
)

// Every template passes the checks the app form and the env editor apply,
// so a bad template fails here rather than on a user's deploy.
func TestTemplatesPassTheAppRules(t *testing.T) {
	slugs := map[string]bool{}
	for _, tmpl := range All() {
		t.Run(tmpl.Slug, func(t *testing.T) {
			assert.True(t, rules.IsSlug(tmpl.Slug), "slug")
			assert.False(t, slugs[tmpl.Slug], "slugs are unique")
			slugs[tmpl.Slug] = true
			assert.NotEmpty(t, tmpl.Name)
			assert.NotEmpty(t, tmpl.Description)
			assert.True(t, rules.IsRepository(tmpl.Repository), "repository %q", tmpl.Repository)
			assert.True(t, rules.IsTag(tmpl.DefaultTag), "tag %q", tmpl.DefaultTag)
			assert.NotEqual(t, "latest", tmpl.DefaultTag, "pin a major version")
			assert.True(t, rules.IsPort(tmpl.Port), "port")
			assert.NotEmpty(t, tmpl.Healthcheck, "a template's deploy is gated on its healthcheck")
			assert.LessOrEqual(t, len(tmpl.Healthcheck), 1000, "the form's healthcheck limit")

			// Docker runs the healthcheck every few seconds, and anyone on
			// the host can read a process's arguments.
			for _, e := range tmpl.Env {
				if e.Value == Generated {
					rest := strings.ReplaceAll(tmpl.Healthcheck, `="$`+e.Key+`"`, "")
					assert.NotContains(t, rest, "$"+e.Key, "the healthcheck passes %s in an env var, not an argument", e.Key)
				}
			}

			keys := map[string]bool{}
			for _, e := range tmpl.Env {
				assert.True(t, rules.IsEnvKey(e.Key), "env key %q", e.Key)
				assert.False(t, keys[e.Key], "env key %q is set once", e.Key)
				keys[e.Key] = true
			}

			if assert.NotNil(t, tmpl.Connect, "connect") {
				assert.Contains(t, tmpl.ConnectPreview("echowa-db", tmpl.Port), "echowa-db:"+strconv.Itoa(tmpl.Port))
				seed, err := tmpl.Seed("db")
				require.NoError(t, err)
				user, _ := tmpl.ConnectCredentials("echowa-db", tmpl.Port, seed.Env)
				assert.NotEmpty(t, user, "a client knows who to sign in as")
			}

			targets := map[string]bool{}
			for _, v := range tmpl.Volumes {
				assert.True(t, rules.IsSlug(v.Name), "volume name %q", v.Name)
				assert.True(t, path.IsAbs(v.Target), "volume target %q", v.Target)
				assert.False(t, targets[v.Target], "volume target %q is mounted once", v.Target)
				targets[v.Target] = true
			}
		})
	}
}

func TestFind(t *testing.T) {
	postgres, ok := Find("postgres")
	require.True(t, ok)
	assert.Equal(t, "PostgreSQL", postgres.Name)

	_, ok = Find("mongodb")
	assert.False(t, ok)
}

func TestSeedMakesValuesAndNamesVolumes(t *testing.T) {
	tmpl := Template{
		Server:     "server",
		ReplaceCMD: true,
		Env: []Env{
			{Key: "USER", Value: AppName},
			{Key: "MODE", Value: Fixed("production")},
			{Key: "PASSWORD", Value: Generated},
		},
		Volumes: []Volume{{Name: "data", Target: "/data"}},
	}

	seed, err := tmpl.Seed("my-db")
	require.NoError(t, err)
	assert.Equal(t, "my-db", seed.Env["USER"])
	assert.Equal(t, "production", seed.Env["MODE"])
	assert.Regexp(t, `^[0-9a-f]{64}$`, seed.Env["PASSWORD"])
	require.Len(t, seed.Volumes, 1)
	assert.Equal(t, "echowa-my-db-data", seed.Volumes[0].Name)
	assert.Equal(t, "/data", seed.Volumes[0].Target)
	assert.Equal(t, tmpl.Args(Options{}), seed.Args)

	again, err := tmpl.Seed("my-db")
	require.NoError(t, err)
	assert.NotEqual(t, seed.Env["PASSWORD"], again.Env["PASSWORD"], "every app gets its own password")
}

func TestAllReturnsACopy(t *testing.T) {
	list := All()
	list[0].Slug = "changed"

	_, ok := Find(All()[0].Slug)
	assert.True(t, ok)
}

func TestConnectURLs(t *testing.T) {
	postgres, _ := Find("postgres")
	assert.Equal(t, "postgres://POSTGRES_USER:POSTGRES_PASSWORD@echowa-db:5432/POSTGRES_DB", postgres.ConnectPreview("echowa-db", 5432))
	assert.Equal(t, "postgres://db:secret@echowa-db:5433/db",
		postgres.Connect("echowa-db", 5433, map[string]string{"POSTGRES_USER": "db", "POSTGRES_PASSWORD": "secret", "POSTGRES_DB": "db"}))

	mysql, _ := Find("mysql")
	assert.Equal(t, "mysql://MYSQL_USER:MYSQL_PASSWORD@echowa-db:3306/MYSQL_DATABASE", mysql.ConnectPreview("echowa-db", 3306))

	redis, _ := Find("redis")
	assert.Equal(t, "redis://:REDIS_PASSWORD@echowa-cache:6379", redis.ConnectPreview("echowa-cache", 6379))

	// A password someone typed into the Environment tab is escaped.
	parsed, err := url.Parse(redis.Connect("echowa-cache", 6379, map[string]string{"REDIS_PASSWORD": "p@ss/w:rd"}))
	require.NoError(t, err)
	password, _ := parsed.User.Password()
	assert.Equal(t, "p@ss/w:rd", password)
}

func TestConnectCredentials(t *testing.T) {
	mysql, _ := Find("mysql")
	user, password := mysql.ConnectCredentials("echowa-shop", 3306, map[string]string{"MYSQL_USER": "app", "MYSQL_PASSWORD": "p@ss/w:rd"})
	assert.Equal(t, "app", user)
	assert.Equal(t, "p@ss/w:rd", password, "unescaped, as it's typed into a client")

	redis, _ := Find("redis")
	user, password = redis.ConnectCredentials("echowa-cache", 6379, map[string]string{"REDIS_PASSWORD": "secret"})
	assert.Equal(t, "default", user, "the user Redis's password belongs to")
	assert.Equal(t, "secret", password)
}

func TestIssueTLSMakesACAThatVerifiesTheServer(t *testing.T) {
	now := time.Now()
	env, err := IssueTLS("db", now)
	require.NoError(t, err)
	require.ElementsMatch(t, TLSEnvKeys, slices.Collect(maps.Keys(env)))

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(env[EnvTLSCA])))
	pair, err := tls.X509KeyPair([]byte(env[EnvTLSCert]), []byte(env[EnvTLSKey]))
	require.NoError(t, err, "the key matches the certificate")
	server, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)

	_, err = server.Verify(x509.VerifyOptions{Roots: roots, DNSName: "echowa-db", CurrentTime: now.AddDate(9, 0, 0)})
	assert.NoError(t, err, "a client trusting the CA accepts the server for years")
	_, err = server.Verify(x509.VerifyOptions{Roots: roots, DNSName: "echowa-other"})
	assert.Error(t, err, "it names only this app's host")

	again, err := IssueTLS("db", now)
	require.NoError(t, err)
	otherRoots := x509.NewCertPool()
	otherRoots.AppendCertsFromPEM([]byte(again[EnvTLSCA]))
	_, err = server.Verify(x509.VerifyOptions{Roots: otherRoots, DNSName: "echowa-db"})
	assert.Error(t, err, "each issue is its own CA")
}

func TestTemplatesWithTLSReadTheCertificateFromTheEnv(t *testing.T) {
	for _, tmpl := range All() {
		if tmpl.TLS == nil {
			continue
		}
		t.Run(tmpl.Slug, func(t *testing.T) {
			args := tmpl.Args(Options{TLS: true})
			require.Len(t, args, 3)
			script := args[2]
			assert.Contains(t, script, "$"+EnvTLSCert)
			assert.Contains(t, script, "$"+EnvTLSKey)
			assert.Contains(t, script, "exec docker-entrypoint.sh", "the image's entrypoint still drops root")
			assert.NotContains(t, strings.Join(args, " "), "BEGIN", "no certificate is baked into the args")
			assert.True(t, tmpl.TLS.Port == 0 || rules.IsPort(tmpl.TLS.Port))
			assert.NotEmpty(t, tmpl.TLS.Client)
		})
	}
}
