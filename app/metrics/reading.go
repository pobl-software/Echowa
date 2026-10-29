// Package metrics reads the server's and the apps' resource use every 15
// seconds, inside Echowa (ADR 0024). A Collector turns cumulative counters
// into per-second rates between readings. Every figure is a pointer: nil
// means it wasn't measured, never zero.
package metrics

import "time"

// Reading is everything one collection saw.
type Reading struct {
	At      time.Time   `json:"at"`
	Host    Host        `json:"host"`
	Disks   []Disk      `json:"disks"`
	DiskIO  []DiskIO    `json:"disk_io"`
	Network []Interface `json:"network"`
	// Apps sums the tasks of each Echowa app.
	Apps []App `json:"apps"`
	// Others are the running containers that aren't an app's own tasks:
	// those Echowa doesn't manage, such as Traefik, and those that run
	// beside an app, such as its pooler. Only their latest reading is ever
	// shown.
	Others []Container `json:"other_containers"`
	// Took is how long the reading took, for checking its overhead.
	Took Timing `json:"-"`
}

type Timing struct {
	Host   time.Duration
	Docker time.Duration
}

type Host struct {
	CPUPercent    *float64 `json:"cpu_percent"` // 100% minus idle, as Telegraf's usage_active
	IOWaitPercent *float64 `json:"iowait_percent"`
	StealPercent  *float64 `json:"steal_percent"`
	CPUCount      *int     `json:"cpu_count"`

	Load1  *float64 `json:"load_1m"`
	Load5  *float64 `json:"load_5m"`
	Load15 *float64 `json:"load_15m"`

	MemoryTotal     *uint64 `json:"memory_total_bytes"`
	MemoryUsed      *uint64 `json:"memory_used_bytes"` // total minus available
	MemoryAvailable *uint64 `json:"memory_available_bytes"`
	SwapTotal       *uint64 `json:"swap_total_bytes"`
	SwapUsed        *uint64 `json:"swap_used_bytes"`

	// Containers is how many containers are running. Nil when Docker
	// didn't answer.
	Containers *int `json:"containers"`
}

// Disk is one filesystem's space.
type Disk struct {
	Mount          string `json:"mount"`
	Device         string `json:"device"`
	Filesystem     string `json:"filesystem"`
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"` // what a non-root user can still write
	InodesTotal    uint64 `json:"inodes_total"`
	InodesFree     uint64 `json:"inodes_free"`
}

// DiskIO is one block device's throughput.
type DiskIO struct {
	Device           string   `json:"device"`
	ReadBytesPerSec  *float64 `json:"read_bytes_per_sec"`
	WriteBytesPerSec *float64 `json:"write_bytes_per_sec"`
	ReadsPerSec      *float64 `json:"read_ops_per_sec"`
	WritesPerSec     *float64 `json:"write_ops_per_sec"`
	BusyPercent      *float64 `json:"busy_percent"`
}

// Interface is one network interface's traffic.
type Interface struct {
	Name           string   `json:"interface"`
	RxBytesPerSec  *float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec  *float64 `json:"tx_bytes_per_sec"`
	RxErrorsPerSec *float64 `json:"rx_errors_per_sec"`
	TxErrorsPerSec *float64 `json:"tx_errors_per_sec"`
	RxDropsPerSec  *float64 `json:"rx_drops_per_sec"`
	TxDropsPerSec  *float64 `json:"tx_drops_per_sec"`
}

// App is an Echowa app's running tasks, summed. A rate is nil if any task
// lacks it, such as a task that started since the last reading.
type App struct {
	Name       string   `json:"app"`
	Tasks      int      `json:"tasks"`
	CPUPercent *float64 `json:"cpu_percent"` // of one core
	MemoryUsed uint64   `json:"memory_used_bytes"`
	// MemoryLimit is the tasks' limits summed, at most the host's memory.
	MemoryLimit           uint64   `json:"memory_limit_bytes"`
	NetRxBytesPerSec      *float64 `json:"net_rx_bytes_per_sec"`
	NetTxBytesPerSec      *float64 `json:"net_tx_bytes_per_sec"`
	BlockReadBytesPerSec  *float64 `json:"block_read_bytes_per_sec"`
	BlockWriteBytesPerSec *float64 `json:"block_write_bytes_per_sec"`
}

// Container is a running container that isn't an app's own task.
type Container struct {
	Name string `json:"name"`
	// App and Role say which app the container runs beside, and as what.
	// Both are empty for a container Echowa doesn't manage.
	App         string   `json:"app"`
	Role        string   `json:"role"`
	CPUPercent  *float64 `json:"cpu_percent"` // of one core
	MemoryUsed  uint64   `json:"memory_used_bytes"`
	MemoryLimit uint64   `json:"memory_limit_bytes"`
}
