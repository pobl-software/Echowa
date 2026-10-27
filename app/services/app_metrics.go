package services

import (
	"context"
	"strconv"
	"time"

	"goravel/app/metrics"
	"goravel/app/models"
)

// AppSeries is everything an app's Metrics section charts, for one range,
// summed over the app's tasks. Mirrored by AppSeries in
// resources/js/types/metrics.ts.
type AppSeries struct {
	metrics.Window

	// CPU is a percentage of one core.
	CPU     []metrics.Point `json:"cpu"`
	Tasks   []metrics.Point `json:"tasks"`
	Memory  []metrics.Point `json:"memory"`
	Network struct {
		Rx []metrics.Point `json:"rx"`
		Tx []metrics.Point `json:"tx"`
	} `json:"network"`
	Block struct {
		Read  []metrics.Point `json:"read"`
		Write []metrics.Point `json:"write"`
	} `json:"block"`
}

// AppMetrics returns an app's series for the range ending at now. Every
// series is empty until its first minute is stored, or without a metrics
// file.
func AppMetrics(ctx context.Context, app *models.App, r metrics.Range, now time.Time) (*AppSeries, error) {
	series := &AppSeries{Window: metrics.WindowFor(r, now)}
	empty := []metrics.Point{}
	series.CPU, series.Tasks, series.Memory = empty, empty, empty
	series.Network.Rx, series.Network.Tx = empty, empty
	series.Block.Read, series.Block.Write = empty, empty

	db, err := MetricsDB()
	if err != nil {
		return series, nil // logged when it was opened
	}

	rows, err := metrics.Query(ctx, db, metrics.AppTable, []string{
		"tasks", "cpu_percent", "memory_used_bytes",
		"net_rx_bytes_per_sec", "net_tx_bytes_per_sec", "block_read_bytes_per_sec", "block_write_bytes_per_sec",
	}, "app_id = ?", []any{app.ID}, r, now)
	if err != nil {
		return nil, err
	}
	key := strconv.FormatUint(uint64(app.ID), 10)
	series.CPU = rows.Get(key, "cpu_percent")
	series.Tasks = rows.Get(key, "tasks")
	series.Memory = rows.Get(key, "memory_used_bytes")
	series.Network.Rx = rows.Get(key, "net_rx_bytes_per_sec")
	series.Network.Tx = rows.Get(key, "net_tx_bytes_per_sec")
	series.Block.Read = rows.Get(key, "block_read_bytes_per_sec")
	series.Block.Write = rows.Get(key, "block_write_bytes_per_sec")

	return series, nil
}

// AppLive is an app's newest reading. MemoryLimited is whether its tasks
// have a memory limit of their own: without one, Docker reports the host's
// memory as the limit.
type AppLive struct {
	metrics.App
	MemoryLimited bool `json:"memory_limited"`
}

// LatestAppMetrics is the app's part of the newest reading, or nil before
// the first reading and while none of its tasks run.
func LatestAppMetrics(app *models.App) *AppLive {
	reading := LatestMetrics()
	if reading == nil {
		return nil
	}
	for _, a := range reading.Apps {
		if a.Name == app.Name {
			total := reading.Host.MemoryTotal
			return &AppLive{App: a, MemoryLimited: total != nil && a.MemoryLimit < *total}
		}
	}

	return nil
}
