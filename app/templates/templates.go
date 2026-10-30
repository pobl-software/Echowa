// Package templates holds the ready-made apps the New app page offers
// (ADR 0025). A template fills in the form: image, tag, port, healthcheck.
// Seed makes what the form doesn't show: env values, including generated
// passwords, the volumes and the container arguments.
package templates

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"slices"
	"strconv"

	"goravel/app/models"
)

// Template is one ready-made app.
type Template struct {
	Slug        string // stored on the app, e.g. "postgres"
	Name        string
	Description string
	Repository  string
	// DefaultTag is a major version, never "latest": a database's files
	// don't survive an unplanned major upgrade.
	DefaultTag string
	Port       int
	// HTTP apps can have a domain. The rest are reached by other apps at
	// echowa-<name>:<port> on the overlay network.
	HTTP        bool
	Healthcheck string
	// Server starts the server through the image's entrypoint, without the
	// flags Options add. Args runs it with sh, so it can read the env: a
	// secret it needs never becomes an argument of its own.
	Server string
	// ReplaceCMD runs Server even with no options on, because the image's
	// CMD lacks something, such as Redis's password. Otherwise the image's
	// CMD runs until an option adds flags.
	ReplaceCMD bool
	Env        []Env
	Volumes    []Volume
	// Connect builds the URL another app connects with, from the service's
	// host and port and the app's env.
	Connect func(host string, port int, env map[string]string) string
	// ConnectUser is who a client signs in as when Connect's URL names no
	// one: Redis's password belongs to its "default" user.
	ConnectUser string
	// TLS is how the server runs while external access is on (ADR 0026).
	// Nil publishes the plain port.
	TLS *TLS
	// Inspect reads the database's own figures (ADR 0027). Nil for a
	// template that has none.
	Inspect *Inspect
	// Backup dumps and restores the database (ADR 0034). Nil for a
	// template Echowa doesn't back up, such as Redis.
	Backup *Backup
	// Pooler is the connection pooler the app can run beside the server
	// (ADR 0035). Nil for a template without one.
	Pooler *Pooler
}

// TLS turns a template's server's TLS on with the certificate in the
// ECHOWA_TLS_* env vars.
type TLS struct {
	// Owner is the user the server runs as, who has to read the key.
	Owner string
	// Flags point Server at the certificate in tlsDir.
	Flags string
	// Port serves TLS and is the one published. 0 means Template.Port.
	Port int
	// Client says how to connect with the app's CA certificate.
	Client string
}

// Env is one env var a template sets.
type Env struct {
	Key   string
	Value EnvValue
}

// EnvValue is where an env var's value comes from. It's a closed set, so a
// template can't carry a placeholder that reaches a container unexpanded.
type EnvValue struct {
	kind  valueKind
	fixed string
}

type valueKind int

const (
	kindFixed valueKind = iota
	kindAppName
	kindGenerated
)

var (
	// AppName is the app's name, e.g. for the database a server creates.
	AppName = EnvValue{kind: kindAppName}
	// Generated is 32 random bytes, hex-encoded: a password safe in a URL
	// or a shell argument without quoting.
	Generated = EnvValue{kind: kindGenerated}
)

// Fixed is the same value for every app.
func Fixed(value string) EnvValue {
	return EnvValue{kind: kindFixed, fixed: value}
}

// Source says where the value comes from: "fixed", "app_name" or
// "generated". Pages show it instead of a value.
func (v EnvValue) Source() string {
	switch v.kind {
	case kindAppName:
		return "app_name"
	case kindGenerated:
		return "generated"
	default:
		return "fixed"
	}
}

// Volume is a named volume a template mounts. Its Docker name is
// echowa-<app>-<Name>.
type Volume struct {
	Name   string // e.g. "data"
	Target string // absolute path in the container
}

// Seed is what an app made from a template starts with, beyond its form.
type Seed struct {
	Env     map[string]string
	Volumes []models.AppVolume // AppID is left for the caller
	// Args are Args(Options{}). The deploy builds a template app's args
	// itself, but an older release, after an update rolls back, reads them
	// from the app's row (ADR 0027).
	Args []string
}

