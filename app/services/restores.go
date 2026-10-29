package services

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/backups"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// KeepRestores is how many restores of each app are remembered.
const KeepRestores = 50

// versionTimeout is how long the restore dialog waits for the app's
// server to say its version.
const versionTimeout = 2 * time.Second

// checkTail is how much of a dump a restore holds back until all of it has
// been read and matches its manifest. Without its end, pg_restore can't
// finish, so a dump that doesn't match rolls back.
const checkTail = 64 << 10

// runningRestores waits for restores in tests.
var runningRestores sync.WaitGroup

// WaitForRestores waits until no restore is going. For tests.
func WaitForRestores() {
	runningRestores.Wait()
}

// RestoreRefusal is why a restore can't go ahead, in words for the page.
type RestoreRefusal struct {
	Reason string
}

func (r *RestoreRefusal) Error() string {
	return r.Reason
}

func refuse(format string, args ...any) error {
	return &RestoreRefusal{Reason: fmt.Sprintf(format, args...)}
}

// RestoreSource is a dump to restore: one in a bucket, with the manifest
// read from beside it, or an upload, with a manifest made from the dump's
// first bytes.
type RestoreSource struct {
	// Location is nil for an upload.
	Location *models.StorageLocation
	// Key is the dump's whole key, or an upload's name.
	Key      string
	Manifest backups.Manifest
	// Backup is the dump's record, or nil for one Echowa has none of.
	Backup *models.Backup
	// Upload is the uploaded file, and Head what its first bytes say, or
	// nil for a dump in a bucket.
	Upload *Upload
	Head   *templates.DumpHead
}

// AppFolder is where a location keeps the app's backups.
func AppFolder(loc *models.StorageLocation, app *models.App) string {
	return backups.JoinKey(loc.Prefix, app.Name) + "/"
}

// BackupSource is the dump of one of the app's successful backups.
func BackupSource(ctx context.Context, app *models.App, backupID uint) (*RestoreSource, error) {
	var rec models.Backup
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").
		Where("id", backupID).Where("app_id", app.ID).Where("status", models.BackupSucceeded).First(&rec)
	if err != nil {
		return nil, err
	}
	if rec.ID == 0 || rec.StorageLocation == nil {
		return nil, refuse("That backup isn't there any more.")
	}

	return readSource(ctx, app, rec.StorageLocation, backups.ManifestKey(rec.Key))
}

// BucketSource is the dump whose manifest is at key in loc, which must be
// in the app's folder, for a backup Echowa may have no record of.
func BucketSource(ctx context.Context, app *models.App, loc *models.StorageLocation, key string) (*RestoreSource, error) {
	name, ok := strings.CutPrefix(key, AppFolder(loc, app))
	if !ok || name == "" || strings.Contains(name, "/") || path.Ext(name) != ".json" {
		return nil, refuse("Choose a manifest in %s's folder of %s.", app.Name, loc.Name)
	}

	return readSource(ctx, app, loc, key)
}

// UploadSource is the app's upload with ID id. Its manifest comes from the
// dump's first bytes: the server it came from and the database, and when
// it was made if the dump says.
func UploadSource(app *models.App, id string) (*RestoreSource, error) {
	u, ok := FindUpload(app.ID, id)
	if !ok {
		return nil, refuse("The upload isn't there any more. Choose the file again.")
	}
	head, err := u.head()
	if err != nil {
		return nil, err
	}
	dump, err := templates.ReadDumpHead(head)
	if err != nil {
		return nil, refuse("%s", err)
	}
	if app.Template == nil || dump.Template != *app.Template {
		return nil, refuse("This is a dump of a %s database, and %s is %s.", templateName(dump.Template), app.Name, templateName(deref(app.Template)))
	}
	m := backups.Manifest{
		Template: dump.Template, ServerVersion: dump.ServerVersion, Database: dump.Database,
		Object: u.Name, Compression: "none", Size: u.Size, StartedAt: dump.Made,
	}
	if dump.Gzip {
		m.Compression = "gzip"
	}

	return &RestoreSource{Key: u.Name, Manifest: m, Upload: u, Head: &dump}, nil
}

