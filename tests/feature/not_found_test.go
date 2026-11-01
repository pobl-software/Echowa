package feature

import (
	nethttp "net/http"
	"testing"

	gonertia "github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/suite"

	"goravel/tests"
)

type NotFoundTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestNotFoundTestSuite(t *testing.T) {
	suite.Run(t, new(NotFoundTestSuite))
}

func (s *NotFoundTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
}

func (s *NotFoundTestSuite) TestTheInertiaClientGetsThePageWithA404() {
	b := signedIn(s.T(), &s.TestCase)
	for _, uri := range []string{"/apps/99999", "/no/such/page"} {
		resp := b.request(nethttp.MethodGet, uri, nil, true, nil)
		resp.AssertNotFound()
		resp.AssertHeader("X-Inertia", "true")
		body, err := resp.Content()
		s.Require().NoError(err)
		page := gonertia.AssertFromString(s.T(), body)
		page.AssertComponent("Errors/NotFound")
		s.Equal(uri, page.Props["path"])
	}
}

// The 404 page shows the path it was asked for, so markup in a link must reach
// the page as text and never as HTML (CodeQL's go/reflected-xss on NotFound).
func (s *NotFoundTestSuite) TestAPathWithMarkupStaysText() {
	b := newBrowser(s.T(), &s.TestCase)
	const uri = "/x%3C%2Fscript%3E%3Cscript%3Ealert(1)%3C%2Fscript%3E%22%3E%3Cimg%20src%3Dx%20onerror%3Dalert(2)%3E"
	const path = `/x</script><script>alert(1)</script>"><img src=x onerror=alert(2)>`

	resp := b.request(nethttp.MethodGet, uri, nil, false, map[string]string{"Accept": "text/html"})
	resp.AssertNotFound()
	body, err := resp.Content()
	s.Require().NoError(err)
	for _, markup := range []string{"</script><script>alert", "<img"} {
		s.NotContains(body, markup)
	}
	s.Equal(path, gonertia.AssertFromString(s.T(), body).Props["path"])

	resp = b.request(nethttp.MethodGet, uri, nil, true, nil)
	resp.AssertNotFound()
	resp.AssertHeader("Content-Type", "application/json")
	body, err = resp.Content()
	s.Require().NoError(err)
	s.NotContains(body, "<")
	s.Equal(path, gonertia.AssertFromString(s.T(), body).Props["path"])
}

func (s *NotFoundTestSuite) TestABrowserGetsThePageAndAnythingElsePlainText() {
	b := newBrowser(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, "/no/such/page", nil, false, map[string]string{"Accept": "text/html"})
	resp.AssertNotFound()
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(body, "Errors/NotFound", "the page object is in the HTML")

	resp = b.request(nethttp.MethodGet, "/no/such/page", nil, false, map[string]string{"Accept": "application/json"})
	resp.AssertNotFound()
	body, err = resp.Content()
	s.Require().NoError(err)
	s.Equal("404 page not found", body)
}
