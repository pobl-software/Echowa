package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPruneRemovesOnlyRowsPastRetention(t *testing.T) {
	db, _ := openTemp(t)
	day := 24 * time.Hour
	for _, age := range []time.Duration{time.Hour, 47 * time.Hour, 49 * time.Hour, 50 * time.Hour, 51 * time.Hour, 52 * time.Hour, 53 * time.Hour} {
		exec(t, db, `INSERT INTO host_samples (ts, readings) VALUES (?, 4)`, now.Add(-age).Unix())
		exec(t, db, `INSERT INTO network_samples (interface, ts, readings) VALUES ('eth0', ?, 4)`, now.Add(-age).Unix())
	}
	exec(t, db, `INSERT INTO host_rollups (resolution, bucket, readings) VALUES
		('1h', ?, 1), ('1h', ?, 1), ('1d', ?, 1), ('1d', ?, 1)`,
		now.Add(-89*day).Unix(), now.Add(-91*day).Unix(), now.Add(-364*day).Unix(), now.Add(-366*day).Unix())
	exec(t, db, `INSERT INTO app_samples (app_id, ts, readings) VALUES (1, ?, 4), (2, ?, 4)`, now.Unix(), now.Unix())
	exec(t, db, `INSERT INTO app_rollups (app_id, resolution, bucket, readings) VALUES (1, '1h', ?, 4), (2, '1h', ?, 4)`, now.Unix(), now.Unix())

	deleted, err := Prune(context.Background(), db, now, []uint{1}, 2)
	require.NoError(t, err)

	assert.EqualValues(t, 5+5+2+2, deleted)
	assert.Len(t, rows(t, db, `SELECT * FROM host_samples`), 2, "only the last 48 hours, five old rows in batches of two")
	assert.Len(t, rows(t, db, `SELECT * FROM network_samples`), 2)
	assert.Equal(t, []map[string]any{
		{"resolution": "1d", "bucket": now.Add(-364 * day).Unix()},
		{"resolution": "1h", "bucket": now.Add(-89 * day).Unix()},
	}, rows(t, db, `SELECT resolution, bucket FROM host_rollups ORDER BY resolution`), "90 days of hourly, 365 of daily")
	assert.Equal(t, []map[string]any{{"app_id": int64(1)}}, rows(t, db, `SELECT app_id FROM app_samples`), "app 2 was deleted")
	assert.Equal(t, []map[string]any{{"app_id": int64(1)}}, rows(t, db, `SELECT app_id FROM app_rollups`))
}

func TestPruneWithNoAppsLeft(t *testing.T) {
	db, _ := openTemp(t)
	exec(t, db, `INSERT INTO app_samples (app_id, ts, readings) VALUES (1, ?, 4)`, now.Unix())

	_, err := Prune(context.Background(), db, now, nil, PruneBatchSize)
	require.NoError(t, err)
	assert.Empty(t, rows(t, db, `SELECT * FROM app_samples`))
}
