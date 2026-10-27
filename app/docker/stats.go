package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func (e *Engine) ContainerStats(ctx context.Context) ([]ContainerStats, error) {
	list, err := e.api.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	stats := make([]ContainerStats, 0, len(list.Items))
	for _, summary := range list.Items {
		response, err := e.containerStats(ctx, summary.ID)
		if cerrdefs.IsNotFound(err) || cerrdefs.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		stats = append(stats, containerStats(summary, response))
	}

	return stats, nil
}

func (e *Engine) containerStats(ctx context.Context, id string) (container.StatsResponse, error) {
	result, err := e.api.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return container.StatsResponse{}, fmt.Errorf("stats for container %.12s: %w", id, err)
	}
	defer result.Body.Close()

	var response container.StatsResponse
	if err := json.NewDecoder(result.Body).Decode(&response); err != nil {
		return container.StatsResponse{}, fmt.Errorf("decode stats for container %.12s: %w", id, err)
	}

	return response, nil
}

func containerStats(summary container.Summary, s container.StatsResponse) ContainerStats {
	stats := ContainerStats{
		ID:          summary.ID,
		App:         summary.Labels[LabelApp],
		Read:        s.Read,
		CPUNanos:    s.CPUStats.CPUUsage.TotalUsage,
		MemoryUsed:  memoryUsed(s.MemoryStats),
		MemoryLimit: s.MemoryStats.Limit,
	}
	if len(summary.Names) > 0 {
		stats.Name = strings.TrimPrefix(summary.Names[0], "/")
	}
	for _, network := range s.Networks {
		stats.NetRxBytes += network.RxBytes
		stats.NetTxBytes += network.TxBytes
	}
	for _, entry := range s.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			stats.BlockReadBytes += entry.Value
		case "write":
			stats.BlockWriteBytes += entry.Value
		}
	}

	return stats
}

// memoryUsed leaves out the page cache the kernel can drop, as the Docker
// CLI does. cgroup v1 calls it total_inactive_file, v2 inactive_file.
func memoryUsed(m container.MemoryStats) uint64 {
	if inactive, v1 := m.Stats["total_inactive_file"]; v1 && inactive < m.Usage {
		return m.Usage - inactive
	}
	if inactive := m.Stats["inactive_file"]; inactive < m.Usage {
		return m.Usage - inactive
	}

	return m.Usage
}
