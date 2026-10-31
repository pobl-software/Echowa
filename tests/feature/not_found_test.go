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
