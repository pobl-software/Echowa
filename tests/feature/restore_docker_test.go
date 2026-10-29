//go:build docker

// Restores each template app that has backups over changed data, through
// Echowa's own deploy, backup and restore, on a real Swarm manager and the
// dev machine's S3 server (deploy/s3), and restores a PostgreSQL 17 backup
// into a new app on 18. Run it with deploy/dev-vm.sh test-docker. It pulls
// postgres:17, postgres:18 and mysql:9.7.
package feature

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/backups"
	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type RestoreDockerTestSuite struct {
	suite.Suite
	tests.TestCase
	engine docker.Client
	loc    models.StorageLocation
}

func TestRestoreDockerTestSuite(t *testing.T) {
	suite.Run(t, new(RestoreDockerTestSuite))
}

// probes read and write a template's database through its client, as the
// template's own user. The statement is an argument, never quoted into
// the script.
var probes = map[string][]string{
	"postgres": {"sh", "-c", `exec psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA -F "$(printf '\t')" -c "$1"`, "sh"},
	"mysql":    {"sh", "-c", `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -u root -N -B "$MYSQL_DATABASE" -e "$1"`, "sh"},
}

func (s *RestoreDockerTestSuite) SetupTest() {
	s.RefreshDatabase()
	cfg := docker.DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = 3 * time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := docker.New(cfg)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = engine.Close() })
	if err := engine.Check(context.Background()); err != nil {
		s.T().Skipf("needs a local Swarm manager: %s", err)
	}
	if out, err := exec.Command("docker", "network", "inspect", cfg.Network).CombinedOutput(); err != nil {
		s.T().Skipf("needs the %s network (deploy/dev-vm.sh up): %s", cfg.Network, out)
	}
	s.engine = engine
	s.UseReal(s.T(), engine)

	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })

	s.loc = models.StorageLocation{
		Name: "Dev S3", Endpoint: "http://localhost:7070", Region: "us-east-1", Bucket: "echowa-dev",
		Prefix: fmt.Sprintf("it-restore-%d", time.Now().UnixNano()%1_000_000), PathStyle: true,
		AccessKeyID: "echowa-dev", SecretKey: "echowa-dev-secret",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.loc))
	s.T().Cleanup(func() {
		store := backups.Open(&s.loc)
		listing, err := store.List(context.Background(), s.loc.Prefix+"/", "")
		if err == nil {
			for _, obj := range listing.Objects {
				_ = store.Delete(context.Background(), obj.Key)
			}
		}
	})
}

// deployed creates a template app and deploys it as the New app page and
// Deploy would.
func (s *RestoreDockerTestSuite) deployed(slug string) *models.App {
	tmpl, ok := templates.Find(slug)
	s.Require().True(ok)
	return s.deployedTag(tmpl, tmpl.DefaultTag)
}

func (s *RestoreDockerTestSuite) deployedTag(tmpl templates.Template, tag string) *models.App {
	ctx := context.Background()
	app := s.created(tmpl, tag)
	d, err := deploy.Request(ctx, app, models.TriggerManual, tag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(ctx, app.ID))
	s.Require().NoError(facades.Orm().Query().Find(d, d.ID))
	s.Require().Equal(models.DeploymentSucceeded, d.Status, "deploy: %v", d.Error)

	return app
}

// created makes a template app running tag, and removes its service and
// volumes when the test ends.
func (s *RestoreDockerTestSuite) created(tmpl templates.Template, tag string) *models.App {
	in := services.TemplateInput(tmpl, fmt.Sprintf("it-%s-%d", tmpl.Slug, time.Now().UnixNano()%1_000_000))
	in.DefaultTag = tag
	app, err := services.CreateApp(context.Background(), in, &tmpl)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_ = s.engine.RemoveService(context.Background(), app.ServiceName())
		var volumes []models.AppVolume
		_ = facades.Orm().Query().Where("app_id", app.ID).Get(&volumes)
		for _, v := range volumes {
			deadline := time.Now().Add(time.Minute)
			for s.engine.RemoveVolume(context.Background(), v.Name) != nil && time.Now().Before(deadline) {
				time.Sleep(time.Second)
			}
		}
	})

	return app
}

func (s *RestoreDockerTestSuite) sql(app *models.App, statement string) string {
	s.T().Helper()
	cmd := append(append([]string{}, probes[*app.Template]...), statement)
	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := s.engine.ExecStream(ctx, app.ServiceName(), cmd, nil, &out, &errs)
	s.Require().NoError(err)
	s.Require().Equal(0, code, errs.String())

	return strings.TrimSpace(out.String())
}

