package services

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"goravel/app/facades"
	"goravel/app/host"
)

// Maintenance is what the server itself needs doing, for the Server page
// (ADR 0041). Echowa reads it and never acts on it. A nil field wasn't
// measured, and its row is left out: never shown as fine. Mirrored by
// Maintenance in resources/js/types/models.ts.
type Maintenance struct {
	Restart *Restart `json:"restart"`
	Updates *Updates `json:"updates"`
	// AutoUpdates is whether unattended-upgrades installs security
	// updates by itself.
	AutoUpdates *bool `json:"auto_updates"`
	// Interrupted is a package install that stopped part way: dpkg has
	// updates it never applied.
	Interrupted *bool    `json:"interrupted"`
	Support     *Support `json:"support"`
	// FailedUnits is systemd's failed units, [] when none.
	FailedUnits []string `json:"failed_units"`
	// ClockSynced is whether the clock is right: in sync by systemd's
	// account, or, when it isn't, within clockTolerance of a reference.
	// OrbStack sets its machines' clocks from the Mac with no sync service,
	// so systemd says unsynchronized there while the time is right.
	ClockSynced *bool `json:"clock_synced"`
	// ClockOffset is how many seconds ahead of the reference the clock is,
	// when systemd said it isn't in sync and the reference answered.
	ClockOffset *float64 `json:"clock_offset"`
	// ReadAt is when it was read: the dashboard shows the last read.
	ReadAt time.Time `json:"read_at"`
}

// Restart is whether the server needs restarting, and why.
type Restart struct {
	Required bool `json:"required"`
	// Since is when a package asked for a restart, and Packages which
	// did; nil and [] when none did.
	Since    *time.Time `json:"since"`
	Packages []string   `json:"packages"`
	// NewKernel is a newer kernel than RunningKernel installed in /boot,
	// empty when there's none.
	RunningKernel string `json:"running_kernel"`
	NewKernel     string `json:"new_kernel"`
}

// Updates is what apt would install, by package name.
type Updates struct {
	Security []string `json:"security"`
	Other    []string `json:"other"`
	// Docker is the ones, of either, whose upgrade restarts dockerd.
	Docker []string `json:"docker"`
	// ListsUpdated is when apt last fetched its package lists. Updates
	// published since are missing, so after listsStaleAfter they're stale.
	ListsUpdated *time.Time `json:"lists_updated"`
	ListsStale   bool       `json:"lists_stale"`
}

// Support is when the release stops getting security updates.
type Support struct {
	// Release is os-release's PRETTY_NAME, "Ubuntu 24.04.3 LTS".
	Release string    `json:"release"`
	Ends    time.Time `json:"ends"`
	// Ending is within supportWarning of Ends, or past it.
	Ending bool `json:"ending"`
}

const (
	listsStaleAfter = 2 * 24 * time.Hour
	// clockTolerance is how far off a clock no service syncs may be. The
	// reference is good to about a second.
	clockTolerance = 5 * time.Second
	supportWarning = 90 * 24 * time.Hour
	// aptTimeout bounds a simulation, which runs apart from the request.
	aptTimeout = 30 * time.Second
)

// HostReadLimit is how long reading maintenance may take, under the 3s
// request timeout: the commands share it, and an apt simulation still
// running then is left to finish for the next read. Tests shorten it.
var HostReadLimit = 2 * time.Second

// The commands maintenance runs. Nothing a request sends goes into one.
var (
	aptSimulation     = []string{"apt-get", "-s", "-o", "Debug::NoLocking=1", "dist-upgrade"}
	failedUnits       = []string{"systemctl", "--failed", "--no-legend", "--plain"}
	ntpSynchronized   = []string{"timedatectl", "show", "-p", "NTPSynchronized", "--value"}
	unattendedUpgrade = []string{"apt-config", "dump", "APT::Periodic::Unattended-Upgrade"}
)

// HostReader is how maintenance reads the machine: its files, from the
// root, and a fixed command's output. Tests swap Host for one over a
// fstest.MapFS with a scripted Run.
type HostReader struct {
	// FS is the root filesystem; paths have no leading slash.
	FS fs.FS
	// Run runs a command under nice with LC_ALL=C, until ctx is done.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// ClockOffset is how far ahead of a reference the clock is. Nil has
	// no reference.
	ClockOffset func(ctx context.Context) (time.Duration, error)
}

