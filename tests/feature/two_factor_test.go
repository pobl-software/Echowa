package feature

import (
	nethttp "net/http"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/totp"
	"goravel/tests"
)

type TwoFactorTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestTwoFactorTestSuite(t *testing.T) {
	suite.Run(t, new(TwoFactorTestSuite))
}

func (s *TwoFactorTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
}

func (s *TwoFactorTestSuite) TearDownTest() {
	carbon.ClearTestNow()
}

// code is the app's code for secret, steps away from now.
func (s *TwoFactorTestSuite) code(secret string, steps int64) string {
	code, err := totp.Code(secret, totp.Step(time.Now())+steps)
	s.Require().NoError(err)
	return code
}

// startSetup is the setup dialog's fetch, returning the secret.
func (s *TwoFactorTestSuite) startSetup(b *browser) string {
	resp := b.request(nethttp.MethodPost, "/settings/security/two-factor/setup", map[string]any{}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})
	resp.AssertOk()
	resp.AssertHeader("Cache-Control", "no-store")
	body, err := resp.Json()
	s.Require().NoError(err)
	secret := body["secret"].(string)
	s.Len(secret, 32)
	s.Equal(totp.URI("Echowa", "admin@example.test", secret), body["uri"])
	qr := body["qr"].(map[string]any)
	s.Positive(qr["size"])
	s.NotEmpty(qr["path"])

	return secret
}

// enable signs in and turns two-factor authentication on, returning the
// browser, still signed in, and the secret.
func (s *TwoFactorTestSuite) enable() (*browser, string) {
	b := signedIn(s.T(), &s.TestCase)
	return b, s.enableIn(b)
}

// enableIn turns it on from b, already signed in.
func (s *TwoFactorTestSuite) enableIn(b *browser) string {
	b.visit("/settings/security")
	secret := s.startSetup(b)
	b.submit(nethttp.MethodPost, "/settings/security/two-factor", "/settings/security", map[string]any{
		"code": s.code(secret, -1),
	}).AssertHeader("Location", "/settings/security")
	s.Require().True(facades.Cache().Flush())

	return secret
}

// password gives the password at login, as far as the code step.
func (s *TwoFactorTestSuite) password(b *browser) {
	b.visit("/login")
	b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": "admin@example.test", "password": "password123",
	}).AssertHeader("Location", "/two-factor-challenge")
}

func (s *TwoFactorTestSuite) TestSetupConfirmsWithACodeAndNeverPutsTheSecretInAPage() {
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings/security")
	page.AssertComponent("Settings/Security")
	s.Equal(false, page.Props["twoFactor"].(map[string]any)["enabled"])

	secret := s.startSetup(b)
	b.forbid(secret)

	// Unconfirmed, it changes nothing.
	_, page = b.visit("/settings/security")
	s.Equal(false, page.Props["twoFactor"].(map[string]any)["enabled"])

	b.submit(nethttp.MethodPost, "/settings/security/two-factor", "/settings/security", map[string]any{"code": "000000"})
	_, page = b.visit("/settings/security")
	s.Equal(rules.WrongTwoFactorCode, page.Props["errors"].(map[string]any)["code"])
	s.Equal(false, page.Props["twoFactor"].(map[string]any)["enabled"])

	b.submit(nethttp.MethodPost, "/settings/security/two-factor", "/settings/security", map[string]any{
		"code": s.code(secret, 0),
	}).AssertHeader("Location", "/settings/security")
	_, page = b.visit("/settings/security")
	s.Equal(true, page.Props["twoFactor"].(map[string]any)["enabled"])
	s.NotNil(page.Props["twoFactor"].(map[string]any)["confirmed_at"])

	_, page = b.visit("/settings")
	security := page.Props["security"].(map[string]any)
	s.Equal(true, security["twoFactor"].(map[string]any)["enabled"])
	s.Equal("2 hours", security["sessionTimeout"])

	// Once it's on, a new secret takes turning it off first.
	b.request(nethttp.MethodPost, "/settings/security/two-factor/setup", map[string]any{}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
	}).AssertStatus(nethttp.StatusConflict)
}

func (s *TwoFactorTestSuite) TestSetupAgainReplacesAnUnfinishedSecret() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings/security")
	first := s.startSetup(b)
	second := s.startSetup(b)
	s.NotEqual(first, second)

	b.submit(nethttp.MethodPost, "/settings/security/two-factor", "/settings/security", map[string]any{
		"code": s.code(first, 0),
	})
	_, page := b.visit("/settings/security")
	s.Equal(false, page.Props["twoFactor"].(map[string]any)["enabled"])
}

