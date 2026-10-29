package feature

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	gonertia "github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/require"

	"goravel/app/inertia"
	"goravel/tests"
)

// browser carries cookies between test requests like a real browser, so
// tests can exercise sessions, CSRF and remember-me end to end. It doesn't
// follow redirects; assert on Location and visit it explicitly.
type browser struct {
	t       *testing.T
	tc      *tests.TestCase
	cookies map[string]*nethttp.Cookie
	// forbidden strings must never appear in a page response: Inertia
	// JSON or a full HTML page. Plain JSON endpoints are exempt.
	forbidden []string
}

// forbid makes every later page response fail the test if it contains any
// of secrets.
func (b *browser) forbid(secrets ...string) {
	b.forbidden = append(b.forbidden, secrets...)
}

func newBrowser(t *testing.T, tc *tests.TestCase) *browser {
	return &browser{t: t, tc: tc, cookies: map[string]*nethttp.Cookie{}}
}

// visit makes an Inertia (XHR) GET and returns the page object.
func (b *browser) visit(uri string) (contractstesting.Response, gonertia.AssertableInertia) {
	resp := b.request(nethttp.MethodGet, uri, nil, true, nil)
	resp.AssertOk()
	body, err := resp.Content()
	require.NoError(b.t, err)

	return resp, gonertia.AssertFromString(b.t, body)
}

// submit posts JSON the way Inertia's useForm does, with the CSRF token from
// the XSRF-TOKEN cookie, from the page at referer.
func (b *browser) submit(method, uri, referer string, data map[string]any) contractstesting.Response {
	return b.request(method, uri, data, true, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Referer":      testOrigin + referer,
	})
}

func (b *browser) request(method, uri string, data map[string]any, isInertia bool, headers map[string]string) contractstesting.Response {
	var body bytes.Buffer
	if data != nil {
		require.NoError(b.t, json.NewEncoder(&body).Encode(data))
	}
	all := map[string]string{"Content-Type": "application/json"}
	if isInertia {
		all["X-Inertia"] = "true"
		all["X-Inertia-Version"] = inertia.Version()
	}
	maps.Copy(all, headers)

	return b.send(method, uri, &body, all)
}

// send makes a request with body as it is, such as a chunk of an upload.
func (b *browser) send(method, uri string, body io.Reader, headers map[string]string) contractstesting.Response {
	req := b.tc.Http(b.t).WithHeaders(headers)
	for _, cookie := range b.cookies {
		req = req.WithCookie(cookie)
	}

	var (
		resp contractstesting.Response
		err  error
	)
	switch method {
	case nethttp.MethodGet:
		resp, err = req.Get(uri)
	case nethttp.MethodHead:
		resp, err = req.Head(uri)
	case nethttp.MethodPost:
		resp, err = req.Post(uri, body)
	case nethttp.MethodPut:
		resp, err = req.Put(uri, body)
	case nethttp.MethodDelete:
		resp, err = req.Delete(uri, body)
	default:
		b.t.Fatalf("unsupported method %s", method)
	}
	require.NoError(b.t, err)
	b.checkForbidden(method, uri, resp)

	for _, cookie := range resp.Cookies() {
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now())) {
			delete(b.cookies, cookie.Name)
			continue
		}
		b.cookies[cookie.Name] = cookie
	}

	return resp
}

func (b *browser) checkForbidden(method, uri string, resp contractstesting.Response) {
	if len(b.forbidden) == 0 {
		return
	}
	plainJSON := strings.HasPrefix(resp.Headers().Get("Content-Type"), "application/json") && resp.Headers().Get("X-Inertia") == ""
	if plainJSON {
		return
	}
	content, err := resp.Content()
	require.NoError(b.t, err)
	for _, secret := range b.forbidden {
		require.NotContains(b.t, content, secret, "%s %s leaked a secret", method, uri)
	}
}

func (b *browser) cookieValue(name string) string {
	if cookie, ok := b.cookies[name]; ok {
		return cookie.Value
	}

	return ""
}

func (b *browser) forget(name string) {
	delete(b.cookies, name)
}

const middlewareXsrfCookie = "XSRF-TOKEN"

// testOrigin is the host Goravel's test client sends requests to (Go's
// httptest default). Referers must match it to count as same-origin.
const testOrigin = "http://example.com"
