package services

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/config"
)

// MetricsDB is the metrics file (ADR 0024), opened and brought up to date
// once. The server opens it at boot. If it can't, the error is logged then
// and metrics are only live, never stored.
var MetricsDB = sync.OnceValues(func() (*sql.DB, error) {
	path := facades.Config().GetString("database.metrics.database")
	db, moved, err := metrics.Open(context.Background(), path, func(p string) (*sql.DB, error) {
		return sql.Open("sqlite3", config.SQLiteDSN(p))
	}, time.Now())
	if moved != "" {
		facades.Log().Warning(fmt.Sprintf("metrics: %s couldn't be read, so it was moved to %s and recreated empty", path, moved))
	}
	if err != nil {
		facades.Log().Error(fmt.Errorf("metrics: %s: %w", path, err))
	}

	return db, err
})

// What the scheduled read keeps in memory. Only ReadMetrics touches the
// collector and the minute, and the scheduler never runs two at once.
var (
	collector = sync.OnceValue(func() *metrics.Collector {
		return metrics.NewCollector(metrics.System{}, Docker(), metrics.Interval)
	})
	minute        metrics.Minute
	latest        atomic.Pointer[metrics.Reading]
	lastReadError string
)

// ReadMetrics takes one reading and keeps it as the latest. When it starts
// a new minute, the previous minute is stored. The scheduler calls it every
// 15 seconds.
func ReadMetrics(ctx context.Context) {
	r, err := collector().Read(ctx)
	// Docker being down would otherwise log four times a minute.
	if message := fmt.Sprint(err); err != nil && message != lastReadError {
		facades.Log().Warning("metrics: " + message)
		lastReadError = message
	} else if err == nil {
		lastReadError = ""
	}
	latest.Store(&r)

	if sample, closed := minute.Add(r); closed {
		if err := storeSample(ctx, sample); err != nil {
			facades.Log().Error(fmt.Errorf("metrics: storing %s: %w", sample.At.UTC().Format(time.DateTime), err))
		}
	}
}

// LatestMetrics is the newest reading, for live figures. Nil until the
// first one.
func LatestMetrics() *metrics.Reading {
	return latest.Load()
}

// SetLatestMetrics replaces the newest reading. Tests use it to script the
// live figures; nil clears them.
func SetLatestMetrics(r *metrics.Reading) {
	latest.Store(r)
}

func storeSample(ctx context.Context, sample metrics.Sample) error {
	db, err := MetricsDB()
	if err != nil {
		return nil // logged when it was opened
	}
	ids, err := AppIDsByName(ctx)
	if err != nil {
		return err
	}

	return metrics.Write(ctx, db, sample, ids)
}

// deleteAppMetrics removes a deleted app's history. A failure is logged,
// not returned: the app is gone either way.
func deleteAppMetrics(ctx context.Context, appID uint) {
	db, err := MetricsDB()
	if err != nil {
		return
	}
	if err := metrics.DeleteApp(ctx, db, appID); err != nil {
		facades.Log().Error(fmt.Errorf("metrics: deleting app %d: %w", appID, err))
	}
}

// RollupMetrics is the scheduled rollup of one resolution (ADR 0024): the
// last few buckets, or everything since the newest stored one.
func RollupMetrics(ctx context.Context, resolution metrics.Resolution, now time.Time) error {
	db, err := MetricsDB()
	if err != nil {
		return nil // logged when it was opened
	}
	_, err = metrics.ScheduledRollup(ctx, db, resolution, now)

	return err
}

// PruneMetrics deletes metrics past retention, and any left by deleted apps.
func PruneMetrics(ctx context.Context, now time.Time) error {
	db, err := MetricsDB()
	if err != nil {
		return nil // logged when it was opened
	}
	var ids []uint
	if err := facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Pluck("id", &ids); err != nil {
		return err
	}
	_, err = metrics.Prune(ctx, db, now, ids, metrics.PruneBatchSize)

	return err
}
