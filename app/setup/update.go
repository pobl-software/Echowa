package setup

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"goravel/app/release"
	"goravel/app/update"
)

// UpdateMain runs `echowa update` with args and returns the exit code.
func UpdateMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("echowa update", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() {
		fmt.Fprint(out, `Usage: sudo echowa update [options]

Updates Echowa to the newest release: checks its signature, backs up the
database, swaps the binary, restarts Echowa, and rolls back if the new version
doesn't come up.

`)
		fs.PrintDefaults()
	}
	check := fs.Bool("check", false, "only show the running and newest versions")
	fromRequest := fs.Bool("from-request", false, "update to the version Echowa asked for (echowa-update.service)")
	version := fs.String("version", "", "update to this release instead of the newest, e.g. v1.2.0")
	allowDowngrade := fs.Bool("allow-downgrade", false, "with --version, allow an older release")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *check {
		if err := release.Check(context.Background(), out); err != nil {
			fmt.Fprintln(out, "echowa update:", err)
			return 1
		}
		return 0
	}
	if fs.NArg() > 0 || (*fromRequest && *version != "") || (*allowDowngrade && *version == "") || (*version != "" && !release.IsRelease(*version)) {
		fs.Usage()
		return 2
	}

	if os.Geteuid() != 0 {
		fmt.Fprintln(out, "echowa update: run as root: sudo echowa update")
		return 1
	}

	u := &updater{target: *version, allowDowngrade: *allowDowngrade}
	if *fromRequest {
		requested, err := update.Take()
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(out, "No update has been requested.")
			return 0
		}
		if err != nil {
			fmt.Fprintln(out, "echowa update:", err)
			return 1
		}
		u.target, u.requested = requested, true
	}

	return u.execute(context.Background(), out)
}

// updater moves this server from the running release to target, as root.
// It runs as the installed binary, the old version, so the old version's
// key decides what gets installed.
type updater struct {
	*steps
	status         *statusWriter
	target         string // "" means the newest
	requested      bool   // from the UI: target must be the newest
	allowDowngrade bool
}

func (u *updater) execute(ctx context.Context, out io.Writer) int {
	u.status = &statusWriter{out: out, s: update.Status{State: update.Running, From: release.Version, To: u.target, StartedAt: time.Now().UTC()}}
	u.steps = &steps{ctx: ctx, ui: newUI(u.status)}
	u.status.flush()

	state, message := u.update()
	u.status.finish(state, message)
	if state != update.Succeeded {
		return 1
	}

	return 0
}

// update does the work, and says how it ended.
func (u *updater) update() (update.State, string) {
	if !release.IsRelease(release.Version) {
		return update.Failed, fmt.Sprintf("Echowa %s is a dev build, which doesn't update itself. Install a release with echowa setup.", release.Version)
	}

	staged, version, err := u.download()
	if errors.Is(err, errUpToDate) {
		if u.target != "" && !u.requested {
			return update.Succeeded, fmt.Sprintf("Echowa %s is already running.", release.Version)
		}
		return update.Succeeded, fmt.Sprintf("Echowa %s is the newest release.", release.Version)
	}
	if err != nil {
		return update.Failed, err.Error()
	}
	defer os.Remove(staged)
	u.status.to(version)

	backup, err := u.backupDatabase(staged, version)
	if err != nil {
		return update.Failed, err.Error()
	}
	if err := swapIn(staged); err != nil {
		return update.Failed, err.Error()
	}
	u.ui.say("Installed %s %s; the previous release is %s.previous", Bin, version, Bin)

	// The new release's setup writes its units, restarts Echowa and waits
	// for it to answer as the new version.
	err = u.stream(nil, Bin, "setup")
	if err == nil {
		return update.Succeeded, fmt.Sprintf("Updated from %s to %s.", release.Version, version)
	}
	u.ui.say("The update failed: %v", err)
	if rollbackErr := u.rollback(backup); rollbackErr != nil {
		return update.Failed, fmt.Sprintf("%s didn't come up (%v), and rolling back failed too: %v. Check journalctl -u echowa -u echowa-update.", version, err, rollbackErr)
	}

	return update.RolledBack, fmt.Sprintf("%s didn't start, so Echowa went back to %s and the database from before the update. The updater's log shows why.", version, release.Version)
}

var errUpToDate = errors.New("up to date")

