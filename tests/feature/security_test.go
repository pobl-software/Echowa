package feature

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	nethttp "net/http"
	"os"
	"regexp"
	"testing"
	"testing/fstest"

	"github.com/goravel/framework/support/path"
	"github.com/stretchr/testify/suite"

	"goravel/app/assets"
	"goravel/app/facades"
	"goravel/app/services"
	"goravel/tests"
)

type SecurityTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestSecurityTestSuite(t *testing.T) {
	suite.Run(t, new(SecurityTestSuite))
}

func (s *SecurityTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	// With no admin, /login sends everyone to the first-run setup page.
	_, _, err := services.SaveUser("Admin", "existing-admin@example.test", "password123")
	s.Require().NoError(err)
	// Serve assets the way a release build does: no Vite hot file, which
	// would turn the Content-Security-Policy off.
	template, err := os.ReadFile(path.Resource("views", "app.html"))
	s.Require().NoError(err)
	assets.Embed(fstest.MapFS{
		assets.RootTemplate:             {Data: template},
		assets.Manifest:                 {Data: []byte(`{"resources/js/app.tsx": {"file": "assets/app.js"}}`)},
		"public/build/assets/app.js":    {Data: []byte("console.log('echowa')")},
		"public/build/assets/app.css":   {Data: []byte("body{}")},
		"public/build/assets/.keep.txt": {Data: []byte("")},
	})
	s.T().Cleanup(func() { assets.Embed(nil) })
}

func (s *SecurityTestSuite) TestEveryResponseCarriesTheSecurityHeaders() {
	// Hash the theme script exactly as the browser receives it.
	page := newBrowser(s.T(), &s.TestCase).request(nethttp.MethodGet, "/login", nil, false, nil)
	body, err := page.Content()
	s.Require().NoError(err)
	script := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindStringSubmatch(body)
	s.Require().NotNil(script, "the page has the inline theme script")
	sum := sha256.Sum256([]byte(script[1]))
	themeHash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	b := newBrowser(s.T(), &s.TestCase)
	responses := map[string]interface {
		Headers() nethttp.Header
	}{
		"full page":      b.request(nethttp.MethodGet, "/login", nil, false, nil),
		"inertia page":   b.request(nethttp.MethodGet, "/login", nil, true, nil),
		"redirect":       b.request(nethttp.MethodGet, "/dashboard", nil, true, nil),
		"asset":          b.request(nethttp.MethodGet, "/build/assets/app.js", nil, false, nil),
		"not found":      b.request(nethttp.MethodGet, "/nope", nil, false, nil),
		"refused stream": b.request(nethttp.MethodGet, "/apps/1/logs/stream", nil, false, nil),
	}
	for name, resp := range responses {
		h := resp.Headers()
		s.Equal("nosniff", h.Get("X-Content-Type-Options"), name)
		s.Equal("DENY", h.Get("X-Frame-Options"), name)
		s.Equal("same-origin", h.Get("Referrer-Policy"), name)
		s.Contains(h.Get("Content-Security-Policy"), "script-src 'self' "+themeHash, name)
		s.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'", name)
		s.Empty(h.Get("Strict-Transport-Security"), "%s: APP_URL is plain http", name)
	}
}

func (s *SecurityTestSuite) TestHTTPSAddsHSTS() {
	original := facades.Config().GetString("http.url")
	facades.Config().Add("http.url", "https://echowa.example.com")
	s.T().Cleanup(func() { facades.Config().Add("http.url", original) })

	resp := newBrowser(s.T(), &s.TestCase).request(nethttp.MethodGet, "/login", nil, false, nil)

	resp.AssertHeader("Strict-Transport-Security", "max-age=31536000")
}

func (s *SecurityTestSuite) TestBuildServesFilesButNoListings() {
	b := newBrowser(s.T(), &s.TestCase)

	b.request(nethttp.MethodGet, "/build/assets/app.js", nil, false, nil).AssertOk()
	b.request(nethttp.MethodGet, "/build/assets/", nil, false, nil).AssertNotFound()
	b.request(nethttp.MethodGet, "/build/../../resources/views/app.html", nil, false, nil).AssertNotFound()
}

func (s *SecurityTestSuite) TestForwardedHeadersDontDodgeTheLoginLimit() {
	_, _, err := services.SaveUser("Admin", "admin@example.test", "password123")
	s.Require().NoError(err)
	s.Require().True(facades.Cache().Flush())
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")

	// The test client isn't on loopback, so neither header counts.
	for i := range 6 {
		ip := "198.51.100." + string(rune('1'+i))
		b.request(nethttp.MethodPost, "/login", map[string]any{"email": "admin@example.test", "password": "wrong"}, true, map[string]string{
			"X-XSRF-TOKEN":     b.cookieValue(middlewareXsrfCookie),
			"Referer":          testOrigin + "/login",
			"X-Forwarded-For":  ip,
			"CF-Connecting-IP": ip,
		})
	}

	_, page := b.visit("/login")
	s.Contains(page.Props["errors"].(map[string]any)["email"], "Too many attempts")
}

func (s *SecurityTestSuite) TestGoingBackNeverLeavesTheSite() {
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")
	form := map[string]any{"email": "nobody@example.test", "password": "wrong-password"}

	for referer, want := range map[string]string{
		"/login?next=1":        "/login?next=1",
		"//evil.example/x":     "/",
		"/\\evil.example/x":    "/",
		"https://evil.example": "/",
	} {
		uri := testOrigin + referer
		if referer == "https://evil.example" {
			uri = referer
		}
		resp := b.request(nethttp.MethodPost, "/login", form, true, map[string]string{
			"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
			"Referer":      uri,
		})
		resp.AssertHeader("Location", want)
	}
}

func (s *SecurityTestSuite) TestOneAddressCantSprayLoginsAcrossEmails() {
	s.Require().True(facades.Cache().Flush())
	b := newBrowser(s.T(), &s.TestCase)
	b.visit("/login")
	attempt := func(email string) {
		b.request(nethttp.MethodPost, "/login", map[string]any{"email": email, "password": "wrong-password"}, true, map[string]string{
			"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
			"Referer":      testOrigin + "/login",
		})
	}

	for i := range 20 {
		attempt(fmt.Sprintf("user%d@example.test", i))
	}
	_, page := b.visit("/login")
	s.NotContains(page.Props["errors"].(map[string]any)["email"], "Too many attempts", "20 a minute are allowed")

	attempt("another@example.test")
	_, page = b.visit("/login")
	s.Contains(page.Props["errors"].(map[string]any)["email"], "Too many attempts")
}
