package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRange(t *testing.T) {
	assert.Equal(t, Range{"1h", time.Hour}, ParseRange("1h"))
	assert.Equal(t, Range{"90d", 90 * 24 * time.Hour}, ParseRange("90d"))
	for _, key := range []string{"", "1y", "1H", "24h ", "48h"} {
		assert.Equal(t, DefaultRange, ParseRange(key).Key, "%q", key)
	}
}

func TestStepsPerRange(t *testing.T) {
	for key, want := range map[string]time.Duration{
		"1h":  time.Minute, // a minute at the finest: 60 points
		"6h":  time.Minute,
		"24h": 5 * time.Minute,
		"7d":  time.Hour, // hourly rollups: never finer than an hour
		"30d": 2 * time.Hour,
		"90d": 6 * time.Hour,
	} {
		r := ParseRange(key)
		q := newSeriesQuery(r, now)
		assert.Equal(t, want, q.step, key)
		assert.Equal(t, int64(want/time.Second), q.window.StepSeconds, key)
		assert.LessOrEqual(t, int(r.Duration/q.step), maxPoints, key)
		assert.Zero(t, q.window.From%q.window.StepSeconds, "%s: buckets count from the epoch", key)
	}
}

func TestRollupRangesReadSamplesForTheNewestTwoSteps(t *testing.T) {
	at := time.Date(2026, 10, 19, 10, 2, 0, 0, time.UTC)
	for key, back := range map[string]time.Duration{
		"7d":  time.Hour + 2*time.Minute,    // from 09:00
		"30d": 2*time.Hour + 2*time.Minute,  // from 08:00
		"90d": 10*time.Hour + 2*time.Minute, // from 00:00
	} {
		q := newSeriesQuery(ParseRange(key), at)
		assert.True(t, q.rollups, key)
		assert.Equal(t, at.Add(-back), q.samplesFrom, key)
	}

	q := newSeriesQuery(ParseRange("24h"), at)
	assert.False(t, q.rollups)
	assert.Equal(t, q.from, q.samplesFrom, "only samples")
}

func TestWithGaps(t *testing.T) {
	v := 1.0
	at := func(ts ...int64) []Point {
		points := make([]Point, len(ts))
		for i, t := range ts {
			points[i] = Point{T: t, Avg: &v, Max: &v}
		}
		return points
	}

	assert.Equal(t, at(0, 60, 120), withGaps(at(0, 60, 120), 60), "continuous")
	assert.Equal(t, at(0, 120, 180), withGaps(at(0, 120, 180), 60), "one missing minute is a restart")

	gapped := withGaps(at(0, 60, 300, 360), 60)
	assert.Len(t, gapped, 5)
	assert.Equal(t, Point{T: 120}, gapped[2], "an empty point after the last one before the gap")
	assert.Equal(t, int64(300), gapped[3].T)

	assert.Empty(t, withGaps(nil, 60))
}

func avgMax(points []Point) map[int64][2]float64 {
	out := map[int64][2]float64{}
	for _, p := range points {
		if p.Avg != nil {
			out[p.T] = [2]float64{*p.Avg, *p.Max}
		}
	}

	return out
}

func TestShortRangesBucketSamples(t *testing.T) {
	db, _ := openTemp(t)
	end := now.Truncate(time.Minute)
	minute := func(back int) int64 { return end.Add(-time.Duration(back) * time.Minute).Unix() }
	// Two minutes, then nothing for ten, then two more.
	exec(t, db, `INSERT INTO host_samples (ts, readings, cpu_percent_avg, cpu_percent_max) VALUES
		(?, 4, 10, 20), (?, 2, 40, 50), (?, 4, 30, 30), (?, 4, 30, 35)`, minute(13), minute(12), minute(1), minute(0))
	exec(t, db, `INSERT INTO disk_samples (mount, ts, readings, used_bytes_avg, used_bytes_max) VALUES
		('/data', ?, 4, 5, 5), ('/', ?, 4, 7, 7)`, minute(0), minute(0))

	host, err := Query(context.Background(), db, HostTable, []string{"cpu_percent", "steal_percent"}, "", nil, ParseRange("1h"), now)
	require.NoError(t, err)
	assert.Equal(t, []string{""}, host.Keys)
	cpu := host.Get("", "cpu_percent")
	assert.Equal(t, map[int64][2]float64{
		minute(13): {10, 20}, minute(12): {40, 50}, minute(1): {30, 30}, minute(0): {30, 35},
	}, avgMax(cpu))
	assert.Equal(t, Point{T: minute(11)}, cpu[2], "a null point where nothing was stored")
	assert.Empty(t, host.Get("", "steal_percent"), "never measured: no series")

	host, err = Query(context.Background(), db, HostTable, []string{"cpu_percent"}, "", nil, ParseRange("24h"), now)
	require.NoError(t, err)
	fiveMinutes := end.Truncate(5 * time.Minute).Unix()
	assert.Equal(t, [2]float64{30, 35}, avgMax(host.Get("", "cpu_percent"))[fiveMinutes], "minutes 0 and 1 share a 5-minute bucket")
	assert.Equal(t, [2]float64{20, 50}, avgMax(host.Get("", "cpu_percent"))[end.Add(-13*time.Minute).Truncate(5*time.Minute).Unix()],
		"(10×4 + 40×2) / 6, and the larger peak")

	disks, err := Query(context.Background(), db, DiskTable, []string{"used_bytes"}, "", nil, ParseRange("1h"), now)
	require.NoError(t, err)
	assert.Equal(t, []string{"/", "/data"}, disks.Keys)
}

