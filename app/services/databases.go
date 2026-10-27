package services

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/templates"
)

// DatabaseCheckTimeout bounds each database's health command.
const DatabaseCheckTimeout = 5 * time.Second

// What the scheduled database check keeps in memory. Only CheckDatabases
// touches the rates and the errors, and the scheduler never runs two at
// once.
var (
	databaseRates   metrics.DatabaseRates
	latestDatabases atomic.Pointer[map[string]metrics.Database]
	databaseErrors  = map[string]string{}
)

// CheckDatabases runs the health command of every template app that has
// one, and stores the round as the minute's figures (ADR 0027). An app
// with no running task is skipped. The scheduler calls it every minute.
func CheckDatabases(ctx context.Context, now time.Time) {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().WhereNotNull("template").Get(&apps); err != nil {
		facades.Log().Error(fmt.Errorf("database check: %w", err))
		return
	}

	statuses := map[string]metrics.DatabaseStatus{}
	for _, app := range apps {
		tmpl, ok := templates.Find(*app.Template)
		if !ok || tmpl.Inspect == nil {
			continue
		}
		status, err := checkDatabase(ctx, &app, tmpl.Inspect)
		if errors.Is(err, docker.ErrNotRunning) {
			continue
		}
		logDatabaseError(app.Name, err)
		if err == nil {
			statuses[app.Name] = status
		}
	}

	dbs := databaseRates.Read(now, statuses)
	SetLatestDatabases(dbs)

	if err := storeSample(ctx, metrics.DatabaseSample(now, dbs)); err != nil {
		facades.Log().Error(fmt.Errorf("database check: storing: %w", err))
	}
}

func checkDatabase(ctx context.Context, app *models.App, inspect *templates.Inspect) (metrics.DatabaseStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, DatabaseCheckTimeout)
	defer cancel()
	out, err := Docker().Exec(ctx, app.ServiceName(), inspect.Health)
	if err != nil {
		return metrics.DatabaseStatus{}, err
	}

	return inspect.ParseHealth(out)
}

// logDatabaseError logs an app's error once, until it changes or the
// check succeeds: a database that's down would otherwise log every minute.
func logDatabaseError(app string, err error) {
	if err == nil {
		delete(databaseErrors, app)
		return
	}
	if message := err.Error(); databaseErrors[app] != message {
		facades.Log().Warning(fmt.Sprintf("database check: %s: %s", app, message))
		databaseErrors[app] = message
	}
}

// LatestDatabase is the app's figures from the newest check, or nil before
// the first and while its check fails or its task isn't running.
func LatestDatabase(app *models.App) *metrics.Database {
	latest := latestDatabases.Load()
	if latest == nil {
		return nil
	}
	if db, ok := (*latest)[app.Name]; ok {
		return &db
	}

	return nil
}

// SetLatestDatabases replaces the newest check's figures. Nil clears them.
func SetLatestDatabases(dbs []metrics.Database) {
	if dbs == nil {
		latestDatabases.Store(nil)
		return
	}
	byName := make(map[string]metrics.Database, len(dbs))
	for _, db := range dbs {
		byName[db.Name] = db
	}
	latestDatabases.Store(&byName)
}

// DatabaseSeries is everything an app's Database section charts, for one
// range. Mirrored by DatabaseSeries in resources/js/types/metrics.ts.
type DatabaseSeries struct {
	metrics.Window

	Size               []metrics.Point `json:"size"`
	Connections        []metrics.Point `json:"connections"`
	MaxConnections     []metrics.Point `json:"max_connections"`
	CacheHit           []metrics.Point `json:"cache_hit"`
	Commits            []metrics.Point `json:"commits"`
	Rollbacks          []metrics.Point `json:"rollbacks"`
	TempBytes          []metrics.Point `json:"temp_bytes"`
	Deadlocks          []metrics.Point `json:"deadlocks"`
	LongestTransaction []metrics.Point `json:"longest_transaction"`
	IdleInTransaction  []metrics.Point `json:"idle_in_transaction"`
	TransactionIDAge   []metrics.Point `json:"transaction_id_age"`
	EvictedKeys        []metrics.Point `json:"evicted_keys"`
}

