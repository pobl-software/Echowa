//go:build docker

// Runs each template on a real Swarm manager, the way a deploy would, and
// checks what its users rely on: the healthcheck passes, the password is
// required, and data survives a redeploy. Run it in the dev machine with
// `deploy/dev-vm.sh test-docker`, or on the Mac with
//
//	go test -tags docker ./app/templates/...
//
// It pulls postgres:18, mysql:9.7 and redis:8, and needs the docker CLI to
// exec into the tasks. PostgreSQL and MySQL are also backed up and
// restored, as ADR 0034 does.
package templates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/docker"
)

func TestPostgresTemplate(t *testing.T) {
	run := deployTemplate(t, "postgres", false)
	user := run.seed.Env["POSTGRES_USER"]
	psql := func(password, sql string) (string, error) {
		// From another container, as an app connects. The image trusts
		// connections from inside its own container, password or not.
		return run.client("env", "PGPASSWORD="+password, "psql", "-h", run.spec.Name, "-U", user, "-d", user, "-tAc", sql)
	}

	_, err := psql(run.seed.Env["POSTGRES_PASSWORD"], "CREATE TABLE kept (x int); INSERT INTO kept VALUES (42)")
	require.NoError(t, err)
	out, err := psql("wrong", "SELECT 1")
	assert.Error(t, err, "a wrong password is refused")
	assert.Contains(t, out, "password authentication failed")

	run.redeploy()
	out, err = psql(run.seed.Env["POSTGRES_PASSWORD"], "SELECT x FROM kept")
	require.NoError(t, err)
	assert.Equal(t, "42", strings.TrimSpace(out), "the table survived the redeploy")
}

