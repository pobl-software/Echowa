package host

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture reads a file captured from an OrbStack machine of the release.
func fixture(t *testing.T, release, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", release, name))
	require.NoError(t, err)

	return data
}

func TestParseAptSimulation(t *testing.T) {
	for release, want := range map[string]struct {
		names, security []string
	}{
		// ubuntu-pro-client and its l10n are kept back by a hold.
		"ubuntu-24.04": {
			names:    []string{"docker-ce-cli", "containerd.io", "docker-ce", "liblzma5", "zlib1g", "gir1.2-glib-2.0", "libglib2.0-0t64", "libglib2.0-data", "libbrotli1"},
			security: []string{"liblzma5", "zlib1g", "libbrotli1"},
		},
		"ubuntu-26.04": {
			names:    []string{"libsystemd0", "libpam-systemd", "systemd-sysv", "systemd-resolved", "libsystemd-shared", "systemd", "systemd-timesyncd", "systemd-cryptsetup", "udev", "libudev1", "docker-ce-cli", "containerd.io", "docker-ce", "liblzma5", "zlib1g", "libnss-systemd", "ubuntu-pro-client-l10n", "ubuntu-pro-client", "libbrotli1"},
			security: []string{"liblzma5", "zlib1g", "libbrotli1"},
		},
		"debian-12": {
			names:    []string{"docker-ce-cli", "containerd.io", "docker-ce", "tzdata", "libexpat1", "libsasl2-modules-db", "libsasl2-2", "libssl3", "libsasl2-modules", "openssl"},
			security: []string{"tzdata", "libexpat1", "libsasl2-modules-db", "libsasl2-2", "libssl3", "libsasl2-modules", "openssl"},
		},
		// tzdata is from stable-updates.
		"debian-13": {
			names:    []string{"docker-ce-cli", "containerd.io", "docker-ce", "openssl-provider-legacy", "libssl3t64", "tzdata", "curl", "libcurl4t64", "openssl"},
			security: []string{"openssl-provider-legacy", "libssl3t64", "curl", "libcurl4t64", "openssl"},
		},
	} {
		var names, security, docker []string
		for _, upgrade := range ParseAptSimulation(fixture(t, release, "apt-upgrades.txt")) {
			names = append(names, upgrade.Name)
			if upgrade.Security {
				security = append(security, upgrade.Name)
			}
			if IsDocker(upgrade.Name) {
				docker = append(docker, upgrade.Name)
			}
		}
		assert.Equal(t, want.names, names, release)
		assert.Equal(t, want.security, security, release)
		assert.Equal(t, []string{"docker-ce-cli", "containerd.io", "docker-ce"}, docker, release)

		none := ParseAptSimulation(fixture(t, release, "apt-none.txt"))
		assert.NotNil(t, none, release)
		assert.Empty(t, none, release)
	}
}

func TestParseAptSimulationLine(t *testing.T) {
	for name, c := range map[string]struct {
		release, file string
		want          Upgrade
	}{
		"in -updates and -security":  {"ubuntu-24.04", "apt-upgrades.txt", Upgrade{Name: "zlib1g", From: "1:1.3.dfsg-3.1ubuntu2", To: "1:1.3.dfsg-3.1ubuntu2.3", Security: true}},
		"Debian's security suite":    {"debian-13", "apt-upgrades.txt", Upgrade{Name: "curl", From: "8.14.1-2+deb13u5", To: "8.14.1-2+deb13u7", Security: true}},
		"Debian's updates suite":     {"debian-13", "apt-upgrades.txt", Upgrade{Name: "tzdata", From: "2026e-0+deb13u1", To: "2026f-0+deb13u1"}},
		"Docker's label has a space": {"debian-12", "apt-upgrades.txt", Upgrade{Name: "docker-ce", From: "5:29.9.1-1~debian.12~bookworm", To: "5:29.9.2-1~debian.12~bookworm"}},
		"apt 3's groups after it":    {"ubuntu-26.04", "apt-upgrades.txt", Upgrade{Name: "libsystemd-shared", From: "259.5-0ubuntu3.4", To: "259.9-0ubuntu0.1"}},
		"newly installed":            {"ubuntu-24.04", "apt-kernel.txt", Upgrade{Name: "linux-image-6.8.0-147-generic", To: "6.8.0-147.147", Security: true}},
		"the kernel's metapackage":   {"ubuntu-24.04", "apt-kernel.txt", Upgrade{Name: "linux-image-virtual", From: "6.8.0-31.31", To: "6.8.0-147.147", Security: true}},
	} {
		upgrades := ParseAptSimulation(fixture(t, c.release, c.file))
		assert.Contains(t, upgrades, c.want, name)
	}
}

func TestIsDocker(t *testing.T) {
	for _, name := range []string{"docker-ce", "docker-ce-cli", "containerd.io"} {
		assert.True(t, IsDocker(name), name)
	}
	// Neither restarts dockerd.
	for _, name := range []string{"docker-buildx-plugin", "docker-compose-plugin", "containerd"} {
		assert.False(t, IsDocker(name), name)
	}
}

