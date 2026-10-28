package feature

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	nethttp "net/http"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/goravel/framework/support/carbon"
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

type BackupsTestSuite struct {
	suite.Suite
	tests.TestCase
	docker  *docker.Fake
	storage *backups.Fake
	loc     models.StorageLocation
	app     *models.App

	mu    sync.Mutex
	dumps [][]string
	// restored holds what each restore read, from restores_test.go.
	restored []string
}

func TestBackupsTestSuite(t *testing.T) {
	suite.Run(t, new(BackupsTestSuite))
}

func (s *BackupsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	s.storage = s.FakeStorage()
	s.dumps = nil
	s.restored = nil
	s.loc = models.StorageLocation{
		Name: "R2", Endpoint: "https://abc123.r2.cloudflarestorage.com", Region: "auto", Bucket: "echowa-backups",
		Prefix: "server-1", AccessKeyID: "AKIAEXAMPLE", SecretKey: models.Secret(storageSecret),
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.loc))
	s.app = s.templateApp("shop-db", "postgres")
	s.T().Cleanup(services.WaitForBackups)
	s.T().Cleanup(services.WaitForRestores)
}

func (s *BackupsTestSuite) templateApp(name, slug string) *models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)

	return app
}

// script makes the app's server report version, and its dump write out
// and stderr and exit with code. gate, when set, holds the dump until it's
// closed.
func (s *BackupsTestSuite) script(version, out string, code int, stderr string, gate chan struct{}) {
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		return []byte(version), nil
	}
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, errs io.Writer) (int, error) {
		s.mu.Lock()
		s.dumps = append(s.dumps, append([]string{service}, cmd...))
		s.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		if _, err := io.WriteString(stdout, out); err != nil {
			return 0, err
		}
		_, _ = io.WriteString(errs, stderr)
		return code, nil
	}
}

func (s *BackupsTestSuite) dumpCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.dumps)
}

func (s *BackupsTestSuite) backUp(b *browser, app *models.App) {
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/backups", app.ID), fmt.Sprintf("/apps/%d?tab=backups", app.ID),
		map[string]any{"location": s.loc.ID}).AssertHeader("Location", fmt.Sprintf("/apps/%d?tab=backups", app.ID))
}

func (s *BackupsTestSuite) only(appID uint) models.Backup {
	var rows []models.Backup
	s.Require().NoError(facades.Orm().Query().Where("app_id", appID).Get(&rows))
	s.Require().Len(rows, 1)

	return rows[0]
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *BackupsTestSuite) TestBackUpNowWritesTheDumpThenItsManifest() {
	const dump = "PGDMP custom format"
	s.script("postgres (PostgreSQL) 18.6 (Debian 18.6-1.pgdg13+2)\n", dump, 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))

	s.backUp(b, s.app)
	services.WaitForBackups()

	rec := s.only(s.app.ID)
	s.Equal(models.BackupSucceeded, rec.Status, "message: %v", rec.Message)
	s.Equal(models.BackupByHand, rec.Trigger)
	s.Regexp(fmt.Sprintf(`^server-1/shop-db/shop-db-\d{8}T\d{6}Z-%d\.dump$`, rec.ID), rec.Key)
	s.EqualValues(len(dump), rec.SizeBytes)
	s.Equal(sha([]byte(dump)), rec.SHA256)
	s.Equal("18.6", rec.ServerVersion)
	s.NotNil(rec.FinishedAt)
	tmpl, _ := templates.Find("postgres")
	s.Equal([][]string{append([]string{"echowa-shop-db"}, tmpl.Backup.Dump...)}, s.dumps, "the template's own command, in the app's service")

	manifestKey := backups.ManifestKey(rec.Key)
	s.Equal([]string{rec.Key, manifestKey}, s.storage.Keys())
	body, err := s.storage.Get(context.Background(), manifestKey)
	s.Require().NoError(err)
	raw, _ := io.ReadAll(body)
	manifest, err := backups.ParseManifest(raw)
	s.Require().NoError(err)
	s.Equal("shop-db", manifest.App)
	s.Equal("postgres", manifest.Template)
	s.Equal("18.6", manifest.ServerVersion)
	s.Equal("shop-db", manifest.Database, "the template's POSTGRES_DB")
	s.Equal(path.Base(rec.Key), manifest.Object)
	s.Equal("none", manifest.Compression)
	s.EqualValues(len(dump), manifest.Size)
	s.Equal(rec.SHA256, manifest.SHA256)
	s.Equal(rec.StartedAt.StdTime().UTC(), manifest.StartedAt)
	s.Equal(s.loc.ID, tests.OpenedStorage[0].ID)

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	section := page.Props["backups"].(map[string]any)
	s.Equal("shop-db", section["database"])
	rows := section["backups"].([]any)
	s.Require().Len(rows, 1)
	row := rows[0].(map[string]any)
	s.Equal("succeeded", row["status"])
	s.Equal("R2", row["location"])
	s.Equal(path.Base(rec.Key), row["name"])
	s.NotContains(row, "output", "the history leaves out each backup's output")
	s.Equal(true, page.Props["template"].(map[string]any)["backups"])
}