// readSource reads the manifest at key and refuses a dump of another
// template's database.
func readSource(ctx context.Context, app *models.App, loc *models.StorageLocation, key string) (*RestoreSource, error) {
	src, err := readManifest(ctx, loc, key)
	if err != nil {
		return nil, err
	}
	m := src.Manifest
	if app.Template == nil || m.Template != *app.Template {
		return nil, refuse("This is a backup of a %s database, and %s is %s.", templateName(m.Template), app.Name, templateName(deref(app.Template)))
	}

	var rec models.Backup
	err = facades.Orm().WithContext(ctx).Query().
		Where("app_id", app.ID).Where("storage_location_id", loc.ID).Where("key", src.Key).First(&rec)
	if err != nil {
		return nil, err
	}
	if rec.ID != 0 {
		src.Backup = &rec
	}

	return src, nil
}

// readManifest reads the manifest at key in loc, which names the dump
// beside it.
func readManifest(ctx context.Context, loc *models.StorageLocation, key string) (*RestoreSource, error) {
	body, err := backups.Open(loc).Get(ctx, key)
	if errors.Is(err, backups.ErrNotFound) {
		return nil, refuse("The backup's manifest isn't in %s any more.", loc.Name)
	}
	if err != nil {
		return nil, refuse("Couldn't read the backup's manifest from %s: %s", loc.Name, backups.Describe(err))
	}
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return nil, refuse("Couldn't read the backup's manifest from %s: %s", loc.Name, backups.Describe(err))
	}
	m, err := backups.ParseManifest(raw)
	if err != nil {
		return nil, refuse("The backup's manifest can't be used: %s.", err)
	}

	return &RestoreSource{Location: loc, Key: path.Dir(key) + "/" + m.Object, Manifest: m}, nil
}

