package metrics

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"goravel/app/docker"
)

// Interval is how often the server reads (ADR 0024).
const Interval = 15 * time.Second

// Collector reads the host and the containers, and turns their counters
// into rates against its previous reading. Read isn't safe for concurrent
// use: the scheduler skips a run while the last one is still going.
type Collector struct {
	host     HostSource
	docker   docker.Client
	interval time.Duration
	now      func() time.Time
	last     *snapshot
}

// snapshot is what the next reading's rates are measured against.
type snapshot struct {
	at         time.Time
	host       HostSnapshot
	containers map[string]docker.ContainerStats // by container ID
}

// NewCollector reads every interval. A previous reading more than three
// intervals old is too stale for rates, so the reading after a gap has
// none, as does the first.
func NewCollector(host HostSource, docker docker.Client, interval time.Duration) *Collector {
	return &Collector{host: host, docker: docker, interval: interval, now: time.Now}
}

// Read takes a reading. When part of the host or Docker can't be read, it
// still returns the rest, with an error saying what's missing.
func (c *Collector) Read(ctx context.Context) (Reading, error) {
	at := c.now()

	start := time.Now()
	host, hostErr := c.host.Read(ctx)
	hostTook := time.Since(start)

	start = time.Now()
	stats, dockerErr := c.docker.ContainerStats(ctx)
	dockerTook := time.Since(start)
	if dockerErr != nil {
		dockerErr = fmt.Errorf("docker: %w", dockerErr)
	}

	var prev *snapshot
	if c.last != nil {
		if gap := at.Sub(c.last.at); gap > 0 && gap <= 3*c.interval {
			prev = c.last
		}
	}
	reading := Reading{
		At:     at,
		Disks:  append([]Disk{}, host.Disks...),
		Apps:   []App{},
		Others: []Container{},
		Took:   Timing{Host: hostTook, Docker: dockerTook},
	}
	reading.Host, reading.DiskIO, reading.Network = hostFigures(host, prev, at)
	if dockerErr == nil {
		reading.Host.Containers = ptr(len(stats))
		reading.Apps, reading.Others = containerFigures(stats, prev, at, host.MemoryTotal)
	}

	c.last = &snapshot{at: at, host: host, containers: map[string]docker.ContainerStats{}}
	for _, s := range stats {
		c.last.containers[s.ID] = s
	}

	return reading, errors.Join(hostErr, dockerErr)
}

func hostFigures(now HostSnapshot, prev *snapshot, at time.Time) (Host, []DiskIO, []Interface) {
	// The host's counters are read within milliseconds of each other, so
	// one elapsed time serves them all.
	var before HostSnapshot
	var seconds float64
	if prev != nil {
		before, seconds = prev.host, at.Sub(prev.at).Seconds()
	}

	host := Host{
		CPUCount:        now.CPUCount,
		MemoryTotal:     now.MemoryTotal,
		MemoryAvailable: now.MemoryAvailable,
		SwapTotal:       now.SwapTotal,
		SwapUsed:        now.SwapUsed,
	}
	if now.CPU != nil && before.CPU != nil {
		host.CPUPercent, host.IOWaitPercent, host.StealPercent = cpuPercents(*before.CPU, *now.CPU)
	}
	if now.Load != nil {
		host.Load1, host.Load5, host.Load15 = &now.Load.One, &now.Load.Five, &now.Load.Fifteen
	}
	if now.MemoryTotal != nil && now.MemoryAvailable != nil && *now.MemoryAvailable <= *now.MemoryTotal {
		host.MemoryUsed = ptr(*now.MemoryTotal - *now.MemoryAvailable)
	}

	diskIO := make([]DiskIO, 0, len(now.DiskIO))
	for _, name := range slices.Sorted(maps.Keys(now.DiskIO)) {
		io := DiskIO{Device: name}
		if was, ok := before.DiskIO[name]; ok {
			cur := now.DiskIO[name]
			io.ReadBytesPerSec = rate(was.ReadBytes, cur.ReadBytes, seconds)
			io.WriteBytesPerSec = rate(was.WriteBytes, cur.WriteBytes, seconds)
			io.ReadsPerSec = rate(was.Reads, cur.Reads, seconds)
			io.WritesPerSec = rate(was.Writes, cur.Writes, seconds)
			if busyMillis := rate(was.BusyMillis, cur.BusyMillis, seconds); busyMillis != nil {
				io.BusyPercent = ptr(min(*busyMillis/10, 100))
			}
		}
		diskIO = append(diskIO, io)
	}

	network := make([]Interface, 0, len(now.Network))
	for _, name := range slices.Sorted(maps.Keys(now.Network)) {
		nic := Interface{Name: name}
		if was, ok := before.Network[name]; ok {
			cur := now.Network[name]
			nic.RxBytesPerSec = rate(was.RxBytes, cur.RxBytes, seconds)
			nic.TxBytesPerSec = rate(was.TxBytes, cur.TxBytes, seconds)
			nic.RxErrorsPerSec = rate(was.RxErrors, cur.RxErrors, seconds)
			nic.TxErrorsPerSec = rate(was.TxErrors, cur.TxErrors, seconds)
			nic.RxDropsPerSec = rate(was.RxDrops, cur.RxDrops, seconds)
			nic.TxDropsPerSec = rate(was.TxDrops, cur.TxDrops, seconds)
		}
		network = append(network, nic)
	}

	return host, diskIO, network
}