// Host is the machine Echowa runs on.
var Host = HostReader{FS: os.DirFS("/"), Run: runOnHost, ClockOffset: traceClockOffset}

// traceClockOffset compares the clock with Cloudflare's trace, whose ts is
// the time it answered: about halfway through the request.
func traceClockOffset(ctx context.Context) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s answered %s", traceURL, resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "ts="); ok {
			seconds, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, err
			}
			reference := time.UnixMilli(int64(seconds * 1000))
			return start.Add(time.Since(start) / 2).Sub(reference), nil
		}
	}

	return 0, fmt.Errorf("%s didn't say the time", traceURL)
}

func runOnHost(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "nice", append([]string{"-n", "10", name}, args...)...)
	// Echowa's own environment holds APP_KEY.
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		err = fmt.Errorf("%w: %s", err, bytes.TrimSpace(exit.Stderr))
	}

	return out, err
}

// lastMaintenance is the last HostMaintenance, which the dashboard shows
// rather than reading the host on every visit.
var lastMaintenance struct {
	sync.Mutex
	m *Maintenance
}

// HostMaintenance reads what the server needs doing. It starts an apt
// simulation when dpkg's status or apt's lists have changed since the
// last, and waits for it within HostReadLimit.
func HostMaintenance(ctx context.Context) Maintenance {
	ctx, cancel := context.WithTimeout(ctx, HostReadLimit)
	defer cancel()
	m := readMaintenance(ctx, Host, true)
	m.ReadAt = time.Now()
	lastMaintenance.Lock()
	lastMaintenance.m = &m
	lastMaintenance.Unlock()

	return m
}

// LastMaintenance is the last HostMaintenance, nil when there's been none
// since Echowa started. It reads nothing.
func LastMaintenance() *Maintenance {
	lastMaintenance.Lock()
	defer lastMaintenance.Unlock()

	return lastMaintenance.m
}

// HostAttention is the rail's dot: a restart, security updates, an
// interrupted install or support ending. It reads files and the last apt
// simulation only, so it never runs a command.
func HostAttention() bool {
	m := readMaintenance(context.Background(), Host, false)

	return m.Restart != nil && m.Restart.Required ||
		m.Updates != nil && len(m.Updates.Security) > 0 ||
		m.Interrupted != nil && *m.Interrupted ||
		m.Support != nil && m.Support.Ending
}

// readMaintenance reads everything, or with live false only what needs no
// command: the files and the last simulation.
func readMaintenance(ctx context.Context, h HostReader, live bool) Maintenance {
	now := time.Now()
	key, hasApt := aptKeyOf(h)
	var simulation <-chan struct{}
	if live && hasApt {
		simulation = startAptSimulation(h, key)
	}

	m := Maintenance{
		Restart:     readRestart(h),
		Interrupted: readInterrupted(h),
		Support:     readSupport(h, now),
	}
	if live {
		if _, err := fs.Stat(h.FS, "run/systemd/system"); err == nil {
			if out, ok := hostCommand(ctx, h, failedUnits); ok {
				m.FailedUnits = host.ParseFailedUnits(out)
			}
			if out, ok := hostCommand(ctx, h, ntpSynchronized); ok {
				m.ClockSynced = parseYesNo(out)
				if m.ClockSynced != nil && !*m.ClockSynced && h.ClockOffset != nil {
					m.ClockSynced, m.ClockOffset = checkClock(ctx, h)
				}
			}
		}
		if hasApt {
			m.AutoUpdates = readAutoUpdates(ctx, h)
		}
	}
	if simulation != nil {
		select {
		case <-simulation:
		case <-ctx.Done():
		}
	}
	if hasApt {
		m.Updates = readUpdates(h, key, now)
	}

	return m
}

// hostCommand runs one of the commands above. It's false when the
// command fails: no D-Bus, say, or out of time.
func hostCommand(ctx context.Context, h HostReader, command []string) ([]byte, bool) {
	out, err := h.Run(ctx, command[0], command[1:]...)
	if err != nil {
		facades.Log().Warning(fmt.Errorf("server maintenance: %s: %w", strings.Join(command, " "), err))
		return nil, false
	}

	return out, true
}

