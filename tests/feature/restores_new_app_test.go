package feature

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"slices"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/backups"
	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
)

// newAppScript is how a new app's server answers: its version, whether
// it takes connections yet, and its restore's exit code.
type newAppScript struct {
	version string
	ready   bool
	code    int
}

// scriptNewApp scripts the Fake for a restore into a new app, holds its
// first deploy until deploy.RunQueued, and makes the waits short.
func (s *BackupsTestSuite) scriptNewApp(script newAppScript) {
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		for _, slug := range []string{"postgres", "mysql"} {
			tmpl, _ := templates.Find(slug)
			if slices.Equal(cmd, tmpl.Backup.Ready) {
				if script.ready {
					return []byte("1\n"), nil
				}
				return nil, errors.New("connection refused")
			}
		}
		return []byte(script.version), nil
	}
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, errs io.Writer) (int, error) {
		read, err := io.ReadAll(stdin)
		s.mu.Lock()
		s.restored = append(s.restored, service+": "+string(read))
		s.mu.Unlock()
		return script.code, err
	}
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	wait, poll := services.NewAppWait, services.NewAppPoll
	services.NewAppWait, services.NewAppPoll = 300*time.Millisecond, 5*time.Millisecond
	s.T().Cleanup(func() {
		services.WaitForRestores()
		deploy.Dispatch = original
		services.NewAppWait, services.NewAppPoll = wait, poll
	})
}

// restoreToNewApp posts the New app page's From a backup form.
func (s *BackupsTestSuite) restoreToNewApp(b *browser, data map[string]any) {
	b.visit("/apps/create?from=backup")
	b.submit(nethttp.MethodPost, "/apps/restore", "/apps/create?from=backup", data)
}

func (s *BackupsTestSuite) newAppError(b *browser, field string) any {
	_, page := b.visit("/apps/create?from=backup")

	return page.Props["errors"].(map[string]any)[field]
}

func (s *BackupsTestSuite) appNamed(name string) *models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("name", name).First(&app))
	s.Require().NotZero(app.ID, "%s was created", name)

	return &app
}

func (s *BackupsTestSuite) appCount() int64 {
	n, err := facades.Orm().Query().Model(&models.App{}).Count()
	s.Require().NoError(err)

	return n
}

// deployFirst runs the new app's queued first deploy, then waits for the
// restore that waited on it.
func (s *BackupsTestSuite) deployFirst(app *models.App) models.Restore {
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	services.WaitForRestores()
	rows := s.restores(app.ID)
	s.Require().Len(rows, 1)

	return rows[0]
}

func (s *BackupsTestSuite) TestABucketBackupRestoresIntoANewAppOnANewerServer() {
	key := s.putBackup("old-shop", "postgres", "17.2", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP from the old server"))
	s.scriptNewApp(newAppScript{version: postgres186, ready: true})
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)

	_, page := b.visit("/apps/create?from=backup")
	s.Len(page.Props["locations"], 1)
	folders := s.getJSON(b, fmt.Sprintf("/backups/browse?location=%d", s.loc.ID), nethttp.StatusOK)
	s.Equal([]any{"old-shop"}, folders["folders"])
	list := s.getJSON(b, fmt.Sprintf("/backups/browse?location=%d&folder=old-shop", s.loc.ID), nethttp.StatusOK)
	s.Require().Len(list["backups"], 1)
	s.Equal(key, list["backups"].([]any)[0].(map[string]any)["key"])
	preview := s.getJSON(b, fmt.Sprintf("/backups/preview?location=%d&key=%s", s.loc.ID, key), nethttp.StatusOK)
	s.Equal("old-shop", preview["app"])
	s.Equal("postgres", preview["template"])
	s.Equal("17.2", preview["server_version"])
	s.Equal("18", preview["default_tag"])

	s.restoreToNewApp(b, map[string]any{"location": s.loc.ID, "key": key, "name": " Shop-Copy ", "tag": "18"})
	app := s.appNamed("shop-copy")
	s.Equal("postgres", *app.Template)
	s.Equal("18", app.DefaultTag)
	waiting := s.restores(app.ID)
	s.Require().Len(waiting, 1)
	s.Equal(models.RestoreRunning, waiting[0].Status)
	s.Equal("Waiting for the first deploy.", *waiting[0].Message)
	_, page = b.visit(fmt.Sprintf("/apps/%d?tab=backups", app.ID))
	s.Equal("Created shop-copy. Deploying PostgreSQL 18, then restoring old-shop-20261001T030000Z-7.dump into it.", page.Flash["success"])

	rec := s.deployFirst(app)
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.True(rec.NewApp)
	s.Nil(rec.SafetyBackupID, "a new app has nothing to keep")
	s.Nil(rec.BackupID)
	s.Nil(rec.Message)
	s.Equal([]string{app.ServiceName() + ": PGDMP from the old server"}, s.restoredSoFar())
	var d models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).First(&d))
	s.Equal("18", d.Ref)
	s.Equal(models.DeploymentSucceeded, d.Status)
}