func TestParseOSRelease(t *testing.T) {
	for release, want := range map[string][2]string{
		"ubuntu-24.04": {"Ubuntu 24.04.5 LTS", "noble"},
		"ubuntu-26.04": {"Ubuntu 26.04.1 LTS", "resolute"},
		"debian-12":    {"Debian GNU/Linux 12 (bookworm)", "bookworm"},
		"debian-13":    {"Debian GNU/Linux 13 (trixie)", "trixie"},
	} {
		info := ParseOSRelease(fixture(t, release, "os-release"))
		assert.Equal(t, want, [2]string{info["PRETTY_NAME"], info["VERSION_CODENAME"]}, release)
	}
}

func TestSupportEnds(t *testing.T) {
	for release, want := range map[string]string{
		"ubuntu-24.04": "2029-05-31",
		"ubuntu-26.04": "2031-05-29",
		// Its eol, 2026-07-11, has passed, but Debian LTS still publishes
		// to its security suite: the capture has upgrades from there.
		"debian-12": "2028-06-30",
		"debian-13": "2030-06-30",
	} {
		info := ParseOSRelease(fixture(t, release, "os-release"))
		ends, ok := SupportEnds(fixture(t, release, info["ID"]+".csv"), info)
		assert.True(t, ok, release)
		assert.Equal(t, want, ends.Format(time.DateOnly), release)
	}

	debian := fixture(t, "debian-13", "debian.csv")
	for name, osRelease := range map[string]map[string]string{
		"another distribution's list": ParseOSRelease(fixture(t, "ubuntu-26.04", "os-release")),
		"testing, with no date yet":   {"ID": "debian", "VERSION_CODENAME": "forky"},
		"no codename":                 {"ID": "debian", "VERSION_ID": "13"},
	} {
		_, ok := SupportEnds(debian, osRelease)
		assert.False(t, ok, name)
	}
	_, ok := SupportEnds([]byte("not,a\n\"csv"), ParseOSRelease(fixture(t, "debian-13", "os-release")))
	assert.False(t, ok, "not a CSV")
}

func TestParseRebootRequired(t *testing.T) {
	// Captured after the kernel was installed twice: its hook writes
	// linux-base each time.
	assert.Equal(t, []string{"linux-base"}, ParseRebootRequired(fixture(t, "ubuntu-24.04", "reboot-required.pkgs")))
	assert.Equal(t, []string{}, ParseRebootRequired(nil))
}

func TestNewestKernel(t *testing.T) {
	// /boot on Ubuntu 24.04 after the kernel upgrade in apt-kernel.txt.
	// Each image has its package's build time; the links are as new as
	// the upgrade itself.
	link := fstest.MapFile{Mode: fs.ModeSymlink, ModTime: time.Date(2026, 10, 31, 1, 38, 24, 0, time.UTC)}
	boot := fstest.MapFS{
		"vmlinuz-6.8.0-31-generic":     {ModTime: time.Date(2024, 4, 19, 21, 48, 50, 0, time.UTC)},
		"vmlinuz-6.8.0-147-generic":    {ModTime: time.Date(2026, 9, 24, 10, 28, 5, 0, time.UTC)},
		"config-6.8.0-147-generic":     {ModTime: time.Date(2026, 9, 24, 9, 3, 24, 0, time.UTC)},
		"initrd.img-6.8.0-147-generic": {ModTime: time.Date(2026, 10, 31, 1, 38, 24, 0, time.UTC)},
		"vmlinuz":                      &link,
		"vmlinuz.old":                  &link,
	}
	entries, err := fs.ReadDir(boot, ".")
	require.NoError(t, err)
	var images []fs.FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		require.NoError(t, err)
		images = append(images, info)
	}

	for _, running := range []string{"6.8.0-31-generic", "6.8.0-147-generic"} {
		newest, ok := NewestKernel(running, images)
		assert.True(t, ok, running)
		assert.Equal(t, "6.8.0-147-generic", newest, running)
	}

	_, ok := NewestKernel("7.2.12-orbstack-00394-gbecdd6130068", images)
	assert.False(t, ok, "a kernel from outside /boot")

	_, ok = NewestKernel("7.2.12-orbstack-00394-gbecdd6130068", nil)
	assert.False(t, ok, "an empty /boot, as in OrbStack")
}

func TestParseFailedUnits(t *testing.T) {
	// Two transient units that ran /bin/false, on echowa-dev.
	assert.Equal(t, []string{"echowa-fixture-backup.service", "echowa-fixture-mount.service"}, ParseFailedUnits(fixture(t, "ubuntu-24.04", "systemctl-failed.txt")))
	assert.Equal(t, []string{}, ParseFailedUnits(nil), "none failed: systemctl prints nothing")
}

func TestParseAptConfig(t *testing.T) {
	config := ParseAptConfig(fixture(t, "ubuntu-24.04", "apt-config.txt"))
	assert.Equal(t, "1", config["APT::Periodic::Unattended-Upgrade"])
	assert.Equal(t, "", config["APT::Periodic"])
	_, ok := config["APT::Periodic::Download-Upgradeable-Packages"]
	assert.False(t, ok)
}