// checkClock compares a clock systemd says isn't in sync with the
// reference. Without an answer it stays not synced, as systemd says.
func checkClock(ctx context.Context, h HostReader) (synced *bool, offset *float64) {
	ahead, err := h.ClockOffset(ctx)
	if err != nil {
		facades.Log().Warning(fmt.Errorf("server maintenance: the reference clock: %w", err))
		return ptr(false), nil
	}

	return ptr(ahead.Abs() <= clockTolerance), ptr(ahead.Round(time.Millisecond).Seconds())
}

func parseYesNo(out []byte) *bool {
	switch strings.TrimSpace(string(out)) {
	case "yes":
		return ptr(true)
	case "no":
		return ptr(false)
	}

	return nil
}

func readRestart(h HostReader) *Restart {
	restart := Restart{Packages: []string{}}
	measured := false
	if info, err := fs.Stat(h.FS, "run/reboot-required"); err == nil {
		measured, restart.Required = true, true
		restart.Since = ptr(info.ModTime())
		if pkgs, err := fs.ReadFile(h.FS, "run/reboot-required.pkgs"); err == nil {
			restart.Packages = host.ParseRebootRequired(pkgs)
		}
	}
	if running, err := fs.ReadFile(h.FS, "proc/sys/kernel/osrelease"); err == nil {
		restart.RunningKernel = strings.TrimSpace(string(running))
		newest, ok := host.NewestKernel(restart.RunningKernel, listing(h, "boot"))
		if ok && newest != restart.RunningKernel {
			restart.Required, restart.NewKernel = true, newest
		}
		measured = measured || ok
	}
	if !measured {
		return nil
	}

	return &restart
}

func readInterrupted(h HostReader) *bool {
	entries, err := fs.ReadDir(h.FS, "var/lib/dpkg/updates")
	if err != nil {
		return nil
	}

	return ptr(len(entries) > 0)
}

func readSupport(h HostReader, now time.Time) *Support {
	data, err := fs.ReadFile(h.FS, "etc/os-release")
	if err != nil {
		return nil
	}
	osRelease := host.ParseOSRelease(data)
	// An ID that isn't a plain name isn't a valid fs path, so it's an error.
	csv, err := fs.ReadFile(h.FS, "usr/share/distro-info/"+osRelease["ID"]+".csv")
	if err != nil {
		return nil
	}
	ends, ok := host.SupportEnds(csv, osRelease)
	if !ok {
		return nil
	}

	return &Support{Release: osRelease["PRETTY_NAME"], Ends: ends, Ending: now.Add(supportWarning).After(ends)}
}

// readAutoUpdates is whether unattended-upgrades is installed and apt's
// daily job runs it.
func readAutoUpdates(ctx context.Context, h HostReader) *bool {
	out, ok := hostCommand(ctx, h, unattendedUpgrade)
	if !ok {
		return nil
	}
	_, err := fs.Stat(h.FS, "usr/bin/unattended-upgrade")
	interval := host.ParseAptConfig(out)["APT::Periodic::Unattended-Upgrade"]

	return ptr(err == nil && interval != "" && interval != "0")
}

// readUpdates is the last simulation's upgrades, if it ran against this
// dpkg status, and how fresh apt's lists are. It's nil when there's no
// such simulation yet, or it failed.
func readUpdates(h HostReader, key aptKey, now time.Time) *Updates {
	upgrades, ok := lastAptSimulation(key)
	if !ok {
		return nil
	}
	updates := Updates{Security: []string{}, Other: []string{}, Docker: []string{}}
	for _, upgrade := range upgrades {
		if upgrade.Security {
			updates.Security = append(updates.Security, upgrade.Name)
		} else {
			updates.Other = append(updates.Other, upgrade.Name)
		}
		if host.IsDocker(upgrade.Name) {
			updates.Docker = append(updates.Docker, upgrade.Name)
		}
	}
	// Ubuntu's update-notifier stamps a successful apt update. Without
	// it, apt gives each list its mirror's Last-Modified, and the
	// security suites are published at least daily.
	if info, err := fs.Stat(h.FS, "var/lib/apt/periodic/update-success-stamp"); err == nil {
		updates.ListsUpdated = ptr(info.ModTime())
	} else {
		for _, info := range listing(h, "var/lib/apt/lists") {
			if info.Mode().IsRegular() && info.Name() != "lock" && (updates.ListsUpdated == nil || info.ModTime().After(*updates.ListsUpdated)) {
				updates.ListsUpdated = ptr(info.ModTime())
			}
		}
	}
	updates.ListsStale = updates.ListsUpdated != nil && now.Sub(*updates.ListsUpdated) > listsStaleAfter

	return &updates
}