// The pooler signs in to PostgreSQL as the app's user, shares at most 50
// server connections in transaction mode, and keeps serving across a
// database redeploy (ADR 0035). It's deployed as services.PoolerSpec
// builds it.
func TestPostgresPooler(t *testing.T) {
	run := deployTemplate(t, "postgres", false)
	tmpl, _ := Find("postgres")
	pooler := tmpl.Pooler
	image, err := run.engine.PullImage(run.ctx, pooler.Image, nil)
	require.NoError(t, err)
	_, digest, _ := strings.Cut(image, "@")
	assert.True(t, strings.HasSuffix(pooler.Image, "@"+digest), "the pull resolved to the pinned digest: %s", image)

	env, err := pooler.EnvFrom(run.spec.Name, run.seed.Env)
	require.NoError(t, err)
	pool := &templateRun{t: t, ctx: run.ctx, engine: run.engine, seed: run.seed, spec: docker.ServiceSpec{
		Name:        run.spec.Name + "-pooler",
		App:         run.spec.App,
		Role:        docker.RolePooler,
		Image:       image,
		Env:         env,
		Replicas:    1,
		Entrypoint:  pooler.Command,
		Healthcheck: &docker.Healthcheck{Command: pooler.Healthcheck},
		Limits:      pooler.Limits,
	}}
	t.Cleanup(func() { _ = run.engine.RemoveService(context.Background(), pool.spec.Name) })
	pool.deploy()

	user, password := run.seed.Env["POSTGRES_USER"], run.seed.Env["POSTGRES_PASSWORD"]
	port := strconv.Itoa(pooler.Port)
	psql := func(password, sql string) (string, error) {
		return run.client("env", "PGPASSWORD="+password, "psql", "-h", pool.spec.Name, "-p", port, "-U", user, "-d", user, "-tAc", sql)
	}
	out, err := psql(password, "SELECT current_user")
	require.NoError(t, err, out)
	assert.Equal(t, user, strings.TrimSpace(out), "signed in through the pooler")
	out, err = psql("wrong", "SELECT 1")
	assert.Error(t, err, "a wrong password is refused")
	assert.Contains(t, out, "authentication failed")

	config, err := exec.Command("docker", "inspect", "-f", "{{json .Config.Cmd}}", pool.container()).Output()
	require.NoError(t, err)
	assert.Contains(t, []string{"null", "[]"}, strings.TrimSpace(string(config)), "Docker dropped the image's CMD")
	cmdline, err := pool.exec("cat", "/proc/1/cmdline")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(cmdline, "pgbouncer"), "sh execs into PgBouncer: %q", cmdline)
	assert.NotContains(t, cmdline, password)
	environ, err := pool.exec("cat", "/proc/1/environ")
	require.NoError(t, err)
	assert.NotContains(t, environ, password, "the script unsets it before PgBouncer starts")
	status, err := pool.exec("cat", "/proc/1/status")
	require.NoError(t, err)
	assert.NotRegexp(t, `(?m)^Uid:\s+0\s`, status, "the image's user, not root")

	poolers, err := run.engine.ServicesWithRole(run.ctx, docker.RolePooler)
	require.NoError(t, err)
	assert.Contains(t, poolers, docker.ServiceRef{Name: pool.spec.Name, App: run.spec.App})
	assert.NotContains(t, poolers, docker.ServiceRef{Name: run.spec.Name, App: run.spec.App}, "the database has no role")

	// Named prepared statements from 60 clients on each of two databases,
	// which PgBouncer pools apart, so 120 clients share the 50 server
	// connections. A thread per client, since pgbench's prepare blocks its
	// thread (ADR 0035).
	databases := []string{user, "postgres"}
	for _, db := range databases {
		out, err := run.client("env", "PGPASSWORD="+password, "pgbench", "-q", "-i", "-s", "1", "-h", run.spec.Name, "-U", user, db)
		require.NoError(t, err, out)
	}
	results := make([]string, len(databases))
	errs := make([]error, len(databases))
	done := make(chan int)
	for i, db := range databases {
		go func() {
			results[i], errs[i] = run.client("env", "PGPASSWORD="+password, "timeout", "90", "pgbench",
				"-M", "prepared", "-c", "60", "-j", "60", "-T", "5", "-h", pool.spec.Name, "-p", port, "-U", user, db)
			done <- i
		}()
	}
	for range databases {
		i := <-done
		require.NoError(t, errs[i], results[i])
		assert.Contains(t, results[i], "number of failed transactions: 0 ", results[i])
		assert.NotContains(t, results[i], "aborted", results[i])
	}
	out, err = run.client("env", "PGPASSWORD="+password, "psql", "-h", run.spec.Name, "-U", user, "-d", user, "-tAc",
		"SELECT count(*) FROM pg_stat_activity WHERE usename = current_user AND backend_type = 'client backend' AND pid <> pg_backend_pid()")
	require.NoError(t, err, out)
	servers, err := strconv.Atoi(strings.TrimSpace(out))
	require.NoError(t, err, out)
	t.Logf("server connections the pooler kept open: %d", servers)
	assert.LessOrEqual(t, servers, 50, "the pooler's server connections, over both databases")
	assert.GreaterOrEqual(t, servers, 25, "the load went through the pool")

	logs, err := exec.Command("docker", "service", "logs", "--raw", pool.spec.Name).CombinedOutput()
	require.NoError(t, err)
	assert.NotContains(t, string(logs), "login attempt", "connections aren't logged")

	// An app keeps querying through the pooler while the database
	// redeploys. Between its tasks Docker's DNS doesn't know the
	// database's name, so the pooler's logins fail, and on PgBouncer's
	// defaults it then refuses everyone for up to 15 s after PostgreSQL is
	// back (server_login_retry and dns_nxdomain_ttl).
	type probe struct {
		at         time.Time
		direct, ok bool
	}
	var (
		mu     sync.Mutex
		probes []probe
	)
	before := pool.container()
	query := func(direct bool) {
		host, p := "127.0.0.1", port
		if direct {
			host, p = run.spec.Name, "5432"
		}
		out, err := exec.Command("docker", "exec", "-e", "PGPASSWORD="+password, "-e", "PGCONNECT_TIMEOUT=2", before,
			"psql", "-h", host, "-p", p, "-U", user, "-d", user, "-tAc", "SELECT 1").CombinedOutput()
		mu.Lock()
		defer mu.Unlock()
		probes = append(probes, probe{time.Now(), direct, err == nil && strings.TrimSpace(string(out)) == "1"})
	}
	querying, stop := context.WithCancel(run.ctx)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for querying.Err() == nil {
				query(i == 0)
			}
		})
	}
	run.redeploy()
	time.Sleep(20 * time.Second)
	stop()
	wg.Wait()
	slices.SortFunc(probes, func(a, b probe) int { return a.at.Compare(b.at) })
	var down, back, lastFailed time.Time
	for _, p := range probes {
		switch {
		case p.direct && !p.ok && down.IsZero():
			down = p.at
		case p.direct && p.ok && !down.IsZero() && back.IsZero():
			back = p.at
		case !p.direct && !p.ok:
			lastFailed = p.at
		}
	}
	require.False(t, back.IsZero(), "PostgreSQL went away and came back during the redeploy")
	t.Logf("PostgreSQL was unreachable for %s, and the pooler failed until %s after it was back",
		back.Sub(down).Round(100*time.Millisecond), lastFailed.Sub(back).Round(100*time.Millisecond))
	assert.Less(t, lastFailed.Sub(back), 3*time.Second, "the pooler signs in again as soon as PostgreSQL answers")
	out, err = psql(password, "SELECT count(*) FROM pgbench_branches")
	require.NoError(t, err, "a query after the database's redeploy: %s", out)
	assert.Equal(t, "1", strings.TrimSpace(out))
	assert.Equal(t, before, pool.container(), "Swarm didn't replace the pooler's task")

	// With no PostgreSQL at all, the pooler is still healthy, so Swarm
	// never restarts it for the database's sake.
	out2, err := exec.Command("docker", "service", "scale", "--detach=false", run.spec.Name+"=0").CombinedOutput()
	require.NoError(t, err, string(out2))
	// scale returns while the task is still stopping.
	require.Eventually(t, func() bool {
		ids, err := exec.Command("docker", "ps", "-q", "--filter", "label=com.docker.swarm.service.name="+run.spec.Name).Output()
		return err == nil && strings.TrimSpace(string(ids)) == ""
	}, time.Minute, 200*time.Millisecond, "the database's container stopped")
	out, err = pool.exec("sh", "-c", pooler.Healthcheck)
	assert.NoError(t, err, "the healthcheck passes without the database: %s", out)
}

