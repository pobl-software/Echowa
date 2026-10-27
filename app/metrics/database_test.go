package metrics

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func status(commits, hits, misses float64) DatabaseStatus {
	return DatabaseStatus{
		SizeBytes:   ptr(1000.0),
		Connections: ptr(3.0),
		Commits:     ptr(commits),
		CacheHits:   ptr(hits),
		CacheMisses: ptr(misses),
	}
}

func TestDatabaseRatesComeFromTheRoundBefore(t *testing.T) {
	var rates DatabaseRates

	first := rates.Read(t0, map[string]DatabaseStatus{"db": status(100, 1000, 10)})
	require.Len(t, first, 1)
	assert.Equal(t, 1000.0, *first[0].SizeBytes, "gauges from the first round")
	assert.Nil(t, first[0].CommitsPerSec, "no rates from the first round")
	assert.Nil(t, first[0].CacheHitPercent)

	second := rates.Read(t0.Add(time.Minute), map[string]DatabaseStatus{
		"db":    status(160, 1990, 20),
		"cache": {EvictedKeys: ptr(5.0)},
	})
	require.Len(t, second, 2)
	assert.Equal(t, "cache", second[0].Name, "sorted by name")
	assert.Nil(t, second[0].EvictedKeysPerSec, "a database new this round has no rates")
	db := second[1]
	assert.InDelta(t, 1, *db.CommitsPerSec, 1e-9)
	assert.InDelta(t, 99, *db.CacheHitPercent, 1e-9, "990 hits and 10 misses")
	assert.Nil(t, db.TempBytesPerSec, "a counter the database doesn't report")

	restarted := rates.Read(t0.Add(2*time.Minute), map[string]DatabaseStatus{"db": status(5, 1990, 20)})
	assert.Nil(t, restarted[0].CommitsPerSec, "a counter that went down is a restart, not a rate")
	assert.Nil(t, restarted[0].CacheHitPercent, "no reads since the last round")

	late := rates.Read(t0.Add(10*time.Minute), map[string]DatabaseStatus{"db": status(65, 2000, 20)})
	assert.Nil(t, late[0].CommitsPerSec, "too long since the last round")
}

func TestADatabaseRoundIsStoredAsOneReading(t *testing.T) {
	db, _ := openTemp(t)
	at := t0.Add(30 * time.Second)
	dbs := []Database{
		{Name: "db", SizeBytes: ptr(2048.0), Connections: ptr(4.0), TransactionIDAge: ptr(25.0)},
		{Name: "deleted", SizeBytes: ptr(1.0)},
	}
	require.NoError(t, Write(context.Background(), db, DatabaseSample(at, dbs), map[string]uint{"db": 7}))

	var ts int64
	var readings int
	var sizeAvg, sizeMax, hit sql.NullFloat64
	require.NoError(t, db.QueryRow(`SELECT ts, readings, size_bytes_avg, size_bytes_max, cache_hit_percent_avg
		FROM database_samples WHERE app_id = 7`).Scan(&ts, &readings, &sizeAvg, &sizeMax, &hit))
	assert.Equal(t, t0.Unix(), ts, "stored at the start of its minute")
	assert.Equal(t, 1, readings)
	assert.Equal(t, 2048.0, sizeAvg.Float64)
	assert.Equal(t, 2048.0, sizeMax.Float64)
	assert.False(t, hit.Valid, "a figure not measured is null")

	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM database_samples`).Scan(&count))
	assert.Equal(t, 1, count, "an app that's gone isn't stored")

	require.NoError(t, DeleteApp(context.Background(), db, 7))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM database_samples`).Scan(&count))
	assert.Zero(t, count, "deleting the app deletes its database figures")
}

func TestDatabaseFiguresRollUpAndChart(t *testing.T) {
	db, _ := openTemp(t)
	for i, connections := range []float64{2, 6} {
		at := t0.Add(time.Duration(i) * time.Minute)
		sample := DatabaseSample(at, []Database{{Name: "db", Connections: ptr(connections)}})
		require.NoError(t, Write(context.Background(), db, sample, map[string]uint{"db": 7}))
	}
	hour := t0.Truncate(time.Hour)
	_, err := Rollup(context.Background(), db, Hourly, hour, hour.Add(time.Hour), hour.Add(2*time.Hour))
	require.NoError(t, err)

	var avg, max float64
	require.NoError(t, db.QueryRow(`SELECT connections_avg, connections_max FROM database_rollups
		WHERE app_id = 7 AND resolution = '1h'`).Scan(&avg, &max))
	assert.Equal(t, 4.0, avg)
	assert.Equal(t, 6.0, max)

	rows, err := Query(context.Background(), db, DatabaseTable, []string{"connections"}, "app_id = ?", []any{7}, ParseRange("1h"), t0.Add(2*time.Minute))
	require.NoError(t, err)
	var points int
	for _, p := range rows.Get("7", "connections") {
		if p.Avg != nil {
			points++
		}
	}
	assert.Equal(t, 2, points)
}