// cpuPercents is usage (100% minus idle), iowait and steal over the time
// between two readings. Times that went backwards give nil.
func cpuPercents(was, now CPUTimes) (usage, iowait, steal *float64) {
	total := now.Total - was.Total
	if total <= 0 {
		return nil, nil, nil
	}
	share := func(before, after float64) *float64 {
		if after < before {
			return nil
		}
		return ptr((after - before) / total * 100)
	}
	if idle := share(was.Idle, now.Idle); idle != nil {
		usage = ptr(100 - *idle)
	}

	return usage, share(was.IOWait, now.IOWait), share(was.Steal, now.Steal)
}

// containerRates are one container's rates since the previous reading.
type containerRates struct {
	cpuPercent                 *float64
	netRx, netTx, blkRd, blkWr *float64
}

func ratesFor(now docker.ContainerStats, prev *snapshot, at time.Time) containerRates {
	if prev == nil {
		return containerRates{}
	}
	// A new ID is a new container, even under an old name.
	was, ok := prev.containers[now.ID]
	if !ok {
		return containerRates{}
	}
	// Docker stamps each read, and containers are read one after another,
	// so their own stamps are more exact than the reading's.
	seconds := at.Sub(prev.at).Seconds()
	if !now.Read.IsZero() && !was.Read.IsZero() {
		seconds = now.Read.Sub(was.Read).Seconds()
	}

	r := containerRates{
		netRx: rate(was.NetRxBytes, now.NetRxBytes, seconds),
		netTx: rate(was.NetTxBytes, now.NetTxBytes, seconds),
		blkRd: rate(was.BlockReadBytes, now.BlockReadBytes, seconds),
		blkWr: rate(was.BlockWriteBytes, now.BlockWriteBytes, seconds),
	}
	if nanos := rate(was.CPUNanos, now.CPUNanos, seconds); nanos != nil {
		r.cpuPercent = ptr(*nanos / 1e9 * 100)
	}

	return r
}

// containerFigures sums each app's tasks, and lists the other containers.
func containerFigures(stats []docker.ContainerStats, prev *snapshot, at time.Time, hostMemory *uint64) ([]App, []Container) {
	apps := map[string]*App{}
	appRates := map[string][]containerRates{}
	others := []Container{}

	for _, s := range stats {
		r := ratesFor(s, prev, at)
		if s.App == "" || s.Role != "" {
			others = append(others, Container{Name: s.Name, App: s.App, Role: s.Role, CPUPercent: r.cpuPercent, MemoryUsed: s.MemoryUsed, MemoryLimit: s.MemoryLimit})
			continue
		}
		app := apps[s.App]
		if app == nil {
			app = &App{Name: s.App}
			apps[s.App] = app
		}
		app.Tasks++
		app.MemoryUsed += s.MemoryUsed
		app.MemoryLimit += s.MemoryLimit
		appRates[s.App] = append(appRates[s.App], r)
	}

	list := make([]App, 0, len(apps))
	for name, app := range apps {
		if hostMemory != nil {
			app.MemoryLimit = min(app.MemoryLimit, *hostMemory)
		}
		rates := appRates[name]
		app.CPUPercent = sum(rates, func(r containerRates) *float64 { return r.cpuPercent })
		app.NetRxBytesPerSec = sum(rates, func(r containerRates) *float64 { return r.netRx })
		app.NetTxBytesPerSec = sum(rates, func(r containerRates) *float64 { return r.netTx })
		app.BlockReadBytesPerSec = sum(rates, func(r containerRates) *float64 { return r.blkRd })
		app.BlockWriteBytesPerSec = sum(rates, func(r containerRates) *float64 { return r.blkWr })
		list = append(list, *app)
	}
	slices.SortFunc(list, func(a, b App) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(others, func(a, b Container) int { return cmp.Compare(a.Name, b.Name) })

	return list, others
}

// sum adds up a rate across an app's tasks, or is nil if any task lacks
// it: a partial sum would read as a drop.
func sum(rates []containerRates, field func(containerRates) *float64) *float64 {
	var total float64
	for _, r := range rates {
		v := field(r)
		if v == nil {
			return nil
		}
		total += *v
	}

	return &total
}

// rate is a counter's change per second, or nil if it went down (a restart
// or a reset).
func rate(was, now uint64, seconds float64) *float64 {
	if now < was || seconds <= 0 {
		return nil
	}

	return ptr(float64(now-was) / seconds)
}

func ptr[T any](v T) *T {
	return &v
}
