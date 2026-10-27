package feature

import (
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type AuthTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestAuthTestSuite(t *testing.T) {
	suite.Run(t, new(AuthTestSuite))
}

func (s *AuthTestSuite) SetupTest() {
	s.RefreshDatabase()
	// Rate limiter counts live in the cache; start each test fresh.
	s.Require().True(facades.Cache().Flush())
}

func (s *AuthTestSuite) createUser(email, password string) models.User {
	user, created, err := services.SaveUser("Test User", email, password)
	s.Require().NoError(err)
	s.Require().True(created)

	return *user
}

func (s *AuthTestSuite) login(b *browser, email, password string, remember bool) {
	b.visit("/login")
	resp := b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": email, "password": password, "remember": remember,
	})
	resp.AssertFound()
	resp.AssertHeader("Location", "/dashboard")
}

func (s *AuthTestSuite) TestLoginWithWrongPassword() {
	s.createUser("user@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	resp := b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{"email": "user@example.test", "password": "nope"})
	resp.AssertHeader("Location", "/login")

	_, page := b.visit("/login")
	s.Equal("These credentials do not match our records.", page.Props["errors"].(map[string]any)["email"])
}

func (s *AuthTestSuite) TestGuestIsSentToLoginThenBack() {
	s.createUser("user@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, "/dashboard?tab=1", nil, true, nil)
	resp.AssertHeader("Location", "/login")

	b.visit("/login")
	resp = b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{"email": "USER@example.test", "password": "password123"})
	resp.AssertHeader("Location", "/dashboard?tab=1")
}

func (s *AuthTestSuite) TestSignedInUserSkipsGuestPages() {
	s.createUser("user@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	s.login(b, "user@example.test", "password123", false)

	b.request(nethttp.MethodGet, "/login", nil, true, nil).AssertHeader("Location", "/dashboard")
}

func (s *AuthTestSuite) TestLoginRotatesSessionAndCsrfToken() {
	s.createUser("user@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")
	before := b.cookieValue(middlewareXsrfCookie)

	s.login(b, "user@example.test", "password123", false)
	b.visit("/dashboard")
	s.NotEqual(before, b.cookieValue(middlewareXsrfCookie))
}

func (s *AuthTestSuite) TestLogout() {
	s.createUser("user@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	s.login(b, "user@example.test", "password123", false)
	b.visit("/dashboard")

	resp := b.submit(nethttp.MethodPost, "/logout", "/dashboard", nil)
	resp.AssertHeader("Location", "/")
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
}

func (s *AuthTestSuite) TestRememberMe() {
	s.createUser("user@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	s.login(b, "user@example.test", "password123", true)
	s.NotEmpty(b.cookieValue("remember_web"))
	rememberCookie := b.cookies["remember_web"]

	// Session gone (browser restarted, or it expired): the cookie signs back in.
	for name := range b.cookies {
		if name != "remember_web" {
			b.forget(name)
		}
	}
	_, page := b.visit("/dashboard")
	page.AssertComponent("Dashboard")

	// Logging out invalidates the token, even for a copied cookie.
	b.submit(nethttp.MethodPost, "/logout", "/dashboard", nil).AssertHeader("Location", "/")
	thief := newBrowser(s.T(), &s.TestCase)
	thief.cookies["remember_web"] = rememberCookie
	thief.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
}

func (s *AuthTestSuite) TestLoginIsRateLimited() {
	s.createUser("limited@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	for range 5 {
		b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{"email": "limited@example.test", "password": "wrong"})
	}
	resp := b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{"email": "limited@example.test", "password": "password123"})
	resp.AssertHeader("Location", "/login")

	_, page := b.visit("/login")
	s.Contains(page.Props["errors"].(map[string]any)["email"], "Too many attempts")
}

// Echowa has one admin, created on the first-run setup page or with
// `artisan user:create`. There's no sign-up page and no reset email.
func (s *AuthTestSuite) TestRegistrationAndPasswordResetAreGone() {
	s.createUser("admin@example.test", "password123")
	b := newBrowser(s.T(), &s.TestCase)
	for _, path := range []string{"/register", "/forgot-password", "/reset-password/some-token"} {
		b.request(nethttp.MethodGet, path, nil, false, nil).AssertNotFound()
	}
	b.visit("/login")
	for _, path := range []string{"/register", "/forgot-password", "/reset-password"} {
		b.submit(nethttp.MethodPost, path, "/login", map[string]any{"email": "a@example.test"}).AssertNotFound()
	}
}

func (s *AuthTestSuite) TestSaveUserResetsAnExistingUser() {
	s.createUser("admin@example.test", "old-password")
	b := newBrowser(s.T(), &s.TestCase)
	s.login(b, "admin@example.test", "old-password", true)

	user, created, err := services.SaveUser("New Name", " Admin@Example.test ", "new-password")
	s.Require().NoError(err)
	s.False(created, "same email, case-insensitively: reset, not a second account")
	s.Equal("admin@example.test", user.Email)

	count, err := facades.Orm().Query().Model(&models.User{}).Count()
	s.Require().NoError(err)
	s.EqualValues(1, count)

	var saved models.User
	s.Require().NoError(facades.Orm().Query().Where("email", "admin@example.test").First(&saved))
	s.Equal("New Name", saved.Name)
	s.Nil(saved.RememberToken, "a reset signs remembered browsers out")

	other := newBrowser(s.T(), &s.TestCase)
	other.visit("/login")
	other.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": "admin@example.test", "password": "old-password",
	}).AssertHeader("Location", "/login")
	s.login(newBrowser(s.T(), &s.TestCase), "admin@example.test", "new-password", false)
}

func (s *AuthTestSuite) TestSaveUserValidates() {
	_, _, err := services.SaveUser("Admin", "not-an-email", "long-enough")
	s.Error(err)
	_, _, err = services.SaveUser("Admin", "Admin <admin@example.test>", "long-enough")
	s.Error(err, "only a bare address")
	_, _, err = services.SaveUser("Admin", "admin@example.test", "short")
	s.Error(err)
}

func (s *AuthTestSuite) TestResettingThePasswordSignsEveryBrowserOut() {
	intruder := signedIn(s.T(), &s.TestCase)
	_, page := intruder.visit("/dashboard")
	page.AssertComponent("Dashboard")

	_, created, err := services.SaveUser("Admin", "admin@example.test", "a-new-password")
	s.Require().NoError(err)
	s.False(created)

	intruder.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
}
