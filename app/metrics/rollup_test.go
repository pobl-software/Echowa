package metrics

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hour is a whole hour two hours before now in these tests.
var (
	hour = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	now  = hour.Add(2*time.Hour + 10*time.Minute)
)

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	require.NoError(t, err)
}

// rows returns a query's rows as maps, for comparing whole tables.
func rows(t *testing.T, db *sql.DB, query string, args ...any) []map[string]any {
	t.Helper()
	r, err := db.Query(query, args...)
	require.NoError(t, err)
	defer r.Close()
	columns, err := r.Columns()
	require.NoError(t, err)
	var out []map[string]any
	for r.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		require.NoError(t, r.Scan(dest...))
		row := map[string]any{}
		for i, c := range columns {
			row[c] = values[i]
		}
		out = append(out, row)
	}
	require.NoError(t, r.Err())

	return out
}

func one(t *testing.T, db *sql.DB, query string, args ...any) map[string]any {
	t.Helper()
	all := rows(t, db, query, args...)
	require.Len(t, all, 1, query)

	return all[0]
}

func TestHourlyRollupsMatchHandCalculatedValues(t *testing.T) {
	db, _ := openTemp(t)
	at := func(d time.Duration) int64 { return hour.Add(d).Unix() }
	exec(t, db, `INSERT INTO host_samples (ts, readings, cpu_percent_avg, cpu_percent_max, load_1m_avg, load_1m_max) VALUES
		(?, 4, 10, 20, 1, 1), (?, 2, 40, 90, 2, 2), (?, 4, NULL, NULL, 3, 3), (?, 4, 99, 99, 4, 4)`,
		at(0), at(15*time.Minute), at(59*time.Minute), at(time.Hour))
	exec(t, db, `INSERT INTO disk_samples (mount, ts, readings, used_bytes_avg, used_bytes_max, available_bytes_avg, available_bytes_max, available_bytes_min)
		VALUES ('/', ?, 4, 20, 25, 80, 85, 75), ('/', ?, 4, 40, 45, 60, 65, 55)`, at(0), at(time.Minute))
	exec(t, db, `INSERT INTO app_samples (app_id, ts, readings, tasks_avg, tasks_max, cpu_percent_avg, cpu_percent_max)
		VALUES (7, ?, 4, 1, 1, 150, 200), (7, ?, 4, 1.5, 2, 50, 60), (8, ?, 4, 1, 1, 1, 1)`, at(0), at(time.Minute), at(0))

	result, err := Rollup(context.Background(), db, Hourly, hour, hour.Add(2*time.Hour), now)
	require.NoError(t, err)
	assert.Equal(t, hour, result.From)
	assert.Equal(t, hour.Add(2*time.Hour), result.To)

	host := one(t, db, `SELECT * FROM host_rollups WHERE resolution = '1h' AND bucket = ?`, at(0))
	assert.EqualValues(t, 10, host["readings"], "the next hour's row isn't in it")
	assert.InDelta(t, 20.0, host["cpu_percent_avg"], 1e-9, "(10×4 + 40×2) / 6: the row without CPU doesn't count")
	assert.InDelta(t, 90.0, host["cpu_percent_max"], 1e-9)
	assert.InDelta(t, 2.0, host["load_1m_avg"], 1e-9, "(1×4 + 2×2 + 3×4) / 10")
	assert.Nil(t, host["steal_percent_avg"], "never measured")
	next := one(t, db, `SELECT readings, cpu_percent_avg FROM host_rollups WHERE bucket = ?`, at(time.Hour))
	assert.EqualValues(t, 4, next["readings"])
	assert.InDelta(t, 99.0, next["cpu_percent_avg"], 1e-9)

	disk := one(t, db, `SELECT * FROM disk_rollups WHERE mount = '/'`)
	assert.InDelta(t, 30.0, disk["used_bytes_avg"], 1e-9)
	assert.InDelta(t, 45.0, disk["used_bytes_max"], 1e-9)
	assert.InDelta(t, 55.0, disk["available_bytes_min"], 1e-9)

	app := one(t, db, `SELECT * FROM app_rollups WHERE app_id = 7`)
	assert.InDelta(t, 100.0, app["cpu_percent_avg"], 1e-9)
	assert.InDelta(t, 200.0, app["cpu_percent_max"], 1e-9)
	assert.InDelta(t, 2.0, app["tasks_max"], 1e-9)
	assert.Len(t, rows(t, db, `SELECT * FROM app_rollups`), 2, "one row per app")
}

