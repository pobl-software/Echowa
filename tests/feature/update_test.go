package feature

import (
	"context"
	"errors"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gonertia "github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/models"
	"goravel/app/release"
	"goravel/app/services"
	"goravel/app/update"
	"goravel/tests"
)

type UpdateTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestUpdateTestSuite(t *testing.T) {
	suite.Run(t, new(UpdateTestSuite))
}

func (s *UpdateTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	services.ResetUpdateCheck()

	version, fetch, dir, dispatch := release.Version, services.FetchLatestRelease, update.Dir, deploy.Dispatch
	release.Version = "v1.2.0"
	update.Dir = filepath.Join(s.T().TempDir(), "update")
	services.FetchLatestRelease = func(context.Context) (services.LatestRelease, error) {
		return services.LatestRelease{Version: "v1.3.0", PublishedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Notes: "- Faster deploys"}, nil
	}
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() {
		release.Version, services.FetchLatestRelease, update.Dir, deploy.Dispatch = version, fetch, dir, dispatch
		services.ResetUpdateCheck()
	})
}

// prop reads a nested prop, "update.check.latest.version".
func prop(page gonertia.AssertableInertia, path string) any {
	var v any = map[string]any(page.Props)
	for _, key := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}

	return v
}

func (s *UpdateTestSuite) check() {
	services.CheckForUpdate(context.Background())
}

func (s *UpdateTestSuite) TestSettingsShowsTheNewestRelease() {
	s.check()
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings")

	s.Equal("v1.2.0", prop(page, "update.current"), "update.current")
	s.Equal(false, prop(page, "update.dev"), "update.dev")
	s.Equal(true, prop(page, "update.available"), "update.available")
	s.Equal("v1.3.0", prop(page, "update.check.latest.version"), "update.check.latest.version")
	s.Equal("- Faster deploys", prop(page, "update.check.latest.notes"), "update.check.latest.notes")
	s.Equal(false, prop(page, "update.busy"), "update.busy")
}

func (s *UpdateTestSuite) TestAFailedCheckKeepsTheLastAnswer() {
	s.check()
	services.FetchLatestRelease = func(context.Context) (services.LatestRelease, error) {
		return services.LatestRelease{}, errors.New("latest.json: the signature doesn't match the release key")
	}
	s.check()

	state := services.UpdateCheckState()
	s.Equal("v1.3.0", state.Latest.Version)
	s.Contains(state.Error, "signature")
}

func (s *UpdateTestSuite) TestUpdatingWritesARequestForTheUpdater() {
	s.check()
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	b.submit(nethttp.MethodPost, "/settings/update", "/settings", map[string]any{"version": "v1.3.0"}).AssertHeader("Location", "/settings")
	requested, ok := update.Requested()
	s.True(ok)
	s.Equal("v1.3.0", requested)

	_, page := b.visit("/settings")
	s.Equal("Update requested. Echowa restarts when it's ready.", page.Flash["success"])
	s.Equal("v1.3.0", prop(page, "update.requested"), "update.requested")
	s.Equal(true, prop(page, "update.busy"), "update.busy")

	s.ErrorIs(services.RequestUpdate(context.Background(), "v1.3.0"), services.ErrUpdating, "not twice")
}

func (s *UpdateTestSuite) TestRefusals() {
	ctx := context.Background()
	s.ErrorIs(services.RequestUpdate(ctx, "v1.3.0"), services.ErrNotNewest, "nothing checked yet")
	s.check()
	s.ErrorIs(services.RequestUpdate(ctx, "v1.2.5"), services.ErrNotNewest, "only the newest release")
	s.ErrorIs(services.RequestUpdate(ctx, "v1.3.0; reboot"), services.ErrNotNewest)

	app := createTestApp(s.T(), "blog", nil)
	_, err := deploy.Request(ctx, app, models.TriggerManual, "v1")
	s.Require().NoError(err)
	s.ErrorIs(services.RequestUpdate(ctx, "v1.3.0"), services.ErrDeploysRunning)
	s.NoFileExists(filepath.Join(update.Dir, "request"))

	release.Version = "v1.2.0-3-gabc1234"
	s.ErrorIs(services.RequestUpdate(ctx, "v1.3.0"), services.ErrDevBuild)
}

func (s *UpdateTestSuite) TestARunningUpdateBlocksAnotherUntilItGoesStale() {
	s.check()
	s.Require().NoError(os.MkdirAll(update.Dir, 0o700))
	s.Require().NoError(update.WriteStatus(update.Status{State: update.Running, From: "v1.2.0", To: "v1.3.0", StartedAt: time.Now()}))
	s.ErrorIs(services.RequestUpdate(context.Background(), "v1.3.0"), services.ErrUpdating)

	s.Require().NoError(update.WriteStatus(update.Status{State: update.Running, From: "v1.2.0", To: "v1.3.0", StartedAt: time.Now().Add(-time.Hour)}))
	s.NoError(services.RequestUpdate(context.Background(), "v1.3.0"), "an updater cut short by a reboot")
}

func (s *UpdateTestSuite) TestDevBuildsNeverCheck() {
	release.Version = "dev"
	called := false
	services.FetchLatestRelease = func(context.Context) (services.LatestRelease, error) {
		called = true
		return services.LatestRelease{}, nil
	}
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings")
	s.Equal(true, prop(page, "update.dev"), "update.dev")
	s.Equal(false, prop(page, "update.available"), "update.available")
	s.check()
	s.False(called)
}

func (s *UpdateTestSuite) TestUpHidesTheVersionFromTheNetwork() {
	resp, err := s.Http(s.T()).Get("/up")
	s.Require().NoError(err)
	resp.AssertOk()
	resp.AssertExactJson(map[string]any{"status": "ok"})
}
