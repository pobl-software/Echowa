package feature

import (
	nethttp "net/http"
	"testing"
	"time"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/services"
	"goravel/tests"
)

type SessionTimeoutTestSuite struct {
	suite.Suite
	tests.TestCase
	start *carbon.Carbon
}

func TestSessionTimeoutTestSuite(t *testing.T) {
	suite.Run(t, new(SessionTimeoutTestSuite))
}

func (s *SessionTimeoutTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.start = carbon.Now()
	carbon.SetTestNow(s.start)
}

func (s *SessionTimeoutTestSuite) TearDownTest() {
	carbon.ClearTestNow()
}

// at moves the clock to d after the test started.
func (s *SessionTimeoutTestSuite) at(d time.Duration) {
	carbon.SetTestNow(s.start.Copy().AddDuration(d.String()))
}

func (s *SessionTimeoutTestSuite) stillIn(b *browser) {
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertOk()
}

func (s *SessionTimeoutTestSuite) loggedOut(b *browser) {
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
}

// poll is a page refreshing itself: an Inertia partial reload.
func (s *SessionTimeoutTestSuite) poll(b *browser) contractstesting.Response {
	return b.request(nethttp.MethodGet, "/dashboard", nil, true, map[string]string{
		"X-Inertia-Partial-Component": "Dashboard",
		"X-Inertia-Partial-Data":      "apps",
	})
}

func (s *SessionTimeoutTestSuite) TestAnIdleSessionLogsOutAfterTwoHours() {
	b := signedIn(s.T(), &s.TestCase)

	s.at(2 * time.Hour)
	s.stillIn(b)

	s.at(4*time.Hour + time.Second)
	resp := b.request(nethttp.MethodGet, "/settings", nil, true, nil)
	resp.AssertHeader("Location", "/login")
	_, page := b.visit("/login")
	page.AssertComponent("Auth/Login")
	s.Contains(page.Flash["info"], "no activity")

	b.visit("/login")
	b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": "admin@example.test", "password": "password123",
	}).AssertHeader("Location", "/settings")
}

func (s *SessionTimeoutTestSuite) TestPollsDontKeepASessionAlive() {
	b := signedIn(s.T(), &s.TestCase)

	for _, d := range []time.Duration{30 * time.Minute, time.Hour, 90 * time.Minute, 2 * time.Hour} {
		s.at(d)
		s.poll(b).AssertOk()
		b.request(nethttp.MethodGet, "/scheduled-tasks/preview?schedule=0+3+*+*+*&zone=UTC", nil, false, map[string]string{
			"Accept": "application/json",
		})
	}

	s.at(2*time.Hour + time.Second)
	s.poll(b).AssertHeader("Location", "/login")
	s.loggedOut(b)
}

func (s *SessionTimeoutTestSuite) TestAnIdleSessionCantOpenAStream() {
	b := signedIn(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/deployments/999/stream", nil, false, nil).AssertNotFound()

	s.at(2*time.Hour + time.Second)
	b.request(nethttp.MethodGet, "/deployments/999/stream", nil, false, nil).AssertUnauthorized()
}

func (s *SessionTimeoutTestSuite) TestALongerTimeout() {
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings/security")
	timeout := page.Props["sessionTimeout"].(map[string]any)
	s.EqualValues(120, timeout["minutes"])
	s.Equal("2 hours", timeout["label"])
	s.Len(timeout["choices"], len(services.SessionTimeouts))

	b.submit(nethttp.MethodPut, "/settings/security/session-timeout", "/settings/security", map[string]any{
		"minutes": 1440,
	}).AssertHeader("Location", "/settings/security")
	_, page = b.visit("/settings/security")
	s.EqualValues(1440, page.Props["sessionTimeout"].(map[string]any)["minutes"])
	s.Equal("Sessions now end after 1 day with no activity.", page.Flash["success"])

	s.at(23 * time.Hour)
	s.stillIn(b)
	s.at(46*time.Hour + time.Second)
	s.stillIn(b)
	s.at(70*time.Hour + 2*time.Second)
	s.loggedOut(b)
}

func (s *SessionTimeoutTestSuite) TestTheTimeoutIsOneOfTheChoices() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings/security")

	for _, minutes := range []any{60, 99999, "forever", nil} {
		b.submit(nethttp.MethodPut, "/settings/security/session-timeout", "/settings/security", map[string]any{
			"minutes": minutes,
		}).AssertHeader("Location", "/settings/security")
		_, page := b.visit("/settings/security")
		s.NotEmpty(page.Props["errors"].(map[string]any)["minutes"], "%v", minutes)
		s.EqualValues(120, page.Props["sessionTimeout"].(map[string]any)["minutes"])
	}
}

// Goravel forgets a session it hasn't saved for session.lifetime, so that
// has to cover the longest choice.
func (s *SessionTimeoutTestSuite) TestGoravelKeepsSessionsForTheLongestChoice() {
	longest := services.SessionTimeouts[len(services.SessionTimeouts)-1]
	s.GreaterOrEqual(time.Duration(facades.Config().GetInt("session.lifetime"))*time.Minute, longest)
}