func (s *BackupsTestSuite) TestMySQLDumpsAreGzippedByEchowa() {
	mysql := s.templateApp("orders", "mysql")
	const dump = "CREATE TABLE kept (id int);\n"
	s.script("/usr/sbin/mysqld  Ver 9.7.4 for Linux on aarch64\n", dump, 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", mysql.ID))

	s.backUp(b, mysql)
	services.WaitForBackups()

	rec := s.only(mysql.ID)
	s.Equal(models.BackupSucceeded, rec.Status, "message: %v", rec.Message)
	s.Regexp(`^server-1/orders/orders-\d{8}T\d{6}Z-\d+\.sql\.gz$`, rec.Key)
	s.Equal("9.7.4", rec.ServerVersion)
	body, err := s.storage.Get(context.Background(), rec.Key)
	s.Require().NoError(err)
	stored, _ := io.ReadAll(body)
	s.Equal(sha(stored), rec.SHA256, "the hash is of what's stored")
	s.EqualValues(len(stored), rec.SizeBytes)
	unzipped, err := gzip.NewReader(bytes.NewReader(stored))
	s.Require().NoError(err)
	plain, err := io.ReadAll(unzipped)
	s.Require().NoError(err)
	s.Equal(dump, string(plain))

	raw, _ := io.ReadAll(must(s.storage.Get(context.Background(), backups.ManifestKey(rec.Key))))
	manifest, err := backups.ParseManifest(raw)
	s.Require().NoError(err)
	s.Equal("gzip", manifest.Compression)
	s.Equal("orders", manifest.Database, "the template's MYSQL_DATABASE")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}

	return v
}

func (s *BackupsTestSuite) TestADumpThatExitsNonZeroLeavesNoObject() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP half a dump", 1, "pg_dump: error: query failed: out of memory", nil)

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()

	rec := s.only(s.app.ID)
	s.Equal(models.BackupFailed, rec.Status)
	s.Equal("The dump exited with status 1.", *rec.Message)
	s.Contains(rec.Output, "pg_dump: error: query failed")
	s.Empty(rec.SHA256)
	s.Empty(s.storage.Keys(), "the upload was aborted")
}

func (s *BackupsTestSuite) TestAnUploadThatFailsFailsTheBackup() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	s.storage.PutErr = &smithy.GenericAPIError{Code: "AccessDenied"}

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()

	rec := s.only(s.app.ID)
	s.Equal(models.BackupFailed, rec.Status)
	s.Equal("The upload to R2 failed: These keys aren't allowed to do this in the bucket.", *rec.Message)
	s.Empty(s.storage.Keys())
}

func (s *BackupsTestSuite) TestAnAppWithNoRunningTaskIsSkipped() {
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		return nil, fmt.Errorf("%s: %w", service, docker.ErrNotRunning)
	}

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()

	rec := s.only(s.app.ID)
	s.Equal(models.BackupSkipped, rec.Status)
	s.Equal("The app has no running task to back up.", *rec.Message)
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestASecondBackUpNowWaitsForTheFirst() {
	gate := make(chan struct{})
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", gate)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))

	s.backUp(b, s.app)
	s.backUp(b, s.app)
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	close(gate)
	services.WaitForBackups()

	s.Equal("A backup of shop-db is already running.", page.Flash["info"])
	s.Equal(models.BackupSucceeded, s.only(s.app.ID).Status, "only the first was recorded")
}

func (s *BackupsTestSuite) TestADeployWaitsForABackup() {
	gate := make(chan struct{})
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", gate)
	var delays []time.Duration
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
	b := signedIn(s.T(), &s.TestCase)

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	s.Eventually(func() bool { return s.dumpCount() == 1 }, time.Second, 10*time.Millisecond, "the dump started")
	d, err := deploy.Request(context.Background(), s.app, models.TriggerManual, "18")
	s.Require().NoError(err)
	s.Require().NoError((&deploy.Job{}).Handle(s.app.ID))

	var queued models.Deployment
	s.Require().NoError(facades.Orm().Query().Find(&queued, d.ID))
	s.Equal(models.DeploymentQueued, queued.Status, "the deployment waits")
	s.Equal([]time.Duration{0, 15 * time.Second}, delays, "and its job comes back in 15 seconds")
	_, page := b.visit(fmt.Sprintf("/apps/%d/deployments/%d", s.app.ID, d.ID))
	s.Equal("Waiting for a backup of this app to finish.", page.Props["waitingFor"])

	close(gate)
	services.WaitForBackups()
	s.Require().NoError(deploy.RunQueued(context.Background(), s.app.ID))
	s.Require().NoError(facades.Orm().Query().Find(&queued, d.ID))
	s.Equal(models.DeploymentSucceeded, queued.Status, "it runs once the backup is done")
	_, page = b.visit(fmt.Sprintf("/apps/%d/deployments/%d", s.app.ID, d.ID))
	s.Empty(page.Props["waitingFor"])
}

