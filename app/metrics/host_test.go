package metrics

import (
	"context"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealMounts(t *testing.T) {
	// Roughly what echowa.service sees: its private /tmp is a bind of the
	// root filesystem, /dev is a private tmpfs, and Docker adds overlays.
	partitions := []disk.PartitionStat{
		{Device: "/dev/sda1", Mountpoint: "/tmp", Fstype: "ext4"},
		{Device: "/dev/sda1", Mountpoint: "/", Fstype: "ext4"},
		{Device: "/dev/sda1", Mountpoint: "/var/tmp", Fstype: "ext4"},
		{Device: "/dev/sda15", Mountpoint: "/boot/efi", Fstype: "vfat"},
		{Device: "/dev/sda16", Mountpoint: "/boot", Fstype: "ext4"},
		{Device: "/dev/sdb", Mountpoint: "/data", Fstype: "xfs"},
		{Device: "tmpfs", Mountpoint: "/dev", Fstype: "tmpfs"},
		{Device: "overlay", Mountpoint: "/var/lib/docker/rootfs/overlayfs/abc", Fstype: "overlay"},
		{Device: "/dev/loop3", Mountpoint: "/snap/core/1", Fstype: "squashfs"},
		{Device: "/dev/sdb", Mountpoint: "/dev/sdb", Fstype: "devtmpfs"},
		{Device: "mac", Mountpoint: "/mnt/mac", Fstype: "virtiofs"},
		{Device: "server:/export", Mountpoint: "/mnt/nfs", Fstype: "nfs4"},
		{Device: "tank/backups", Mountpoint: "/backups", Fstype: "zfs"},
	}

	var mounts []string
	for _, p := range realMounts(partitions) {
		mounts = append(mounts, p.Device+" "+p.Mountpoint)
	}
	assert.Equal(t, []string{"/dev/sda1 /", "tank/backups /backups", "/dev/sdb /data"}, mounts)
}

func TestIgnoredInterface(t *testing.T) {
	for name, ignored := range map[string]bool{
		"eth0": false, "ens5": false, "enp0s1": false, "wlan0": false,
		"lo": true, "lo0": true, "docker0": true, "docker_gwbridge": true,
		"veth3f2a1b0": true, "br-6c0d8e1f2a3b": true,
	} {
		assert.Equal(t, ignored, ignoredInterface(name), name)
	}
}

func TestIgnoredDevice(t *testing.T) {
	for name, ignored := range map[string]bool{
		"sda": false, "sda1": false, "nvme0n1": false, "vda": false,
		"loop0": true, "ram1": true, "zram0": true, "fd0": true,
	} {
		assert.Equal(t, ignored, ignoredDevice(name), name)
	}
}

// TestSystemReadsThisMachine runs gopsutil for real: whatever the OS, the
// essentials come back.
func TestSystemReadsThisMachine(t *testing.T) {
	snap, err := System{}.Read(context.Background())
	if err != nil {
		t.Logf("partial read: %v", err)
	}

	require.NotNil(t, snap.CPU)
	assert.Positive(t, snap.CPU.Total)
	require.NotNil(t, snap.MemoryTotal)
	assert.Positive(t, *snap.MemoryTotal)
	assert.NotEmpty(t, snap.Disks)
}