// DatabaseMetrics returns an app's database series for the range ending
// at now. Every series is empty until its first minute is stored, or
// without a metrics file.
func DatabaseMetrics(ctx context.Context, app *models.App, r metrics.Range, now time.Time) (*DatabaseSeries, error) {
	series := &DatabaseSeries{Window: metrics.WindowFor(r, now)}
	fields := map[string]*[]metrics.Point{
		"size_bytes":                  &series.Size,
		"connections":                 &series.Connections,
		"max_connections":             &series.MaxConnections,
		"cache_hit_percent":           &series.CacheHit,
		"commits_per_sec":             &series.Commits,
		"rollbacks_per_sec":           &series.Rollbacks,
		"temp_bytes_per_sec":          &series.TempBytes,
		"deadlocks_per_sec":           &series.Deadlocks,
		"longest_transaction_seconds": &series.LongestTransaction,
		"idle_in_transaction":         &series.IdleInTransaction,
		"transaction_id_age":          &series.TransactionIDAge,
		"evicted_keys_per_sec":        &series.EvictedKeys,
	}
	for _, field := range fields {
		*field = []metrics.Point{}
	}

	db, err := MetricsDB()
	if err != nil {
		return series, nil // logged when it was opened
	}
	rows, err := metrics.Query(ctx, db, metrics.DatabaseTable, slices.Collect(maps.Keys(fields)), "app_id = ?", []any{app.ID}, r, now)
	if err != nil {
		return nil, err
	}
	key := strconv.FormatUint(uint64(app.ID), 10)
	for figure, field := range fields {
		*field = rows.Get(key, figure)
	}

	return series, nil
}

// DatabaseLive is an app's newest database figures, with what its meters
// measure them against. Mirrored by DatabaseLive in
// resources/js/types/metrics.ts.
type DatabaseLive struct {
	metrics.Database
	// Disk is the filesystem holding Docker's data, and so the app's
	// volume. Nil when it isn't known.
	Disk *metrics.Disk `json:"disk"`
	// MemoryUsedBytes is the app's tasks', from the newest reading. Nil
	// before one.
	MemoryUsedBytes *uint64 `json:"memory_used_bytes"`
	// The server's memory: the database has no limit of its own.
	HostMemoryTotalBytes     *uint64 `json:"host_memory_total_bytes"`
	HostMemoryAvailableBytes *uint64 `json:"host_memory_available_bytes"`
}

// LatestDatabaseLive is the app's newest database figures, or nil before
// its first check and while its check fails or its task isn't running.
func LatestDatabaseLive(ctx context.Context, app *models.App) *DatabaseLive {
	db := LatestDatabase(app)
	if db == nil {
		return nil
	}
	live := &DatabaseLive{Database: *db}
	reading := LatestMetrics()
	if reading == nil {
		return live
	}
	live.HostMemoryTotalBytes = reading.Host.MemoryTotal
	live.HostMemoryAvailableBytes = reading.Host.MemoryAvailable
	for _, a := range reading.Apps {
		if a.Name == app.Name {
			live.MemoryUsedBytes = &a.MemoryUsed
		}
	}
	if host, err := DockerHost(ctx); err == nil {
		live.Disk = diskHolding(reading.Disks, host.DataRoot)
	}

	return live
}

var dockerHost atomic.Pointer[docker.Host]