func (s *BackupsTestSuite) TestAnAppsBackupRestoresIntoANewApp() {
	orders := s.templateApp("orders", "mysql")
	old := s.backedUp(orders, mysql974, "CREATE TABLE orders;")
	s.scriptNewApp(newAppScript{version: mysql974, ready: true})
	b := signedIn(s.T(), &s.TestCase)

	preview := s.getJSON(b, fmt.Sprintf("/backups/preview?backup=%d", old.ID), nethttp.StatusOK)
	s.Equal("orders", preview["app"])
	s.Equal(false, preview["atomic"])
	s.restoreToNewApp(b, map[string]any{"backup": old.ID, "name": "orders-copy", "tag": "9.7"})
	app := s.appNamed("orders-copy")

	rec := s.deployFirst(app)
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.Equal([]string{app.ServiceName() + ": CREATE TABLE orders;"}, s.restoredSoFar(), "gunzipped, as in place")
	s.Equal(old.Key, rec.Key)
}

func (s *BackupsTestSuite) TestARestoreIntoANewAppThatCantBeMadeMakesNothing() {
	key := s.putBackup("old-shop", "postgres", "17.2", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP"))
	redis := s.putBackup("cache", "redis", "8.0", "none", "cache-20261001T030000Z-8.dump", []byte("x"))
	s.scriptNewApp(newAppScript{version: postgres186, ready: true})
	tmpl, _ := templates.Find("postgres")
	seed, err := tmpl.Seed("leftover")
	s.Require().NoError(err)
	s.docker.AddVolume(seed.Volumes[0].Name)
	b := signedIn(s.T(), &s.TestCase)
	apps := s.appCount()

	cases := []struct {
		data         map[string]any
		field, error string
	}{
		{map[string]any{"location": s.loc.ID, "key": key, "name": "shop-db", "tag": "18"}, "name", "The name has already been taken."},
		{map[string]any{"location": s.loc.ID, "key": key, "name": "shop-copy", "tag": "16-alpine"}, "tag",
			"The backup is from PostgreSQL 17.2, which is newer than 16-alpine. A backup never restores into an older server."},
		{map[string]any{"location": s.loc.ID, "key": key, "name": "leftover", "tag": "18"}, "name",
			"The volume " + seed.Volumes[0].Name + " is left from an earlier app called leftover and may hold its data. Pick another name, or remove it on the server with docker volume rm " + seed.Volumes[0].Name + "."},
		{map[string]any{"location": s.loc.ID, "key": "elsewhere/old-shop/x.json", "name": "shop-copy", "tag": "18"}, "source", "Choose a backup in R2."},
		{map[string]any{"location": s.loc.ID, "key": backups.JoinKey(s.loc.Prefix, "old-shop/old-shop-20261001T030000Z-7.dump"), "name": "shop-copy", "tag": "18"}, "source", "Choose a backup in R2."},
		{map[string]any{"location": s.loc.ID, "key": redis, "name": "shop-copy", "tag": "18"}, "source", "This is a backup of a Redis database, which Echowa can't restore."},
		{map[string]any{"location": s.loc.ID, "key": backups.JoinKey(s.loc.Prefix, "gone/gone-1.json"), "name": "shop-copy", "tag": "18"}, "source", "The backup's manifest isn't in R2 any more."},
	}
	for _, c := range cases {
		s.restoreToNewApp(b, c.data)
		s.Equal(c.error, s.newAppError(b, c.field), "%v", c.data)
	}
	s.Equal(apps, s.appCount(), "no app was made")
	s.Empty(s.restoredSoFar())
}

func (s *BackupsTestSuite) TestAFailedFirstDeploySaysTheNewAppIsEmpty() {
	key := s.putBackup("old-shop", "postgres", "17.2", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP"))
	s.scriptNewApp(newAppScript{version: postgres186, ready: true})
	s.docker.Wait = func(docker.ServiceSpec) (docker.UpdateResult, error) {
		return docker.UpdateResult{Outcome: docker.OutcomeRolledBack, Message: "task: non-zero exit (1)"}, nil
	}
	b := signedIn(s.T(), &s.TestCase)

	s.restoreToNewApp(b, map[string]any{"location": s.loc.ID, "key": key, "name": "shop-copy", "tag": "18"})
	app := s.appNamed("shop-copy")
	rec := s.deployFirst(app)

	s.Equal(models.RestoreFailed, rec.Status)
	s.Regexp(`^shop-copy's first deploy failed: .+\. Nothing was restored, so shop-copy's database is empty\. `+
		`Restore into it from its Backups section once it's running, or delete it\.$`, *rec.Message)
	s.Empty(s.restoredSoFar())
}

func (s *BackupsTestSuite) TestANewAppWhoseServerNeverTakesConnectionsIsntRestored() {
	key := s.putBackup("old-shop", "postgres", "17.2", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP"))
	s.scriptNewApp(newAppScript{version: postgres186, ready: false})
	b := signedIn(s.T(), &s.TestCase)

	s.restoreToNewApp(b, map[string]any{"location": s.loc.ID, "key": key, "name": "shop-copy", "tag": "18"})
	app := s.appNamed("shop-copy")
	rec := s.deployFirst(app)

	s.Equal(models.RestoreFailed, rec.Status)
	s.Contains(*rec.Message, "PostgreSQL in shop-copy didn't take connections within")
	s.Contains(*rec.Message, "Nothing was restored, so shop-copy's database is empty.")
	s.Empty(s.restoredSoFar())
}

func (s *BackupsTestSuite) TestANewAppRunningAnOlderServerThanItsTagSaidIsntRestored() {
	key := s.putBackup("old-shop", "postgres", "18.6", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP"))
	s.scriptNewApp(newAppScript{version: "postgres (PostgreSQL) 17.2\n", ready: true})
	b := signedIn(s.T(), &s.TestCase)

	s.restoreToNewApp(b, map[string]any{"location": s.loc.ID, "key": key, "name": "shop-copy", "tag": "latest"})
	rec := s.deployFirst(s.appNamed("shop-copy"))

	s.Equal(models.RestoreFailed, rec.Status)
	s.Equal("The backup is from PostgreSQL 18.6, and shop-copy runs 17.2. A backup never restores into an older server. "+
		"Nothing was restored, so shop-copy's database is empty. Restore into it from its Backups section once it's running, or delete it.", *rec.Message)
}

func (s *BackupsTestSuite) TestAFailedRestoreIntoANewMySQLAppSaysItMayHoldPartOfIt() {
	orders := s.templateApp("orders", "mysql")
	old := s.backedUp(orders, mysql974, "CREATE TABLE orders;")
	s.scriptNewApp(newAppScript{version: mysql974, ready: true, code: 1})
	b := signedIn(s.T(), &s.TestCase)

	s.restoreToNewApp(b, map[string]any{"backup": old.ID, "name": "orders-copy", "tag": "9.7"})
	rec := s.deployFirst(s.appNamed("orders-copy"))

	s.Equal(models.RestoreFailed, rec.Status)
	s.Equal("The restore exited with status 1. orders-copy may hold part of the backup. Delete it and restore into a new app again.", *rec.Message)
}

func (s *BackupsTestSuite) TestAFirstDeployThatCantBeQueuedSaysSo() {
	key := s.putBackup("old-shop", "postgres", "17.2", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP"))
	s.scriptNewApp(newAppScript{version: postgres186, ready: true})
	deploy.Dispatch = func(uint, time.Duration) error { return errors.New("queue is full") }
	b := signedIn(s.T(), &s.TestCase)

	s.restoreToNewApp(b, map[string]any{"location": s.loc.ID, "key": key, "name": "shop-copy", "tag": "18"})
	rows := s.restores(s.appNamed("shop-copy").ID)

	s.Require().Len(rows, 1)
	s.Equal(models.RestoreFailed, rows[0].Status)
	s.Regexp(`^Couldn't queue shop-copy's first deploy: .*queue is full\. Nothing was restored, so shop-copy's database is empty\.`, *rows[0].Message)
}

func (s *BackupsTestSuite) TestANewAppWaitingForItsRestoreRefusesAnotherAndRecoverySaysWhatsLeft() {
	key := s.putBackup("old-shop", "postgres", "17.2", "none", "old-shop-20261001T030000Z-7.dump", []byte("PGDMP"))
	s.scriptNewApp(newAppScript{version: postgres186, ready: true})
	b := signedIn(s.T(), &s.TestCase)
	s.restoreToNewApp(b, map[string]any{"location": s.loc.ID, "key": key, "name": "shop-copy", "tag": "18"})
	app := s.appNamed("shop-copy")

	src, err := services.FolderSource(context.Background(), &s.loc, key)
	s.Require().NoError(err)
	_, err = services.StartRestore(app, src, nil)
	s.EqualError(err, "A restore of shop-copy is running. Restore once it has finished.")

	// As if Echowa had stopped while the restore waited.
	s.deployFirst(app)
	waiting := "Waiting for the first deploy."
	rec := &models.Restore{
		AppID: app.ID, StorageLocationID: &s.loc.ID, Key: "k", NewApp: true, Status: models.RestoreRunning,
		Message: &waiting, StartedAt: carbon.NewDateTime(carbon.Now().SubHour()),
	}
	s.Require().NoError(facades.Orm().Query().Create(rec))
	s.Require().NoError(services.RecoverRestores(context.Background()))
	s.Require().NoError(facades.Orm().Query().Find(rec, rec.ID))
	s.Equal(models.RestoreInterrupted, rec.Status)
	s.Equal("Echowa restarted before the restore began. Nothing was restored, so shop-copy's database is empty. "+
		"Restore into it from its Backups section once it's running, or delete it.", *rec.Message)
}
