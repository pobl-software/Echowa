package feature

import (
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/services"
	"goravel/tests"
)

type CsrfTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestCsrfTestSuite(t *testing.T) {
	suite.Run(t, new(CsrfTestSuite))
}

// SetupTest makes the admin, so /login shows rather than redirecting to
// the first-run setup page (ADR 0023).
func (s *CsrfTestSuite) SetupTest() {
	s.RefreshDatabase()
	_, _, err := services.SaveUser("Admin", "admin@example.test", "password123")
	s.Require().NoError(err)
}

func (s *CsrfTestSuite) TestGetSetsReadableXsrfCookie() {
	b := newBrowser(s.T(), &s.TestCase)
	resp, page := b.visit("/login")

	page.AssertComponent("Auth/Login")
	cookie := resp.Cookie(middlewareXsrfCookie)
	s.Require().NotNil(cookie)
	s.NotEmpty(cookie.Value)
	s.False(cookie.HttpOnly, "Inertia's client must be able to read it")
}

func (s *CsrfTestSuite) TestPostWithoutTokenIsRejected() {
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	resp := b.request(nethttp.MethodPost, "/login", map[string]any{"email": "a@example.test"}, false, nil)
	resp.AssertStatus(419)
}

func (s *CsrfTestSuite) TestPostWithWrongTokenIsRejected() {
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	resp := b.request(nethttp.MethodPost, "/login", map[string]any{"email": "a@example.test"}, false,
		map[string]string{"X-CSRF-TOKEN": "not-the-token"})
	resp.AssertStatus(419)
}

// An expired session on an Inertia form goes back with a toast, not a 419
// error modal.
func (s *CsrfTestSuite) TestInertiaPostWithoutTokenGoesBackWithToast() {
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")
	b.forget(middlewareXsrfCookie)

	resp := b.request(nethttp.MethodPost, "/login", map[string]any{"email": "a@example.test"}, true,
		map[string]string{"Referer": testOrigin + "/login"})
	resp.AssertStatus(nethttp.StatusSeeOther)
	resp.AssertHeader("Location", "/login")

	_, page := b.visit("/login")
	s.Equal("This page expired. Please try again.", page.Flash["error"])
}

// Inertia's client sends the XSRF-TOKEN cookie back as X-XSRF-TOKEN.
func (s *CsrfTestSuite) TestXsrfHeaderIsAccepted() {
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	resp := b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{"email": "", "password": ""})
	resp.AssertFound() // past CSRF: back to the form with validation errors
}

func (s *CsrfTestSuite) TestCrossOriginRefererIsNotFollowed() {
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	resp := b.request(nethttp.MethodPost, "/login", map[string]any{"email": "", "password": ""}, true, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Referer":      "https://evil.example/phish",
	})
	resp.AssertHeader("Location", "/")
}