func templateName(slug string) string {
	if tmpl, ok := templates.Find(slug); ok {
		return tmpl.Name
	}
	if slug == "" {
		return "custom"
	}

	return slug
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// RestorePreview is what the restore dialog shows before it's confirmed.
type RestorePreview struct {
	Name string `json:"name"`
	// Location is "" for an upload.
	Location string `json:"location"`
	Uploaded bool   `json:"uploaded"`
	BackupID *uint  `json:"backup_id"`
	// Database and ServerVersion are the backup's.
	Database      string `json:"database"`
	ServerVersion string `json:"server_version"`
	SizeBytes     int64  `json:"size_bytes"`
	// StartedAt is nil for an upload whose dump doesn't say.
	StartedAt *time.Time `json:"started_at"`
	// AppDatabase and AppVersion are what the restore goes into.
	// AppVersion is "" when the app isn't running.
	AppDatabase string `json:"app_database"`
	AppVersion  string `json:"app_version"`
	Template    string `json:"template"`
	// DefaultTag is the template's, for a restore into a new app.
	DefaultTag string `json:"default_tag"`
	// Atomic means the restore applies all of the backup or none of it.
	Atomic bool `json:"atomic"`
	// Refusal says why the restore can't go ahead, and Warning what to
	// know before it does.
	Refusal string `json:"refusal"`
	Warning string `json:"warning"`
}

// PreviewRestore checks src against the app's running server.
func PreviewRestore(ctx context.Context, app *models.App, src *RestoreSource) (*RestorePreview, error) {
	b := AppBackup(app)
	env, err := EnvVars(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	m := src.Manifest
	preview := &RestorePreview{
		Name: m.Object, Uploaded: src.Upload != nil, Database: m.Database, ServerVersion: m.ServerVersion,
		SizeBytes: m.Size, AppDatabase: env[b.Database], Template: templateName(m.Template), Atomic: b.Atomic,
	}
	if src.Location != nil {
		preview.Location = src.Location.Name
	}
	if !m.StartedAt.IsZero() {
		preview.StartedAt = &m.StartedAt
	}
	if tmpl, ok := templates.Find(deref(app.Template)); ok {
		preview.DefaultTag = tmpl.DefaultTag
	}
	if src.Backup != nil {
		preview.BackupID = &src.Backup.ID
	}
	versionCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	preview.AppVersion, preview.Warning, err = checkVersion(versionCtx, app, b, src)
	var refused *RestoreRefusal
	if errors.As(err, &refused) {
		preview.Refusal = refused.Reason
	} else if err != nil {
		return nil, err
	}

	return preview, nil
}

// checkVersion reads the app's server version and refuses a dump from a
// newer one, or an upload its restore command can't read (ADR 0034). The
// warning is for a newer major version.
func checkVersion(ctx context.Context, app *models.App, b *templates.Backup, src *RestoreSource) (version, warning string, err error) {
	m := src.Manifest
	out, err := Docker().Exec(ctx, app.ServiceName(), b.Version)
	if errors.Is(err, docker.ErrNotRunning) {
		return "", "", refuse("%s has no running task to restore into. Deploy it first.", app.Name)
	}
	if err != nil {
		return "", "", refuse("Couldn't read %s's server version: %s", app.Name, err)
	}
	if version, err = templates.ServerVersion(out); err != nil {
		return "", "", refuse("Couldn't read %s's server version: %s", app.Name, err)
	}
	name := templateName(m.Template)
	if !b.Compatible(m.ServerVersion, version) {
		return version, "", refuse("The backup is from %s %s, and %s runs %s. A backup never restores into an older server.",
			name, m.ServerVersion, app.Name, version)
	}
	if src.Head != nil {
		if err := src.Head.ReadableBy(version); err != nil {
			return version, "", refuse("%s", err)
		}
	}
	from, _, _ := strings.Cut(m.ServerVersion, ".")
	to, _, _ := strings.Cut(version, ".")
	if from != to {
		warning = fmt.Sprintf("The backup is from %s %s, and %s runs %s. Restoring into a newer major version works, "+
			"but check what changed between them before your app uses it.", name, m.ServerVersion, app.Name, version)
	}

	return version, warning, nil
}

// StartRestore restores src into the app in the background, after a
// safety backup to the location safety when it isn't nil. It's refused at
// once while a deploy, backup, restore or export holds the app (ADR 0034).
// An upload has to have arrived whole, and the restore deletes it when it
// ends.
func StartRestore(app *models.App, src *RestoreSource, safety *models.StorageLocation) (*models.Restore, error) {
	b := AppBackup(app)
	if b == nil {
		return nil, fmt.Errorf("%s has no backups", app.Name)
	}
	unlock, holder, ok := LockApp(app.ID, WorkRestore)
	if !ok {
		return nil, refuse("%s of %s is running. Restore once it has finished.", upperFirst(holder.Describe()), app.Name)
	}
	// A restore into a new app waits for its first deploy without the lock.
	waiting, err := facades.Orm().Query().Model(&models.Restore{}).
		Where("app_id", app.ID).Where("status", models.RestoreRunning).Exists()
	if err != nil || waiting {
		unlock()
		if err != nil {
			return nil, err
		}
		return nil, refuse("A restore of %s is running. Restore once it has finished.", app.Name)
	}
	removeUpload := func() {}
	if u := src.Upload; u != nil {
		sum, err := u.take()
		if err != nil {
			unlock()
			return nil, err
		}
		src.Manifest.SHA256 = sum
		removeUpload = func() { _ = os.Remove(u.path) }
	}

	rec := &models.Restore{
		AppID:     app.ID,
		Key:       src.Key,
		Status:    models.RestoreRunning,
		SizeBytes: src.Manifest.Size,
		StartedAt: carbon.NewDateTime(carbon.Now()),
	}
	if src.Location != nil {
		rec.StorageLocationID = &src.Location.ID
	}
	if src.Backup != nil {
		rec.BackupID = &src.Backup.ID
	}
	if err := facades.Orm().Query().Create(rec); err != nil {
		removeUpload()
		unlock()
		return nil, err
	}

	runningRestores.Add(1)
	go func() {
		defer runningRestores.Done()
		defer unlock()
		defer removeUpload()
		ctx, cancel := context.WithTimeout(context.Background(), BackupTimeout)
		defer cancel()
		finishRestore(rec, restore(ctx, app, b, src, rec, safety))
	}()

	return rec, nil
}

// partly says what a restore that stopped part way leaves.
func partly(app *models.App, b *templates.Backup, rec *models.Restore) string {
	atomic := b != nil && b.Atomic
	switch {
	case atomic && rec.NewApp:
		return notRestored(app)
	case atomic:
		return " Nothing was restored."
	case rec.NewApp:
		return fmt.Sprintf(" %s may hold part of the backup. Delete it and restore into a new app again.", app.Name)
	case rec.SafetyBackupID != nil:
		return fmt.Sprintf(" Some tables may have been restored and others not. The safety backup is #%d.", *rec.SafetyBackupID)
	default:
		return " Some tables may have been restored and others not, and there's no safety backup."
	}
}

// notRestored is what a restore into a new app that never began leaves.
func notRestored(app *models.App) string {
	return fmt.Sprintf(" Nothing was restored, so %s's database is empty. Restore into it from its Backups section once it's running, or delete it.", app.Name)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

type restoreResult struct {
	status  models.RestoreStatus
	message string
	output  string
	read    int64
}

// restore checks the app's version, takes the safety backup, then streams
// the dump from the bucket or the uploaded file into the template's
// restore command. The caller holds the app's lock.
func restore(ctx context.Context, app *models.App, b *templates.Backup, src *RestoreSource, rec *models.Restore, safety *models.StorageLocation) restoreResult {
	failed := func(format string, args ...any) restoreResult {
		return restoreResult{status: models.RestoreFailed, message: fmt.Sprintf(format, args...)}
	}
	if _, _, err := checkVersion(ctx, app, b, src); err != nil {
		if rec.NewApp {
			return failed("%s%s", err, notRestored(app))
		}
		return failed("%s Nothing was restored.", err)
	}
	if safety != nil {
		backup, result := safetyBackup(ctx, app, b, safety, rec)
		if backup == nil || result.status != models.BackupSucceeded {
			return failed("The safety backup failed, so nothing was restored. %s", result.message)
		}
		rec.SafetyBackupID = &backup.ID
	}
	partly := func() string { return partly(app, b, rec) }

	body, err := openDump(ctx, src)
	if err != nil {
		return failed("%s", err)
	}
	defer func() { _ = body.Close() }()
	checked := &checkedDump{r: body, size: src.Manifest.Size, sha256: src.Manifest.SHA256, h: sha256.New()}
	var stdin io.Reader = checked
	if src.Manifest.Compression == "gzip" {
		gz, err := gzip.NewReader(checked)
		if err != nil {
			return failed("The dump isn't gzipped as its manifest says, so nothing was restored: %s", err)
		}
		stdin = gz
	}
	stopProgress := saveProgress(func() {
		if err := updateRestore(rec.ID, map[string]any{"read_bytes": checked.n.Load()}); err != nil {
			facades.Log().Error(fmt.Errorf("restore %d progress: %w", rec.ID, err))
		}
	})
	output := &docker.TailBuffer{Limit: MaxBackupOutput}
	code, err := Docker().ExecStream(ctx, app.ServiceName(), b.Restore, stdin, output, output)
	stopProgress()

	result := restoreResult{status: models.RestoreFailed, output: output.String(), read: checked.n.Load()}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.message = fmt.Sprintf("Stopped after %d hours.", int(BackupTimeout/time.Hour)) + partly()
	case errors.Is(err, errDumpMismatch) && src.Upload != nil:
		result.message = "The uploaded file changed on the server's disk, so the restore was stopped before its end." + partly()
	case errors.Is(err, errDumpMismatch):
		result.message = "The dump in the bucket doesn't match its manifest, so the restore was stopped before its end." + partly()
	case errors.Is(err, docker.ErrNotRunning):
		result.message = app.Name + " stopped running before the restore started." + partly()
	case err != nil:
		result.message = "The restore failed: " + err.Error() + "." + partly()
	case code != 0:
		result.message = fmt.Sprintf("The restore exited with status %d.", code) + partly()
	case !checked.done.Load():
		result.message = "The restore ended before it had read all of the dump." + partly()
	default:
		result.status = models.RestoreSucceeded
	}

	return result
}

// openDump opens the dump in the bucket or the uploaded file. Its error is
// for the page.
func openDump(ctx context.Context, src *RestoreSource) (io.ReadCloser, error) {
	if src.Upload != nil {
		f, err := os.Open(src.Upload.path)
		if err != nil {
			return nil, fmt.Errorf("Couldn't read the uploaded file, so nothing was restored: %w", err)
		}
		return f, nil
	}
	body, err := backups.Open(src.Location).Get(ctx, src.Key)
	if errors.Is(err, backups.ErrNotFound) {
		return nil, fmt.Errorf("The dump isn't in %s any more, so nothing was restored.", src.Location.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("Couldn't read the dump from %s, so nothing was restored: %s", src.Location.Name, backups.Describe(err))
	}

	return body, nil
}

// safetyBackup backs the app up to loc before a restore, inside the
// restore's lock, and records it on the restore.
func safetyBackup(ctx context.Context, app *models.App, b *templates.Backup, loc *models.StorageLocation, rec *models.Restore) (*models.Backup, backupResult) {
	backup := &models.Backup{
		AppID:             app.ID,
		StorageLocationID: loc.ID,
		Trigger:           models.BackupBeforeRestore,
		Status:            models.BackupRunning,
		StartedAt:         carbon.NewDateTime(carbon.Now()),
	}
	if err := facades.Orm().Query().Create(backup); err != nil {
		return nil, backupResult{message: err.Error()}
	}
	if err := updateRestore(rec.ID, map[string]any{"safety_backup_id": backup.ID}); err != nil {
		facades.Log().Error(fmt.Errorf("restore %d: %w", rec.ID, err))
	}
	result := dump(ctx, app, b, loc, backup)
	finishBackup(backup, result)

	return backup, result
}

// errDumpMismatch ends a restore's input when the dump read from the
// bucket isn't the one its manifest describes.
var errDumpMismatch = errors.New("the dump doesn't match its manifest")

// checkedDump passes a dump through, holding its last checkTail bytes back
// until all of it has been read and its size and SHA-256 match the
// manifest's. A dump that doesn't match ends in errDumpMismatch instead of
// its last bytes.
type checkedDump struct {
	r      io.Reader
	size   int64
	sha256 string
	h      hash.Hash
	// n is how much has been read from the bucket, for the page.
	n atomic.Int64
	// done is set once the dump has been read to its end and matched.
	done atomic.Bool

	held []byte
	out  []byte
	err  error
}

func (c *checkedDump) Read(p []byte) (int, error) {
	for len(c.out) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		chunk := make([]byte, 32<<10)
		n, err := c.r.Read(chunk)
		c.h.Write(chunk[:n])
		c.n.Add(int64(n))
		c.held = append(c.held, chunk[:n]...)
		if over := len(c.held) - checkTail; over > 0 {
			c.out = slices.Clone(c.held[:over])
			c.held = slices.Clone(c.held[over:])
		}
		switch {
		case errors.Is(err, io.EOF):
			if c.n.Load() != c.size || hex.EncodeToString(c.h.Sum(nil)) != c.sha256 {
				c.err = errDumpMismatch
				break
			}
			c.out = append(c.out, c.held...)
			c.held = nil
			c.err = io.EOF
			c.done.Store(true)
		case err != nil:
			c.err = err
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]

	return n, nil
}

func updateRestore(id uint, fields map[string]any) error {
	_, err := facades.Orm().Query().Model(&models.Restore{}).Where("id", id).Update(fields)

	return err
}

func finishRestore(rec *models.Restore, result restoreResult) {
	var message *string
	if result.message != "" {
		message = &result.message
	}
	err := updateRestore(rec.ID, map[string]any{
		"status":      result.status,
		"message":     message,
		"output":      result.output,
		"read_bytes":  result.read,
		"finished_at": carbon.NewDateTime(carbon.Now()),
	})
	if err != nil {
		facades.Log().Error(fmt.Errorf("restore %d: %w", rec.ID, err))
	}
}

// RecoverRestores marks restores an earlier Echowa left running as
// interrupted. Its exec's connection closed when it stopped, which ends
// the restore's input: PostgreSQL rolls back, and MySQL keeps the tables
// it had got to.
func RecoverRestores(ctx context.Context) error {
	var running []models.Restore
	if err := facades.Orm().WithContext(ctx).Query().Where("status", models.RestoreRunning).Get(&running); err != nil {
		return err
	}
	for i := range running {
		rec := &running[i]
		if rec.StartedAt != nil && !rec.StartedAt.StdTime().Before(backupStart) {
			continue
		}
		var app models.App
		if err := facades.Orm().WithContext(ctx).Query().Find(&app, rec.AppID); err != nil {
			return err
		}
		// A restore into a new app has a message only while it waits.
		message := "Echowa restarted while this ran." + partly(&app, AppBackup(&app), rec)
		if rec.NewApp && rec.Message != nil {
			message = "Echowa restarted before the restore began." + notRestored(&app)
		}
		finishRestore(rec, restoreResult{status: models.RestoreInterrupted, message: message, read: rec.ReadBytes})
	}

	return nil
}

// pruneRestores forgets each app's restores past its newest KeepRestores.
func pruneRestores(ctx context.Context) error {
	var appIDs []uint
	if err := facades.Orm().WithContext(ctx).Query().Model(&models.Restore{}).Distinct("app_id").Pluck("app_id", &appIDs); err != nil {
		return err
	}
	for _, appID := range appIDs {
		var ids []any
		err := facades.Orm().WithContext(ctx).Query().Model(&models.Restore{}).Where("app_id", appID).
			Where("status <> ?", models.RestoreRunning).Order("id desc").Offset(KeepRestores).Limit(1000).Pluck("id", &ids)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			continue
		}
		if _, err := facades.Orm().WithContext(ctx).Query().WhereIn("id", ids).Delete(&models.Restore{}); err != nil {
			return err
		}
	}

	return nil
}

// BucketBackup is a dump with a manifest in an app's folder of a location.
type BucketBackup struct {
	// Key is the manifest's, which a restore from the bucket names.
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	Modified  time.Time `json:"modified"`
	// BackupID is its record's, or nil when Echowa has none.
	BackupID *uint `json:"backup_id"`
}

// ListBucketBackups lists, newest first, the dumps in the app's folder of
// loc that have a manifest. A dump without one is incomplete.
func ListBucketBackups(ctx context.Context, app *models.App, loc *models.StorageLocation) ([]BucketBackup, error) {
	found, dumps, err := listFolder(ctx, loc, AppFolder(loc, app))
	if err != nil {
		return nil, err
	}
	if len(dumps) > 0 {
		var recs []models.Backup
		err := facades.Orm().WithContext(ctx).Query().Select("id", "key").
			Where("app_id", app.ID).Where("storage_location_id", loc.ID).WhereIn("key", dumps).Get(&recs)
		if err != nil {
			return nil, err
		}
		ids := map[string]uint{}
		for _, rec := range recs {
			ids[path.Base(rec.Key)] = rec.ID
		}
		for i := range found {
			if id, ok := ids[found[i].Name]; ok {
				found[i].BackupID = &id
			}
		}
	}

	return found, nil
}

// listFolder lists, newest first, the dumps in folder of loc that have a
// manifest, and the dumps' keys.
func listFolder(ctx context.Context, loc *models.StorageLocation, folder string) ([]BucketBackup, []any, error) {
	listing, err := backups.Open(loc).List(ctx, folder, "/")
	if err != nil {
		return nil, nil, err
	}
	keys := map[string]bool{}
	for _, obj := range listing.Objects {
		keys[obj.Key] = true
	}
	found := []BucketBackup{}
	dumps := []any{}
	for _, obj := range listing.Objects {
		manifest := backups.ManifestKey(obj.Key)
		if manifest == obj.Key || !keys[manifest] {
			continue
		}
		found = append(found, BucketBackup{Key: manifest, Name: path.Base(obj.Key), SizeBytes: obj.Size, Modified: obj.Modified})
		dumps = append(dumps, obj.Key)
	}
	// Names start with the app and the time, so they sort by time.
	slices.SortFunc(found, func(a, b BucketBackup) int { return strings.Compare(b.Name, a.Name) })

	return found, dumps, nil
}

// RestoreSummary is a row of the Backups section's restores, without the
// output.
type RestoreSummary struct {
	ID     uint                 `json:"id"`
	Status models.RestoreStatus `json:"status"`
	// Location is "" for an upload.
	Location       string           `json:"location"`
	Uploaded       bool             `json:"uploaded"`
	Name           string           `json:"name"`
	BackupID       *uint            `json:"backup_id"`
	SafetyBackupID *uint            `json:"safety_backup_id"`
	NewApp         bool             `json:"new_app"`
	SizeBytes      int64            `json:"size_bytes"`
	ReadBytes      int64            `json:"read_bytes"`
	Message        *string          `json:"message"`
	StartedAt      *carbon.DateTime `json:"started_at"`
	FinishedAt     *carbon.DateTime `json:"finished_at"`
}

// appRestores lists the app's newest limit restores.
func appRestores(ctx context.Context, appID uint, limit int) ([]RestoreSummary, error) {
	var rows []models.Restore
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").
		Where("app_id", appID).Order("id desc").Limit(limit).Get(&rows)
	if err != nil {
		return nil, err
	}
	list := make([]RestoreSummary, len(rows))
	for i, rec := range rows {
		list[i] = RestoreSummary{
			ID: rec.ID, Status: rec.Status, Name: path.Base(rec.Key), Uploaded: rec.StorageLocationID == nil,
			BackupID: rec.BackupID, SafetyBackupID: rec.SafetyBackupID, NewApp: rec.NewApp,
			SizeBytes: rec.SizeBytes, ReadBytes: rec.ReadBytes, Message: rec.Message, StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt,
		}
		if rec.StorageLocation != nil {
			list[i].Location = rec.StorageLocation.Name
		}
	}

	return list, nil
}
