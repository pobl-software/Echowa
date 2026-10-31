// Package host reads what the server itself needs (ADR 0041): updates, a
// restart for a new kernel, the release's end of support. Its functions
// take what was read from the machine and return values, with no I/O, so
// they're tested on output captured from real machines (testdata/).
package host

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// Upgrade is one package apt would install.
type Upgrade struct {
	Name string
	// From is empty for a package apt would newly install, such as a
	// kernel with a new ABI.
	From, To string
	// Security is true when one of the version's origins is a -security
	// suite. Ubuntu copies security fixes to -updates too, so a version
	// often has both.
	Security bool
}

// ParseAptSimulation reads `apt-get -s dist-upgrade` with LC_ALL=C: its
// "Inst" lines, such as
//
//	Inst zlib1g [1:1.3.dfsg-3.1ubuntu2] (1:1.3.dfsg-3.1ubuntu2.3 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])
//	Inst curl [8.14.1-2+deb13u5] (8.14.1-2+deb13u7 Debian-Security:13/stable-security [arm64]) []
//	Inst docker-ce [5:29.9.1-1~debian.12~bookworm] (5:29.9.2-1~debian.12~bookworm Docker CE:bookworm [arm64])
func ParseAptSimulation(out []byte) []Upgrade {
	upgrades := []Upgrade{}
	for line := range strings.SplitSeq(string(out), "\n") {
		rest, ok := strings.CutPrefix(line, "Inst ")
		if !ok {
			continue
		}
		name, rest, _ := strings.Cut(rest, " ")
		var from string
		if strings.HasPrefix(rest, "[") {
			from, rest, _ = strings.Cut(rest[1:], "] ")
		}
		// (version origin, origin [arch]), where an origin is
		// Label:Version/Suite, or Label:Suite without a version. apt 3
		// adds groups in brackets after it, which never hold a ")".
		end := strings.LastIndex(rest, ")")
		if !strings.HasPrefix(rest, "(") || end < 0 {
			continue
		}
		to, origins, _ := strings.Cut(rest[1:end], " ")
		if i := strings.LastIndex(origins, " ["); i >= 0 {
			origins = origins[:i]
		}
		upgrades = append(upgrades, Upgrade{
			Name: name,
			From: from,
			To:   to,
			Security: slices.ContainsFunc(strings.Split(origins, ", "), func(origin string) bool {
				return strings.HasSuffix(origin, "-security")
			}),
		})
	}

	return upgrades
}

// IsDocker reports whether installing a package's upgrade restarts
// dockerd, and with it every app: Swarm has no live restore.
func IsDocker(name string) bool {
	switch name {
	case "docker-ce", "docker-ce-cli", "containerd.io":
		return true
	}

	return false
}

// ParseOSRelease reads /etc/os-release (or an env file): KEY=value lines,
// values optionally quoted.
func ParseOSRelease(data []byte) map[string]string {
	values := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}

	return values
}

// SupportEnds reads distro-info's CSV (/usr/share/distro-info/<ID>.csv)
// for the release os-release names, by its VERSION_CODENAME: the day its
// security updates stop coming without a subscription. That's the eol-lts
// column where there is one, since Debian's LTS team publishes to the same
// security suite until then, and otherwise eol: after it, Ubuntu's need
// Ubuntu Pro. ok is false when the release isn't listed or has no date
// yet, as testing releases don't.
func SupportEnds(data []byte, osRelease map[string]string) (ends time.Time, ok bool) {
	codename := osRelease["VERSION_CODENAME"]
	reader := csv.NewReader(bytes.NewReader(data))
	// Older releases' rows stop before the newer columns.
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil || len(rows) == 0 || codename == "" {
		return time.Time{}, false
	}
	header := rows[0]
	for _, row := range rows[1:] {
		fields := map[string]string{}
		for i, value := range row[:min(len(row), len(header))] {
			fields[header[i]] = value
		}
		if fields["series"] == codename {
			ends, err := time.Parse(time.DateOnly, cmp.Or(fields["eol-lts"], fields["eol"]))

			return ends, err == nil
		}
	}

	return time.Time{}, false
}

// ParseRebootRequired reads /run/reboot-required.pkgs: one package a
// line, each once, though a package that asks twice is written twice.
func ParseRebootRequired(pkgs []byte) []string {
	names := []string{}
	for line := range strings.SplitSeq(string(pkgs), "\n") {
		if name := strings.TrimSpace(line); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names
}

// NewestKernel names the newest kernel installed: the release whose
// /boot/vmlinuz-<release> is newest, since dpkg gives each image its
// package's build time. A restart is due when it isn't the one running
// (/proc/sys/kernel/osrelease). images is /boot's listing. ok is false
// when /boot has no image for the running kernel, as in OrbStack and
// containers.
func NewestKernel(running string, images []fs.FileInfo) (newest string, ok bool) {
	var newestAt time.Time
	for _, image := range images {
		release, isImage := strings.CutPrefix(image.Name(), "vmlinuz-")
		if !isImage {
			continue
		}
		ok = ok || release == running
		if image.ModTime().After(newestAt) {
			newest, newestAt = release, image.ModTime()
		}
	}
	if !ok {
		return "", false
	}

	return newest, true
}

// ParseFailedUnits reads `systemctl --failed --no-legend --plain`: a unit
// a line, then its load, active and sub states and description.
func ParseFailedUnits(out []byte) []string {
	units := []string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			units = append(units, fields[0])
		}
	}

	return units
}

// ParseAptConfig reads `apt-config dump`: `Key "value";` lines.
func ParseAptConfig(out []byte) map[string]string {
	values := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if ok {
			values[key] = strings.TrimSuffix(strings.TrimPrefix(value, `"`), `";`)
		}
	}

	return values
}