func (s *BackupsTestSuite) TestABackupWaitsForADeploy() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	unlock, _, ok := services.LockApp(s.app.ID, services.WorkDeploy)
	s.Require().True(ok)

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	time.Sleep(400 * time.Millisecond)
	s.Zero(s.dumpCount(), "nothing runs while the deploy holds the app")
	s.Equal(models.BackupRunning, s.only(s.app.ID).Status)

	unlock()
	services.WaitForBackups()
	s.Equal(models.BackupSucceeded, s.only(s.app.ID).Status)
}

func (s *BackupsTestSuite) TestABackupHeldUpTooLongIsSkipped() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	wait := services.BackupLockWait
	services.BackupLockWait = 300 * time.Millisecond
	s.T().Cleanup(func() { services.BackupLockWait = wait })
	unlock, _, ok := services.LockApp(s.app.ID, services.WorkDeploy)
	s.Require().True(ok)
	defer unlock()

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()

	rec := s.only(s.app.ID)
	s.Equal(models.BackupSkipped, rec.Status)
	s.Equal("Waited 300ms for a deploy of this app to finish.", *rec.Message)
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestRecoveryMarksEarlierBackupsInterruptedAndAbortsTheirUploads() {
	old := &models.Backup{
		AppID: s.app.ID, StorageLocationID: s.loc.ID, Trigger: models.BackupByHand, Status: models.BackupRunning,
		Key: "server-1/shop-db/shop-db-20261027T221734Z-1.dump", StartedAt: carbon.NewDateTime(carbon.Now().SubHour()),
	}
	s.Require().NoError(facades.Orm().Query().Create(old))
	current := &models.Backup{
		AppID: s.app.ID, StorageLocationID: s.loc.ID, Trigger: models.BackupByHand, Status: models.BackupRunning,
		Key: "server-1/shop-db/shop-db-later-2.dump", StartedAt: carbon.NewDateTime(carbon.Now().AddMinute()),
	}
	s.Require().NoError(facades.Orm().Query().Create(current))

	s.Require().NoError(services.RecoverBackups(context.Background()))

	var rec models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&rec, old.ID))
	s.Equal(models.BackupInterrupted, rec.Status)
	s.Equal("Echowa restarted while this ran, so the dump was abandoned.", *rec.Message)
	s.Equal([]string{old.Key}, s.storage.Aborted())
	var ours models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&ours, current.ID))
	s.Equal(models.BackupRunning, ours.Status, "this process's own backups are left alone")
}

func (s *BackupsTestSuite) TestRedisAndCustomAppsHaveNoBackups() {
	redis := s.templateApp("cache", "redis")
	custom := createTestApp(s.T(), "blog", nil)
	rec := &models.Backup{
		AppID: redis.ID, StorageLocationID: s.loc.ID, Trigger: models.BackupByHand, Status: models.BackupSucceeded,
		Key: "k", StartedAt: carbon.NewDateTime(carbon.Now()),
	}
	s.Require().NoError(facades.Orm().Query().Create(rec))
	b := signedIn(s.T(), &s.TestCase)

	for _, app := range []*models.App{redis, custom} {
		_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID))
		s.Nil(page.Props["backups"], app.Name)
		b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/backups", app.ID), fmt.Sprintf("/apps/%d", app.ID),
			map[string]any{"location": s.loc.ID}).AssertNotFound()
		b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/backups/%d", app.ID, rec.ID), nil, false, nil).AssertNotFound()
		b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/backups/%d/download", app.ID, rec.ID), nil, false, nil).AssertNotFound()
	}
	_, page := b.visit(fmt.Sprintf("/apps/%d", redis.ID))
	s.Equal(false, page.Props["template"].(map[string]any)["backups"])
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestDownloadStreamsTheDumpThroughEchowa() {
	const dump = "PGDMP a whole dump"
	s.script("postgres (PostgreSQL) 18.6", dump, 0, "", nil)
	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	rec := s.only(s.app.ID)
	b := signedIn(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/backups/%d/download", s.app.ID, rec.ID), nil, false, nil)

	resp.AssertOk()
	resp.AssertHeader("Content-Disposition", `attachment; filename="`+path.Base(rec.Key)+`"`)
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal(dump, body)

	detail := b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/backups/%d", s.app.ID, rec.ID), nil, false,
		map[string]string{"Accept": "application/json"})
	detail.AssertOk()
	json, err := detail.Json()
	s.Require().NoError(err)
	s.Equal(rec.SHA256, json["sha256"])
	s.NotContains(json, "storage_location", "the location, with its keys, stays on the server")
}

