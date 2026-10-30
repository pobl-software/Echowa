package feature

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"path"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/alerts"
	"goravel/app/backups"
	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/services"
)

const (
	postgres186 = "postgres (PostgreSQL) 18.6 (Debian 18.6-1.pgdg13+2)\n"
	mysql974    = "mysqld  Ver 9.7.4 for Linux on aarch64 (MySQL Community Server - GPL)\n"
)

// scriptRestore makes the app's server report version and its dump write
// current. Its restore reads all of stdin into s.restored, after gate
// closes when it's set, and exits with code.
func (s *BackupsTestSuite) scriptRestore(version, current string, code int, gate chan struct{}) {
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		return []byte(version), nil
	}
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, errs io.Writer) (int, error) {
		if stdin == nil {
			s.mu.Lock()
			s.dumps = append(s.dumps, cmd)
			s.mu.Unlock()
			_, err := io.WriteString(stdout, current)
			return 0, err
		}
		if gate != nil {
			<-gate
		}
		read, err := io.ReadAll(stdin)
		s.mu.Lock()
		s.restored = append(s.restored, string(read))
		s.mu.Unlock()
		if err != nil {
			// As the engine reports a failed read of the input.
			return 0, err
		}
		_, _ = io.WriteString(errs, "restore: done\n")
		return code, nil
	}
}

func (s *BackupsTestSuite) restoredSoFar() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string{}, s.restored...)
}

// backedUp is a successful backup of app to s.loc whose dump is data.
func (s *BackupsTestSuite) backedUp(app *models.App, version, data string) models.Backup {
	s.script(version, data, 0, "", nil)
	rec, err := services.BackUpNow(app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	s.Require().NoError(facades.Orm().Query().Find(rec, rec.ID))
	s.Require().Equal(models.BackupSucceeded, rec.Status, "message: %v", rec.Message)

	return *rec
}

// putBackup writes a dump and its manifest to s.loc as another server
// would, with no record here, and returns the manifest's key.
func (s *BackupsTestSuite) putBackup(app, template, version, compression, name string, dump []byte) string {
	key := backups.JoinKey(s.loc.Prefix, app+"/"+name)
	manifest := must(json.Marshal(backups.Manifest{
		Format: backups.ManifestFormat, App: app, Template: template, ServerVersion: version, Database: app,
		Object: name, Compression: compression, Size: int64(len(dump)), SHA256: sha(dump),
		StartedAt: time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 1, 3, 0, 5, 0, time.UTC),
	}))
	s.Require().NoError(s.storage.Put(context.Background(), key, bytes.NewReader(dump)))
	s.Require().NoError(s.storage.Put(context.Background(), backups.ManifestKey(key), bytes.NewReader(manifest)))

	return backups.ManifestKey(key)
}

func (s *BackupsTestSuite) restore(b *browser, app *models.App, data map[string]any) {
	page := fmt.Sprintf("/apps/%d?tab=backups", app.ID)
	b.visit(page)
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/restores", app.ID), page, data)
}

// restoreError is the restore form's error for field after a submit.
func (s *BackupsTestSuite) restoreError(b *browser, app *models.App, field string) any {
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", app.ID))

	return page.Props["errors"].(map[string]any)[field]
}

func (s *BackupsTestSuite) restores(appID uint) []models.Restore {
	var rows []models.Restore
	s.Require().NoError(facades.Orm().Query().Where("app_id", appID).Order("id").Get(&rows))

	return rows
}

// getJSON asserts uri answers status, and returns its body.
func (s *BackupsTestSuite) getJSON(b *browser, uri string, status int) map[string]any {
	resp := b.request(nethttp.MethodGet, uri, nil, false, map[string]string{"Accept": "application/json"})
	resp.AssertStatus(status)

	return must(resp.Json())
}

