package services

import (
	"context"
	"math"
	"time"

	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
)

// ServerSeries is everything the Server page charts, for one range.
// Mirrored by ServerSeries in resources/js/types/metrics.ts.
type ServerSeries struct {
	metrics.Window

	CPU struct {
		Usage  []metrics.Point `json:"usage"`
		IOWait []metrics.Point `json:"iowait"`
		Steal  []metrics.Point `json:"steal"`
	} `json:"cpu"`
	Load struct {
		Load1m  []metrics.Point `json:"load_1m"`
		Load5m  []metrics.Point `json:"load_5m"`
		Load15m []metrics.Point `json:"load_15m"`
	} `json:"load"`
	// Percent of total. Swap is empty on a server without any.
	Memory []metrics.Point `json:"memory"`
	Swap   []metrics.Point `json:"swap"`
	// Fill level per mount, used / (used + available), as df shows it.
	Disks   []MountSeries     `json:"disks"`
	DiskIO  []DeviceIOSeries  `json:"disk_io"`
	Network []InterfaceSeries `json:"network"`
}

type MountSeries struct {
	Mount   string          `json:"mount"`
	Percent []metrics.Point `json:"percent"`
}

type DeviceIOSeries struct {
	Device string          `json:"device"`
	Read   []metrics.Point `json:"read"`
	Write  []metrics.Point `json:"write"`
}

type InterfaceSeries struct {
	Interface string          `json:"interface"`
	Rx        []metrics.Point `json:"rx"`
	Tx        []metrics.Point `json:"tx"`
}

// ServerMetrics returns the Server page's series for the range ending at
// now. Without a metrics file, every series is empty.
func ServerMetrics(ctx context.Context, r metrics.Range, now time.Time) (*ServerSeries, error) {
	series := &ServerSeries{Window: metrics.WindowFor(r, now)}
	empty := []metrics.Point{}
	series.CPU.Usage, series.CPU.IOWait, series.CPU.Steal = empty, empty, empty
	series.Load.Load1m, series.Load.Load5m, series.Load.Load15m = empty, empty, empty
	series.Memory, series.Swap = empty, empty
	series.Disks, series.DiskIO, series.Network = []MountSeries{}, []DeviceIOSeries{}, []InterfaceSeries{}

	db, err := MetricsDB()
	if err != nil {
		return series, nil // logged when it was opened
	}

	host, err := metrics.Query(ctx, db, metrics.HostTable, []string{
		"cpu_percent", "iowait_percent", "steal_percent", "load_1m", "load_5m", "load_15m",
		"memory_used_bytes", "memory_total_bytes", "swap_used_bytes", "swap_total_bytes",
	}, "", nil, r, now)
	if err != nil {
		return nil, err
	}
	series.CPU.Usage = host.Get("", "cpu_percent")
	series.CPU.IOWait = host.Get("", "iowait_percent")
	series.CPU.Steal = host.Get("", "steal_percent")
	series.Load.Load1m = host.Get("", "load_1m")
	series.Load.Load5m = host.Get("", "load_5m")
	series.Load.Load15m = host.Get("", "load_15m")
	series.Memory = percentOf(host.Get("", "memory_used_bytes"), host.Get("", "memory_total_bytes"))
	series.Swap = percentOf(host.Get("", "swap_used_bytes"), host.Get("", "swap_total_bytes"))

	disks, err := metrics.Query(ctx, db, metrics.DiskTable, []string{"used_bytes", "available_bytes"}, "", nil, r, now)
	if err != nil {
		return nil, err
	}
	for _, mount := range disks.Keys {
		used := disks.Get(mount, "used_bytes")
		series.Disks = append(series.Disks, MountSeries{Mount: mount, Percent: percentOf(used, sumOf(used, disks.Get(mount, "available_bytes")))})
	}

	diskIO, err := metrics.Query(ctx, db, metrics.DiskIOTable, []string{"read_bytes_per_sec", "write_bytes_per_sec"}, "", nil, r, now)
	if err != nil {
		return nil, err
	}
	for _, device := range diskIO.Keys {
		series.DiskIO = append(series.DiskIO, DeviceIOSeries{
			Device: device, Read: diskIO.Get(device, "read_bytes_per_sec"), Write: diskIO.Get(device, "write_bytes_per_sec"),
		})
	}

	network, err := metrics.Query(ctx, db, metrics.NetworkTable, []string{"rx_bytes_per_sec", "tx_bytes_per_sec"}, "", nil, r, now)
	if err != nil {
		return nil, err
	}
	for _, name := range network.Keys {
		series.Network = append(series.Network, InterfaceSeries{
			Interface: name, Rx: network.Get(name, "rx_bytes_per_sec"), Tx: network.Get(name, "tx_bytes_per_sec"),
		})
	}

	return series, nil
}

// percentOf is part as a percentage of whole, bucket by bucket: the average
// part of the average whole, and the peak part of the average whole. The
// wholes (memory, a disk's size) barely move, so that's what a chart of the
// percentage would show. Empty when nothing has both.
func percentOf(part, whole []metrics.Point) []metrics.Point {
	wholes := map[int64]float64{}
	for _, p := range whole {
		if p.Avg != nil && *p.Avg > 0 {
			wholes[p.T] = *p.Avg
		}
	}
	out := make([]metrics.Point, 0, len(part))
	measured := false
	for _, p := range part {
		w, ok := wholes[p.T]
		if p.Avg == nil || !ok {
			out = append(out, metrics.Point{T: p.T})
			continue
		}
		point := metrics.Point{T: p.T, Avg: percent(*p.Avg, w)}
		if p.Max != nil {
			point.Max = percent(*p.Max, w)
		}
		out = append(out, point)
		measured = true
	}
	if !measured {
		return []metrics.Point{}
	}

	return out
}

// sumOf adds two series' averages bucket by bucket, where both have one.
func sumOf(a, b []metrics.Point) []metrics.Point {
	bs := map[int64]float64{}
	for _, p := range b {
		if p.Avg != nil {
			bs[p.T] = *p.Avg
		}
	}
	out := make([]metrics.Point, 0, len(a))
	for _, p := range a {
		if v, ok := bs[p.T]; ok && p.Avg != nil {
			total := *p.Avg + v
			out = append(out, metrics.Point{T: p.T, Avg: &total})
		}
	}

	return out
}

func percent(part, whole float64) *float64 {
	v := math.Round(100*part/whole*100) / 100

	return &v
}

// AppIDsByName maps every app's name to its ID: readings know apps by the
// echowa.app label, which is the name.
func AppIDsByName(ctx context.Context) (map[string]uint, error) {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().Select("id", "name").Get(&apps); err != nil {
		return nil, err
	}
	ids := make(map[string]uint, len(apps))
	for _, app := range apps {
		ids[app.Name] = app.ID
	}

	return ids, nil
}
