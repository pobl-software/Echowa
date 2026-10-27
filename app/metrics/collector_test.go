package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/docker"
)

var t0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// script feeds the collector one host snapshot and one set of container
// stats per reading, each taken at the given time.
type script struct {
	at         []time.Time
	hosts      []HostSnapshot
	containers [][]docker.ContainerStats
	dockerErr  error
	i          int
}

func (s *script) Read(context.Context) (HostSnapshot, error) {
	return s.hosts[s.i], nil
}

func newCollector(s *script) *Collector {
	fake := &docker.Fake{Stats: func() ([]docker.ContainerStats, error) {
		if s.dockerErr != nil {
			return nil, s.dockerErr
		}
		return s.containers[s.i], nil
	}}
	c := NewCollector(s, fake, 15*time.Second)
	c.now = func() time.Time { return s.at[s.i] }

	return c
}

func (s *script) readAll(t *testing.T, c *Collector) []Reading {
	t.Helper()
	var readings []Reading
	for s.i = range s.at {
		r, err := c.Read(context.Background())
		require.NoError(t, err)
		readings = append(readings, r)
	}

	return readings
}

func host(cpu CPUTimes, rxBytes uint64, readBytes uint64, busyMillis uint64) HostSnapshot {
	return HostSnapshot{
		CPU:             &cpu,
		CPUCount:        ptr(4),
		Load:            &LoadAvg{0.5, 0.4, 0.3},
		MemoryTotal:     ptr(uint64(8 << 30)),
		MemoryAvailable: ptr(uint64(6 << 30)),
		SwapTotal:       ptr(uint64(2 << 30)),
		SwapUsed:        ptr(uint64(0)),
		Disks:           []Disk{{Mount: "/", Device: "/dev/sda1", Filesystem: "ext4", TotalBytes: 100, UsedBytes: 40, AvailableBytes: 55}},
		DiskIO:          map[string]DiskIOCounters{"sda": {ReadBytes: readBytes, BusyMillis: busyMillis}},
		Network:         map[string]NetCounters{"eth0": {RxBytes: rxBytes}},
	}
}

func TestFirstReadingHasGaugesButNoRates(t *testing.T) {
	s := &script{
		at:         []time.Time{t0},
		hosts:      []HostSnapshot{host(CPUTimes{Total: 1000, Idle: 900}, 0, 0, 0)},
		containers: [][]docker.ContainerStats{{{ID: "a", App: "blog", CPUNanos: 5e9, MemoryUsed: 100}}},
	}
	r := s.readAll(t, newCollector(s))[0]

	assert.Equal(t, uint64(2<<30), *r.Host.MemoryUsed, "total minus available")
	assert.Equal(t, 0.5, *r.Host.Load1)
	assert.Equal(t, 4, *r.Host.CPUCount)
	assert.Equal(t, 1, *r.Host.Containers)
	assert.Len(t, r.Disks, 1)
	assert.Nil(t, r.Host.CPUPercent)
	assert.Nil(t, r.Network[0].RxBytesPerSec)
	assert.Nil(t, r.DiskIO[0].ReadBytesPerSec)
	assert.Nil(t, r.Apps[0].CPUPercent)
	assert.Equal(t, uint64(100), r.Apps[0].MemoryUsed)
}

func TestRatesBetweenReadings(t *testing.T) {
	s := &script{
		at: []time.Time{t0, t0.Add(15 * time.Second)},
		hosts: []HostSnapshot{
			host(CPUTimes{Total: 1000, Idle: 900, IOWait: 10, Steal: 1}, 1000, 0, 0),
			host(CPUTimes{Total: 1060, Idle: 945, IOWait: 13, Steal: 1}, 31000, 15<<20, 3000),
		},
		containers: [][]docker.ContainerStats{nil, nil},
	}
	r := s.readAll(t, newCollector(s))[1]

	assert.InDelta(t, 25, *r.Host.CPUPercent, 1e-9, "45 of 60 seconds idle")
	assert.InDelta(t, 5, *r.Host.IOWaitPercent, 1e-9)
	assert.InDelta(t, 0, *r.Host.StealPercent, 1e-9)
	assert.InDelta(t, 2000, *r.Network[0].RxBytesPerSec, 1e-9)
	assert.InDelta(t, 1<<20, *r.DiskIO[0].ReadBytesPerSec, 1e-9)
	assert.InDelta(t, 20, *r.DiskIO[0].BusyPercent, 1e-9, "3 of 15 seconds busy")
}

func TestNoRatesAfterAGap(t *testing.T) {
	s := &script{
		at: []time.Time{t0, t0.Add(46 * time.Second)},
		hosts: []HostSnapshot{
			host(CPUTimes{Total: 1000, Idle: 900}, 1000, 0, 0),
			host(CPUTimes{Total: 1060, Idle: 945}, 2000, 0, 0),
		},
		containers: [][]docker.ContainerStats{{{ID: "a", App: "blog"}}, {{ID: "a", App: "blog", CPUNanos: 1e9}}},
	}
	r := s.readAll(t, newCollector(s))[1]

	assert.Nil(t, r.Host.CPUPercent)
	assert.Nil(t, r.Network[0].RxBytesPerSec)
	assert.Nil(t, r.Apps[0].CPUPercent)
}

