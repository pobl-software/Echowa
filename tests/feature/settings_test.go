package feature

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type SettingsTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestSettingsTestSuite(t *testing.T) {
	suite.Run(t, new(SettingsTestSuite))
}

func (s *SettingsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	s.FakeNotify()
}

func (s *SettingsTestSuite) overview() map[string]any {
	_, page := signedIn(s.T(), &s.TestCase).visit("/settings")
	page.AssertComponent("Settings/Index")

	return page.Props
}

func (s *SettingsTestSuite) TestTheOverviewSaysWhatEachSectionNeeds() {
	props := s.overview()
	s.Equal(map[string]any{"count": float64(0), "apps": float64(0)}, props["registries"])
	s.Equal(map[string]any{"locations": float64(0), "backups": float64(0), "databases": float64(0)}, props["storage"])
	s.Equal(map[string]any{"providers": float64(0), "enabled": float64(0), "failing": []any{}}, props["notifications"])
	s.Contains(props["update"], "current")
	s.Contains(props["prune"], "last")

	registry := createTestRegistry(s.T(), "ghp_secret")
	createTestApp(s.T(), "blog", &registry.ID)
	createTestApp(s.T(), "docs", nil)
	tmpl, _ := templates.Find("postgres")
	_, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "blog-db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	redis, _ := templates.Find("redis")
	_, err = services.CreateApp(context.Background(), services.AppInput{
		Name: "cache", Repository: redis.Repository, DefaultTag: redis.DefaultTag, Port: redis.Port, Replicas: 1,
	}, &redis)
	s.Require().NoError(err)

	ops := createTestProvider(s.T(), "Slack #ops")
	off := createTestProvider(s.T(), "Slack #quiet")
	_, err = facades.Orm().Query().Model(&off).Update("enabled", false)
	s.Require().NoError(err)
	failure := "the provider answered 404 Not Found: no_service"
	s.Require().NoError(facades.Orm().Query().Create(&models.Notification{NotificationProviderID: ops.ID, Trigger: alerts.DeployFailed,
		Severity: "critical", Title: "Deploy of blog failed", Status: models.NotificationFailed, Attempts: 1, Error: &failure}))

	props = s.overview()
	s.Equal(map[string]any{"count": float64(1), "apps": float64(1)}, props["registries"])
	s.Equal(float64(2), props["storage"].(map[string]any)["databases"], "PostgreSQL and Redis are both backed up")
	s.Equal(map[string]any{"providers": float64(2), "enabled": float64(1), "failing": []any{"Slack #ops"}}, props["notifications"])
	s.Equal(float64(0), props["alerts"].(map[string]any)["rules"])
	s.NotZero(props["alerts"].(map[string]any)["suggestions"])
}

func (s *SettingsTestSuite) TestEachSectionHasItsOwnPage() {
	b := signedIn(s.T(), &s.TestCase)
	for url, component := range map[string]string{
		"/settings/updates":       "Settings/Updates",
		"/settings/registries":    "Settings/Registries",
		"/settings/storage":       "Settings/Storage",
		"/settings/pruning":       "Settings/Pruning",
		"/settings/notifications": "Settings/Notifications",
		"/settings/alerts":        "Settings/Alerts",
	} {
		_, page := b.visit(url)
		page.AssertComponent(component)
	}
}
