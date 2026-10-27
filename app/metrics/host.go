package metrics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// HostSource reads the machine. Counters are cumulative; the Collector
// turns them into rates.
type HostSource interface {
	// Read returns what it could read and an error naming what it
	// couldn't. A part it couldn't read is nil or empty.
	Read(ctx context.Context) (HostSnapshot, error)
}

type HostSnapshot struct {
	CPU      *CPUTimes
	CPUCount *int
	Load     *LoadAvg

	MemoryTotal     *uint64
	MemoryAvailable *uint64
	SwapTotal       *uint64
	SwapUsed        *uint64

	Disks   []Disk
	DiskIO  map[string]DiskIOCounters
	Network map[string]NetCounters
}

type LoadAvg struct {
	One, Five, Fifteen float64
}

// CPUTimes are seconds spent in each state since boot, all cores together.
type CPUTimes struct {
	Total, Idle, IOWait, Steal float64
}

type DiskIOCounters struct {
	ReadBytes, WriteBytes, Reads, Writes uint64
	BusyMillis                           uint64 // time the device had I/O in flight
}

type NetCounters struct {
	RxBytes, TxBytes, RxErrors, TxErrors, RxDrops, TxDrops uint64
}

// System reads this machine with gopsutil, which is what Telegraf uses, so
// the figures mean what they did in the old monitoring tool.
type System struct{}

func (System) Read(ctx context.Context) (HostSnapshot, error) {
	var snap HostSnapshot
	var errs []error
	fail := func(part string, err error) {
		errs = append(errs, fmt.Errorf("%s: %w", part, err))
	}

	times, err := cpu.TimesWithContext(ctx, false)
	switch {
	case err != nil:
		fail("cpu", err)
	case len(times) == 0:
		fail("cpu", errors.New("no totals"))
	default:
		snap.CPU = cpuTimes(times[0])
	}
	if count, err := cpu.CountsWithContext(ctx, true); err != nil {
		fail("cpu count", err)
	} else {
		snap.CPUCount = &count
	}
	if avg, err := load.AvgWithContext(ctx); err != nil {
		fail("load", err)
	} else {
		snap.Load = &LoadAvg{avg.Load1, avg.Load5, avg.Load15}
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		fail("memory", err)
	} else {
		snap.MemoryTotal, snap.MemoryAvailable = &vm.Total, &vm.Available
	}
	if swap, err := mem.SwapMemoryWithContext(ctx); err != nil {
		fail("swap", err)
	} else {
		snap.SwapTotal, snap.SwapUsed = &swap.Total, &swap.Used
	}

	disks, err := readDisks(ctx)
	if err != nil {
		fail("disks", err)
	}
	snap.Disks = disks

	if io, err := disk.IOCountersWithContext(ctx); err != nil {
		fail("disk io", err)
	} else {
		snap.DiskIO = map[string]DiskIOCounters{}
		for name, c := range io {
			if !ignoredDevice(name) && wholeDisk(name) {
				snap.DiskIO[name] = DiskIOCounters{c.ReadBytes, c.WriteBytes, c.ReadCount, c.WriteCount, c.IoTime}
			}
		}
	}

	if nics, err := net.IOCountersWithContext(ctx, true); err != nil {
		fail("network", err)
	} else {
		snap.Network = map[string]NetCounters{}
		for _, c := range nics {
			if !ignoredInterface(c.Name) && c.BytesRecv+c.BytesSent > 0 {
				snap.Network[c.Name] = NetCounters{c.BytesRecv, c.BytesSent, c.Errin, c.Errout, c.Dropin, c.Dropout}
			}
		}
	}

	return snap, errors.Join(errs...)
}

// cpuTimes leaves out guest time, which Linux already counts in user.
func cpuTimes(t cpu.TimesStat) *CPUTimes {
	return &CPUTimes{
		Total:  t.User + t.Nice + t.System + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal,
		Idle:   t.Idle,
		IOWait: t.Iowait,
		Steal:  t.Steal,
	}
}

// readDisks reports each real filesystem once. A filesystem it can't stat
// (one the sandbox hides, say) is left out.
func readDisks(ctx context.Context) ([]Disk, error) {
	// All mounts, not gopsutil's "real" ones: those leave out any mount of
	// a subdirectory as a bind, which drops / where the root filesystem is
	// a subvolume (OrbStack machines, some btrfs installs). realMounts
	// does the filtering instead.
	partitions, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		return nil, err
	}

	disks := []Disk{}
	for _, p := range realMounts(partitions) {
		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue
		}
		disks = append(disks, Disk{
			Mount:          p.Mountpoint,
			Device:         p.Device,
			Filesystem:     p.Fstype,
			TotalBytes:     usage.Total,
			UsedBytes:      usage.Used,
			AvailableBytes: usage.Free,
			InodesTotal:    usage.InodesTotal,
			InodesFree:     usage.InodesFree,
		})
	}

	return disks, nil
}

// realMounts keeps filesystems on a block device (or ZFS), drops /boot,
// and keeps one mount per device: the shortest path. Inside
// echowa.service the mount namespace also holds systemd's private /tmp,
// bound from the root filesystem, and Docker's bind mounts show up the
// same way. Device nodes bound into a container show up as devtmpfs
// mounts named after the device, hence the filesystem check too.
func realMounts(partitions []disk.PartitionStat) []disk.PartitionStat {
	byDevice := map[string]disk.PartitionStat{}
	for _, p := range partitions {
		onDevice := strings.HasPrefix(p.Device, "/") || p.Fstype == "zfs"
		if !onDevice || ignoredFilesystems[p.Fstype] || p.Mountpoint == "/boot" || strings.HasPrefix(p.Mountpoint, "/boot/") {
			continue
		}
		if kept, seen := byDevice[p.Device]; !seen || len(p.Mountpoint) < len(kept.Mountpoint) {
			byDevice[p.Device] = p
		}
	}

	mounts := make([]disk.PartitionStat, 0, len(byDevice))
	for _, p := range byDevice {
		mounts = append(mounts, p)
	}
	slices.SortFunc(mounts, func(a, b disk.PartitionStat) int { return strings.Compare(a.Mountpoint, b.Mountpoint) })

	return mounts
}

// The noise the old agent's Telegraf config dropped.
var ignoredFilesystems = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "devfs": true, "overlay": true, "aufs": true, "squashfs": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "efivarfs": true, "fusectl": true, "configfs": true,
	"hugetlbfs": true, "mqueue": true, "nsfs": true, "autofs": true, "rpc_pipefs": true,
}

// ignoredDevice drops loop, RAM-backed and floppy devices.
func ignoredDevice(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

// wholeDisk reports whether a device is a disk rather than a partition of
// one, which would count the same I/O twice. Without /sys/block (macOS)
// every device counts.
func wholeDisk(name string) bool {
	if _, err := os.Stat("/sys/block"); err != nil {
		return true
	}
	_, err := os.Stat(filepath.Join("/sys/block", name))

	return err == nil
}

// ignoredInterface drops loopback and the interfaces Docker creates.
// Interfaces that have never moved a byte are dropped by the caller.
func ignoredInterface(name string) bool {
	switch name {
	case "lo", "lo0", "docker0", "docker_gwbridge":
		return true
	}

	return strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-")
}
