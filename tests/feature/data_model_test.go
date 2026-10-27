package feature

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type DataModelTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestDataModelTestSuite(t *testing.T) {
	suite.Run(t, new(DataModelTestSuite))
}

func (s *DataModelTestSuite) SetupTest() {
	s.RefreshDatabase()
}

const plainToken = "ghp_plaintextTokenThatMustNeverBeStoredOrShown"

// rawColumn reads a column straight from the database, bypassing Secret.
func (s *DataModelTestSuite) rawColumn(table, column string, id uint) string {
	var value string
	s.Require().NoError(facades.DB().Table(table).Where("id", id).Value(column, &value))

	return value
}

func (s *DataModelTestSuite) createRegistry() *models.Registry {
	registry, err := services.CreateRegistry(context.Background(), services.RegistryInput{
		Name: "ghcr", Host: "ghcr.io", Username: "louis", Token: plainToken,
	})
	s.Require().NoError(err)

	return registry
}

func (s *DataModelTestSuite) createApp(name string, registryID *uint) *models.App {
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: "ghcr.io/owner/" + name, DefaultTag: "latest",
		RegistryID: registryID, Port: 8080, Replicas: 1, TLS: true,
	}, nil)
	s.Require().NoError(err)

	return app
}

func (s *DataModelTestSuite) TestRegistryTokenIsEncryptedAtRestAndRoundTrips() {
	registry := s.createRegistry()

	stored := s.rawColumn("registries", "token", registry.ID)
	s.NotContains(stored, plainToken)
	decrypted, err := facades.Crypt().DecryptString(stored)
	s.Require().NoError(err)
	s.Equal(plainToken, decrypted, "stored with Goravel's Crypt and APP_KEY")

	var loaded models.Registry
	s.Require().NoError(facades.Orm().Query().Find(&loaded, registry.ID))
	s.Equal(plainToken, string(loaded.Token))
	s.Equal("ghcr.io", loaded.Host)

	// A fresh nonce on every save.
	s.Require().NoError(services.UpdateRegistry(context.Background(), &loaded, services.RegistryInput{
		Name: "ghcr", Host: "ghcr.io", Username: "louis",
	}))
	again := s.rawColumn("registries", "token", registry.ID)
	s.NotEqual(stored, again)
	s.Require().NoError(facades.Orm().Query().Find(&loaded, registry.ID))
	s.Equal(plainToken, string(loaded.Token), "a blank token on update keeps the stored one")

	s.Require().NoError(services.UpdateRegistry(context.Background(), &loaded, services.RegistryInput{
		Name: "ghcr", Host: "ghcr.io", Username: "louis", Token: "ghp_new",
	}))
	s.Require().NoError(facades.Orm().Query().Find(&loaded, registry.ID))
	s.Equal("ghp_new", string(loaded.Token))
}

func (s *DataModelTestSuite) TestSecretsNeverLeakThroughJSONOrLogs() {
	registry := s.createRegistry()
	app := s.createApp("blog", &registry.ID)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{"API_KEY": "env-secret-value"}))

	var loaded models.App
	s.Require().NoError(facades.Orm().Query().With("Registry").Find(&loaded, app.ID))
	s.Require().NotNil(loaded.Registry)
	var envVars []models.AppEnvVar
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).Get(&envVars))

	secrets := []string{plainToken, string(loaded.WebhookSecret), "env-secret-value"}
	for _, value := range []any{loaded, &loaded, envVars, map[string]any{"token": registry.Token}} {
		encoded, err := json.Marshal(value)
		s.Require().NoError(err)
		printed := fmt.Sprintf("%v %+v %#v", value, value, value)
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, nil)).Info("x", "value", value, "token", registry.Token)

		for _, secret := range secrets {
			s.NotContains(string(encoded), secret)
			s.NotContains(printed, secret)
			s.NotContains(logged.String(), secret)
		}
	}
	encoded, err := json.Marshal(loaded)
	s.Require().NoError(err)
	s.NotContains(string(encoded), "webhook_secret")
	s.Contains(string(encoded), `"repository":"ghcr.io/owner/blog"`)
	s.Contains(string(encoded), `"registry":{`, "relations still serialise, minus their secrets")
}

