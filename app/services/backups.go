package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/backups"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/release"
	"goravel/app/templates"
)

// Limits on a backup (ADR 0034).
const (
	// MaxBackupOutput is how much of the dump's stderr is kept, from its end.
	MaxBackupOutput = 16 << 10
	// BackupTimeout ends a backup that's still going.
	BackupTimeout = 12 * time.Hour
	// cleanupTimeout is how long deleting what a failed backup left gets.
	cleanupTimeout = 30 * time.Second
)

var (
	// BackupLockWait is how long a backup waits for a deploy or restore of
	// its app to finish before it's skipped. Tests shorten it.
	BackupLockWait = 15 * time.Minute
	// BackupProgressInterval is how often a running backup's size is saved
	// for the page.
	BackupProgressInterval = 5 * time.Second
)

// ErrBackupRunning means a backup of the app is already going, so Back up
// now doesn't start another.
var ErrBackupRunning = errors.New("a backup of this app is already running")

// errDumpFailed ends the upload when the dump does, so the multipart
// upload is aborted and no object appears.
var errDumpFailed = errors.New("the dump failed")

// backupStart separates this process's backups from those an earlier one
// left running. Stored times have second precision, hence the truncation.
var backupStart = time.Now().Truncate(time.Second)

// runningBackups counts each app's backups in progress, and waits for them
// in tests. One Echowa process runs per server (ADR 0013).
var runningBackups = struct {
	sync.Mutex
	apps map[uint]int
	wg   sync.WaitGroup
}{apps: map[uint]int{}}

// AppBackup is how the app's template backs it up, or nil for a custom
// app or a template without backups, such as Redis.
func AppBackup(app *models.App) *templates.Backup {
	if app.Template == nil {
		return nil
	}
	tmpl, ok := templates.Find(*app.Template)
	if !ok {
		return nil
	}

	return tmpl.Backup
}

// BackUpNow starts a backup of the app to loc in the background, unless
// one is already going (ErrBackupRunning).
func BackUpNow(app *models.App, loc *models.StorageLocation) (*models.Backup, error) {
	return startBackup(app, loc, models.BackupByHand, nil, true)
}

// WaitForBackups waits until no backup is going. For tests.
func WaitForBackups() {
	runningBackups.wg.Wait()
}

func startBackup(app *models.App, loc *models.StorageLocation, trigger models.BackupTrigger, scheduleID *uint, alone bool) (*models.Backup, error) {
	b := AppBackup(app)
	if b == nil {
		return nil, fmt.Errorf("%s has no backups", app.Name)
	}
	runningBackups.Lock()
	defer runningBackups.Unlock()
	if alone && runningBackups.apps[app.ID] > 0 {
		return nil, ErrBackupRunning
	}

	rec := &models.Backup{
		AppID:             app.ID,
		StorageLocationID: loc.ID,
		BackupScheduleID:  scheduleID,
		Trigger:           trigger,
		Status:            models.BackupRunning,
		StartedAt:         carbon.NewDateTime(carbon.Now()),
	}
	if err := facades.Orm().Query().Create(rec); err != nil {
		return nil, err
	}

	runningBackups.apps[app.ID]++
	runningBackups.wg.Add(1)
	go func() {
		defer func() {
			runningBackups.Lock()
			runningBackups.apps[app.ID]--
			runningBackups.Unlock()
			runningBackups.wg.Done()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), BackupTimeout)
		defer cancel()
		finishBackup(rec, lockedBackup(ctx, app, b, loc, rec))
	}()

	return rec, nil
}

// backupResult is how a backup ended.
type backupResult struct {
	status  models.BackupStatus
	message string
	output  string
	size    int64
	sha256  string
}

// lockedBackup waits for the app's lock, then dumps it.
func lockedBackup(ctx context.Context, app *models.App, b *templates.Backup, loc *models.StorageLocation, rec *models.Backup) backupResult {
	waitCtx, cancel := context.WithTimeout(ctx, BackupLockWait)
	defer cancel()
	unlock, err := WaitLockApp(waitCtx, app.ID, WorkBackup)
	if err != nil {
		holder := AppBusy(app.ID)
		if holder == "" {
			holder = WorkDeploy
		}
		return backupResult{status: models.BackupSkipped, message: fmt.Sprintf("Waited %s for %s of this app to finish.", waited(BackupLockWait), holder.Describe())}
	}
	defer unlock()

	return dump(ctx, app, b, loc, rec)
}

func waited(d time.Duration) string {
	if d >= time.Minute {
		return minutes(int(d / time.Minute))
	}

	return d.String()
}