// download fetches the release to install, checks it, and stages it next to
// the binary it replaces, so the swap is a rename. It returns the staged
// file and its version.
func (u *updater) download() (string, string, error) {
	key, err := release.PublicKey()
	if err != nil {
		return "", "", err
	}
	channel, err := release.Channel()
	if err != nil {
		return "", "", err
	}
	client := &nethttp.Client{Timeout: 10 * time.Minute}
	arch := runtime.GOARCH
	platform := runtime.GOOS + "/" + arch

	var version, name, sha256 string
	if u.target != "" && !u.requested {
		// A release named from a shell. Its signature names the version
		// and file, which is all there is to check without its latest.json.
		version, name = u.target, "releases/"+u.target+"/echowa-linux-"+arch
		if version == release.Version {
			return "", "", errUpToDate
		}
		if !release.Newer(version, release.Version) && !u.allowDowngrade {
			return "", "", fmt.Errorf("%s is older than the running %s (--allow-downgrade, and restore a backup from before %s)", version, release.Version, release.Version)
		}
	} else {
		u.ui.say("Checking %s for the newest release", channel)
		m, err := release.Latest(u.ctx, client, channel, key)
		if err != nil {
			return "", "", err
		}
		if u.requested && m.Version != u.target {
			return "", "", fmt.Errorf("%s was requested, but the newest release is %s. Check for updates again", u.target, m.Version)
		}
		if !release.Newer(m.Version, release.Version) {
			return "", "", errUpToDate
		}
		b, ok := m.Binaries[platform]
		if !ok {
			return "", "", fmt.Errorf("%s has no build for %s", m.Version, platform)
		}
		version, name, sha256 = m.Version, b.Path, b.SHA256
	}

	u.ui.say("Downloading Echowa %s for %s", version, platform)
	data, err := release.Download(u.ctx, client, channel, key, version, name, sha256, 512<<20)
	if err != nil {
		return "", "", err
	}
	u.ui.say("Signature and checksum ok")

	staged := Bin + ".download"
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		return "", "", err
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return "", "", err
	}
	// It must run here, and be the release it claims to be.
	got, err := u.output(staged, "version")
	if err != nil || got != "echowa "+version {
		os.Remove(staged)
		return "", "", fmt.Errorf("the downloaded release doesn't run as %s here (%q, %v)", version, got, err)
	}

	return staged, version, nil
}

// rollback puts the previous binary and the database from before the
// update back, then runs the previous release's setup, which rewrites its
// own units and starts it.
func (u *updater) rollback(backup string) error {
	u.ui.say("Rolling back to %s", release.Version)
	if err := u.quiet(nil, "systemctl", "stop", "echowa"); err != nil {
		return err
	}
	if err := os.Rename(Bin+".previous", Bin); err != nil {
		return err
	}
	if backup != "" {
		if err := u.restoreDatabase(backup); err != nil {
			return err
		}
	}

	return u.stream(nil, Bin, "setup")
}

// statusWriter keeps update.Status current: in the status file for the
// Settings page, and on out for the journal. Each line written becomes a
// log line.
type statusWriter struct {
	mu      sync.Mutex
	out     io.Writer
	s       update.Status
	partial []byte
}

const maxLogLines = 1000

func (w *statusWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.out.Write(p)
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.s.Log = append(w.s.Log, plain(string(w.partial[:i])))
		w.partial = w.partial[i+1:]
	}
	if len(w.s.Log) > maxLogLines {
		w.s.Log = w.s.Log[len(w.s.Log)-maxLogLines:]
	}
	w.flushLocked()

	return len(p), nil
}

func (w *statusWriter) to(version string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.s.To = version
	w.flushLocked()
}

func (w *statusWriter) finish(state update.State, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now().UTC()
	w.s.State, w.s.Message, w.s.FinishedAt = state, message, &now
	fmt.Fprintln(w.out, message)
	w.flushLocked()
}

func (w *statusWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
}

// flushLocked writes the status file. Failing to is logged, never fatal:
// the update matters more than its report.
func (w *statusWriter) flushLocked() {
	if err := update.WriteStatus(w.s); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(w.out, "warning: writing the update status:", err)
	}
}

// plain strips the terminal colours setup's log lines carry.
func plain(line string) string {
	return strings.NewReplacer("\033[1m", "", "\033[0m", "").Replace(strings.TrimRight(line, "\r"))
}
