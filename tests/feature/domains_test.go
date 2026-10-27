package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
	"net/netip"
	"testing"

	"github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/services"
	"goravel/tests"
)

type DomainsTestSuite struct {
	suite.Suite
	tests.TestCase
	lookups []string
}

func TestDomainsTestSuite(t *testing.T) {
	suite.Run(t, new(DomainsTestSuite))
}

var serverAddr = netip.MustParseAddr("203.0.113.10")

func (s *DomainsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.FakeDocker()

	lookup, addrs := services.LookupDomain, services.ServerAddresses
	s.T().Cleanup(func() { services.LookupDomain, services.ServerAddresses = lookup, addrs })
	s.lookups = nil
	services.LookupDomain = func(_ context.Context, domain string) ([]netip.Addr, error) {
		s.lookups = append(s.lookups, domain)
		if domain == "elsewhere.example.com" {
			return []netip.Addr{netip.MustParseAddr("198.51.100.7")}, nil
		}
		return []netip.Addr{serverAddr}, nil
	}
	services.ServerAddresses = func(context.Context) ([]netip.Addr, error) { return []netip.Addr{serverAddr}, nil }
}

// checkDomain is the Domains tab's fetch before it adds a domain. It
// expects status back.
func (s *DomainsTestSuite) checkDomain(b *browser, appID uint, domain string, status int) map[string]any {
	resp := b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/domain/check", appID), map[string]any{"domain": domain}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})
	resp.AssertStatus(status)
	result, err := resp.Json()
	s.Require().NoError(err)

	return result
}

func (s *DomainsTestSuite) TestCheckSaysWhetherTheDomainPointsHere() {
	app := createTestApp(s.T(), "blog", nil)
	createTestApp(s.T(), "shop", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=domains", app.ID))

	result := s.checkDomain(b, app.ID, "  New.Example.com ", nethttp.StatusOK)
	s.Equal("points_here", result["state"])
	s.Equal([]any{map[string]any{"address": "203.0.113.10", "here": true}}, result["records"])
	s.Equal([]string{"new.example.com"}, s.lookups, "trimmed and lowercased first")

	result = s.checkDomain(b, app.ID, "elsewhere.example.com", nethttp.StatusOK)
	s.Equal("elsewhere", result["state"])

	result = s.checkDomain(b, app.ID, "blog.example.com", nethttp.StatusOK)
	s.Equal("points_here", result["state"], "its own domain isn't taken")

	for _, domain := range []string{"", "not a domain", "shop.example.com"} {
		result = s.checkDomain(b, app.ID, domain, nethttp.StatusUnprocessableEntity)
		s.NotEmpty(result["message"], domain)
	}
	s.Len(s.lookups, 3, "nothing invalid is looked up")
	s.Equal("blog.example.com", *s.loadDomain("blog"), "checking saves nothing")
}

func (s *DomainsTestSuite) TestRemoveClearsTheDomain() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	resp := b.submit(nethttp.MethodDelete, showURL+"/domain", showURL, nil)
	resp.AssertStatus(nethttp.StatusSeeOther)
	resp.AssertHeader("Location", showURL+"?tab=domains")
	s.Nil(s.loadDomain("blog"))

	_, page := b.visit(showURL + "?tab=domains")
	s.NotContains(page.Props, "domainStatus", "never checked on a full visit")
	s.Nil(s.domainStatus(b, app.ID), "no domain, nothing to check")
}

func (s *DomainsTestSuite) TestStatusChecksTheSavedDomain() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)
	b.submit(nethttp.MethodPut, showURL+"/domain", showURL, map[string]any{"domain": "blog.example.com", "tls": false})

	status := s.domainStatus(b, app.ID)
	s.Equal("blog.example.com", status["domain"])
	s.Equal("points_here", status["dns"].(map[string]any)["state"])
	s.Equal("off", status["certificate"].(map[string]any)["state"], "plain HTTP has no certificate to read")
	s.NotEmpty(status["checked_at"])
}

// domainStatus asks the app page for its domainStatus prop, the way the
// Domains section's partial reload does.
func (s *DomainsTestSuite) domainStatus(b *browser, appID uint) map[string]any {
	resp := b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d?tab=domains", appID), nil, true, map[string]string{
		"X-Inertia-Partial-Component": "Apps/Show",
		"X-Inertia-Partial-Data":      "domainStatus",
	})
	resp.AssertOk()
	body, err := resp.Content()
	s.Require().NoError(err)
	result, _ := gonertia.AssertFromString(s.T(), body).Props["domainStatus"].(map[string]any)

	return result
}

func (s *DomainsTestSuite) loadDomain(name string) *string {
	var domain struct{ Domain *string }
	s.Require().NoError(facades.Orm().Query().Table("apps").Select("domain").Where("name", name).First(&domain))

	return domain.Domain
}