// dump streams the app's database through the pipe into the bucket at
// the backup's key, then writes the manifest beside it (ADR 0034). The
// caller holds the app's lock.
func dump(ctx context.Context, app *models.App, b *templates.Backup, loc *models.StorageLocation, rec *models.Backup) backupResult {
	service := app.ServiceName()
	out, err := Docker().Exec(ctx, service, b.Version)
	if errors.Is(err, docker.ErrNotRunning) {
		return backupResult{status: models.BackupSkipped, message: "The app has no running task to back up."}
	}
	if err != nil {
		return backupResult{status: models.BackupFailed, message: "Couldn't read the server's version: " + err.Error()}
	}
	version, err := templates.ServerVersion(out)
	if err != nil {
		return backupResult{status: models.BackupFailed, message: "Couldn't read the server's version: " + err.Error()}
	}
	env, err := EnvVars(ctx, app.ID)
	if err != nil {
		return backupResult{status: models.BackupFailed, message: err.Error()}
	}

	key := backups.Key(loc.Prefix, app.Name, rec.StartedAt.StdTime(), rec.ID, b.Extension)
	// Saved first, so recovery can abort the upload if Echowa stops.
	if err := updateBackup(rec.ID, map[string]any{"key": key, "server_version": version}); err != nil {
		return backupResult{status: models.BackupFailed, message: err.Error()}
	}
	store := backups.Open(loc)

	pr, pw := io.Pipe()
	uploaded := make(chan error, 1)
	go func() {
		err := store.Put(ctx, key, pr)
		// A Put that gives up stops reading, so the dump's writes fail
		// rather than block.
		pr.CloseWithError(err)
		uploaded <- err
	}()
	counted := &countingHash{h: sha256.New()}
	stdout := io.MultiWriter(pw, counted)
	var gz *gzip.Writer
	if b.Gzip {
		gz = gzip.NewWriter(stdout)
		stdout = gz
	}
	stopProgress := saveProgress(rec.ID, counted)

	stderr := &docker.TailBuffer{Limit: MaxBackupOutput}
	code, dumpErr := Docker().ExecStream(ctx, service, b.Dump, nil, stdout, stderr)
	if dumpErr == nil && code == 0 && gz != nil {
		dumpErr = gz.Close()
	}
	if dumpErr != nil || code != 0 {
		pw.CloseWithError(errDumpFailed)
	} else {
		pw.Close()
	}
	uploadErr := <-uploaded
	stopProgress()

	result := backupResult{status: models.BackupFailed, output: stderr.String(), size: counted.n.Load()}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.message = fmt.Sprintf("Stopped after %d hours.", int(BackupTimeout/time.Hour))
		return result
	case uploadErr != nil && !errors.Is(uploadErr, errDumpFailed):
		result.message = fmt.Sprintf("The upload to %s failed: %s", loc.Name, backups.Describe(uploadErr))
		return result
	case dumpErr != nil:
		result.message = "The dump failed: " + dumpErr.Error()
		return result
	case code != 0:
		result.message = fmt.Sprintf("The dump exited with status %d.", code)
		return result
	}

	result.sha256 = hex.EncodeToString(counted.h.Sum(nil))
	compression := "none"
	if b.Gzip {
		compression = "gzip"
	}
	manifest, err := json.MarshalIndent(backups.Manifest{
		Format:        backups.ManifestFormat,
		App:           app.Name,
		Template:      *app.Template,
		ServerVersion: version,
		Database:      env[b.Database],
		Object:        path.Base(key),
		Compression:   compression,
		Size:          result.size,
		SHA256:        result.sha256,
		StartedAt:     rec.StartedAt.StdTime().UTC(),
		FinishedAt:    time.Now().UTC(),
		EchowaVersion: release.Version,
	}, "", "  ")
	if err == nil {
		err = store.Put(ctx, backups.ManifestKey(key), bytes.NewReader(manifest))
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := store.Delete(cleanup, key); err != nil {
			facades.Log().Error(fmt.Errorf("backup %d: delete the dump without a manifest: %w", rec.ID, err))
		}
		result.message = "The dump uploaded, but its manifest didn't, so the dump was deleted: " + backups.Describe(err)
		return result
	}
	result.status = models.BackupSucceeded

	return result
}

// countingHash hashes what's written and counts it, for the page to read
// while the dump runs.
type countingHash struct {
	h hash.Hash
	n atomic.Int64
}

func (c *countingHash) Write(p []byte) (int, error) {
	c.h.Write(p)
	c.n.Add(int64(len(p)))

	return len(p), nil
}

// saveProgress saves the size uploaded so far every BackupProgressInterval
// until the returned func is called.
func saveProgress(id uint, counted *countingHash) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(BackupProgressInterval)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if err := updateBackup(id, map[string]any{"size_bytes": counted.n.Load()}); err != nil {
					facades.Log().Error(fmt.Errorf("backup %d progress: %w", id, err))
				}
			}
		}
	})

	return func() {
		close(done)
		wg.Wait()
	}
}

func updateBackup(id uint, fields map[string]any) error {
	_, err := facades.Orm().Query().Model(&models.Backup{}).Where("id", id).Update(fields)

	return err
}