// listing is a directory's entries, without those that went before Info.
func listing(h HostReader, dir string) []fs.FileInfo {
	entries, _ := fs.ReadDir(h.FS, dir)
	infos := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil {
			infos = append(infos, info)
		}
	}

	return infos
}

// aptKey is what an apt simulation depends on: the installed packages,
// and the lists of what's available.
type aptKey struct{ status, lists int64 }

// aptKeyOf is the machine's aptKey, and false when it has no apt: not
// Linux, or not Debian's family.
func aptKeyOf(h HostReader) (aptKey, bool) {
	if _, err := fs.Stat(h.FS, "usr/bin/apt-get"); err != nil {
		return aptKey{}, false
	}
	status, err := fs.Stat(h.FS, "var/lib/dpkg/status")
	if err != nil {
		return aptKey{}, false
	}
	lists, err := fs.Stat(h.FS, "var/lib/apt/lists")
	if err != nil {
		return aptKey{}, false
	}

	return aptKey{status.ModTime().UnixNano(), lists.ModTime().UnixNano()}, true
}

// aptSimulations is the last simulation, and the one running. It's in
// memory: it takes about a second, and only reruns once something's
// changed.
var aptSimulations struct {
	sync.Mutex
	key aptKey
	ran bool
	// upgrades is nil when the last one failed.
	upgrades []host.Upgrade
	// running is closed when the one running ends, and nil when none is.
	running chan struct{}
}

// startAptSimulation starts one unless the last ran against key, and
// returns the one running, nil when none is. One runs at a time, apart
// from the request: a page that stops waiting leaves it to finish for the
// next, where a run cut off with its request would never finish on a
// slow server.
func startAptSimulation(h HostReader, key aptKey) <-chan struct{} {
	aptSimulations.Lock()
	defer aptSimulations.Unlock()
	if aptSimulations.running != nil || aptSimulations.ran && aptSimulations.key == key {
		return aptSimulations.running
	}
	done := make(chan struct{})
	aptSimulations.running = done
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), aptTimeout)
		defer cancel()
		out, err := h.Run(ctx, aptSimulation[0], aptSimulation[1:]...)
		var upgrades []host.Upgrade
		if err != nil {
			facades.Log().Warning(fmt.Errorf("server maintenance: %s: %w", strings.Join(aptSimulation, " "), err))
		} else {
			upgrades = host.ParseAptSimulation(out)
		}
		aptSimulations.Lock()
		defer aptSimulations.Unlock()
		aptSimulations.key, aptSimulations.ran, aptSimulations.upgrades, aptSimulations.running = key, true, upgrades, nil
	}()

	return done
}

// lastAptSimulation is the last simulation's upgrades, if it ran against
// the dpkg status in key. Newer lists can only add upgrades, so until
// it's run against them too it's still right as far as it goes.
func lastAptSimulation(key aptKey) ([]host.Upgrade, bool) {
	aptSimulations.Lock()
	defer aptSimulations.Unlock()
	if !aptSimulations.ran || aptSimulations.key.status != key.status || aptSimulations.upgrades == nil {
		return nil, false
	}

	return aptSimulations.upgrades, true
}

// ResetHostMaintenance waits for the apt simulation running and forgets
// the last one and the last read, for tests.
func ResetHostMaintenance() {
	lastMaintenance.Lock()
	lastMaintenance.m = nil
	lastMaintenance.Unlock()
	aptSimulations.Lock()
	running := aptSimulations.running
	aptSimulations.Unlock()
	if running != nil {
		<-running
	}
	aptSimulations.Lock()
	defer aptSimulations.Unlock()
	aptSimulations.key, aptSimulations.ran, aptSimulations.upgrades = aptKey{}, false, nil
}