func (s *BackupsTestSuite) TestARestoreFromTheHistoryTakesASafetyBackupFirst() {
	old := s.backedUp(s.app, postgres186, "PGDMP old data")
	s.scriptRestore(postgres186, "PGDMP new data", 0, nil)
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)

	preview := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?backup=%d", s.app.ID, old.ID), nethttp.StatusOK)
	s.Equal(path.Base(old.Key), preview["name"])
	s.Equal("18.6", preview["app_version"])
	s.Equal(true, preview["atomic"])
	s.Empty(preview["refusal"])
	s.Empty(preview["warning"])

	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": true, "confirm": "shop-db"})
	services.WaitForRestores()

	rows := s.restores(s.app.ID)
	s.Require().Len(rows, 1)
	rec := rows[0]
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.Equal(old.ID, *rec.BackupID)
	s.Equal(old.Key, rec.Key)
	s.EqualValues(len("PGDMP old data"), rec.ReadBytes)
	s.Contains(rec.Output, "restore: done")
	s.Equal([]string{"PGDMP old data"}, s.restoredSoFar(), "the restore read the backup")

	s.Require().NotNil(rec.SafetyBackupID)
	var safety models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&safety, *rec.SafetyBackupID))
	s.Equal(models.BackupBeforeRestore, safety.Trigger)
	s.Equal(models.BackupSucceeded, safety.Status)
	body := must(s.storage.Get(context.Background(), safety.Key))
	s.Equal("PGDMP new data", string(must(io.ReadAll(body))), "the safety backup holds the data the restore replaced")
	s.Contains(s.storage.Keys(), backups.ManifestKey(safety.Key))

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	s.Equal("Restoring shop-db from "+path.Base(old.Key)+".", page.Flash["success"])
	row := page.Props["backups"].(map[string]any)["restores"].([]any)[0].(map[string]any)
	s.Equal("succeeded", row["status"])
	s.Equal(float64(*rec.SafetyBackupID), row["safety_backup_id"])

	detail := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/%d", s.app.ID, rec.ID), nethttp.StatusOK)
	s.Equal("restore: done\n", detail["output"])
}

func (s *BackupsTestSuite) TestARestoreNeedsTheAppsNameTyped() {
	old := s.backedUp(s.app, postgres186, "PGDMP")
	s.scriptRestore(postgres186, "PGDMP", 0, nil)
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": true, "confirm": "shop"})
	s.Equal("Type shop-db to confirm.", s.restoreError(b, s.app, "confirm"))
	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": true})
	s.Equal("Type the app's name to confirm.", s.restoreError(b, s.app, "confirm"))
	s.Empty(s.restores(s.app.ID))
}

func (s *BackupsTestSuite) TestABackupWithNoRecordRestoresFromTheBucket() {
	orders := s.templateApp("orders", "mysql")
	const sql = "DROP TABLE IF EXISTS `items`;\nCREATE TABLE `items` (id int);\n"
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(sql))
	s.Require().NoError(w.Close())
	key := s.putBackup("orders", "mysql", "9.7.4", "gzip", "orders-20261001T030000Z-7.sql.gz", gz.Bytes())
	// A dump without a manifest is incomplete, and isn't offered.
	s.Require().NoError(s.storage.Put(context.Background(), "server-1/orders/orders-20261002T030000Z-8.sql.gz", bytes.NewReader([]byte("half"))))
	s.scriptRestore(mysql974, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)

	listing := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/browse?location=%d", orders.ID, s.loc.ID), nethttp.StatusOK)
	s.Equal("server-1/orders/", listing["folder"])
	found := listing["backups"].([]any)
	s.Require().Len(found, 1)
	s.Equal(key, found[0].(map[string]any)["key"])
	s.Nil(found[0].(map[string]any)["backup_id"])

	query := fmt.Sprintf("/apps/%d/restores/preview?location=%d&key=%s", orders.ID, s.loc.ID, key)
	preview := s.getJSON(b, query, nethttp.StatusOK)
	s.Equal(false, preview["atomic"])
	s.Nil(preview["backup_id"])
	s.Equal("MySQL", preview["template"])

	s.restore(b, orders, map[string]any{"location": s.loc.ID, "key": key, "safety": false, "confirm": "orders"})
	services.WaitForRestores()

	rows := s.restores(orders.ID)
	s.Require().Len(rows, 1)
	s.Equal(models.RestoreSucceeded, rows[0].Status, "message: %v", rows[0].Message)
	s.Nil(rows[0].BackupID)
	s.Nil(rows[0].SafetyBackupID)
	s.Equal("server-1/orders/orders-20261001T030000Z-7.sql.gz", rows[0].Key)
	s.Equal([]string{sql}, s.restoredSoFar(), "Echowa gunzips the dump into mysql")
	n, err := facades.Orm().Query().Model(&models.Backup{}).Where("app_id", orders.ID).Count()
	s.Require().NoError(err)
	s.Zero(n, "no safety backup was asked for")
}