func finishBackup(rec *models.Backup, result backupResult) {
	var message *string
	if result.message != "" {
		message = &result.message
	}
	err := updateBackup(rec.ID, map[string]any{
		"status":      result.status,
		"message":     message,
		"output":      result.output,
		"size_bytes":  result.size,
		"sha256":      result.sha256,
		"finished_at": carbon.NewDateTime(carbon.Now()),
	})
	if err != nil {
		facades.Log().Error(fmt.Errorf("backup %d: %w", rec.ID, err))
	}
}

// RecoverBackups marks backups an earlier Echowa left running as
// interrupted, and aborts their uploads, whose parts the provider would
// otherwise keep.
func RecoverBackups(ctx context.Context) error {
	var running []models.Backup
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").Where("status", models.BackupRunning).Get(&running)
	if err != nil {
		return err
	}
	for i := range running {
		rec := &running[i]
		// Compared in Go, as deploy.Recover does.
		if rec.StartedAt != nil && !rec.StartedAt.StdTime().Before(backupStart) {
			continue
		}
		if rec.Key != "" && rec.StorageLocation != nil {
			abort, cancel := context.WithTimeout(ctx, cleanupTimeout)
			if err := backups.Open(rec.StorageLocation).AbortUploads(abort, rec.Key); err != nil {
				facades.Log().Error(fmt.Errorf("backup %d: abort its upload: %w", rec.ID, err))
			}
			cancel()
		}
		finishBackup(rec, backupResult{status: models.BackupInterrupted, message: "Echowa restarted while this ran, so the dump was abandoned."})
	}

	return nil
}

// DeleteBackup deletes the backup's manifest and dump, then its record. The
// manifest goes first: a dump without one is only incomplete, while a
// manifest without its dump would offer a restore that can't work.
func DeleteBackup(ctx context.Context, rec *models.Backup, loc *models.StorageLocation) error {
	if rec.Key != "" {
		store := backups.Open(loc)
		if err := store.Delete(ctx, backups.ManifestKey(rec.Key)); err != nil {
			return err
		}
		if err := store.Delete(ctx, rec.Key); err != nil {
			return err
		}
	}
	_, err := facades.Orm().WithContext(ctx).Query().Delete(rec)

	return err
}

// BackupSummary is a row of the Backups section's history, without the
// output.
type BackupSummary struct {
	ID       uint                 `json:"id"`
	Trigger  models.BackupTrigger `json:"trigger"`
	Status   models.BackupStatus  `json:"status"`
	Location string               `json:"location"`
	// Name is the dump's file name, for a download.
	Name          string           `json:"name"`
	SizeBytes     int64            `json:"size_bytes"`
	ServerVersion string           `json:"server_version"`
	Message       *string          `json:"message"`
	StartedAt     *carbon.DateTime `json:"started_at"`
	FinishedAt    *carbon.DateTime `json:"finished_at"`
}

// StorageChoice is a location Back up now offers.
type StorageChoice struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
}

// BackupsSection is a template app's Backups section (ADR 0034).
type BackupsSection struct {
	// Database is the one database a backup covers.
	Database string `json:"database"`
	// Locations come with the one the app last backed up to first.
	Locations []StorageChoice `json:"locations"`
	Backups   []BackupSummary `json:"backups"`
}

// AppBackups is the app's Backups section, with its newest limit backups.
func AppBackups(ctx context.Context, app *models.App, limit int) (*BackupsSection, error) {
	b := AppBackup(app)
	if b == nil {
		return nil, nil
	}
	env, err := EnvVars(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	var locations []models.StorageLocation
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&locations); err != nil {
		return nil, err
	}
	var rows []models.Backup
	err = facades.Orm().WithContext(ctx).Query().With("StorageLocation").
		Where("app_id", app.ID).Order("id desc").Limit(limit).Get(&rows)
	if err != nil {
		return nil, err
	}

	section := &BackupsSection{Database: env[b.Database], Locations: []StorageChoice{}, Backups: []BackupSummary{}}
	for _, loc := range locations {
		choice := StorageChoice{ID: loc.ID, Name: loc.Name, Bucket: loc.Bucket, Prefix: loc.Prefix}
		if len(rows) > 0 && rows[0].StorageLocationID == loc.ID {
			section.Locations = append([]StorageChoice{choice}, section.Locations...)
		} else {
			section.Locations = append(section.Locations, choice)
		}
	}
	for _, rec := range rows {
		summary := BackupSummary{
			ID: rec.ID, Trigger: rec.Trigger, Status: rec.Status, SizeBytes: rec.SizeBytes,
			ServerVersion: rec.ServerVersion, Message: rec.Message, StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt,
		}
		if rec.Key != "" {
			summary.Name = path.Base(rec.Key)
		}
		if rec.StorageLocation != nil {
			summary.Location = rec.StorageLocation.Name
		}
		section.Backups = append(section.Backups, summary)
	}

	return section, nil
}