func (s *DataModelTestSuite) TestTamperedCiphertextFailsLoudly() {
	registry := s.createRegistry()
	_, err := facades.DB().Table("registries").Where("id", registry.ID).Update("token", "not-a-ciphertext")
	s.Require().NoError(err)

	var loaded models.Registry
	err = facades.Orm().Query().Find(&loaded, registry.ID)
	s.ErrorContains(err, "decrypt secret")
}

func (s *DataModelTestSuite) TestAppWebhookSecretIsGeneratedEncryptedAndRotates() {
	app := s.createApp("blog", nil)

	secret := string(app.WebhookSecret)
	s.Regexp(`^[0-9a-f]{64}$`, secret)
	s.NotContains(s.rawColumn("apps", "webhook_secret", app.ID), secret)
	s.Equal("echowa-blog", app.ServiceName())

	s.Require().NoError(services.RotateWebhookSecret(context.Background(), app))
	s.NotEqual(secret, string(app.WebhookSecret))
	var loaded models.App
	s.Require().NoError(facades.Orm().Query().Find(&loaded, app.ID))
	s.Equal(string(app.WebhookSecret), string(loaded.WebhookSecret))

	other := s.createApp("shop", nil)
	s.NotEqual(string(loaded.WebhookSecret), string(other.WebhookSecret))
}

func (s *DataModelTestSuite) TestAppDefaultsAndOptionalFields() {
	blank := "  "
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "worker", Repository: "ghcr.io/owner/worker", DefaultTag: "latest",
		Domain: &blank, HealthcheckCmd: &blank, Port: 3000, Replicas: 1,
	}, nil)
	s.Require().NoError(err)
	s.Nil(app.Domain, "a blank domain is stored as NULL")
	s.Nil(app.HealthcheckCmd)

	// Two apps without a domain don't collide on unique(domain).
	_, err = services.CreateApp(context.Background(), services.AppInput{
		Name: "worker2", Repository: "ghcr.io/owner/worker", DefaultTag: "latest", Port: 3000, Replicas: 1,
	}, nil)
	s.NoError(err)

	// Column defaults, for rows written without the service.
	_, err = facades.DB().Table("apps").Insert(map[string]any{
		"name": "raw", "repository": "nginx", "port": 80, "webhook_secret": "x",
	})
	s.Require().NoError(err)
	var defaults struct {
		DefaultTag string `db:"default_tag"`
		Replicas   int    `db:"replicas"`
		TLS        bool   `db:"tls"`
	}
	s.Require().NoError(facades.DB().Table("apps").Select("default_tag", "replicas", "tls").Where("name", "raw").First(&defaults))
	s.Equal("latest", defaults.DefaultTag)
	s.Equal(1, defaults.Replicas)
	s.True(defaults.TLS)

	_, err = facades.DB().Table("registries").Insert(map[string]any{"name": "r", "username": "u", "token": "x"})
	s.Require().NoError(err)
	var host string
	s.Require().NoError(facades.DB().Table("registries").Where("name", "r").Value("host", &host))
	s.Equal("ghcr.io", host)
}

func (s *DataModelTestSuite) TestEnvVarsRoundTripEncryptedAndReplaceTheWholeSet() {
	app := s.createApp("blog", nil)
	ctx := context.Background()

	s.Require().NoError(services.SetEnvVars(ctx, app.ID, map[string]string{
		"DATABASE_URL": "postgres://user:pa55@db/blog", "EMPTY": "", "MULTI": "line one\nline two",
	}))
	env, err := services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	s.Equal(map[string]string{"DATABASE_URL": "postgres://user:pa55@db/blog", "EMPTY": "", "MULTI": "line one\nline two"}, env)

	var stored []string
	s.Require().NoError(facades.DB().Table("app_env_vars").Pluck("value", &stored))
	s.Len(stored, 3)
	for _, value := range stored {
		s.NotContains(value, "pa55")
		s.NotContains(value, "line one")
	}

	s.Require().NoError(services.SetEnvVars(ctx, app.ID, map[string]string{"ONLY": "1"}))
	env, err = services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	s.Equal(map[string]string{"ONLY": "1"}, env)

	other := s.createApp("shop", nil)
	env, err = services.EnvVars(ctx, other.ID)
	s.Require().NoError(err)
	s.Empty(env, "env vars belong to one app")
}

