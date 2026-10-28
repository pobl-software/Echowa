package services

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/backups"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// NewAppWait is how long a restore into a new app waits for the app's
// first deploy, and then for its server to take connections.
var NewAppWait = 10 * time.Minute

// NewAppPoll is how often it looks. Tests shorten it.
var NewAppPoll = 2 * time.Second

// StorageChoices lists every location by name, for the New app page's
// From a backup.
func StorageChoices(ctx context.Context) ([]StorageChoice, error) {
	var locations []models.StorageLocation
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&locations); err != nil {
		return nil, err
	}
	choices := make([]StorageChoice, len(locations))
	for i, loc := range locations {
		choices[i] = StorageChoice{ID: loc.ID, Name: loc.Name, Bucket: loc.Bucket, Prefix: loc.Prefix}
	}

	return choices, nil
}

// locationRoot is where a location's app folders are.
func locationRoot(loc *models.StorageLocation) string {
	return backups.JoinKey(loc.Prefix, "")
}

// LocationFolders lists the app folders in loc, one for each app name that
// has backed up to it, from this server or another with the same prefix.
func LocationFolders(ctx context.Context, loc *models.StorageLocation) ([]string, error) {
	root := locationRoot(loc)
	listing, err := backups.Open(loc).List(ctx, root, "/")
	if err != nil {
		return nil, err
	}
	folders := []string{}
	for _, prefix := range listing.Prefixes {
		if name := strings.TrimSuffix(strings.TrimPrefix(prefix, root), "/"); name != "" {
			folders = append(folders, name)
		}
	}
	slices.Sort(folders)

	return folders, nil
}

// FolderBackups lists, newest first, the dumps with a manifest in one app
// folder of loc.
func FolderBackups(ctx context.Context, loc *models.StorageLocation, folder string) ([]BucketBackup, error) {
	if folder == "" || strings.Contains(folder, "/") {
		return []BucketBackup{}, nil
	}
	found, _, err := listFolder(ctx, loc, locationRoot(loc)+folder+"/")

	return found, err
}

// FolderSource is the dump whose manifest is at key, in any app folder of
// loc, for a restore into a new app.
func FolderSource(ctx context.Context, loc *models.StorageLocation, key string) (*RestoreSource, error) {
	rest, ok := strings.CutPrefix(key, locationRoot(loc))
	folder, name, _ := strings.Cut(rest, "/")
	if !ok || folder == "" || name == "" || strings.Contains(name, "/") || path.Ext(name) != ".json" {
		return nil, refuse("Choose a backup in %s.", loc.Name)
	}
	src, err := readManifest(ctx, loc, key)
	if err != nil {
		return nil, err
	}
	if tmpl, ok := templates.Find(src.Manifest.Template); !ok || tmpl.Backup == nil {
		return nil, refuse("This is a backup of a %s database, which Echowa can't restore.", templateName(src.Manifest.Template))
	}

	return src, nil
}

// NewAppPreview is a backup as the New app page shows it before restoring
// it into a new app.
type NewAppPreview struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	// App is the name of the app the backup was taken from.
	App           string    `json:"app"`
	Template      string    `json:"template"`
	TemplateName  string    `json:"template_name"`
	Database      string    `json:"database"`
	ServerVersion string    `json:"server_version"`
	SizeBytes     int64     `json:"size_bytes"`
	StartedAt     time.Time `json:"started_at"`
	Atomic        bool      `json:"atomic"`
	// DefaultTag is the template's, which the new app runs unless the
	// form says otherwise.
	DefaultTag string `json:"default_tag"`
}

// PreviewNewApp describes src, which FolderSource has checked.
func PreviewNewApp(src *RestoreSource) NewAppPreview {
	m := src.Manifest
	tmpl, _ := templates.Find(m.Template)

	return NewAppPreview{
		Name: path.Base(src.Key), Location: src.Location.Name, App: m.App, Template: m.Template, TemplateName: tmpl.Name,
		Database: m.Database, ServerVersion: m.ServerVersion, SizeBytes: m.Size, StartedAt: m.StartedAt,
		Atomic: tmpl.Backup.Atomic, DefaultTag: tmpl.DefaultTag,
	}
}

var tagVersion = regexp.MustCompile(`^\d+(?:\.\d+)*`)