func TestMySQLTemplate(t *testing.T) {
	run := deployTemplate(t, "mysql", false)
	env := run.seed.Env
	mysql := func(user, password, sql string) (string, error) {
		return run.client("env", "MYSQL_PWD="+password, "mysql", "-h", run.spec.Name, "-u", user, "-NBe", sql, env["MYSQL_DATABASE"])
	}

	_, err := mysql(env["MYSQL_USER"], env["MYSQL_PASSWORD"], "CREATE TABLE kept (x int); INSERT INTO kept VALUES (42)")
	require.NoError(t, err)
	out, err := mysql(env["MYSQL_USER"], "wrong", "SELECT 1")
	assert.Error(t, err, "a wrong password is refused")
	assert.Contains(t, out, "Access denied")
	out, err = mysql("root", env["MYSQL_ROOT_PASSWORD"], "SELECT COUNT(*) FROM kept")
	require.NoError(t, err, "root signs in with its generated password: %s", out)

	run.redeploy()
	out, err = mysql(env["MYSQL_USER"], env["MYSQL_PASSWORD"], "SELECT x FROM kept")
	require.NoError(t, err)
	assert.Equal(t, "42", strings.TrimSpace(out), "the table survived the redeploy")
}

func TestRedisTemplate(t *testing.T) {
	run := deployTemplate(t, "redis", false)
	cli := func(args ...string) (string, error) {
		return run.client(append([]string{"redis-cli", "-h", run.spec.Name, "-a", run.seed.Env["REDIS_PASSWORD"], "--no-auth-warning"}, args...)...)
	}

	out, _ := run.client("redis-cli", "-h", run.spec.Name, "ping")
	assert.Contains(t, out, "NOAUTH", "commands need the password")
	out, err := cli("SET", "kept", "yes")
	require.NoError(t, err)
	assert.Equal(t, "OK", strings.TrimSpace(out))

	cmdline, err := run.exec("cat", "/proc/1/cmdline")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(cmdline, "redis-server"), "sh and the entrypoint exec into the server: %q", cmdline)
	status, err := run.exec("cat", "/proc/1/status")
	require.NoError(t, err)
	assert.NotRegexp(t, `(?m)^Uid:\s+0\s`, status, "the entrypoint still dropped root")
	assert.NotContains(t, cmdline, "$REDIS_PASSWORD", "sh expanded the password")

	run.redeploy()
	out, err = cli("GET", "kept")
	require.NoError(t, err)
	assert.Equal(t, "yes", strings.TrimSpace(out), "the key survived the redeploy")
}