// Seed makes the env values, generating passwords, and names the volumes
// for an app called appName.
func (t Template) Seed(appName string) (Seed, error) {
	env := make(map[string]string, len(t.Env))
	for _, e := range t.Env {
		switch e.Value.kind {
		case kindFixed:
			env[e.Key] = e.Value.fixed
		case kindAppName:
			env[e.Key] = appName
		case kindGenerated:
			secret, err := password()
			if err != nil {
				return Seed{}, err
			}
			env[e.Key] = secret
		}
	}

	volumes := make([]models.AppVolume, 0, len(t.Volumes))
	for _, v := range t.Volumes {
		volumes = append(volumes, models.AppVolume{Name: VolumeName(appName, v.Name), Target: v.Target})
	}

	return Seed{Env: env, Volumes: volumes, Args: t.Args(Options{})}, nil
}

// VolumeName is the Docker volume for the app's volume called name.
func VolumeName(appName, name string) string {
	return models.ServicePrefix + appName + "-" + name
}

// VolumeNames are the Docker volumes an app called appName gets from t,
// none for a custom app, whose t is nil.
func VolumeNames(t *Template, appName string) []string {
	if t == nil {
		return nil
	}
	names := make([]string, 0, len(t.Volumes))
	for _, v := range t.Volumes {
		names = append(names, VolumeName(appName, v.Name))
	}

	return names
}

func password() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

// ConnectPreview is the template's URL with each env var's key in place of
// its value, so a page can show its shape without a secret.
func (t Template) ConnectPreview(host string, port int) string {
	keys := make(map[string]string, len(t.Env))
	for _, e := range t.Env {
		keys[e.Key] = e.Key
	}

	return t.Connect(host, port, keys)
}

// ConnectCredentials is the user and password in the template's URL, for
// a client that asks for them apart.
func (t Template) ConnectCredentials(host string, port int, env map[string]string) (user, password string) {
	u, err := url.Parse(t.Connect(host, port, env))
	if err != nil || u.User == nil {
		return "", ""
	}
	password, _ = u.User.Password()
	user = u.User.Username()
	if user == "" {
		user = t.ConnectUser
	}

	return user, password
}

// All lists the templates in the order the New app page shows them.
func All() []Template {
	return slices.Clone(all)
}

// Find returns the template with this slug.
func Find(slug string) (Template, bool) {
	i := slices.IndexFunc(all, func(t Template) bool { return t.Slug == slug })
	if i < 0 {
		return Template{}, false
	}

	return all[i], true
}

