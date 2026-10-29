package templates

import (
	"fmt"

	"goravel/app/docker"
)

// Pooler is a connection pooler that runs as a second service beside a
// template's server, and signs in to it as the app's user (ADR 0035).
type Pooler struct {
	// Image is pinned by digest. It changes with Echowa's releases, never
	// the user's.
	Image string
	Port  int
	// Command replaces the image's entrypoint, so only the image's
	// binaries run. It writes the pooler's config from the env.
	Command     []string
	Healthcheck string
	// Env lists the app's env vars the pooler gets, beside PoolerServerEnv.
	Env []string
	// Limits cap the pooler whatever the app's own limits are.
	Limits docker.Limits
}

// EnvFrom is the pooler's env: the vars it reads from env, the app's, and
// server, the service name of the server it connects to.
func (p *Pooler) EnvFrom(server string, env map[string]string) (map[string]string, error) {
	out := map[string]string{PoolerServerEnv: server}
	for _, key := range p.Env {
		if env[key] == "" {
			return nil, fmt.Errorf("%s is missing from the environment, so the pooler can't sign in", key)
		}
		out[key] = env[key]
	}

	return out, nil
}

// PoolerServerEnv holds the server's service name, which the pooler
// connects to.
const PoolerServerEnv = "POOLER_SERVER"

// pgbouncer runs PgBouncer in transaction mode with at most 50 server
// connections, which leaves PostgreSQL's other 47 for direct connections
// (ADR 0035). Every database name goes to the server, and PgBouncer pools
// each database apart, so max_user_connections caps them all together:
// the auth file has one user. The auth file holds the plain password, which PgBouncer needs
// to sign in to PostgreSQL with SCRAM; printf is the shell's own, so the
// password is never an argument, and a `"` in it is doubled, as the file's
// format wants. Connections and disconnections aren't logged: the healthcheck and apps that connect per
// request would fill the log with them. Nor is a stats line a minute, which would push
// failed logins, what the Logs tab is for, out of its tail. While a database
// redeploy has no task running, Docker's DNS doesn't know its name, so the
// pooler's next login fails. PgBouncer then refuses every client for
// server_login_retry and remembers the failed lookup for dns_nxdomain_ttl,
// 15 s each by default: apps failed for 14 s through the pooler and 5 s
// without it. At 1 s each, the pooler signs in again as soon as the name
// resolves.
var pgbouncer = &Pooler{
	Image: "edoburu/pgbouncer:v1.26.1-p0@sha256:488422e38cff912895d0d65d8963fcd306e6ac589a724398b6eb8e294e94d624",
	Port:  6432,
	Command: []string{"sh", "-c", `set -e
umask 077
dir=/tmp/pgbouncer
mkdir -p "$dir"
quote() { printf '"%s"' "$(printf '%s' "$1" | sed 's/"/""/g')"; }
printf '%s %s\n' "$(quote "$POSTGRES_USER")" "$(quote "$POSTGRES_PASSWORD")" > "$dir/userlist.txt"
cat > "$dir/pgbouncer.ini" <<EOF
[databases]
* = host=$POOLER_SERVER port=5432
[pgbouncer]
listen_addr = 0.0.0.0
listen_port = 6432
auth_type = scram-sha-256
auth_file = $dir/userlist.txt
pool_mode = transaction
default_pool_size = 50
max_user_connections = 50
max_client_conn = 1000
max_prepared_statements = 200
ignore_startup_parameters = extra_float_digits
log_connections = 0
log_disconnections = 0
log_stats = 0
server_login_retry = 1
dns_nxdomain_ttl = 1
EOF
unset POSTGRES_PASSWORD
exec pgbouncer "$dir/pgbouncer.ini"`},
	// Ready once it accepts connections, whether or not PostgreSQL is up,
	// so a database redeploy doesn't fail the pooler's healthcheck too.
	Healthcheck: `pg_isready -q -h 127.0.0.1 -p 6432 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`,
	Env:         []string{"POSTGRES_USER", "POSTGRES_DB", "POSTGRES_PASSWORD"},
	// It used about 7.5 MiB under load (ADR 0035).
	Limits: docker.Limits{NanoCPUs: 500_000_000, MemoryBytes: 128 << 20},
}
