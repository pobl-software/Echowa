package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

// The fake server has 4 cores and 8 GiB, so the defaults are 2 cores and
// 4096 MiB.
var fakeDefaults = docker.Limits{NanoCPUs: 2_000_000_000, MemoryBytes: 4096 << 20}

type ResourcesTestSuite struct {
	suite.Suite
	tests.TestCase
	docker     *docker.Fake
	dispatched []uint
}

func TestResourcesTestSuite(t *testing.T) {
	suite.Run(t, new(ResourcesTestSuite))
}

func (s *ResourcesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.docker = s.FakeDocker()
	s.dispatched = nil
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, _ time.Duration) error {
		s.dispatched = append(s.dispatched, appID)
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
	s.withSwap(2048)
	s.T().Cleanup(func() { services.SetLatestMetrics(nil) })
}

// withSwap gives the server mib of swap, as the metrics reader would.
func (s *ResourcesTestSuite) withSwap(mib uint64) {
	swap := mib << 20
	services.SetLatestMetrics(&metrics.Reading{Host: metrics.Host{SwapTotal: &swap}})
}

func (s *ResourcesTestSuite) app(name string, deployed bool) models.App {
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: "ghcr.io/owner/" + name, DefaultTag: "latest", Port: 8080, Replicas: 1,
	}, nil)
	s.Require().NoError(err)
	if deployed {
		_, err = services.CreateDeployment(context.Background(), app, models.TriggerManual, app.DefaultTag)
		s.Require().NoError(err)
		s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	}

	return s.reload(app.ID)
}

func (s *ResourcesTestSuite) reload(id uint) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&app))

	return app
}

func (s *ResourcesTestSuite) lastSpec() docker.ServiceSpec {
	deploys := s.docker.Deploys()
	s.Require().NotEmpty(deploys)

	return deploys[len(deploys)-1]
}

func (s *ResourcesTestSuite) TestEveryAppDeploysWithTheDefaultsUntilItSetsItsOwn() {
	s.app("api", true)

	s.Equal(fakeDefaults, s.lastSpec().Limits)
}

func (s *ResourcesTestSuite) TestSavingRedeploysTheRunningImageWithTheNewLimits() {
	app := s.app("api", true)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	_, shown := b.visit(page)
	resources := shown.Props["resources"].(map[string]any)
	s.Equal(map[string]any{"cpu_millicores": float64(2000), "memory_mib": float64(4096), "swap_mib": float64(0)}, resources["defaults"])
	s.Equal(map[string]any{"cpu_millicores": float64(4000), "memory_mib": float64(8192), "swap_mib": float64(0)}, resources["host"])
	s.Equal(float64(2048), resources["host_swap_mib"])

	b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{"cpu_millicores": 500, "memory_mib": 256})

	app = s.reload(app.ID)
	s.Equal(ptr(500), app.CPUMillicores)
	s.Equal(ptr(256), app.MemoryMiB)
	s.Equal([]uint{app.ID}, s.dispatched)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal(docker.Limits{NanoCPUs: 500_000_000, MemoryBytes: 256 << 20}, s.lastSpec().Limits)

	_, shown = b.visit(page)
	status := shown.Props["status"].(map[string]any)
	s.Equal(map[string]any{"cpu_millicores": float64(500), "memory_mib": float64(256), "swap_mib": float64(0)}, status["limits"])

	b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{"cpu_millicores": nil, "memory_mib": 256})
	s.Nil(s.reload(app.ID).CPUMillicores, "an empty field goes back to the default")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal(docker.Limits{NanoCPUs: 2_000_000_000, MemoryBytes: 256 << 20}, s.lastSpec().Limits)
}

func (s *ResourcesTestSuite) TestSwapIsOffUntilAnAppAllowsSome() {
	app := s.app("api", true)
	s.Zero(s.lastSpec().Limits.SwapBytes)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(page)

	b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{"memory_mib": 256, "swap_mib": 512})

	s.Equal(512, s.reload(app.ID).SwapMiB)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal(docker.Limits{NanoCPUs: 2_000_000_000, MemoryBytes: 256 << 20, SwapBytes: 512 << 20}, s.lastSpec().Limits)
	_, shown := b.visit(page)
	s.Equal(float64(512), shown.Props["status"].(map[string]any)["limits"].(map[string]any)["swap_mib"])

	b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{"memory_mib": 256, "swap_mib": nil})
	s.Zero(s.reload(app.ID).SwapMiB, "an empty field is no swap")
}

func (s *ResourcesTestSuite) TestSwapIsAtMostWhatTheServerHas() {
	app := s.app("api", true)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	for _, tt := range []struct {
		swap  uint64
		value any
		error string
	}{
		{2048, 4096, "This server has 2048 MiB of swap."},
		{2048, -1, "Use 0 for no swap."},
		{0, 64, "This server has no swap, so there's none to allow."},
	} {
		s.withSwap(tt.swap)
		b.visit(page)
		b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{"swap_mib": tt.value}).AssertHeader("Location", page)
		_, shown := b.visit(page)
		s.Equal(tt.error, shown.Props["errors"].(map[string]any)["swap_mib"], "swap %v on a server with %d MiB", tt.value, tt.swap)
	}

	s.Zero(s.reload(app.ID).SwapMiB)
	s.Empty(s.dispatched)
}

func (s *ResourcesTestSuite) TestAnAppNeverDeployedOnlySaves() {
	app := s.app("worker", false)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(page)

	b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{"cpu_millicores": 1000, "memory_mib": nil}).
		AssertHeader("Location", page+"?tab=resources")

	s.Equal(ptr(1000), s.reload(app.ID).CPUMillicores)
	s.Empty(s.dispatched)
}

func (s *ResourcesTestSuite) TestRefusesLimitsTooSmallToStartOrBiggerThanTheServer() {
	app := s.app("api", true)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	for _, tt := range []struct {
		field string
		value any
		error string
	}{
		{"cpu_millicores", 50, "Give it at least 0.1 of a core."},
		{"cpu_millicores", 4500, "This server has 4 cores."},
		{"memory_mib", 32, "Give it at least 64 MiB."},
		{"memory_mib", 9000, "This server has 8192 MiB."},
	} {
		b.visit(page)
		b.submit(nethttp.MethodPut, page+"/resources", page, map[string]any{tt.field: tt.value}).AssertHeader("Location", page)
		_, shown := b.visit(page)
		s.Equal(tt.error, shown.Props["errors"].(map[string]any)[tt.field], "%s %v", tt.field, tt.value)
	}

	app = s.reload(app.ID)
	s.Nil(app.CPUMillicores)
	s.Nil(app.MemoryMiB)
	s.Empty(s.dispatched)
}

func (s *ResourcesTestSuite) TestTheAppPageSaysWhenATaskRanOutOfMemory() {
	app := s.app("api", true)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	_, shown := b.visit(page)
	s.Nil(shown.Props["status"].(map[string]any)["oom_killed_at"])

	killed := time.Date(2026, 10, 27, 14, 3, 0, 0, time.UTC)
	s.docker.OOMKilled = map[string]time.Time{app.ServiceName(): killed}
	_, shown = b.visit(page)

	s.Equal(killed.Format(time.RFC3339), shown.Props["status"].(map[string]any)["oom_killed_at"])
}