func (s *BackupsTestSuite) TestOnlyThisAppsManifestsCanBeRestored() {
	s.putBackup("other-db", "postgres", "18.6", "none", "other-db-20261001T030000Z-1.dump", []byte("PGDMP"))
	mysqlKey := s.putBackup("shop-db", "mysql", "9.7.4", "gzip", "shop-db-20261001T030000Z-2.sql.gz", []byte("x"))
	s.scriptRestore(postgres186, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)

	outside := "Choose a manifest in shop-db's folder of R2."
	for _, key := range []string{
		"server-1/other-db/other-db-20261001T030000Z-1.json",
		"server-1/shop-db/shop-db-20261001T030000Z-2.sql.gz",
		"server-1/shop-db/nested/shop-db-20261001T030000Z-2.json",
		"shop-db/shop-db-20261001T030000Z-2.json",
	} {
		body := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?location=%d&key=%s", s.app.ID, s.loc.ID, key), nethttp.StatusUnprocessableEntity)
		s.Equal(outside, body["error"], key)
		s.restore(b, s.app, map[string]any{"location": s.loc.ID, "key": key, "safety": false, "confirm": "shop-db"})
		s.Equal(outside, s.restoreError(b, s.app, "source"), key)
	}

	body := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?location=%d&key=%s", s.app.ID, s.loc.ID, mysqlKey), nethttp.StatusUnprocessableEntity)
	s.Equal("This is a backup of a MySQL database, and shop-db is PostgreSQL.", body["error"])
	s.restore(b, s.app, map[string]any{"location": s.loc.ID, "key": mysqlKey, "safety": false, "confirm": "shop-db"})
	s.Equal("This is a backup of a MySQL database, and shop-db is PostgreSQL.", s.restoreError(b, s.app, "source"))

	s.Empty(s.restores(s.app.ID))
	s.Empty(s.restoredSoFar())
}

func (s *BackupsTestSuite) TestAnOlderServerIsRefusedAndANewerMajorVersionWarned() {
	old := s.backedUp(s.app, postgres186, "PGDMP")
	b := signedIn(s.T(), &s.TestCase)
	preview := fmt.Sprintf("/apps/%d/restores/preview?backup=%d", s.app.ID, old.ID)

	s.scriptRestore("postgres (PostgreSQL) 17.2", "PGDMP", 0, nil)
	body := s.getJSON(b, preview, nethttp.StatusOK)
	refusal := "The backup is from PostgreSQL 18.6, and shop-db runs 17.2. A backup never restores into an older server."
	s.Equal(refusal, body["refusal"])
	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": true, "confirm": "shop-db"})
	s.Equal(refusal, s.restoreError(b, s.app, "source"))
	s.Empty(s.restores(s.app.ID))

	s.scriptRestore("postgres (PostgreSQL) 19.0", "PGDMP", 0, nil)
	body = s.getJSON(b, preview, nethttp.StatusOK)
	s.Empty(body["refusal"])
	s.Contains(body["warning"], "The backup is from PostgreSQL 18.6, and shop-db runs 19.0.")

	s.docker.ExecFunc = func(string, []string) ([]byte, error) {
		return nil, fmt.Errorf("echowa-shop-db: %w", docker.ErrNotRunning)
	}
	body = s.getJSON(b, preview, nethttp.StatusOK)
	s.Equal("shop-db has no running task to restore into. Start or deploy it first.", body["refusal"])
}

