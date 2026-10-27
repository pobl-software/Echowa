package services

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"sync"
	"time"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/release"
	"goravel/app/update"
)

var (
	ErrDevBuild       = errors.New("this is a dev build, which doesn't update itself")
	ErrNotNewest      = errors.New("that isn't the newest release; check for updates again")
	ErrDeploysRunning = errors.New("a deployment is queued or running; update when it has finished")
	ErrUpdating       = errors.New("an update is already under way")
)

// LatestRelease is the channel's newest release, signature checked.
type LatestRelease struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	Notes       string    `json:"notes"`
}

// UpdateCheck is the last look at the release channel.
type UpdateCheck struct {
	Checking  bool           `json:"checking"`
	CheckedAt *time.Time     `json:"checked_at"`
	Latest    *LatestRelease `json:"latest"`
	Error     string         `json:"error"`
}

// FetchLatestRelease reads the channel's latest.json and release notes,
// both checked against the release key. Tests replace it.
var FetchLatestRelease = func(ctx context.Context) (LatestRelease, error) {
	key, err := release.PublicKey()
	if err != nil {
		return LatestRelease{}, err
	}
	channel, err := release.Channel()
	if err != nil {
		return LatestRelease{}, err
	}
	client := &nethttp.Client{Timeout: 30 * time.Second}
	m, err := release.Latest(ctx, client, channel, key)
	if err != nil {
		return LatestRelease{}, err
	}
	latest := LatestRelease{Version: m.Version, PublishedAt: m.PublishedAt}
	if m.Notes != "" {
		notes, err := release.Download(ctx, client, channel, key, m.Version, m.Notes, "", 64<<10)
		if err != nil {
			return LatestRelease{}, err
		}
		latest.Notes = string(notes)
	}

	return latest, nil
}

// The last check, in memory like the last prune: one Echowa runs per
// server, and the next check is never more than a day away.
var updateCheck = struct {
	sync.Mutex
	UpdateCheck
}{}

// UpdateCheckState returns the last check. The first call starts one, so
// the Settings page has something to show soon after Echowa starts.
func UpdateCheckState() UpdateCheck {
	updateCheck.Lock()
	state := updateCheck.UpdateCheck
	updateCheck.Unlock()
	if state.CheckedAt == nil && !state.Checking && release.IsRelease(release.Version) {
		StartUpdateCheck()
		state.Checking = true
	}

	return state
}

// StartUpdateCheck checks the channel in the background.
func StartUpdateCheck() {
	if !beginUpdateCheck() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		finishUpdateCheck(FetchLatestRelease(ctx))
	}()
}

// CheckForUpdate checks the channel and waits for the answer: the daily
// schedule. Dev builds never check.
func CheckForUpdate(ctx context.Context) {
	if !beginUpdateCheck() {
		return
	}
	finishUpdateCheck(FetchLatestRelease(ctx))
}

func beginUpdateCheck() bool {
	if !release.IsRelease(release.Version) {
		return false
	}
	updateCheck.Lock()
	defer updateCheck.Unlock()
	if updateCheck.Checking {
		return false
	}
	updateCheck.Checking = true

	return true
}

func finishUpdateCheck(latest LatestRelease, err error) {
	now := time.Now()
	updateCheck.Lock()
	defer updateCheck.Unlock()
	updateCheck.Checking, updateCheck.CheckedAt = false, &now
	if err != nil {
		// The last good answer stays: a flaky network shouldn't hide a
		// release already seen.
		updateCheck.Error = err.Error()
		facades.Log().Warning(fmt.Errorf("update check: %w", err))
		return
	}
	updateCheck.Latest, updateCheck.Error = &latest, ""
}

// ResetUpdateCheck forgets the last check, for tests.
func ResetUpdateCheck() {
	updateCheck.Lock()
	defer updateCheck.Unlock()
	updateCheck.UpdateCheck = UpdateCheck{}
}

// staleAfter is how long a "running" status is believed. The updater's
// unit gives up after 30 minutes, so an older one was cut short, by a
// reboot say, and mustn't block updates for good.
const staleAfter = 45 * time.Minute

// UpdateInProgress reports whether an update is waiting for the updater or
// running, and the status file's last report.
func UpdateInProgress() (requested string, status *update.Status, busy bool) {
	status, err := update.ReadStatus()
	if err != nil {
		facades.Log().Warning(fmt.Errorf("reading the update status: %w", err))
	}
	requested, waiting := update.Requested()
	running := status != nil && status.State == update.Running && time.Since(status.StartedAt) < staleAfter

	return requested, status, waiting || running
}

// RequestUpdate asks the root updater to move to version, the newest
// release the last check found (ADR 0021).
func RequestUpdate(ctx context.Context, version string) error {
	if !release.IsRelease(release.Version) {
		return ErrDevBuild
	}
	updateCheck.Lock()
	latest := updateCheck.Latest
	updateCheck.Unlock()
	if latest == nil || latest.Version != version || !release.Newer(version, release.Version) {
		return ErrNotNewest
	}
	if _, _, busy := UpdateInProgress(); busy {
		return ErrUpdating
	}
	running, err := facades.Orm().WithContext(ctx).Query().Model(&models.Deployment{}).
		WhereIn("status", []any{models.DeploymentQueued, models.DeploymentPulling, models.DeploymentDeploying}).Count()
	if err != nil {
		return err
	}
	if running > 0 {
		return ErrDeploysRunning
	}
	if err := update.Request(version); err != nil {
		if errors.Is(err, update.ErrRequested) {
			return ErrUpdating
		}
		return err
	}
	facades.Log().Info(fmt.Sprintf("update: requested %s (running %s)", version, release.Version))

	return nil
}