func TestRollingUpAgainChangesNothing(t *testing.T) {
	db, _ := openTemp(t)
	for m := range 120 {
		exec(t, db, `INSERT INTO host_samples (ts, readings, cpu_percent_avg, cpu_percent_max) VALUES (?, 4, ?, ?)`,
			hour.Add(time.Duration(m)*time.Minute).Unix(), m%7, m%11)
	}
	exec(t, db, `INSERT INTO disk_samples (mount, ts, readings, used_bytes_avg) VALUES ('/', ?, 4, 1)`, hour.Unix())

	rollUp := func() {
		_, err := Rollup(context.Background(), db, Hourly, hour, hour.Add(2*time.Hour), now)
		require.NoError(t, err)
		_, err = Rollup(context.Background(), db, Daily, hour, hour.Add(2*time.Hour), now.Add(24*time.Hour))
		require.NoError(t, err)
	}
	rollUp()
	before := map[string][]map[string]any{}
	for _, table := range Tables {
		before[table.rollupTable()] = rows(t, db, "SELECT * FROM "+table.rollupTable()+" ORDER BY 1, 2, 3")
	}
	assert.Len(t, before["host_rollups"], 3, "two hours and their day")

	rollUp()
	for _, table := range Tables {
		assert.Equal(t, before[table.rollupTable()], rows(t, db, "SELECT * FROM "+table.rollupTable()+" ORDER BY 1, 2, 3"), table.Name)
	}
}

// With nothing rolled up yet (a new install, or after Echowa was stopped),
// a scheduled run goes back as far as the samples are kept, and no further.
func TestTheScheduledRunCatchesUp(t *testing.T) {
	db, _ := openTemp(t)
	kept, pruned := now.Add(-30*time.Hour).Truncate(time.Hour), now.Add(-50*time.Hour)
	exec(t, db, `INSERT INTO host_samples (ts, readings, cpu_percent_avg) VALUES (?, 4, 1), (?, 4, 2)`, kept.Unix(), pruned.Unix())

	result, err := ScheduledRollup(context.Background(), db, Hourly, now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-SampleRetention).Truncate(time.Hour).Add(time.Hour), result.From, "from the oldest whole hour still kept")
	assert.Equal(t, now.Truncate(time.Hour), result.To, "up to this hour, which isn't over")
	assert.Len(t, rows(t, db, `SELECT * FROM host_rollups`), 1, "the 50-hour-old row would make a partial bucket")
	assert.EqualValues(t, 4, one(t, db, `SELECT readings FROM host_rollups WHERE bucket = ?`, kept.Unix())["readings"])

	result, err = ScheduledRollup(context.Background(), db, Daily, now)
	require.NoError(t, err)
	assert.Equal(t, now.Truncate(24*time.Hour), result.To, "up to today, which isn't over")
}

// After missed runs, the next goes back to the newest stored bucket rather
// than only its usual 3 hours.
func TestTheScheduledRunMakesUpMissedRuns(t *testing.T) {
	db, _ := openTemp(t)
	newest := now.Truncate(time.Hour).Add(-10 * time.Hour)
	exec(t, db, `INSERT INTO host_rollups (resolution, bucket, readings) VALUES ('1h', ?, 240)`, newest.Unix())
	for h := 1; h <= 9; h++ {
		exec(t, db, `INSERT INTO host_samples (ts, readings, cpu_percent_avg) VALUES (?, 4, ?)`, newest.Add(time.Duration(h)*time.Hour).Unix(), h)
	}

	result, err := ScheduledRollup(context.Background(), db, Hourly, now)
	require.NoError(t, err)
	assert.Equal(t, newest.Add(time.Hour), result.From)
	assert.Len(t, rows(t, db, `SELECT * FROM host_rollups WHERE bucket > ?`, newest.Unix()), 9)
}