func (s *BackupsTestSuite) TestAFailedSafetyBackupRestoresNothing() {
	old := s.backedUp(s.app, postgres186, "PGDMP old data")
	s.scriptRestore(postgres186, "PGDMP new data", 0, nil)
	s.storage.PutErr = errors.New("the bucket is full")
	sent := s.FakeNotify()
	ops := models.NotificationProvider{Name: "Slack #ops", Kind: models.NotifySlack, Enabled: true}
	s.Require().NoError(facades.Orm().Query().Create(&ops))
	for _, trigger := range []string{alerts.BackupFailed, alerts.RestoreFailed} {
		rule := models.AlertRule{Scope: models.AlertAllApps, Trigger: trigger, Severity: "critical", Enabled: true}
		s.Require().NoError(facades.Orm().Query().Create(&rule))
		s.Require().NoError(facades.Orm().Query().Model(&rule).Association("Providers").Append([]models.NotificationProvider{ops}))
	}
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": true, "confirm": "shop-db"})
	services.WaitForRestores()
	services.WaitForNotifications()

	rec := s.restores(s.app.ID)[0]
	s.Equal(models.RestoreFailed, rec.Status)
	s.Contains(*rec.Message, "The safety backup failed, so nothing was restored. The upload to R2 failed")
	messages := map[string]notify.Message{}
	for _, m := range sent.Sent() {
		messages[m.Message.Title] = m.Message
	}
	s.Len(sent.Sent(), 2)
	s.Contains(messages["Backup of shop-db failed"].Fields, notify.Field{Label: "Started by", Value: "A restore, for safety"})
	s.Equal(*rec.Message, messages["Restore of shop-db failed"].Summary)
	s.Contains(messages["Restore of shop-db failed"].Fields, notify.Field{Label: "From", Value: path.Base(old.Key)})
	s.Empty(s.restoredSoFar(), "the restore never ran")
	s.Require().NotNil(rec.SafetyBackupID)
	var safety models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&safety, *rec.SafetyBackupID))
	s.Equal(models.BackupFailed, safety.Status)
}

func (s *BackupsTestSuite) TestARestoreIsRefusedWhileTheAppIsHeld() {
	old := s.backedUp(s.app, postgres186, "PGDMP")
	s.scriptRestore(postgres186, "PGDMP", 0, nil)
	unlock, _, ok := services.LockApp(s.app.ID, services.WorkDeploy)
	s.Require().True(ok)
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": true, "confirm": "shop-db"})
	unlock()

	s.Equal("A deploy of shop-db is running. Restore once it has finished.", s.restoreError(b, s.app, "source"))
	s.Empty(s.restores(s.app.ID))
}

func (s *BackupsTestSuite) TestDeploysAndBackupsWaitForARestore() {
	old := s.backedUp(s.app, postgres186, "PGDMP old")
	gate := make(chan struct{})
	s.scriptRestore(postgres186, "PGDMP new", 0, gate)
	var delays []time.Duration
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": false, "confirm": "shop-db"})
	s.Equal(services.WorkRestore, services.AppBusy(s.app.ID))
	_, page := b.visit(fmt.Sprintf("/apps/%d", s.app.ID))
	s.Equal("restore", page.Props["busy"], "Deploy says what it would wait for")

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/deploy", s.app.ID), fmt.Sprintf("/apps/%d", s.app.ID), map[string]any{"ref": "18"})
	var queued models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", s.app.ID).Order("id desc").First(&queued))
	_, page = b.visit(fmt.Sprintf("/apps/%d/deployments/%d", s.app.ID, queued.ID))
	s.Equal(fmt.Sprintf("Deployment #%d of 18 queued. It starts once a restore of shop-db has finished.", queued.ID), page.Flash["info"])
	s.Equal("Waiting for a restore of this app to finish.", page.Props["waitingFor"])
	s.Require().NoError((&deploy.Job{}).Handle(s.app.ID))
	s.Require().NoError(facades.Orm().Query().Find(&queued, queued.ID))
	s.Equal(models.DeploymentQueued, queued.Status, "the deployment waits")
	s.Equal(15*time.Second, delays[len(delays)-1])

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	time.Sleep(300 * time.Millisecond)
	s.Equal(1, s.dumpCount(), "the backup waits too")

	close(gate)
	services.WaitForRestores()
	services.WaitForBackups()
	s.Equal(2, s.dumpCount(), "and runs once the restore is done")
	s.Equal(models.RestoreSucceeded, s.restores(s.app.ID)[0].Status)
	s.Require().NoError(deploy.RunQueued(context.Background(), s.app.ID))
	s.Require().NoError(facades.Orm().Query().Find(&queued, queued.ID))
	s.Equal(models.DeploymentSucceeded, queued.Status)
}