func TestCounterThatWentDownHasNoRate(t *testing.T) {
	s := &script{
		at: []time.Time{t0, t0.Add(15 * time.Second)},
		hosts: []HostSnapshot{
			host(CPUTimes{Total: 1000, Idle: 900}, 5000, 100, 0),
			host(CPUTimes{Total: 990, Idle: 880}, 1000, 400, 0),
		},
		containers: [][]docker.ContainerStats{nil, nil},
	}
	r := s.readAll(t, newCollector(s))[1]

	assert.Nil(t, r.Network[0].RxBytesPerSec, "reset counter")
	assert.Nil(t, r.Host.CPUPercent, "CPU times went backwards")
	assert.InDelta(t, 20, *r.DiskIO[0].ReadBytesPerSec, 1e-9, "other counters are unaffected")
}

func TestAppTasksAreSummed(t *testing.T) {
	read := func(d time.Duration) time.Time { return t0.Add(d) }
	s := &script{
		at:    []time.Time{t0, t0.Add(15 * time.Second)},
		hosts: []HostSnapshot{{MemoryTotal: ptr(uint64(1000))}, {MemoryTotal: ptr(uint64(1000))}},
		containers: [][]docker.ContainerStats{
			{
				{ID: "a", App: "blog", Read: read(0), CPUNanos: 0, NetRxBytes: 0},
				{ID: "b", App: "blog", Read: read(time.Second), CPUNanos: 0, NetRxBytes: 0},
				{ID: "t", Name: "traefik", Read: read(0)},
			},
			{
				// Docker read "b" ten seconds after the last time, not
				// fifteen: its rates use Docker's stamps.
				{ID: "a", App: "blog", Read: read(15 * time.Second), CPUNanos: 3e9, NetRxBytes: 1500, MemoryUsed: 300, MemoryLimit: 800},
				{ID: "b", App: "blog", Read: read(11 * time.Second), CPUNanos: 1e9, NetRxBytes: 1000, MemoryUsed: 200, MemoryLimit: 800},
				{ID: "t", Name: "traefik", Read: read(15 * time.Second), CPUNanos: 1.5e9, MemoryUsed: 50, MemoryLimit: 1000},
			},
		},
	}
	r := s.readAll(t, newCollector(s))[1]

	require.Len(t, r.Apps, 1)
	blog := r.Apps[0]
	assert.Equal(t, "blog", blog.Name)
	assert.Equal(t, 2, blog.Tasks)
	assert.InDelta(t, 30, *blog.CPUPercent, 1e-9, "20% from a, 10% from b")
	assert.InDelta(t, 200, *blog.NetRxBytesPerSec, 1e-9, "100/s each")
	assert.Equal(t, uint64(500), blog.MemoryUsed)
	assert.Equal(t, uint64(1000), blog.MemoryLimit, "two limits capped at the host's memory")

	require.Len(t, r.Others, 1)
	assert.Equal(t, "traefik", r.Others[0].Name)
	assert.InDelta(t, 10, *r.Others[0].CPUPercent, 1e-9)
	assert.Equal(t, 3, *r.Host.Containers)
}

func TestANewTaskLeavesItsAppWithoutRates(t *testing.T) {
	s := &script{
		at:    []time.Time{t0, t0.Add(15 * time.Second)},
		hosts: []HostSnapshot{{}, {}},
		containers: [][]docker.ContainerStats{
			{{ID: "old", App: "blog", CPUNanos: 0}},
			{
				{ID: "old", App: "blog", CPUNanos: 1.5e9, MemoryUsed: 10},
				{ID: "new", App: "blog", CPUNanos: 9e9, MemoryUsed: 20},
			},
		},
	}
	r := s.readAll(t, newCollector(s))[1]

	assert.Equal(t, 2, r.Apps[0].Tasks)
	assert.Nil(t, r.Apps[0].CPUPercent, "half a sum would read as a drop")
	assert.Equal(t, uint64(30), r.Apps[0].MemoryUsed)
}

func TestDockerDownStillReadsTheHost(t *testing.T) {
	s := &script{
		at:        []time.Time{t0},
		hosts:     []HostSnapshot{host(CPUTimes{}, 0, 0, 0)},
		dockerErr: errors.New("connection refused"),
	}
	c := newCollector(s)
	r, err := c.Read(context.Background())

	require.ErrorContains(t, err, "docker: connection refused")
	assert.NotNil(t, r.Host.MemoryUsed)
	assert.Nil(t, r.Host.Containers, "unknown, not zero")
	assert.NotNil(t, r.Apps, "an empty list, which encodes as []")
	assert.Empty(t, r.Apps)
	assert.NotNil(t, r.Others)
}