// waitForSQL waits until the app's real server answers, as a restore into
// a new app does.
func (s *RestoreDockerTestSuite) waitForSQL(app *models.App) {
	ready := services.AppBackup(app).Ready
	s.Require().Eventually(func() bool {
		out, err := s.engine.Exec(context.Background(), app.ServiceName(), ready)
		return err == nil && strings.TrimSpace(string(out)) == "1"
	}, 2*time.Minute, time.Second, "%s answers", app.Name)
}

func (s *RestoreDockerTestSuite) restored(app *models.App, src *services.RestoreSource, safety *models.StorageLocation) models.Restore {
	_, err := services.StartRestore(app, src, safety)
	s.Require().NoError(err)
	services.WaitForRestores()
	var rows []models.Restore
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).Order("id desc").Limit(1).Get(&rows))
	s.Require().Len(rows, 1)
	s.T().Logf("%s restore: %s, read %d of %d bytes in %s; %v; output %q", app.Name, rows[0].Status, rows[0].ReadBytes,
		rows[0].SizeBytes, rows[0].FinishedAt.StdTime().Sub(rows[0].StartedAt.StdTime()), rows[0].Message, rows[0].Output)

	return rows[0]
}

func (s *RestoreDockerTestSuite) TestARestoreOverChangedDataBringsBackTheBackup() {
	for _, slug := range []string{"postgres", "mysql"} {
		s.Run(slug, func() {
			app := s.deployed(slug)
			s.waitForSQL(app)
			s.sql(app, "CREATE TABLE kept (id int PRIMARY KEY, note varchar(50))")
			s.sql(app, "INSERT INTO kept VALUES (1, 'café'), (2, 'naïve ☃')")

			rec, err := services.BackUpNow(app, &s.loc)
			s.Require().NoError(err)
			services.WaitForBackups()
			s.Require().NoError(facades.Orm().Query().Find(rec, rec.ID))
			s.Require().Equal(models.BackupSucceeded, rec.Status, "backup: %v %s", rec.Message, rec.Output)

			s.sql(app, "UPDATE kept SET note = 'changed' WHERE id = 1")
			s.sql(app, "DELETE FROM kept WHERE id = 2")
			s.Equal("1\tchanged", s.sql(app, "SELECT id, note FROM kept ORDER BY id"))

			src, err := services.BackupSource(context.Background(), app, rec.ID)
			s.Require().NoError(err)
			restore := s.restored(app, src, &s.loc)
			s.Require().Equal(models.RestoreSucceeded, restore.Status)
			s.Equal("1\tcafé\n2\tnaïve ☃", s.sql(app, "SELECT id, note FROM kept ORDER BY id"), "the backup's data is back")

			s.Require().NotNil(restore.SafetyBackupID)
			var safety models.Backup
			s.Require().NoError(facades.Orm().Query().Find(&safety, *restore.SafetyBackupID))
			s.Equal(models.BackupSucceeded, safety.Status)
			store := backups.Open(&s.loc)
			for _, key := range []string{safety.Key, backups.ManifestKey(safety.Key)} {
				body, err := store.Get(context.Background(), key)
				s.Require().NoError(err, "the safety backup is in the bucket: %s", key)
				_ = body.Close()
			}

			// Restoring the safety backup brings the changed data back.
			src, err = services.BackupSource(context.Background(), app, safety.ID)
			s.Require().NoError(err)
			s.Require().Equal(models.RestoreSucceeded, s.restored(app, src, nil).Status)
			s.Equal("1\tchanged", s.sql(app, "SELECT id, note FROM kept ORDER BY id"))
		})
	}
}

