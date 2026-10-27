package feature

import (
	nethttp "net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

// The first run (ADR 0023): with no admin, the UI creates one, but only for
// whoever has the one-time link the server prints.
type SetupTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestSetupTestSuite(t *testing.T) {
	suite.Run(t, new(SetupTestSuite))
}

func (s *SetupTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.Require().NoError(services.ForgetSetupToken())
}

func (s *SetupTestSuite) TearDownTest() {
	s.Require().NoError(services.ForgetSetupToken())
}

// link runs `echowa-artisan setup:link` and returns the path it printed.
func (s *SetupTestSuite) link() string {
	s.Require().NoError(facades.Artisan().Call("--no-ansi setup:link"))
	token, err := services.SetupToken()
	s.Require().NoError(err)

	return "/setup?token=" + token
}

func (s *SetupTestSuite) users() int64 {
	count, err := facades.Orm().Query().Model(&models.User{}).Count()
	s.Require().NoError(err)

	return count
}

func (s *SetupTestSuite) TestTheFirstVisitorIsSentToSetup() {
	b := newBrowser(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
	b.request(nethttp.MethodGet, "/login", nil, true, nil).AssertHeader("Location", "/setup")
}

func (s *SetupTestSuite) TestSetupWithTheLinkCreatesTheAdminAndSignsIn() {
	link := s.link()
	b := newBrowser(s.T(), &s.TestCase)
	b.forbid(strings.TrimPrefix(link, "/setup?token="))

	b.request(nethttp.MethodGet, link, nil, true, nil).AssertHeader("Location", "/setup")
	_, page := b.visit("/setup")
	page.AssertComponent("Auth/Setup")
	s.Equal(true, page.Props["ready"])

	resp := b.submit(nethttp.MethodPost, "/setup", "/setup", map[string]any{
		"name": " Louis ", "email": " Admin@Example.test ", "password": "a-long-password",
	})
	resp.AssertHeader("Location", "/dashboard")
	_, page = b.visit("/dashboard")
	page.AssertComponent("Dashboard")

	user, err := services.FindUserByEmail("admin@example.test")
	s.Require().NoError(err)
	s.Require().NotNil(user)
	s.Equal("Louis", user.Name)
	s.True(facades.Hash().Check("a-long-password", user.Password))

	other := newBrowser(s.T(), &s.TestCase)
	other.request(nethttp.MethodGet, link, nil, true, nil).AssertHeader("Location", "/login")
	other.request(nethttp.MethodGet, "/setup", nil, true, nil).AssertHeader("Location", "/login")
	_, page = other.visit("/login")
	page.AssertComponent("Auth/Login")
}

func (s *SetupTestSuite) TestWithoutTheLinkThereIsNoForm() {
	link := s.link()
	b := newBrowser(s.T(), &s.TestCase)

	_, page := b.visit("/setup")
	s.Equal(false, page.Props["ready"])

	b.request(nethttp.MethodGet, "/setup?token=wrong", nil, true, nil).AssertHeader("Location", "/setup")
	_, page = b.visit("/setup")
	s.Equal(false, page.Props["ready"])

	b.submit(nethttp.MethodPost, "/setup", "/setup", map[string]any{
		"name": "Intruder", "email": "intruder@example.test", "password": "a-long-password",
	}).AssertHeader("Location", "/setup")
	s.Zero(s.users())

	b.request(nethttp.MethodGet, link, nil, true, nil)
	_, page = b.visit("/setup")
	s.Equal(true, page.Props["ready"], "the real link still works")
}

func (s *SetupTestSuite) TestNoLinkUntilOneIsPrinted() {
	b := newBrowser(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/setup?token=", nil, true, nil)
	_, page := b.visit("/setup")
	s.Equal(false, page.Props["ready"])
	s.False(services.ValidSetupToken(""))
}

func (s *SetupTestSuite) TestValidation() {
	link := s.link()
	b := newBrowser(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, link, nil, true, nil)
	b.visit("/setup")

	b.submit(nethttp.MethodPost, "/setup", "/setup", map[string]any{
		"name": "", "email": "not-an-email", "password": "short",
	}).AssertHeader("Location", "/setup")
	_, page := b.visit("/setup")
	errors := page.Props["errors"].(map[string]any)
	s.Contains(errors, "name")
	s.Contains(errors, "email")

	b.submit(nethttp.MethodPost, "/setup", "/setup", map[string]any{
		"name": "Louis", "email": "admin@example.test", "password": "short",
	})
	_, page = b.visit("/setup")
	s.Equal("Use at least 8 characters.", page.Props["errors"].(map[string]any)["password"])
	s.Zero(s.users())
	s.Equal(true, page.Props["ready"], "a failed attempt keeps the link")
}

func (s *SetupTestSuite) TestOnlyOneOfTwoSubmissionsAtOnceGetsIn() {
	link := s.link()
	token := strings.TrimPrefix(link, "/setup?token=")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = services.CreateFirstUser(token, "Admin", []string{"a@example.test", "b@example.test"}[i], "a-long-password")
		}()
	}
	wg.Wait()

	s.EqualValues(1, s.users())
	s.True(errs[0] == nil != (errs[1] == nil), "exactly one succeeds: %v", errs)
}

func (s *SetupTestSuite) TestSetupLinkOnceThereIsAnAdmin() {
	token := strings.TrimPrefix(s.link(), "/setup?token=")
	_, _, err := services.SaveUser("Admin", "admin@example.test", "a-long-password")
	s.Require().NoError(err)

	s.ErrorContains(facades.Artisan().Call("--no-ansi setup:link"), "an admin already exists")
	s.False(services.ValidSetupToken(token), "the stale token is gone")
}