// With external access on, each template serves TLS with the app's
// certificate. Clients verify it against the app's CA, and fail against
// another CA, so the server isn't using a certificate of its own.
func TestTemplatesServeTLSWithTheAppsCertificate(t *testing.T) {
	type check struct {
		slug string
		// connect runs a client that verifies the server with ca, and
		// returns what it printed.
		connect func(run *templateRun, ca string) (string, error)
		want    string
	}
	withCA := func(run *templateRun, ca, command string) (string, error) {
		return run.client("sh", "-c", `printf '%s\n' "$1" > /tmp/ca.crt; export SECRET="$2"; `+command, "sh", ca, run.password())
	}
	for _, c := range []check{
		{"postgres", func(run *templateRun, ca string) (string, error) {
			user := run.seed.Env["POSTGRES_USER"]
			return withCA(run, ca, fmt.Sprintf(`PGPASSWORD="$SECRET" psql "host=%s user=%s dbname=%s sslmode=verify-full sslrootcert=/tmp/ca.crt" -tAc "select ssl from pg_stat_ssl where pid = pg_backend_pid()"`, run.spec.Name, user, user))
		}, "t"},
		{"mysql", func(run *templateRun, ca string) (string, error) {
			return withCA(run, ca, fmt.Sprintf(`MYSQL_PWD="$SECRET" mysql --ssl-mode=VERIFY_IDENTITY --ssl-ca=/tmp/ca.crt -h %s -u app -NBe "SELECT VARIABLE_VALUE <> '' FROM performance_schema.session_status WHERE VARIABLE_NAME = 'Ssl_cipher'"`, run.spec.Name))
		}, "1"},
		{"redis", func(run *templateRun, ca string) (string, error) {
			return withCA(run, ca, fmt.Sprintf(`redis-cli -h %s -p 6380 --tls --cacert /tmp/ca.crt -a "$SECRET" --no-auth-warning ping`, run.spec.Name))
		}, "PONG"},
	} {
		t.Run(c.slug, func(t *testing.T) {
			run := deployTemplate(t, c.slug, true)
			out, err := c.connect(run, run.seed.Env[EnvTLSCA])
			require.NoError(t, err, out)
			assert.Equal(t, c.want, strings.TrimSpace(out), "connected over verified TLS")
			out, err = run.exec("stat", "-c", "%a %U", tlsDir+"/server.key")
			require.NoError(t, err, out)
			assert.Equal(t, "600 "+c.slug, strings.TrimSpace(out), "only the server's user reads the key")

			other, err := IssueTLS("other", time.Now())
			require.NoError(t, err)
			out, err = c.connect(run, other[EnvTLSCA])
			assert.Error(t, err, "another CA doesn't verify the server: %s", out)

			if c.slug == "redis" {
				out, _ := run.client("redis-cli", "-h", run.spec.Name, "-p", "6380", "-a", run.password(), "--no-auth-warning", "ping")
				assert.NotContains(t, out, "PONG", "the TLS port refuses plain connections")
				out, err = run.client("redis-cli", "-h", run.spec.Name, "-a", run.password(), "--no-auth-warning", "ping")
				require.NoError(t, err)
				assert.Equal(t, "PONG", strings.TrimSpace(out), "apps on the server keep the plain port")
			}
		})
	}
}

type templateRun struct {
	t      *testing.T
	ctx    context.Context
	engine *docker.Engine
	spec   docker.ServiceSpec
	seed   Seed
}

