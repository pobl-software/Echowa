//go:build docker

// Runs each template on a real Swarm manager, the way a deploy would, and
// checks what its users rely on: the healthcheck passes, the password is
// required, and data survives a redeploy. Run it in the dev machine with
// `deploy/dev-vm.sh test-docker`, or on the Mac with
//
//	go test -tags docker ./app/templates/...
//
// It pulls postgres:18, mysql:9.7 and redis:8, and needs the docker CLI to
// exec into the tasks.
package templates

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"strings"
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