// 7d to 90d read hourly rollups, re-bucketed with averages weighted by
// readings, and samples for the newest buckets, which aren't rolled up yet.
func TestLongRangesReadRollups(t *testing.T) {
	db, _ := openTemp(t)
	bucket := now.Truncate(2 * time.Hour).Add(-10 * 24 * time.Hour)
	exec(t, db, `INSERT INTO app_rollups (app_id, resolution, bucket, readings, cpu_percent_avg, cpu_percent_max) VALUES
		(7, '1h', ?, 60, 10, 15), (7, '1h', ?, 180, 40, 90), (7, '1d', ?, 240, 99, 99), (8, '1h', ?, 240, 1, 1)`,
		bucket.Unix(), bucket.Add(time.Hour).Unix(), bucket.Unix(), bucket.Unix())
	recent := now.Truncate(time.Minute).Add(-time.Minute)
	exec(t, db, `INSERT INTO app_samples (app_id, ts, readings, cpu_percent_avg, cpu_percent_max) VALUES (7, ?, 4, 5, 5)`, recent.Unix())

	series, err := Query(context.Background(), db, AppTable, []string{"cpu_percent"}, "app_id = ?", []any{7}, ParseRange("30d"), now)
	require.NoError(t, err)
	assert.Equal(t, "30d", series.Range)
	assert.EqualValues(t, 7200, series.StepSeconds)
	assert.Equal(t, []string{"7"}, series.Keys, "only the app asked for")
	assert.Equal(t, map[int64][2]float64{
		bucket.Unix():                         {32.5, 90}, // (10×60 + 40×180) / 240; not the daily row
		recent.Truncate(2 * time.Hour).Unix(): {5, 5},     // the newest bucket, from samples
	}, avgMax(series.Get("7", "cpu_percent")))

	series, err = Query(context.Background(), db, AppTable, []string{"cpu_percent"}, "app_id = ?", []any{7}, ParseRange("90d"), now)
	require.NoError(t, err)
	assert.Equal(t, [2]float64{32.5, 90}, avgMax(series.Get("7", "cpu_percent"))[bucket.Truncate(6*time.Hour).Unix()])
}

func TestQueryRejectsAnUnknownFigure(t *testing.T) {
	db, _ := openTemp(t)
	_, err := Query(context.Background(), db, HostTable, []string{"cpu_percent; DROP TABLE host_samples"}, "", nil, ParseRange("1h"), now)
	assert.ErrorContains(t, err, "no figure")
}

// 90 days of hourly rollups for the host and two disks, as a real server
// would have them, chart quickly.
func TestNinetyDaysOfRollupsStayQuick(t *testing.T) {
	db, _ := openTemp(t)
	tx, err := db.Begin()
	require.NoError(t, err)
	start := now.Truncate(time.Hour).Add(-90 * 24 * time.Hour)
	for h := range 90 * 24 {
		bucket := start.Add(time.Duration(h) * time.Hour).Unix()
		_, err := tx.Exec(`INSERT INTO host_rollups (resolution, bucket, readings, cpu_percent_avg, cpu_percent_max) VALUES ('1h', ?, 240, ?, 100)`, bucket, h%100)
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO disk_rollups (mount, resolution, bucket, readings, used_bytes_avg, used_bytes_max) VALUES
			('/', '1h', ?, 240, 40, 41), ('/data', '1h', ?, 240, 40, 41)`, bucket, bucket)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	started := time.Now()
	host, err := Query(context.Background(), db, HostTable, []string{"cpu_percent", "memory_used_bytes", "load_1m"}, "", nil, ParseRange("90d"), now)
	require.NoError(t, err)
	_, err = Query(context.Background(), db, DiskTable, []string{"used_bytes"}, "", nil, ParseRange("90d"), now)
	require.NoError(t, err)
	elapsed := time.Since(started)

	assert.InDelta(t, 360, len(host.Get("", "cpu_percent")), 2, "6-hour buckets")
	assert.Less(t, elapsed, 500*time.Millisecond)
	t.Logf("90 days: %s", elapsed)
}