// deployTemplate deploys the template as an app would be, minus the
// database: the template's image and healthcheck, and a fresh seed. With
// tls, it deploys as external access does: the app's certificate in the
// env and the template's TLS arguments.
func deployTemplate(t *testing.T, slug string, tls bool) *templateRun {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	cfg := docker.DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = 2 * time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := docker.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	if out, err := exec.Command("docker", "network", "inspect", cfg.Network).CombinedOutput(); err != nil {
		t.Skipf("needs the %s network (deploy/dev-vm.sh up): %s", cfg.Network, out)
	}

	tmpl, ok := Find(slug)
	require.True(t, ok)
	app := fmt.Sprintf("it-%s-%d", slug, time.Now().UnixNano()%1_000_000)
	seed, err := tmpl.Seed(app)
	require.NoError(t, err)
	image, err := engine.PullImage(ctx, tmpl.Repository+":"+tmpl.DefaultTag, nil)
	require.NoError(t, err)

	spec := docker.ServiceSpec{
		Name:        "echowa-" + app,
		App:         app,
		Image:       image,
		Env:         seed.Env,
		Replicas:    1,
		Args:        seed.Args,
		Healthcheck: &docker.Healthcheck{Command: tmpl.Healthcheck},
	}
	for _, v := range seed.Volumes {
		spec.Volumes = append(spec.Volumes, docker.Volume{Name: v.Name, Target: v.Target})
	}
	if tls {
		certs, err := IssueTLS(app, time.Now())
		require.NoError(t, err)
		maps.Copy(seed.Env, certs)
		spec.Args = tmpl.Args(Options{TLS: true})
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = engine.RemoveService(ctx, spec.Name)
		for _, v := range spec.Volumes {
			deadline := time.Now().Add(time.Minute)
			for errors.Is(engine.RemoveVolume(ctx, v.Name), docker.ErrVolumeInUse) && time.Now().Before(deadline) {
				time.Sleep(time.Second)
			}
		}
	})

	run := &templateRun{t: t, ctx: ctx, engine: engine, spec: spec, seed: seed}
	run.deploy()

	return run
}

// deploy waits for Swarm to report the update done, which with a
// healthcheck means the new task passed it.
func (r *templateRun) deploy() {
	r.t.Helper()
	require.NoError(r.t, r.engine.DeployService(r.ctx, r.spec))
	result, err := r.engine.WaitForUpdate(r.ctx, r.spec.Name)
	require.NoError(r.t, err)
	require.Equal(r.t, docker.OutcomeCompleted, result.Outcome, "message: %s", result.Message)
}

// redeploy rolls the task. The old container is gone before the next exec,
// since stop-first only starts the new task after the old one stops.
func (r *templateRun) redeploy() {
	r.t.Helper()
	before := r.container()
	r.deploy()
	require.NotEqual(r.t, before, r.container(), "the redeploy started a new task")
}

func (r *templateRun) container() string {
	r.t.Helper()
	var id string
	require.Eventually(r.t, func() bool {
		out, err := exec.Command("docker", "ps", "-q",
			"--filter", "label=com.docker.swarm.service.name="+r.spec.Name,
			"--filter", "health=healthy").Output()
		ids := strings.Fields(string(out))
		if err != nil || len(ids) != 1 {
			return false
		}
		id = ids[0]
		return true
	}, 30*time.Second, 500*time.Millisecond, "one healthy task of %s", r.spec.Name)

	return id
}

// password is the seed's generated password for the template's user.
func (r *templateRun) password() string {
	for _, key := range []string{"POSTGRES_PASSWORD", "MYSQL_PASSWORD", "REDIS_PASSWORD"} {
		if p, ok := r.seed.Env[key]; ok {
			return p
		}
	}
	r.t.Fatal("the seed has no password")

	return ""
}

// client runs a command in a throwaway container of the same image on the
// overlay network, where other apps reach the service by its name.
func (r *templateRun) client(command ...string) (string, error) {
	args := append([]string{"run", "--rm", "--network", docker.DefaultConfig().Network, "--entrypoint", command[0], r.spec.Image}, command[1:]...)
	out, err := exec.Command("docker", args...).CombinedOutput()

	return string(out), err
}

// exec runs a command in the service's task and returns its combined
// output, so a failure's message can be checked too.
func (r *templateRun) exec(command ...string) (string, error) {
	out, err := exec.Command("docker", append([]string{"exec", r.container()}, command...)...).CombinedOutput()

	return string(out), err
}