// An export, uploaded back as the restore dialog sends it, restores over
// changed data: the round trip Restore from a file is for. Its first bytes
// say which server it came from, with no manifest.
func (s *RestoreDockerTestSuite) TestAnExportUploadedBackRestoresOverChangedData() {
	for _, slug := range []string{"postgres", "mysql"} {
		s.Run(slug, func() {
			ctx := context.Background()
			app := s.deployed(slug)
			s.waitForSQL(app)
			s.sql(app, "CREATE TABLE kept (id int PRIMARY KEY, note varchar(50))")
			s.sql(app, "INSERT INTO kept VALUES (1, 'café'), (2, 'naïve ☃')")

			format, _ := services.AppBackup(app).FindExport("")
			export, err := services.StartExport(ctx, app, format, time.Now())
			s.Require().NoError(err)
			file, err := io.ReadAll(export)
			s.Require().NoError(err)
			s.Require().NoError(export.Err())
			s.Require().NoError(export.Close())

			s.sql(app, "UPDATE kept SET note = 'changed' WHERE id = 1")
			s.sql(app, "DELETE FROM kept WHERE id = 2")

			upload, err := services.StartUpload(app, export.Filename, int64(len(file)))
			s.Require().NoError(err)
			for sent := 0; sent < len(file); sent += services.UploadChunk {
				_, err := upload.Write(int64(sent), bytes.NewReader(file[sent:min(len(file), sent+services.UploadChunk)]))
				s.Require().NoError(err)
			}
			src, err := services.UploadSource(app, upload.ID)
			s.Require().NoError(err)
			preview, err := services.PreviewRestore(ctx, app, src)
			s.Require().NoError(err)
			s.Empty(preview.Refusal)
			s.Equal(preview.AppVersion, preview.ServerVersion, "the dump says which server it came from")
			s.Equal(preview.AppDatabase, preview.Database)

			restore := s.restored(app, src, nil)
			s.Require().Equal(models.RestoreSucceeded, restore.Status)
			s.Equal("1\tcafé\n2\tnaïve ☃", s.sql(app, "SELECT id, note FROM kept ORDER BY id"), "the export's data is back")
		})
	}
}

// A dump changed in the bucket fails its check only at its end. Holding
// that end back means pg_restore never commits.
func (s *RestoreDockerTestSuite) TestAPostgresDumpThatDoesntMatchItsManifestChangesNothing() {
	app := s.deployed("postgres")
	s.waitForSQL(app)
	s.sql(app, "CREATE TABLE kept (id int PRIMARY KEY, note text)")
	s.sql(app, "INSERT INTO kept VALUES (1, 'backed up')")
	rec, err := services.BackUpNow(app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	s.Require().NoError(facades.Orm().Query().Find(rec, rec.ID))
	s.Require().Equal(models.BackupSucceeded, rec.Status)

	store := backups.Open(&s.loc)
	body, err := store.Get(context.Background(), rec.Key)
	s.Require().NoError(err)
	dump, err := io.ReadAll(body)
	_ = body.Close()
	s.Require().NoError(err)
	// The same size, with its last byte changed: only the hash tells.
	dump[len(dump)-1] ^= 0xff
	s.Require().NoError(store.Put(context.Background(), rec.Key, bytes.NewReader(dump)))
	s.sql(app, "UPDATE kept SET note = 'changed'")

	src, err := services.BackupSource(context.Background(), app, rec.ID)
	s.Require().NoError(err)
	restore := s.restored(app, src, nil)
	s.Equal(models.RestoreFailed, restore.Status)
	s.Contains(*restore.Message, "doesn't match its manifest")
	s.Equal("changed", s.sql(app, "SELECT note FROM kept"), "pg_restore rolled back")
}

// A backup restores into a new app on a newer major version, made and
// deployed by Echowa, which waits for its real server before restoring.
func (s *RestoreDockerTestSuite) TestAPostgres17BackupRestoresIntoANewAppOn18() {
	tmpl, _ := templates.Find("postgres")
	old := s.deployedTag(tmpl, "17")
	s.waitForSQL(old)
	s.sql(old, "CREATE TABLE kept (id int PRIMARY KEY, note text)")
	s.sql(old, "INSERT INTO kept VALUES (1, 'from 17'), (2, 'naïve ☃')")
	rec, err := services.BackUpNow(old, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	s.Require().NoError(facades.Orm().Query().Find(rec, rec.ID))
	s.Require().Equal(models.BackupSucceeded, rec.Status, "backup: %v %s", rec.Message, rec.Output)

	src, err := services.FolderSource(context.Background(), &s.loc, backups.ManifestKey(rec.Key))
	s.Require().NoError(err)
	s.Require().NoError(services.CheckRestoreTag(tmpl, src.Manifest, "18"))
	app := s.created(tmpl, "18")
	restore, err := services.StartNewAppRestore(app, src, func() (uint, error) {
		d, err := deploy.Request(context.Background(), app, models.TriggerManual, "18")
		if err != nil {
			return 0, err
		}
		return d.ID, nil
	})
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	services.WaitForRestores()
	s.Require().NoError(facades.Orm().Query().Find(restore, restore.ID))
	s.T().Logf("%s restore: %s in %s; %v; output %q", app.Name, restore.Status,
		restore.FinishedAt.StdTime().Sub(restore.StartedAt.StdTime()), restore.Message, restore.Output)

	s.Require().Equal(models.RestoreSucceeded, restore.Status)
	s.Equal("18", strings.SplitN(s.sql(app, "SHOW server_version"), ".", 2)[0])
	s.Equal("1\tfrom 17\n2\tnaïve ☃", s.sql(app, "SELECT id, note FROM kept ORDER BY id"))
}