func (s *TwoFactorTestSuite) TestLoginAsksForACode() {
	_, secret := s.enable()
	b := newBrowser(s.T(), &s.TestCase)

	// Where they were going survives both steps.
	b.request(nethttp.MethodGet, "/server?x=1", nil, true, nil).AssertHeader("Location", "/login")
	s.password(b)

	_, page := b.visit("/two-factor-challenge")
	page.AssertComponent("Auth/TwoFactorChallenge")

	b.submit(nethttp.MethodPost, "/two-factor-challenge", "/two-factor-challenge", map[string]any{"code": "000000"}).
		AssertHeader("Location", "/two-factor-challenge")
	_, page = b.visit("/two-factor-challenge")
	s.Equal(rules.WrongTwoFactorCode, page.Props["errors"].(map[string]any)["code"])

	code := s.code(secret, 0)
	b.submit(nethttp.MethodPost, "/two-factor-challenge", "/two-factor-challenge", map[string]any{
		"code": code[:3] + " " + code[3:],
	}).AssertHeader("Location", "/server?x=1")
	_, page = b.visit("/dashboard")
	page.AssertComponent("Dashboard")
}

func (s *TwoFactorTestSuite) TestACodeWorksOnce() {
	_, secret := s.enable()
	code := s.code(secret, 1)

	first := newBrowser(s.T(), &s.TestCase)
	s.password(first)
	// The password alone signs nothing in.
	first.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
	first.submit(nethttp.MethodPost, "/two-factor-challenge", "/two-factor-challenge", map[string]any{"code": code}).
		AssertHeader("Location", "/dashboard")

	// Someone who saw the code, with the password too.
	second := newBrowser(s.T(), &s.TestCase)
	s.password(second)
	second.submit(nethttp.MethodPost, "/two-factor-challenge", "/two-factor-challenge", map[string]any{"code": code}).
		AssertHeader("Location", "/two-factor-challenge")
	second.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
}

func (s *TwoFactorTestSuite) TestTheCodeStepExpires() {
	_, secret := s.enable()
	b := newBrowser(s.T(), &s.TestCase)
	s.password(b)

	carbon.SetTestNow(carbon.Now().AddDuration((services.TwoFactorLoginTTL + time.Second).String()))
	b.request(nethttp.MethodGet, "/two-factor-challenge", nil, true, nil).AssertHeader("Location", "/login")
	b.submit(nethttp.MethodPost, "/two-factor-challenge", "/login", map[string]any{
		"code": s.code(secret, 0),
	}).AssertHeader("Location", "/login")
	carbon.ClearTestNow()
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
}

func (s *TwoFactorTestSuite) TestTheCodeStepNeedsThePasswordFirst() {
	s.enable()
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")
	b.request(nethttp.MethodGet, "/two-factor-challenge", nil, true, nil).AssertHeader("Location", "/login")
	b.submit(nethttp.MethodPost, "/two-factor-challenge", "/login", map[string]any{"code": "123456"}).
		AssertHeader("Location", "/login")
}

func (s *TwoFactorTestSuite) TestCodesAreRateLimitedPerUser() {
	_, secret := s.enable()
	b := newBrowser(s.T(), &s.TestCase)
	s.password(b)

	for range 5 {
		b.submit(nethttp.MethodPost, "/two-factor-challenge", "/two-factor-challenge", map[string]any{"code": "000000"})
	}
	b.submit(nethttp.MethodPost, "/two-factor-challenge", "/two-factor-challenge", map[string]any{
		"code": s.code(secret, 1),
	}).AssertHeader("Location", "/two-factor-challenge")
	_, page := b.visit("/two-factor-challenge")
	s.Contains(page.Props["errors"].(map[string]any)["code"], "Too many attempts")
}

func (s *TwoFactorTestSuite) TestTurningItOffTakesACode() {
	b, secret := s.enable()
	b.visit("/settings/security")

	b.submit(nethttp.MethodDelete, "/settings/security/two-factor", "/settings/security", map[string]any{"code": "000000"})
	_, page := b.visit("/settings/security")
	s.Equal(rules.WrongTwoFactorCode, page.Props["errors"].(map[string]any)["code"])
	s.Equal(true, page.Props["twoFactor"].(map[string]any)["enabled"])

	b.submit(nethttp.MethodDelete, "/settings/security/two-factor", "/settings/security", map[string]any{
		"code": s.code(secret, 0),
	}).AssertHeader("Location", "/settings/security")
	_, page = b.visit("/settings/security")
	s.Equal(false, page.Props["twoFactor"].(map[string]any)["enabled"])

	// The password alone signs in again.
	other := newBrowser(s.T(), &s.TestCase)
	other.visit("/login")
	other.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": "admin@example.test", "password": "password123",
	}).AssertHeader("Location", "/dashboard")
}

func (s *TwoFactorTestSuite) TestArtisanTurnsItOff() {
	s.enable()
	s.Require().NoError(facades.Artisan().Call("--no-ansi user:disable-two-factor"))

	user, err := services.FindUserByEmail("admin@example.test")
	s.Require().NoError(err)
	s.False(services.TwoFactorOn(user))
	s.Empty(string(user.TwoFactorSecret))

	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")
	b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": "admin@example.test", "password": "password123",
	}).AssertHeader("Location", "/dashboard")
}