func (s *BackupsTestSuite) TestDeleteRemovesTheObjectsAndTheRecord() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	rec := s.only(s.app.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/backups/%d", s.app.ID, rec.ID), fmt.Sprintf("/apps/%d?tab=backups", s.app.ID), nil).
		AssertHeader("Location", fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))

	s.Empty(s.storage.Keys())
	count, err := facades.Orm().Query().Model(&models.Backup{}).Count()
	s.Require().NoError(err)
	s.Zero(count)
}

func (s *BackupsTestSuite) TestAFailedDeleteKeepsTheRecord() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	s.storage.Err = &smithy.GenericAPIError{Code: "AccessDenied"}
	rec := s.only(s.app.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/backups/%d", s.app.ID, rec.ID), fmt.Sprintf("/apps/%d?tab=backups", s.app.ID), nil)

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	s.Contains(page.Flash["error"], "These keys aren't allowed")
	s.Equal(rec.ID, s.only(s.app.ID).ID)
}

func (s *BackupsTestSuite) TestAnAppCantBeDeletedDuringABackup() {
	gate := make(chan struct{})
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", gate)
	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	s.Eventually(func() bool { return s.dumpCount() == 1 }, time.Second, 10*time.Millisecond)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=danger", s.app.ID))

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d", s.app.ID), fmt.Sprintf("/apps/%d?tab=danger", s.app.ID), nil)
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=danger", s.app.ID))
	close(gate)

	s.Equal("A backup of shop-db is running. Wait for it to finish, then delete the app.", page.Flash["error"])
	count, err := facades.Orm().Query().Model(&models.App{}).Where("id", s.app.ID).Count()
	s.Require().NoError(err)
	s.EqualValues(1, count)
}

func (s *BackupsTestSuite) TestTheLastUsedLocationComesFirst() {
	other := models.StorageLocation{
		Name: "AAA first by name", Endpoint: "https://s3.eu-west-2.amazonaws.com", Region: "eu-west-2", Bucket: "other",
		AccessKeyID: "AKIAOTHER", SecretKey: "other-secret",
	}
	s.Require().NoError(facades.Orm().Query().Create(&other))
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	locations := page.Props["backups"].(map[string]any)["locations"].([]any)
	s.Equal("AAA first by name", locations[0].(map[string]any)["name"])

	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	_, page = b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	locations = page.Props["backups"].(map[string]any)["locations"].([]any)
	s.Equal("R2", locations[0].(map[string]any)["name"])
	s.Len(locations, 2)
}

func (s *BackupsTestSuite) TestALocationWithBackupsKeepsItsBucket() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	_, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)

	_, page := b.visit("/settings")
	row := page.Props["storageLocations"].([]any)[0].(map[string]any)
	s.Equal(float64(1), row["backups"])
	s.NotContains(row, "secret_key")

	url := fmt.Sprintf("/settings/storage/%d", s.loc.ID)
	b.submit(nethttp.MethodPut, url, "/settings", storageForm(map[string]any{"bucket": "another-bucket", "secret_key": ""}))
	_, page = b.visit("/settings")
	s.Contains(page.Props["errors"].(map[string]any)["bucket"], "can't change")
	var loc models.StorageLocation
	s.Require().NoError(facades.Orm().Query().Find(&loc, s.loc.ID))
	s.Equal("echowa-backups", loc.Bucket)

	b.submit(nethttp.MethodPut, url, "/settings", storageForm(map[string]any{"name": "Renamed", "secret_key": ""})).
		AssertHeader("Location", "/settings")
	s.Require().NoError(facades.Orm().Query().Find(&loc, s.loc.ID))
	s.Equal("Renamed", loc.Name, "the rest can change")

	b.submit(nethttp.MethodDelete, url, "/settings", nil)
	_, page = b.visit("/settings")
	s.Equal("Deleted Renamed and Echowa's records of its 1 backup. They're still in the bucket.", page.Flash["success"])
	count, err := facades.Orm().Query().Model(&models.Backup{}).Count()
	s.Require().NoError(err)
	s.Zero(count, "the records go with the location")
	s.Len(s.storage.Keys(), 2, "the objects stay")
}