func (s *DataModelTestSuite) TestEnvKeysAreUniquePerApp() {
	app := s.createApp("blog", nil)
	other := s.createApp("shop", nil)
	insert := func(appID uint) error {
		return facades.Orm().Query().Create(&models.AppEnvVar{AppID: appID, Key: "PORT", Value: "1"})
	}

	s.Require().NoError(insert(app.ID))
	s.Error(insert(app.ID), "unique(app_id, key)")
	s.NoError(insert(other.ID), "the same key in another app is fine")
}

func (s *DataModelTestSuite) TestRegistryInUseCannotBeDeleted() {
	registry := s.createRegistry()
	app := s.createApp("blog", &registry.ID)
	ctx := context.Background()

	s.ErrorIs(services.DeleteRegistry(ctx, registry), services.ErrRegistryInUse)

	s.Require().NoError(services.DeleteApp(ctx, app))
	s.NoError(services.DeleteRegistry(ctx, registry))
}

func (s *DataModelTestSuite) TestDeletingAnAppDeletesItsEnvVarsAndDeployments() {
	app := s.createApp("blog", nil)
	keep := s.createApp("shop", nil)
	ctx := context.Background()
	s.Require().NoError(services.SetEnvVars(ctx, app.ID, map[string]string{"A": "1"}))
	s.Require().NoError(services.SetEnvVars(ctx, keep.ID, map[string]string{"B": "2"}))
	deployment, err := services.CreateDeployment(ctx, app, models.TriggerManual, "latest")
	s.Require().NoError(err)
	_, err = services.CreateDeployment(ctx, keep, models.TriggerWebhook, "v1")
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Model(app).Update("current_deployment_id", deployment.ID)
	s.Require().NoError(err)

	s.Require().NoError(services.DeleteApp(ctx, app))

	count := func(table string) int64 {
		n, err := facades.DB().Table(table).Count()
		s.Require().NoError(err)
		return n
	}
	s.EqualValues(1, count("app_env_vars"))
	s.EqualValues(1, count("deployments"))
	s.EqualValues(1, count("apps"))
}

func (s *DataModelTestSuite) TestScheduledTasksAndRunsGoWithTheirApp() {
	ctx := context.Background()
	app := s.createApp("blog", nil)
	keep := s.createApp("shop", nil)
	addTask := func(app *models.App) *models.ScheduledTask {
		task := &models.ScheduledTask{
			AppID: app.ID, Name: "Scheduler", Schedule: "* * * * *", Timezone: "UTC",
			Command: "php artisan schedule:run", TimeoutMinutes: 15, Enabled: true,
		}
		s.Require().NoError(facades.Orm().Query().Create(task))
		exit := 0
		s.Require().NoError(facades.Orm().Query().Create(&models.ScheduledTaskRun{
			ScheduledTaskID: task.ID, Trigger: models.RunOnSchedule, Status: models.RunSucceeded,
			ExitCode: &exit, Output: "done\n", StartedAt: carbon.NewDateTime(carbon.Now()),
		}))
		return task
	}
	addTask(app)
	kept := addTask(keep)

	s.Require().NoError(services.DeleteApp(ctx, app))

	var tasks []models.ScheduledTask
	s.Require().NoError(facades.Orm().Query().Get(&tasks))
	s.Require().Len(tasks, 1)
	s.Equal(kept.ID, tasks[0].ID)
	var runs []models.ScheduledTaskRun
	s.Require().NoError(facades.Orm().Query().Get(&runs))
	s.Require().Len(runs, 1)
	s.Equal(kept.ID, runs[0].ScheduledTaskID)
	s.Equal("done\n", runs[0].Output)
	s.Require().NotNil(runs[0].ExitCode)
	s.Equal(0, *runs[0].ExitCode, "a zero exit code is stored, not dropped as empty")
}

func (s *DataModelTestSuite) TestScheduledTaskDefaults() {
	app := s.createApp("blog", nil)
	_, err := facades.DB().Table("scheduled_tasks").Insert(map[string]any{
		"app_id": app.ID, "name": "Scheduler", "schedule": "* * * * *", "timezone": "UTC", "command": "true",
	})
	s.Require().NoError(err)

	var task models.ScheduledTask
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).First(&task))
	s.Equal(15, task.TimeoutMinutes)
	s.True(task.Enabled)
	s.Nil(task.DismissedRunID)
}

