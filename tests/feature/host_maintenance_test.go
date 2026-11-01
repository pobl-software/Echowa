package feature

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	nethttp "net/http"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/goravel/framework/support/path"
	gonertia "github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/suite"

	"goravel/app/services"
	"goravel/tests"
)

type HostMaintenanceTestSuite struct {
	suite.Suite
	tests.TestCase
	machine fstest.MapFS
	script  *hostScript
}

func TestHostMaintenanceTestSuite(t *testing.T) {
	suite.Run(t, new(HostMaintenanceTestSuite))
}

// SetupTest gives each test an Ubuntu 24.04 server with nothing to do.
func (s *HostMaintenanceTestSuite) SetupTest() {
	services.ResetHostMaintenance()
	now := time.Now()
	s.machine = fstest.MapFS{
		"etc/os-release":                   {Data: hostFixture(s.T(), "ubuntu-24.04", "os-release")},
		"usr/share/distro-info/ubuntu.csv": {Data: distroInfo("noble", now.AddDate(2, 0, 0))},
		"proc/sys/kernel/osrelease":        {Data: []byte("6.8.0-147-generic\n")},
		"boot/vmlinuz-6.8.0-31-generic":    {ModTime: time.Date(2024, 4, 19, 21, 48, 50, 0, time.UTC)},
		"boot/vmlinuz-6.8.0-147-generic":   {ModTime: time.Date(2026, 9, 24, 10, 28, 5, 0, time.UTC)},
		"run/systemd/system":               {Mode: fs.ModeDir},
		"usr/bin/apt-get":                  {},
		"usr/bin/unattended-upgrade":       {},
		"var/lib/dpkg/status":              {ModTime: now.Add(-time.Hour)},
		"var/lib/dpkg/updates":             {Mode: fs.ModeDir},
		"var/lib/apt/lists":                {Mode: fs.ModeDir, ModTime: now.Add(-3 * time.Hour)},
		"var/lib/apt/lists/security.ubuntu.com_ubuntu_dists_noble-security_InRelease": {ModTime: now.Add(-3 * time.Hour)},
		// apt locks the lists on every run, so its time says nothing.
		"var/lib/apt/lists/lock": {ModTime: now},
	}
	s.script = &hostScript{
		out: map[string][]byte{
			"apt-get":     hostFixture(s.T(), "ubuntu-24.04", "apt-none.txt"),
			"systemctl":   {},
			"timedatectl": []byte("yes\n"),
			"apt-config":  hostFixture(s.T(), "ubuntu-24.04", "apt-config.txt"),
		},
		err:   map[string]error{},
		calls: map[string]int{},
	}
	services.Host = services.HostReader{FS: s.machine, Run: s.script.run}
}

func (s *HostMaintenanceTestSuite) SetupSubTest() {
	s.TearDownTest()
	s.SetupTest()
}

func (s *HostMaintenanceTestSuite) TearDownTest() {
	if s.script != nil {
		s.script.release()
	}
	services.ResetHostMaintenance()
	services.Host = tests.EmptyHost
	services.HostReadLimit = 2 * time.Second
}

func (s *HostMaintenanceTestSuite) TestNothingToDo() {
	m := services.HostMaintenance(context.Background())

	s.Require().NotNil(m.Restart)
	s.False(m.Restart.Required)
	s.Equal("6.8.0-147-generic", m.Restart.RunningKernel)
	s.Empty(m.Restart.NewKernel)
	s.Require().NotNil(m.Updates)
	s.Equal([]string{}, m.Updates.Security)
	s.Equal([]string{}, m.Updates.Other)
	s.Require().NotNil(m.Updates.ListsUpdated)
	s.WithinDuration(time.Now().Add(-3*time.Hour), *m.Updates.ListsUpdated, time.Minute, "the newest list, not the lock")
	s.False(m.Updates.ListsStale)
	s.Equal(ptr(true), m.AutoUpdates)
	s.Equal(ptr(false), m.Interrupted)
	s.Require().NotNil(m.Support)
	s.Equal("Ubuntu 24.04.5 LTS", m.Support.Release)
	s.False(m.Support.Ending)
	s.Equal([]string{}, m.FailedUnits)
	s.Equal(ptr(true), m.ClockSynced)
	s.False(services.HostAttention())

	page, err := json.Marshal(m)
	s.Require().NoError(err)
	s.Contains(string(page), `"packages":[]`, "empty lists, never null")
	s.Contains(string(page), `"failed_units":[]`)
	s.Contains(string(page), `"docker":[]`)
}