func (s *BackupsTestSuite) TestADumpThatDoesntMatchItsManifestIsCutOffBeforeItsEnd() {
	old := s.backedUp(s.app, postgres186, "PGDMP old data")
	s.Require().NoError(s.storage.Put(context.Background(), old.Key, bytes.NewReader([]byte("PGDMP bad data"))))
	s.scriptRestore(postgres186, "PGDMP", 0, nil)
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, s.app, map[string]any{"backup": old.ID, "safety": false, "confirm": "shop-db"})
	services.WaitForRestores()

	rec := s.restores(s.app.ID)[0]
	s.Equal(models.RestoreFailed, rec.Status)
	s.Equal("The dump in the bucket doesn't match its manifest, so the restore was stopped before its end. Nothing was restored.", *rec.Message)
	s.Equal([]string{""}, s.restoredSoFar(), "the restore never got the dump's end")
}

func (s *BackupsTestSuite) TestAFailedMySQLRestoreSaysWhatItMayHaveLeft() {
	orders := s.templateApp("orders", "mysql")
	old := s.backedUp(orders, mysql974, "CREATE TABLE a;")
	s.scriptRestore(mysql974, "CREATE TABLE b;", 1, nil)
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, orders, map[string]any{"backup": old.ID, "safety": true, "confirm": "orders"})
	services.WaitForRestores()

	rec := s.restores(orders.ID)[0]
	s.Equal(models.RestoreFailed, rec.Status)
	s.Equal(fmt.Sprintf("The restore exited with status 1. Some tables may have been restored and others not. The safety backup is #%d.", *rec.SafetyBackupID), *rec.Message)
}

func (s *BackupsTestSuite) TestRecoveryMarksEarlierRestoresInterrupted() {
	orders := s.templateApp("orders", "mysql")
	started := carbon.NewDateTime(carbon.Now().SubHour())
	pg := &models.Restore{AppID: s.app.ID, StorageLocationID: &s.loc.ID, Key: "k", Status: models.RestoreRunning, StartedAt: started}
	s.Require().NoError(facades.Orm().Query().Create(pg))
	safety := s.backedUp(orders, mysql974, "x")
	my := &models.Restore{
		AppID: orders.ID, StorageLocationID: &s.loc.ID, Key: "k", Status: models.RestoreRunning, StartedAt: started, SafetyBackupID: &safety.ID,
	}
	s.Require().NoError(facades.Orm().Query().Create(my))
	ours := &models.Restore{
		AppID: s.app.ID, StorageLocationID: &s.loc.ID, Key: "k", Status: models.RestoreRunning, StartedAt: carbon.NewDateTime(carbon.Now().AddMinute()),
	}
	s.Require().NoError(facades.Orm().Query().Create(ours))

	s.Require().NoError(services.RecoverRestores(context.Background()))

	rows := s.restores(s.app.ID)
	s.Equal(models.RestoreInterrupted, rows[0].Status)
	s.Equal("Echowa restarted while this ran. Nothing was restored.", *rows[0].Message)
	s.Equal(models.RestoreRunning, rows[1].Status, "this process's own restores are left alone")
	rows = s.restores(orders.ID)
	s.Equal(models.RestoreInterrupted, rows[0].Status)
	s.Equal(fmt.Sprintf("Echowa restarted while this ran. Some tables may have been restored and others not. The safety backup is #%d.", safety.ID), *rows[0].Message)
}

func (s *BackupsTestSuite) TestOnlyTheNewestRestoresAreKept() {
	for range services.KeepRestores + 2 {
		finished := carbon.NewDateTime(carbon.Now())
		rec := &models.Restore{AppID: s.app.ID, StorageLocationID: &s.loc.ID, Key: "k", Status: models.RestoreSucceeded, StartedAt: finished, FinishedAt: finished}
		s.Require().NoError(facades.Orm().Query().Create(rec))
	}
	first := s.restores(s.app.ID)[0].ID

	s.Require().NoError(services.PruneBackupRecords(context.Background(), time.Now()))

	rows := s.restores(s.app.ID)
	s.Len(rows, services.KeepRestores)
	s.Equal(first+2, rows[0].ID, "the oldest two went")
}