func (s *DataModelTestSuite) TestAppArgsAndVolumesRoundTrip() {
	app := s.createApp("db", nil)
	plain := s.createApp("blog", nil)
	var raw sql.NullString
	s.Require().NoError(facades.DB().Table("apps").Where("id", plain.ID).Value("args", &raw))
	s.False(raw.Valid, "no args is NULL, which keeps the image's CMD")

	args := []string{"sh", "-c", `exec redis-server --requirepass "$REDIS_PASSWORD"`}
	app.Args = args
	s.Require().NoError(facades.Orm().Query().Save(app))
	addTestVolume(s.T(), app, "echowa-db-1", "/data")
	addTestVolume(s.T(), app, "echowa-db-2", "/logs")

	var loaded models.App
	s.Require().NoError(facades.Orm().Query().With("Volumes").Where("id", app.ID).FirstOrFail(&loaded))
	s.Equal(args, loaded.Args)
	s.Nil(loaded.Template)
	s.Len(loaded.Volumes, 2)
	s.Equal(`["sh","-c","exec redis-server --requirepass \"$REDIS_PASSWORD\""]`, s.rawColumn("apps", "args", app.ID))

	hasVolumes, err := services.HasVolumes(context.Background(), app.ID)
	s.Require().NoError(err)
	s.True(hasVolumes)
	hasVolumes, err = services.HasVolumes(context.Background(), plain.ID)
	s.Require().NoError(err)
	s.False(hasVolumes)
}

func (s *DataModelTestSuite) TestVolumeNamesAreUniqueAndGoWithTheApp() {
	app := s.createApp("db", nil)
	other := s.createApp("cache", nil)
	addTestVolume(s.T(), app, "echowa-db-1", "/data")

	s.Error(facades.Orm().Query().Create(&models.AppVolume{AppID: other.ID, Name: "echowa-db-1", Target: "/data"}),
		"two apps can't mount one volume")
	s.Error(facades.Orm().Query().Create(&models.AppVolume{AppID: app.ID, Name: "echowa-db-2", Target: "/data"}),
		"one path can't have two volumes")

	s.Require().NoError(services.DeleteApp(context.Background(), app))
	n, err := facades.DB().Table("app_volumes").Count()
	s.Require().NoError(err)
	s.Zero(n)
}

func (s *DataModelTestSuite) TestDeploymentDefaultsAndCurrentDeploymentLink() {
	app := s.createApp("blog", nil)
	ctx := context.Background()

	deployment, err := services.CreateDeployment(ctx, app, models.TriggerWebhook, "sha256:"+strings.Repeat("ab", 32))
	s.Require().NoError(err)
	s.Equal(models.DeploymentQueued, deployment.Status)

	var loaded models.Deployment
	s.Require().NoError(facades.Orm().Query().Find(&loaded, deployment.ID))
	s.Equal(models.TriggerWebhook, loaded.Trigger)
	s.Equal("", loaded.Log)
	s.Nil(loaded.Digest)
	s.Nil(loaded.StartedAt)
	s.Nil(loaded.FinishedAt)

	_, err = facades.Orm().Query().Model(app).Update("current_deployment_id", deployment.ID)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Delete(&loaded)
	s.Require().NoError(err)

	var reloaded models.App
	s.Require().NoError(facades.Orm().Query().Find(&reloaded, app.ID))
	s.Nil(reloaded.CurrentDeploymentID, "deleting the current deployment clears the link")
}

func (s *DataModelTestSuite) TestDeletingAnAppDeletesItsMetrics() {
	app := s.createApp("blog", nil)
	keep := s.createApp("shop", nil)
	ctx := context.Background()
	db, err := services.MetricsDB()
	s.Require().NoError(err)
	_, err = db.Exec("DELETE FROM app_samples")
	s.Require().NoError(err)
	var minute metrics.Minute
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	minute.Add(metrics.Reading{At: at, Apps: []metrics.App{{Name: "blog"}, {Name: "shop"}}})
	sample, _ := minute.Add(metrics.Reading{At: at.Add(time.Minute)})
	s.Require().NoError(metrics.Write(ctx, db, sample, map[string]uint{"blog": app.ID, "shop": keep.ID}))

	s.Require().NoError(services.DeleteApp(ctx, app))

	var ids []uint
	rows, err := db.Query("SELECT app_id FROM app_samples")
	s.Require().NoError(err)
	defer rows.Close()
	for rows.Next() {
		var id uint
		s.Require().NoError(rows.Scan(&id))
		ids = append(ids, id)
	}
	s.Equal([]uint{keep.ID}, ids)
}