var all = []Template{
	{
		Slug:        "postgres",
		Name:        "PostgreSQL",
		Description: "A PostgreSQL server with one database and a generated password.",
		Repository:  "postgres",
		DefaultTag:  "18",
		Port:        5432,
		// Over TCP, so the server the entrypoint runs while it initialises,
		// which only listens on the socket, doesn't count as ready.
		Healthcheck: `pg_isready -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`,
		Server:      "docker-entrypoint.sh postgres",
		Env: []Env{
			{Key: "POSTGRES_USER", Value: AppName},
			{Key: "POSTGRES_DB", Value: AppName},
			{Key: "POSTGRES_PASSWORD", Value: Generated},
		},
		// 18 and later keep PGDATA in a versioned directory under here.
		Volumes: []Volume{{Name: "data", Target: "/var/lib/postgresql"}},
		Connect: func(host string, port int, env map[string]string) string {
			u := url.URL{
				Scheme: "postgres",
				User:   url.UserPassword(env["POSTGRES_USER"], env["POSTGRES_PASSWORD"]),
				Host:   net.JoinHostPort(host, strconv.Itoa(port)),
				Path:   "/" + env["POSTGRES_DB"],
			}
			return u.String()
		},
		TLS: &TLS{
			Owner: "postgres",
			Flags: "-c ssl=on -c ssl_cert_file=" + tlsDir + "/server.crt -c ssl_key_file=" + tlsDir + "/server.key",
			// Clients that don't ask for TLS still connect in the clear on the
			// same port; libpq and pgAdmin ask by default.
			Client: "Set SSL mode to verify-ca and the CA certificate as the root certificate: sslmode=verify-ca sslrootcert=<file> for psql.",
		},
		Inspect: postgresInspect,
		Backup:  postgresBackup,
		Pooler:  pgbouncer,
	},
	{
		Slug:        "mysql",
		Name:        "MySQL",
		Description: "A MySQL server with one database and generated passwords.",
		Repository:  "mysql",
		// The LTS release. Newer innovation releases can't be rolled back
		// from: MySQL won't start on files a later version has upgraded.
		DefaultTag: "9.7",
		Port:       3306,
		// Over TCP, so the server the entrypoint runs while it initialises,
		// which skips networking, doesn't count as ready. Logging in also
		// proves the user exists.
		Healthcheck: `MYSQL_PWD="$MYSQL_PASSWORD" mysql -h 127.0.0.1 -u "$MYSQL_USER" -e 'SELECT 1' "$MYSQL_DATABASE" >/dev/null`,
		Server:      "docker-entrypoint.sh mysqld",
		Env: []Env{
			// Not the app's name: MySQL user names stop at 32 characters,
			// and the image refuses "root".
			{Key: "MYSQL_USER", Value: Fixed("app")},
			{Key: "MYSQL_DATABASE", Value: AppName},
			{Key: "MYSQL_PASSWORD", Value: Generated},
			{Key: "MYSQL_ROOT_PASSWORD", Value: Generated},
		},
		Volumes: []Volume{{Name: "data", Target: "/var/lib/mysql"}},
		Connect: func(host string, port int, env map[string]string) string {
			u := url.URL{
				Scheme: "mysql",
				User:   url.UserPassword(env["MYSQL_USER"], env["MYSQL_PASSWORD"]),
				Host:   net.JoinHostPort(host, strconv.Itoa(port)),
				Path:   "/" + env["MYSQL_DATABASE"],
			}
			return u.String()
		},
		TLS: &TLS{
			// Without its own certificate, the image makes a self-signed one
			// that clients can't verify.
			Owner:  "mysql",
			Flags:  "--ssl-cert=" + tlsDir + "/server.crt --ssl-key=" + tlsDir + "/server.key",
			Client: "Set SSL mode to VERIFY_CA and the CA certificate as the CA file: --ssl-mode=VERIFY_CA --ssl-ca=<file> for mysql.",
		},
		Inspect: mysqlInspect,
		Backup:  mysqlBackup,
	},
	{
		Slug:        "redis",
		Name:        "Redis",
		Description: "A Redis server that saves to disk, with a generated password.",
		Repository:  "redis",
		DefaultTag:  "8",
		Port:        6379,
		Healthcheck: redisCLI + ` ping | grep -q PONG`,
		// The image takes its password only as an argument. sh reads it from
		// the encrypted env var, and the image's entrypoint still drops root.
		Server:     `docker-entrypoint.sh redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"`,
		ReplaceCMD: true,
		Env: []Env{
			{Key: "REDIS_PASSWORD", Value: Generated},
		},
		Volumes:     []Volume{{Name: "data", Target: "/data"}},
		ConnectUser: "default",
		Connect: func(host string, port int, env map[string]string) string {
			u := url.URL{
				Scheme: "redis",
				User:   url.UserPassword("", env["REDIS_PASSWORD"]),
				Host:   net.JoinHostPort(host, strconv.Itoa(port)),
			}
			return u.String()
		},
		// A second port that only speaks TLS, so a client outside never
		// sends the password in the clear. Apps on the server keep 6379.
		TLS: &TLS{
			Owner:  "redis",
			Flags:  "--tls-port 6380 --tls-cert-file " + tlsDir + "/server.crt --tls-key-file " + tlsDir + "/server.key --tls-auth-clients no",
			Port:   6380,
			Client: "Connect with TLS and the CA certificate: redis-cli --tls --cacert <file>, or a rediss:// URL.",
		},
		Inspect: redisInspect,
	},
}
