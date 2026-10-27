package metrics

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openFile(path string) (*sql.DB, error) {
	return sql.Open("sqlite3", "file:"+path+"?_pragma=journal_mode(wal)")
}

func openTemp(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metrics.sqlite")
	db, moved, err := Open(context.Background(), path, openFile, t0)
	require.NoError(t, err)
	require.Empty(t, moved)
	t.Cleanup(func() { db.Close() })

	return db, path
}

func TestAMinuteIsStoredAsAvgAndMax(t *testing.T) {
	db, _ := openTemp(t)
	var m Minute
	readings := []Reading{
		// The first reading after a start has no rates.
		{At: t0, Host: Host{Load1: ptr(1.0)}, Apps: []App{{Name: "blog", Tasks: 1, MemoryUsed: 100}}},
		{At: t0.Add(15 * time.Second), Host: Host{CPUPercent: ptr(10.0), Load1: ptr(3.0)}, Apps: []App{{Name: "blog", Tasks: 2, CPUPercent: ptr(4.0), MemoryUsed: 300}}},
		// Docker didn't answer: no apps, and no container count.
		{At: t0.Add(30 * time.Second), Host: Host{CPUPercent: ptr(30.0), Load1: ptr(2.0)}},
		{At: t0.Add(45 * time.Second), Host: Host{CPUPercent: ptr(20.0), Load1: ptr(2.0), Containers: ptr(3)},
			Disks: []Disk{{Mount: "/", AvailableBytes: 50}},
			Apps:  []App{{Name: "blog", Tasks: 1, CPUPercent: ptr(2.0), MemoryUsed: 200}, {Name: "deleted", Tasks: 1}}},
	}
	for _, r := range readings {
		_, closed := m.Add(r)
		require.False(t, closed, "the minute is still open")
	}
	sample, closed := m.Add(Reading{At: t0.Add(time.Minute), Disks: []Disk{{Mount: "/", AvailableBytes: 10}}})
	require.True(t, closed, "the next minute's first reading closes it")
	require.NoError(t, Write(context.Background(), db, sample, map[string]uint{"blog": 7}))

	var readingsCount int
	var cpuAvg, cpuMax, loadAvg, loadMax, containersAvg, stealAvg sql.NullFloat64
	require.NoError(t, db.QueryRow(`SELECT readings, cpu_percent_avg, cpu_percent_max, load_1m_avg, load_1m_max, containers_avg, steal_percent_avg
		FROM host_samples WHERE ts = ?`, t0.Unix()).Scan(&readingsCount, &cpuAvg, &cpuMax, &loadAvg, &loadMax, &containersAvg, &stealAvg))
	assert.Equal(t, 4, readingsCount)
	assert.InDelta(t, 20, cpuAvg.Float64, 1e-9, "the three readings with a rate")
	assert.InDelta(t, 30, cpuMax.Float64, 1e-9)
	assert.InDelta(t, 2, loadAvg.Float64, 1e-9)
	assert.InDelta(t, 3, loadMax.Float64, 1e-9)
	assert.InDelta(t, 3, containersAvg.Float64, 1e-9, "the one reading that counted them")
	assert.False(t, stealAvg.Valid, "never measured: null, not zero")

	var appReadings int
	var tasksAvg, tasksMax, appCPUAvg, memAvg float64
	require.NoError(t, db.QueryRow(`SELECT readings, tasks_avg, tasks_max, cpu_percent_avg, memory_used_bytes_avg
		FROM app_samples WHERE app_id = 7 AND ts = ?`, t0.Unix()).Scan(&appReadings, &tasksAvg, &tasksMax, &appCPUAvg, &memAvg))
	assert.Equal(t, 3, appReadings, "seen in three of the four readings")
	assert.InDelta(t, 4.0/3, tasksAvg, 1e-9)
	assert.InDelta(t, 2, tasksMax, 1e-9, "the deploy's overlap shows in the max")
	assert.InDelta(t, 3, appCPUAvg, 1e-9)
	assert.InDelta(t, 200, memAvg, 1e-9)

	var apps int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM app_samples`).Scan(&apps))
	assert.Equal(t, 1, apps, "an app that's gone from Echowa isn't stored")

	var diskRows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM disk_samples WHERE ts = ?`, t0.Add(time.Minute).Unix()).Scan(&diskRows))
	assert.Zero(t, diskRows, "the new minute stays in memory")
}