func TestTemplatesReportTheirHealth(t *testing.T) {
	for _, tmpl := range All() {
		if tmpl.Inspect == nil {
			continue
		}
		t.Run(tmpl.Slug, func(t *testing.T) {
			run := deployTemplate(t, tmpl.Slug, false)
			start := time.Now()
			out, err := run.engine.Exec(run.ctx, run.spec.Name, tmpl.Inspect.Health)
			require.NoError(t, err)
			t.Logf("health check took %s: %s", time.Since(start), out)
			status, err := tmpl.Inspect.ParseHealth(out)
			require.NoError(t, err)

			require.NotNil(t, status.SizeBytes)
			assert.Positive(t, *status.SizeBytes)
			require.NotNil(t, status.MaxConnections)
			assert.Positive(t, *status.MaxConnections)
			require.NotNil(t, status.Connections)
			assert.Less(t, *status.Connections, *status.MaxConnections)
			assert.NotNil(t, status.CacheHits)
			assert.NotNil(t, status.CacheMisses)
		})
	}
}

// slowQueryProbes run a query the slow query list should then show as
// want, without its literal 4242. own is in Echowa's own commands, which
// it shouldn't show.
var slowQueryProbes = map[string]struct {
	run       func(r *templateRun) (string, error)
	want, own string
}{
	"postgres": {
		run: func(r *templateRun) (string, error) {
			return r.exec("sh", "-c", `psql -X -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT 4242 AS echowa_probe"`)
		},
		want: "echowa_probe", own: "pg_stat_activity",
	},
	// Over the network, as an app connects: the container's own
	// connections aren't recorded.
	"mysql": {
		run: func(r *templateRun) (string, error) {
			return r.client("sh", "-c", fmt.Sprintf(`MYSQL_PWD='%s' mysql -h %s -u %s %s -e "SELECT 4242 AS echowa_probe"`,
				r.password(), r.spec.Name, r.seed.Env["MYSQL_USER"], r.seed.Env["MYSQL_DATABASE"]))
		},
		want: "echowa_probe", own: "performance_schema",
	},
	"redis": {
		run: func(r *templateRun) (string, error) {
			return r.exec("sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli SET echowa_probe 4242`)
		},
		want: "SET", own: "INFO",
	},
}

func TestTemplatesListTheirSlowQueries(t *testing.T) {
	for _, tmpl := range All() {
		if tmpl.Inspect == nil || tmpl.Inspect.SlowQueries == nil {
			continue
		}
		t.Run(tmpl.Slug, func(t *testing.T) {
			run := deployTemplate(t, tmpl.Slug, false)
			read := func() SlowQueryReport {
				t.Helper()
				start := time.Now()
				out, err := run.engine.Exec(run.ctx, run.spec.Name, tmpl.Inspect.SlowQueries)
				require.NoError(t, err)
				t.Logf("reading the slow queries took %s", time.Since(start))
				report, err := tmpl.Inspect.ParseSlowQueries(out)
				require.NoError(t, err)
				return report
			}

			if tmpl.Inspect.SlowQueryFlags != "" {
				_, err := run.engine.Exec(run.ctx, run.spec.Name, tmpl.Inspect.SlowQueries)
				var exit *docker.ExecError
				require.ErrorAs(t, err, &exit, "nothing to read until switched on")
				t.Logf("switched off: %s", exit.Stderr)
				run.spec.Args = tmpl.Args(Options{SlowQueries: true})
				run.redeploy()
			}
			_, err := run.engine.Exec(run.ctx, run.spec.Name, tmpl.Inspect.Health)
			require.NoError(t, err, "the health check works with the list on")
			probe, ok := slowQueryProbes[tmpl.Slug]
			require.True(t, ok, "add a probe for %s", tmpl.Slug)
			out, err := probe.run(run)
			require.NoError(t, err, out)

			report := read()
			var queries []string
			for _, q := range report.Queries {
				queries = append(queries, q.Query)
			}
			joined := strings.Join(queries, "\n")
			t.Logf("listed:\n%s", joined)
			assert.Contains(t, joined, probe.want)
			assert.NotContains(t, joined, "4242", "values are replaced by placeholders")
			assert.NotContains(t, joined, probe.own, "Echowa's own checks aren't listed")

			_, err = run.engine.Exec(run.ctx, run.spec.Name, tmpl.Inspect.ResetSlowQueries)
			require.NoError(t, err)
			report = read()
			for _, q := range report.Queries {
				assert.NotContains(t, q.Query, probe.want, "reset clears the totals")
			}
			if report.Since != nil {
				assert.WithinDuration(t, time.Now(), *report.Since, time.Minute, "the totals start at the reset")
			}
		})
	}
}

// backupProbes make and read the data a backup test restores: kept has
// UTF-8 text, binary data and enough rows that a cut-off dump ends inside
// a statement, and kept_count is a view.
var backupProbes = map[string]struct {
	sql    func(r *templateRun, statements string) (string, error)
	create string
	// binary prints row 1's data as hex.
	binary string
	// load reads an export that isn't the backup's own dump on stdin, as
	// someone loading it elsewhere would.
	load []string
}{
	"postgres": {
		sql: func(r *templateRun, statements string) (string, error) {
			return r.exec("sh", "-c", `psql -X -q -A -t -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"`, "sh", statements)
		},
		create: `CREATE TABLE kept (id int PRIMARY KEY, note text NOT NULL, data bytea);
			INSERT INTO kept VALUES (1, 'café', '\x00ff'), (2, 'naïve ☃', NULL);
			INSERT INTO kept SELECT n, encode(sha256(n::text::bytea), 'hex'), sha256(n::text::bytea) FROM generate_series(3, 5000) n;
			CREATE VIEW kept_count AS SELECT COUNT(*) AS n FROM kept`,
		binary: "SELECT encode(data, 'hex') FROM kept WHERE id = 1",
		load:   []string{"sh", "-c", `exec psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`},
	},
	"mysql": {
		sql: func(r *templateRun, statements string) (string, error) {
			return r.exec("sh", "-c", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -u root -N -B -e "$1" "$MYSQL_DATABASE"`, "sh", statements)
		},
		create: `CREATE TABLE kept (id int PRIMARY KEY, note text NOT NULL, data varbinary(32));
			INSERT INTO kept VALUES (1, 'café', 0x00ff), (2, 'naïve ☃', NULL);
			SET SESSION cte_max_recursion_depth = 10000;
			INSERT INTO kept WITH RECURSIVE s (n) AS (SELECT 3 UNION ALL SELECT n + 1 FROM s WHERE n < 5000)
				SELECT n, SHA2(n, 256), UNHEX(SHA2(n, 256)) FROM s;
			CREATE VIEW kept_count AS SELECT COUNT(*) AS n FROM kept`,
		binary: "SELECT HEX(data) FROM kept WHERE id = 1",
	},
}

func TestTemplatesBackUpAndRestore(t *testing.T) {
	for _, tmpl := range All() {
		if tmpl.Backup == nil {
			continue
		}
		t.Run(tmpl.Slug, func(t *testing.T) {
			b := tmpl.Backup
			probe, ok := backupProbes[tmpl.Slug]
			require.True(t, ok, "add a probe for %s", tmpl.Slug)
			sql := func(r *templateRun, statements string) string {
				t.Helper()
				out, err := probe.sql(r, statements)
				require.NoError(t, err, out)
				return strings.TrimSpace(out)
			}
			assertKept := func(r *templateRun, msg string) {
				t.Helper()
				assert.Equal(t, "café\nnaïve ☃", sql(r, "SELECT note FROM kept WHERE id <= 2 ORDER BY id"), msg)
				assert.Equal(t, "5000", sql(r, "SELECT n FROM kept_count"), msg)
				assert.Equal(t, "00ff", strings.ToLower(sql(r, probe.binary)), msg)
			}

			// Reading the slow queries leaves pg_stat_statements in the
			// database, so a real dump carries it, and the target has it.
			inspected := func(r *templateRun) {
				t.Helper()
				if tmpl.Inspect != nil && tmpl.Inspect.SlowQueryFlags == "" {
					_, err := r.engine.Exec(r.ctx, r.spec.Name, tmpl.Inspect.SlowQueries)
					require.NoError(t, err)
				}
			}

			source := deployTemplate(t, tmpl.Slug, false)
			sql(source, probe.create)
			inspected(source)
			out, err := source.engine.Exec(source.ctx, source.spec.Name, b.Version)
			require.NoError(t, err)
			version, err := ServerVersion(out)
			require.NoError(t, err)
			t.Logf("server version %s (%s)", version, strings.TrimSpace(string(out)))
			assert.True(t, b.Compatible(version, version))

			start := time.Now()
			dump, code, stderr := source.stream(b.Dump, nil)
			require.Equal(t, 0, code, stderr)
			t.Logf("dumped %d bytes in %s", len(dump), time.Since(start))
			assert.Empty(t, stderr, "a clean dump writes nothing to stderr")

			target := deployTemplate(t, tmpl.Slug, false)
			inspected(target)
			_, code, stderr = target.stream(b.Restore, bytes.NewReader(dump))
			require.Equal(t, 0, code, stderr)
			assertKept(target, "restored into a fresh app, with its own database and passwords")

			sql(source, "UPDATE kept SET note = 'changed' WHERE id = 1; DELETE FROM kept WHERE id = 2")
			_, code, stderr = source.stream(b.Restore, bytes.NewReader(dump))
			require.Equal(t, 0, code, stderr)
			assertKept(source, "restored over changed data")

			for _, export := range b.Exports {
				if slices.Equal(export.Dump, b.Dump) {
					continue
				}
				require.NotNil(t, probe.load, "add a load for %s's %s export", tmpl.Slug, export.Format)
				out, code, stderr := source.stream(export.Dump, nil)
				require.Equal(t, 0, code, stderr)
				assert.NotContains(t, string(out), "OWNER TO", "the %s export names no owners", export.Format)
				elsewhere := deployTemplate(t, tmpl.Slug, false)
				inspected(elsewhere)
				_, code, stderr = elsewhere.stream(probe.load, bytes.NewReader(out))
				require.Equal(t, 0, code, stderr)
				assertKept(elsewhere, "the "+export.Format+" export loaded into a fresh app, with its own database and user")
			}

			_, code, stderr = target.stream(b.Restore, strings.NewReader("this is not a dump\n"))
			assert.NotEqual(t, 0, code, "a dump that isn't one fails")
			t.Logf("not a dump: %s", stderr)

			sql(target, "INSERT INTO kept (id, note) VALUES (9999, 'after')")
			_, code, stderr = target.stream(b.Restore, bytes.NewReader(dump[:len(dump)/2]))
			assert.NotEqual(t, 0, code, "a restore that fails half way exits non-zero")
			t.Logf("cut off: %s", stderr)
			if b.Atomic {
				assert.Equal(t, "5001", sql(target, "SELECT COUNT(*) FROM kept"), "the failed restore rolled back")
				assert.Equal(t, "after", sql(target, "SELECT note FROM kept WHERE id = 9999"))
			}

			out, err = exec.Command("docker", "service", "scale", "--detach", source.spec.Name+"=0").CombinedOutput()
			require.NoError(t, err, string(out))
			assert.Eventually(t, func() bool {
				_, err := source.engine.ExecStream(source.ctx, source.spec.Name, b.Dump, nil, io.Discard, io.Discard)
				return errors.Is(err, docker.ErrNotRunning)
			}, time.Minute, time.Second, "a dump of a stopped service fails with ErrNotRunning")
		})
	}
}

// stream runs cmd in the service's task through ExecStream, as a backup
// or restore does, and returns its stdout, exit code and stderr.
func (r *templateRun) stream(cmd []string, stdin io.Reader) ([]byte, int, string) {
	r.t.Helper()
	var out bytes.Buffer
	stderr := &docker.TailBuffer{Limit: 16 << 10}
	code, err := r.engine.ExecStream(r.ctx, r.spec.Name, cmd, stdin, &out, stderr)
	require.NoError(r.t, err)

	return out.Bytes(), code, strings.TrimSpace(stderr.String())
}
