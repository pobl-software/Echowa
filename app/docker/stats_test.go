package docker

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
)

func TestContainerStatsFromDocker(t *testing.T) {
	read := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	summary := container.Summary{
		ID:     "abc123",
		Names:  []string{"/echowa-blog.1.x7k2"},
		Labels: map[string]string{LabelApp: "blog", "com.docker.swarm.task.id": "x7k2"},
	}
	response := container.StatsResponse{
		Read:     read,
		CPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 9_000_000_000}},
		MemoryStats: container.MemoryStats{
			Usage: 300 << 20,
			Limit: 512 << 20,
			Stats: map[string]uint64{"inactive_file": 100 << 20},
		},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 1000, TxBytes: 2000},
			"eth1": {RxBytes: 10, TxBytes: 20},
		},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Major: 8, Op: "read", Value: 4096},
			{Major: 8, Op: "write", Value: 8192},
			{Major: 259, Op: "read", Value: 4096},
			{Major: 8, Op: "discard", Value: 1},
		}},
	}

	assert.Equal(t, ContainerStats{
		ID:              "abc123",
		Name:            "echowa-blog.1.x7k2",
		App:             "blog",
		Read:            read,
		CPUNanos:        9_000_000_000,
		MemoryUsed:      200 << 20,
		MemoryLimit:     512 << 20,
		NetRxBytes:      1010,
		NetTxBytes:      2020,
		BlockReadBytes:  8192,
		BlockWriteBytes: 8192,
	}, containerStats(summary, response))
}

func TestContainerStatsUnmanaged(t *testing.T) {
	stats := containerStats(container.Summary{ID: "def", Names: []string{"/traefik"}}, container.StatsResponse{})

	assert.Equal(t, "traefik", stats.Name)
	assert.Empty(t, stats.App)
}

func TestMemoryUsed(t *testing.T) {
	tests := []struct {
		name  string
		stats container.MemoryStats
		want  uint64
	}{
		{"cgroup v2", container.MemoryStats{Usage: 100, Stats: map[string]uint64{"inactive_file": 30}}, 70},
		{"cgroup v1", container.MemoryStats{Usage: 100, Stats: map[string]uint64{"total_inactive_file": 40, "inactive_file": 30}}, 60},
		{"cache larger than usage", container.MemoryStats{Usage: 100, Stats: map[string]uint64{"inactive_file": 200}}, 100},
		{"no breakdown", container.MemoryStats{Usage: 100}, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, memoryUsed(tt.stats))
		})
	}
}
