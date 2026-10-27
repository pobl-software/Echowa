package feature

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/services"
	"goravel/tests"
)

type HealthTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestHealthTestSuite(t *testing.T) {
	suite.Run(t, new(HealthTestSuite))
}

func (s *HealthTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	// The poller's record is process-wide: leave it empty for other suites.
	s.T().Cleanup(func() {
		_, err := facades.Orm().Query().Exec("DELETE FROM apps")
		s.Require().NoError(err)
		s.Require().NoError(services.PollAppStatus(context.Background(), time.Now()))
	})
}

func (s *HealthTestSuite) replicas(running, desired uint64) {
	s.docker.Status = func(name string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{Name: name, Running: running, Desired: desired}, nil
	}
}

func (s *HealthTestSuite) dashboardStatus() map[string]any {
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/dashboard")

	return page.Props["apps"].([]any)[0].(map[string]any)["status"].(map[string]any)
}

func (s *HealthTestSuite) TestAnAppShortOfReplicasForTwoMinutesIsUnhealthy() {
	createTestApp(s.T(), "blog", nil)
	ctx := context.Background()
	s.replicas(1, 2)
	start := time.Now().Add(-3 * time.Minute)

	s.Require().NoError(services.PollAppStatus(ctx, start))
	s.Require().NoError(services.PollAppStatus(ctx, start.Add(30*time.Second)))
	status := s.dashboardStatus()
	s.Equal("degraded", status["state"])
	s.NotNil(status["unhealthy_since"], "short since the first poll, three minutes ago")
	since, err := time.Parse(time.RFC3339Nano, status["unhealthy_since"].(string))
	s.Require().NoError(err)
	s.WithinDuration(start, since, time.Second)

	// Docker stops answering: nothing is known, so the record stays.
	s.docker.Status = func(string) (docker.ServiceStatus, error) { return docker.ServiceStatus{}, errors.New("daemon gone") }
	s.Require().NoError(services.PollAppStatus(ctx, time.Now()))
	s.replicas(1, 2)
	s.NotNil(s.dashboardStatus()["unhealthy_since"])

	s.replicas(2, 2)
	s.Nil(s.dashboardStatus()["unhealthy_since"], "the page's own read shows it recovered")
	s.Require().NoError(services.PollAppStatus(ctx, time.Now()))
	s.replicas(1, 2)
	s.Nil(s.dashboardStatus()["unhealthy_since"], "short again, but only just")
}

func (s *HealthTestSuite) TestShortForLessThanTwoMinutesIsOnlyDegraded() {
	createTestApp(s.T(), "blog", nil)
	s.replicas(0, 1)

	s.Require().NoError(services.PollAppStatus(context.Background(), time.Now().Add(-90*time.Second)))

	status := s.dashboardStatus()
	s.Equal("down", status["state"])
	s.Nil(status["unhealthy_since"])
}