func (s *HostMaintenanceTestSuite) TestEachItemWhenDue() {
	for name, c := range map[string]struct {
		due       func()
		check     func(m services.Maintenance)
		attention bool
	}{
		"a package asked for a restart": {
			due: func() {
				s.machine["run/reboot-required"] = &fstest.MapFile{ModTime: time.Date(2026, 10, 31, 1, 40, 0, 0, time.UTC)}
				s.machine["run/reboot-required.pkgs"] = &fstest.MapFile{Data: hostFixture(s.T(), "ubuntu-24.04", "reboot-required.pkgs")}
			},
			check: func(m services.Maintenance) {
				s.True(m.Restart.Required)
				s.Equal(ptr(time.Date(2026, 10, 31, 1, 40, 0, 0, time.UTC)), m.Restart.Since)
				s.Equal([]string{"linux-base"}, m.Restart.Packages)
				s.Empty(m.Restart.NewKernel)
			},
			attention: true,
		},
		"a newer kernel installed": {
			due: func() {
				s.machine["boot/vmlinuz-6.8.0-150-generic"] = &fstest.MapFile{ModTime: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
			},
			check: func(m services.Maintenance) {
				s.True(m.Restart.Required)
				s.Equal("6.8.0-150-generic", m.Restart.NewKernel)
				s.Nil(m.Restart.Since, "no package asked")
			},
			attention: true,
		},
		"security and Docker updates": {
			due: func() { s.script.set("apt-get", hostFixture(s.T(), "ubuntu-24.04", "apt-upgrades.txt"), nil) },
			check: func(m services.Maintenance) {
				s.Equal([]string{"liblzma5", "zlib1g", "libbrotli1"}, m.Updates.Security)
				s.Equal([]string{"docker-ce-cli", "containerd.io", "docker-ce", "gir1.2-glib-2.0", "libglib2.0-0t64", "libglib2.0-data"}, m.Updates.Other)
				s.Equal([]string{"docker-ce-cli", "containerd.io", "docker-ce"}, m.Updates.Docker)
			},
			attention: true,
		},
		"package lists not fetched for 3 days": {
			due: func() {
				s.machine["var/lib/apt/lists/security.ubuntu.com_ubuntu_dists_noble-security_InRelease"].ModTime = time.Now().Add(-72 * time.Hour)
			},
			check: func(m services.Maintenance) { s.True(m.Updates.ListsStale) },
		},
		"automatic updates not installed": {
			due:   func() { delete(s.machine, "usr/bin/unattended-upgrade") },
			check: func(m services.Maintenance) { s.Equal(ptr(false), m.AutoUpdates) },
		},
		"automatic updates not turned on": {
			due:   func() { s.script.set("apt-config", nil, nil) },
			check: func(m services.Maintenance) { s.Equal(ptr(false), m.AutoUpdates) },
		},
		"an interrupted install": {
			due:       func() { s.machine["var/lib/dpkg/updates/0003"] = &fstest.MapFile{} },
			check:     func(m services.Maintenance) { s.Equal(ptr(true), m.Interrupted) },
			attention: true,
		},
		"support ending in a month": {
			due: func() {
				s.machine["usr/share/distro-info/ubuntu.csv"].Data = distroInfo("noble", time.Now().AddDate(0, 1, 0))
			},
			check:     func(m services.Maintenance) { s.True(m.Support.Ending) },
			attention: true,
		},
		"support ended": {
			due: func() {
				s.machine["usr/share/distro-info/ubuntu.csv"].Data = distroInfo("noble", time.Now().AddDate(0, -1, 0))
			},
			check:     func(m services.Maintenance) { s.True(m.Support.Ending) },
			attention: true,
		},
		"failed units": {
			due: func() { s.script.set("systemctl", hostFixture(s.T(), "ubuntu-24.04", "systemctl-failed.txt"), nil) },
			check: func(m services.Maintenance) {
				s.Equal([]string{"echowa-fixture-backup.service", "echowa-fixture-mount.service"}, m.FailedUnits)
			},
		},
		"clock not synced": {
			due:   func() { s.script.set("timedatectl", []byte("no\n"), nil) },
			check: func(m services.Maintenance) { s.Equal(ptr(false), m.ClockSynced) },
		},
	} {
		s.Run(name, func() {
			c.due()
			c.check(services.HostMaintenance(context.Background()))
			s.Equal(c.attention, services.HostAttention())
		})
	}
}

func (s *HostMaintenanceTestSuite) TestListsUpdatedFromUpdateNotifiersStamp() {
	stamp := time.Now().Add(-time.Hour).Truncate(time.Second)
	s.machine["var/lib/apt/lists/security.ubuntu.com_ubuntu_dists_noble-security_InRelease"].ModTime = time.Now().Add(-72 * time.Hour)
	s.machine["var/lib/apt/periodic/update-success-stamp"] = &fstest.MapFile{ModTime: stamp}

	m := services.HostMaintenance(context.Background())
	s.Equal(ptr(stamp), m.Updates.ListsUpdated, "the last apt update, though the mirror published nothing new")
	s.False(m.Updates.ListsStale)
}

func (s *HostMaintenanceTestSuite) TestWhatCantBeReadIsNil() {
	for name, c := range map[string]struct {
		change func()
		check  func(m services.Maintenance)
		// commands that mustn't even be tried
		untried []string
	}{
		"not Linux": {
			change: func() {
				for name := range s.machine {
					delete(s.machine, name)
				}
			},
			check: func(m services.Maintenance) {
				s.Equal(services.Maintenance{ReadAt: m.ReadAt}, m)
			},
			untried: []string{"apt-get", "apt-config", "systemctl", "timedatectl"},
		},
		"a kernel from outside /boot, as in OrbStack": {
			change: func() {
				s.machine["proc/sys/kernel/osrelease"].Data = []byte("7.2.12-orbstack-00394-gbecdd6130068\n")
			},
			check: func(m services.Maintenance) { s.Nil(m.Restart) },
		},
		"no apt": {
			change: func() { delete(s.machine, "usr/bin/apt-get") },
			check: func(m services.Maintenance) {
				s.Nil(m.Updates)
				s.Nil(m.AutoUpdates)
			},
			untried: []string{"apt-get", "apt-config"},
		},
		"not systemd": {
			change: func() { delete(s.machine, "run/systemd/system") },
			check: func(m services.Maintenance) {
				s.Nil(m.FailedUnits)
				s.Nil(m.ClockSynced)
			},
			untried: []string{"systemctl", "timedatectl"},
		},
		"every command failing": {
			change: func() {
				for _, name := range []string{"apt-get", "apt-config", "systemctl", "timedatectl"} {
					s.script.set(name, nil, errors.New("exit status 1"))
				}
			},
			check: func(m services.Maintenance) {
				s.Nil(m.Updates)
				s.Nil(m.AutoUpdates)
				s.Nil(m.FailedUnits)
				s.Nil(m.ClockSynced)
				s.NotNil(m.Restart, "files are still read")
			},
		},
		"a release distro-info doesn't list": {
			change: func() {
				s.machine["usr/share/distro-info/ubuntu.csv"].Data = distroInfo("questing", time.Now().AddDate(1, 0, 0))
			},
			check: func(m services.Maintenance) { s.Nil(m.Support) },
		},
	} {
		s.Run(name, func() {
			c.change()
			c.check(services.HostMaintenance(context.Background()))
			for _, command := range c.untried {
				s.Zero(s.script.count(command), command)
			}
		})
	}
}

func (s *HostMaintenanceTestSuite) TestAptRunsAgainOnlyWhenSomethingChanged() {
	ctx := context.Background()
	services.HostMaintenance(ctx)
	services.HostMaintenance(ctx)
	s.Equal(1, s.script.count("apt-get"))

	s.touch("var/lib/dpkg/status")
	services.HostMaintenance(ctx)
	s.Equal(2, s.script.count("apt-get"), "packages installed or removed")

	s.touch("var/lib/apt/lists")
	services.HostMaintenance(ctx)
	s.Equal(3, s.script.count("apt-get"), "new package lists")

	s.script.set("apt-get", nil, errors.New("exit status 100"))
	s.touch("var/lib/dpkg/status")
	s.Nil(services.HostMaintenance(ctx).Updates)
	s.Nil(services.HostMaintenance(ctx).Updates)
	s.Equal(4, s.script.count("apt-get"), "a failure isn't retried until something changes")
}

func (s *HostMaintenanceTestSuite) TestASlowRunDoesntHoldUpTheNextPage() {
	ctx := context.Background()
	services.HostReadLimit = 50 * time.Millisecond
	s.script.set("apt-get", hostFixture(s.T(), "ubuntu-24.04", "apt-upgrades.txt"), nil)
	s.script.holdApt()

	start := time.Now()
	first := services.HostMaintenance(ctx)
	second := services.HostMaintenance(ctx)
	s.Less(time.Since(start), time.Second)
	s.Nil(first.Updates, "nothing to show yet")
	s.Nil(second.Updates)
	s.NotNil(second.Restart, "the rest is read meanwhile")
	s.Equal(1, s.script.count("apt-get"), "the second page waited on the first's run")

	s.script.release()
	services.HostReadLimit = 2 * time.Second
	s.Len(services.HostMaintenance(ctx).Updates.Security, 3, "the run finished for the next page")
	s.Equal(1, s.script.count("apt-get"))

	services.HostReadLimit = 50 * time.Millisecond
	s.script.holdApt()
	s.touch("var/lib/apt/lists")
	s.Len(services.HostMaintenance(ctx).Updates.Security, 3, "newer lists only add updates, so the last run's still show")

	s.script.release()
	services.ResetHostMaintenance()
	services.HostMaintenance(ctx)
	s.script.holdApt()
	s.touch("var/lib/dpkg/status")
	s.Nil(services.HostMaintenance(ctx).Updates, "but not once packages have changed: they may be installed now")
}

func (s *HostMaintenanceTestSuite) TestAttentionNeverRunsACommand() {
	s.script.set("apt-get", hostFixture(s.T(), "ubuntu-24.04", "apt-upgrades.txt"), nil)
	s.False(services.HostAttention(), "no apt run yet")
	s.Zero(s.script.total())

	services.HostMaintenance(context.Background())
	runs := s.script.total()
	s.True(services.HostAttention(), "the last run's security updates")

	s.touch("var/lib/dpkg/status")
	s.False(services.HostAttention(), "packages changed since: those may be installed now")
	s.Equal(runs, s.script.total())
}

// A clock systemd says isn't in sync is checked against the reference, as
// OrbStack's are: the Mac sets them, with no sync service in the machine.
func (s *HostMaintenanceTestSuite) TestAClockNoServiceSyncsIsCheckedAgainstTheReference() {
	asked := 0
	reference := func(ahead time.Duration, err error) {
		services.Host.ClockOffset = func(context.Context) (time.Duration, error) {
			asked++
			return ahead, err
		}
	}

	reference(time.Hour, nil)
	m := services.HostMaintenance(context.Background())
	s.Equal(ptr(true), m.ClockSynced)
	s.Nil(m.ClockOffset)
	s.Zero(asked, "in sync by systemd's account: nothing to check")

	s.script.set("timedatectl", []byte("no\n"), nil)
	for name, c := range map[string]struct {
		ahead  time.Duration
		err    error
		synced bool
		offset *float64
	}{
		"right to within a second": {ahead: 300 * time.Millisecond, synced: true, offset: ptr(0.3)},
		"42 seconds slow":          {ahead: -42 * time.Second, synced: false, offset: ptr(-42.0)},
		"no answer":                {err: errors.New("offline"), synced: false},
	} {
		reference(c.ahead, c.err)
		m := services.HostMaintenance(context.Background())
		s.Equal(ptr(c.synced), m.ClockSynced, name)
		s.Equal(c.offset, m.ClockOffset, name)
	}
}

// The Server page asks for maintenance once it's loaded, so an apt run
// never holds it up, and its polls never ask for it.
func (s *HostMaintenanceTestSuite) TestTheServerPageAsksForItAfterLoading() {
	s.RefreshDatabase()
	s.FakeDocker()
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/server")
	s.NotContains(page.Props, "maintenance")
	page.AssertDeferredProps(map[string][]string{"default": {"maintenance"}})
	s.NotContains(s.partial(b, "/server", "Server", "latest,series"), "maintenance")
	s.Zero(s.script.total(), "nothing ran on the host")

	m, _ := s.partial(b, "/server", "Server", "maintenance")["maintenance"].(map[string]any)
	s.Equal(true, m["clock_synced"])
	s.Equal([]any{}, m["updates"].(map[string]any)["security"])
	s.NotEmpty(m["read_at"])
}

// The dashboard shows the Server page's last read and never reads the host
// itself, except when its Refresh asks.
func (s *HostMaintenanceTestSuite) TestTheDashboardShowsTheLastRead() {
	s.RefreshDatabase()
	s.FakeDocker()
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/dashboard")
	s.Nil(page.Props["maintenance"], "nothing read since Echowa started")
	s.NotContains(page.Props, "freshMaintenance")

	read, _ := s.partial(b, "/server", "Server", "maintenance")["maintenance"].(map[string]any)
	runs := s.script.total()
	_, page = b.visit("/dashboard")
	s.Equal(read, page.Props["maintenance"], "the Server page's read, as it was")
	for _, only := range []string{"apps,activity", "metrics,cpu"} {
		s.NotContains(s.partial(b, "/dashboard", "Dashboard", only), "maintenance")
	}
	s.Equal(runs, s.script.total(), "the dashboard ran nothing on the host")

	s.script.set("timedatectl", []byte("no\n"), nil)
	fresh, _ := s.partial(b, "/dashboard", "Dashboard", "freshMaintenance")["freshMaintenance"].(map[string]any)
	s.Equal(false, fresh["clock_synced"], "Refresh reads the host")
	_, page = b.visit("/dashboard")
	s.Equal(fresh, page.Props["maintenance"], "and keeps what it read")
}

// The rail's dot is on every signed-in page, so rendering one never runs a
// command: the dot reads files and apt's last run, whichever page made it.
func (s *HostMaintenanceTestSuite) TestTheRailsDotNeverRunsACommand() {
	s.RefreshDatabase()
	s.FakeDocker()
	s.script.set("apt-get", hostFixture(s.T(), "ubuntu-24.04", "apt-upgrades.txt"), nil)

	b := signedIn(s.T(), &s.TestCase)
	_, page := newBrowser(s.T(), &s.TestCase).visit("/login")
	s.Equal(false, page.Props["serverAttention"], "guests get no dot")

	_, page = b.visit("/settings")
	s.Equal(false, page.Props["serverAttention"], "no apt run yet")
	s.Zero(s.script.total())

	s.partial(b, "/server", "Server", "maintenance")
	runs := s.script.total()
	_, page = b.visit("/settings")
	s.Equal(true, page.Props["serverAttention"], "the Server page's run found security updates")
	s.Equal(true, s.partial(b, "/server", "Server", "serverAttention")["serverAttention"], "asked for again once maintenance arrives")
	s.NotContains(s.partial(b, "/server", "Server", "latest,series"), "serverAttention", "polls don't ask")
	s.Equal(runs, s.script.total())
}

// partial reloads a page for only some of its props, as usePoll and
// Inertia's deferred props do.
func (s *HostMaintenanceTestSuite) partial(b *browser, uri, component, only string) map[string]any {
	resp := b.request(nethttp.MethodGet, uri, nil, true, map[string]string{
		"X-Inertia-Partial-Component": component,
		"X-Inertia-Partial-Data":      only,
	})
	resp.AssertOk()
	body, err := resp.Content()
	s.Require().NoError(err)

	return gonertia.AssertFromString(s.T(), body).Props
}

// touch moves a file's time on, as installing a package does to dpkg's
// status and apt update to its lists.
func (s *HostMaintenanceTestSuite) touch(name string) {
	s.machine[name].ModTime = s.machine[name].ModTime.Add(time.Minute)
}

// hostFixture reads a file captured from a real machine (app/host).
func hostFixture(t *testing.T, release, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(path.App("host", "testdata", release, name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// distroInfo is a distro-info CSV listing one Ubuntu series, ending on
// eol: the real one's dates would stop being in the future.
func distroInfo(series string, eol time.Time) []byte {
	day := eol.Format(time.DateOnly)

	return []byte("version,codename,series,created,release,eol,eol-server,eol-esm,eol-legacy\n" +
		"24.04 LTS,Noble Numbat," + series + ",2023-10-12,2024-04-25," + day + "," + day + ",2034-04-25,2039-04-28\n")
}

// hostScript answers maintenance's commands by name, and counts them.
type hostScript struct {
	sync.Mutex
	out   map[string][]byte
	err   map[string]error
	calls map[string]int
	// hold, while open, keeps apt-get running.
	hold chan struct{}
}

func (h *hostScript) run(ctx context.Context, name string, _ ...string) ([]byte, error) {
	h.Lock()
	h.calls[name]++
	out, err, hold := h.out[name], h.err[name], h.hold
	h.Unlock()
	if name == "apt-get" && hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return out, err
}

func (h *hostScript) set(name string, out []byte, err error) {
	h.Lock()
	defer h.Unlock()
	h.out[name], h.err[name] = out, err
}

func (h *hostScript) holdApt() {
	h.Lock()
	defer h.Unlock()
	h.hold = make(chan struct{})
}

func (h *hostScript) release() {
	h.Lock()
	defer h.Unlock()
	if h.hold != nil {
		close(h.hold)
		h.hold = nil
	}
}

func (h *hostScript) count(name string) int {
	h.Lock()
	defer h.Unlock()

	return h.calls[name]
}

func (h *hostScript) total() int {
	h.Lock()
	defer h.Unlock()
	total := 0
	for _, n := range h.calls {
		total += n
	}

	return total
}