func TestDiskAvailableKeepsItsMinimum(t *testing.T) {
	db, _ := openTemp(t)
	var m Minute
	for i, available := range []uint64{50, 20, 40} {
		m.Add(Reading{At: t0.Add(time.Duration(i) * 15 * time.Second), Disks: []Disk{{Mount: "/", TotalBytes: 100, AvailableBytes: available}}})
	}
	sample, _ := m.Add(Reading{At: t0.Add(time.Minute)})
	require.NoError(t, Write(context.Background(), db, sample, nil))

	var avg, maxAvailable, minAvailable float64
	require.NoError(t, db.QueryRow(`SELECT available_bytes_avg, available_bytes_max, available_bytes_min FROM disk_samples WHERE mount = '/'`).
		Scan(&avg, &maxAvailable, &minAvailable))
	assert.InDelta(t, 110.0/3, avg, 1e-9)
	assert.InDelta(t, 50, maxAvailable, 1e-9)
	assert.InDelta(t, 20, minAvailable, 1e-9)
}

func TestEveryFigureHasItsColumns(t *testing.T) {
	db, _ := openTemp(t)
	for _, table := range Tables {
		insert := table.insert()
		args := make([]any, strings.Count(insert, "?"))
		for i := range args {
			args[i] = i
		}
		_, err := db.Exec(insert, args...)
		require.NoError(t, err, table.Name)
	}
}

func TestAnOlderFileIsUpgraded(t *testing.T) {
	db, path := openTemp(t)
	_, err := db.Exec(`INSERT INTO host_samples (ts, readings) VALUES (60, 4)`)
	require.NoError(t, err)
	db.Close()

	defer func(was []string) { migrations = was }(migrations)
	migrations = append(migrations[:len(migrations):len(migrations)], `ALTER TABLE host_samples ADD COLUMN extra REAL`)

	db, moved, err := Open(context.Background(), path, openFile, t0)
	require.NoError(t, err)
	require.Empty(t, moved)
	defer db.Close()

	var version, kept int
	require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&version))
	assert.Equal(t, len(migrations), version)
	require.NoError(t, db.QueryRow(`SELECT count(extra) + count(*) FROM host_samples`).Scan(&kept))
	assert.Equal(t, 1, kept, "the new column exists and the old row is still there")
}

func TestAFileFromANewerReleaseIsLeftAlone(t *testing.T) {
	db, path := openTemp(t)
	_, err := db.Exec(`PRAGMA user_version = 99`)
	require.NoError(t, err)
	db.Close()

	db, moved, err := Open(context.Background(), path, openFile, t0)
	require.NoError(t, err, "an updater rollback runs an older release against it")
	require.Empty(t, moved)
	defer db.Close()
	var version int
	require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&version))
	assert.Equal(t, 99, version)
}

func TestACorruptFileIsMovedAsideAndRecreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.sqlite")
	garbage := []byte("this is not a database, but it is long enough to look like a header to SQLite")
	require.NoError(t, os.WriteFile(path, garbage, 0o600))

	db, moved, err := Open(context.Background(), path, openFile, t0)
	require.NoError(t, err)
	defer db.Close()

	assert.Equal(t, path+".broken-20261025-120000", moved)
	kept, err := os.ReadFile(moved)
	require.NoError(t, err)
	assert.Equal(t, garbage, kept, "moved, not deleted")
	_, err = db.Exec(`INSERT INTO host_samples (ts, readings) VALUES (60, 4)`)
	assert.NoError(t, err, "a fresh file with the schema")
}

func TestDeleteAppRemovesItsRows(t *testing.T) {
	db, _ := openTemp(t)
	var m Minute
	m.Add(Reading{At: t0, Apps: []App{{Name: "blog"}, {Name: "shop"}}})
	sample, _ := m.Add(Reading{At: t0.Add(time.Minute)})
	require.NoError(t, Write(context.Background(), db, sample, map[string]uint{"blog": 1, "shop": 2}))

	require.NoError(t, DeleteApp(context.Background(), db, 1))

	var left []int
	rows, err := db.Query(`SELECT app_id FROM app_samples`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id int
		require.NoError(t, rows.Scan(&id))
		left = append(left, id)
	}
	assert.Equal(t, []int{2}, left)
}