// CheckRestoreTag refuses a tag that names a server older than the
// backup's, compared as far as the tag goes: 17 takes a 17.2 backup. A tag
// with no version, like latest, is checked once the app runs it.
func CheckRestoreTag(tmpl templates.Template, m backups.Manifest, tag string) error {
	to := tagVersion.FindString(tag)
	if to == "" {
		return nil
	}
	parts := strings.Split(m.ServerVersion, ".")
	from := strings.Join(parts[:min(len(parts), strings.Count(to, ".")+1)], ".")
	if tmpl.Backup.Compatible(from, to) {
		return nil
	}

	return refuse("The backup is from %s %s, which is newer than %s. A backup never restores into an older server.",
		tmpl.Name, m.ServerVersion, tag)
}

// StartNewAppRestore restores src into app, which was made for it, once
// queue has queued the app's first deploy, the deploy has succeeded and
// the server takes connections. There's nothing to keep, so there's no
// safety backup. Whatever step fails, the restore's message says what's
// left of app.
func StartNewAppRestore(app *models.App, src *RestoreSource, queue func() (uint, error)) (*models.Restore, error) {
	b := AppBackup(app)
	if b == nil {
		return nil, fmt.Errorf("%s has no backups", app.Name)
	}
	waiting := "Waiting for the first deploy."
	rec := &models.Restore{
		AppID:             app.ID,
		StorageLocationID: src.Location.ID,
		Key:               src.Key,
		NewApp:            true,
		Status:            models.RestoreRunning,
		SizeBytes:         src.Manifest.Size,
		Message:           &waiting,
		StartedAt:         carbon.NewDateTime(carbon.Now()),
	}
	if err := facades.Orm().Query().Create(rec); err != nil {
		return nil, err
	}
	deploymentID, err := queue()
	if err != nil {
		finishRestore(rec, restoreResult{
			status:  models.RestoreFailed,
			message: fmt.Sprintf("Couldn't queue %s's first deploy: %s.", app.Name, err) + notRestored(app),
		})
		return rec, nil
	}

	runningRestores.Add(1)
	go func() {
		defer runningRestores.Done()
		ctx, cancel := context.WithTimeout(context.Background(), BackupTimeout)
		defer cancel()
		finishRestore(rec, restoreNewApp(ctx, app, b, src, rec, deploymentID))
	}()

	return rec, nil
}

// restoreNewApp waits for the app's first deploy and its server, then
// restores into it under the app's lock.
func restoreNewApp(ctx context.Context, app *models.App, b *templates.Backup, src *RestoreSource, rec *models.Restore, deploymentID uint) restoreResult {
	failed := func(format string, args ...any) restoreResult {
		return restoreResult{status: models.RestoreFailed, message: fmt.Sprintf(format, args...) + notRestored(app)}
	}
	deadline := time.Now().Add(NewAppWait)
	within := fmt.Sprintf("within %d minutes", int(NewAppWait/time.Minute))

	for {
		var d models.Deployment
		if err := facades.Orm().Query().Find(&d, deploymentID); err != nil {
			return failed("Couldn't read %s's first deploy: %s.", app.Name, err)
		}
		if d.Status == models.DeploymentSucceeded {
			break
		}
		if !d.Status.Running() {
			reason := "it " + strings.ReplaceAll(string(d.Status), "_", " ")
			if d.Error != nil {
				reason = strings.TrimSuffix(*d.Error, ".")
			}
			return failed("%s's first deploy failed: %s.", app.Name, reason)
		}
		if time.Now().After(deadline) {
			return failed("%s's first deploy didn't finish %s.", app.Name, within)
		}
		if !sleep(ctx, NewAppPoll) {
			return failed("%s's first deploy didn't finish in time.", app.Name)
		}
	}

	tmpl, _ := templates.Find(*app.Template)
	waiting := fmt.Sprintf("Waiting for %s to take connections.", tmpl.Name)
	if err := updateRestore(rec.ID, map[string]any{"message": waiting}); err != nil {
		facades.Log().Error(fmt.Errorf("restore %d: %w", rec.ID, err))
	}
	for {
		out, err := Docker().Exec(ctx, app.ServiceName(), b.Ready)
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			break
		}
		if time.Now().After(deadline) || !sleep(ctx, NewAppPoll) {
			return failed("%s in %s didn't take connections %s.", tmpl.Name, app.Name, within)
		}
	}

	unlock, err := WaitLockApp(ctx, app.ID, WorkRestore)
	if err != nil {
		return failed("%s stayed busy, so the restore never began.", app.Name)
	}
	defer unlock()
	if err := updateRestore(rec.ID, map[string]any{"message": nil}); err != nil {
		facades.Log().Error(fmt.Errorf("restore %d: %w", rec.ID, err))
	}
	rec.Message = nil

	return restore(ctx, app, b, src, rec, false)
}

// sleep waits for d, or returns false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