// DockerHost asks the daemon about its machine once, until it answers.
func DockerHost(ctx context.Context) (docker.Host, error) {
	if host := dockerHost.Load(); host != nil {
		return *host, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	host, err := Docker().Host(ctx)
	if err != nil {
		return docker.Host{}, err
	}
	dockerHost.Store(&host)

	return host, nil
}

// diskHolding is the filesystem whose mount is the longest one containing
// path, or nil if none does.
func diskHolding(disks []metrics.Disk, path string) *metrics.Disk {
	var best *metrics.Disk
	for i, d := range disks {
		within := d.Mount == "/" || path == d.Mount || strings.HasPrefix(path, strings.TrimSuffix(d.Mount, "/")+"/")
		if within && (best == nil || len(d.Mount) > len(best.Mount)) {
			best = &disks[i]
		}
	}

	return best
}

// SlowQueryTimeout bounds reading or resetting an app's slow queries,
// which a request waits for.
const SlowQueryTimeout = 2 * time.Second

// SlowQueryResult is an app's slow queries, or why they couldn't be read.
// Mirrored by SlowQueryResult in resources/js/types/models.ts.
type SlowQueryResult struct {
	Report  *templates.SlowQueryReport `json:"report"`
	Problem string                     `json:"problem"`
}

// SlowQueries reads the queries that took the app's database the most
// time in total. Nil when it can't list them, or its switch is off.
func SlowQueries(ctx context.Context, app *models.App) *SlowQueryResult {
	tmpl, ok := slowQueryTemplate(app)
	if !ok {
		return nil
	}
	out, err := slowQueryExec(ctx, app, tmpl.Inspect.SlowQueries)
	if err != nil {
		return &SlowQueryResult{Problem: slowQueryProblem(app, err)}
	}
	report, err := tmpl.Inspect.ParseSlowQueries(out)
	if err != nil {
		facades.Log().Error(fmt.Errorf("%s: %w", app.Name, err))
		return &SlowQueryResult{Problem: "Echowa couldn't read what the database sent back."}
	}

	return &SlowQueryResult{Report: &report}
}

// ResetSlowQueries clears the totals the slow queries are ranked by. The
// error is a sentence for the person who asked.
func ResetSlowQueries(ctx context.Context, app *models.App) error {
	tmpl, ok := slowQueryTemplate(app)
	if !ok {
		return errors.New("This database doesn't list its slow queries.")
	}
	if _, err := slowQueryExec(ctx, app, tmpl.Inspect.ResetSlowQueries); err != nil {
		return errors.New(slowQueryProblem(app, err))
	}

	return nil
}

// SetSlowQueries stores the app's switch. It takes effect on the next
// deploy.
func SetSlowQueries(ctx context.Context, app *models.App, on bool) error {
	if _, err := facades.Orm().WithContext(ctx).Query().Model(app).Update("slow_queries", on); err != nil {
		return err
	}
	app.SlowQueries = on

	return nil
}

func slowQueryTemplate(app *models.App) (templates.Template, bool) {
	if app.Template == nil {
		return templates.Template{}, false
	}
	tmpl, ok := templates.Find(*app.Template)

	return tmpl, ok && tmpl.HasSlowQueries(app.SlowQueries)
}

func slowQueryExec(ctx context.Context, app *models.App, cmd []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, SlowQueryTimeout)
	defer cancel()

	return Docker().Exec(ctx, app.ServiceName(), cmd)
}

// slowQueryProblem says why reading the slow queries failed. An error the
// database printed is shown as it is: it's about Echowa's own query.
func slowQueryProblem(app *models.App, err error) string {
	var exit *docker.ExecError
	switch {
	case errors.Is(err, docker.ErrNotRunning):
		return app.Name + " isn't running."
	case errors.Is(err, context.DeadlineExceeded):
		return "The database took too long to answer."
	case errors.As(err, &exit):
		line, _, _ := strings.Cut(strings.TrimSpace(exit.Stderr), "\n")
		return "The database answered: " + line
	}
	facades.Log().Error(fmt.Errorf("%s: slow queries: %w", app.Name, err))

	return "Echowa couldn't reach the database."
}
