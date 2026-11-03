package feature

import (
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"time"

	"goravel/app/facades"
	"goravel/routes"
)

// The listener for hooks only (ADR 0020): a tunnel hostname without
// Cloudflare Access reaches it, so it must serve /hooks/ and nothing else.

// post sends a hook as CI would, to a listener at base.
func (s *HooksTestSuite) post(base string, h hook) *nethttp.Response {
	ts := h.timestamp()
	req, err := nethttp.NewRequest(nethttp.MethodPost, base+"/hooks/deploy/"+h.app, strings.NewReader(h.body))
	s.Require().NoError(err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Echowa-Timestamp", ts)
	req.Header.Set("X-Echowa-Signature", h.signature(ts))
	resp, err := nethttp.DefaultClient.Do(req)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

// get requests a raw path, unchanged by URL parsing, and returns the
// status and body.
func (s *HooksTestSuite) get(base, method, rawPath string) (int, string) {
	req, err := nethttp.NewRequest(method, base+"/", nil)
	s.Require().NoError(err)
	req.URL.Opaque = rawPath
	resp, err := nethttp.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	s.Require().NoError(err)

	return resp.StatusCode, string(body)
}

func (s *HooksTestSuite) TestTheHooksListenerServesHooksOnly() {
	app, secret := s.app("blog")
	srv := httptest.NewServer(routes.HooksOnly(facades.Route()))
	defer srv.Close()

	for _, path := range []string{"/", "/login", "/up", "/setup", "/apps", "/build/manifest.json", "/hooks", "/hooks/../login", "/hooks/%2e%2e/login", "/hooks/deploy/../../login", "//hooks/deploy/blog"} {
		for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
			status, body := s.get(srv.URL, method, path)
			s.Equal(nethttp.StatusNotFound, status, "%s %s", method, path)
			s.Equal(`{"error":"not found"}`+"\n", body, "%s %s", method, path)
		}
	}
	status, _ := s.get(srv.URL, nethttp.MethodGet, "/hooks/deploy/blog")
	s.Equal(nethttp.StatusNotFound, status, "the router's own 404 for a GET")

	resp := s.post(srv.URL, hook{app: "blog", secret: secret, body: `{"ref":"v1"}`})
	s.Equal(nethttp.StatusAccepted, resp.StatusCode)
	s.Len(s.deployments(app.ID), 1)
	s.Equal(nethttp.StatusUnauthorized, s.post(srv.URL, hook{app: "blog", secret: "wrong"}).StatusCode, "the same handler, signature check included")
}

// Goravel's gin driver reads a JSON body whole before Echowa's code, so
// on the public listener the cap comes before the router.
func (s *HooksTestSuite) TestTheHooksListenerCapsBodiesBeforeTheRouter() {
	var reached []int
	srv := httptest.NewServer(routes.HooksOnly(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, err := io.ReadAll(r.Body)
		s.NoError(err)
		reached = append(reached, len(body))
		w.WriteHeader(nethttp.StatusAccepted)
	})))
	defer srv.Close()
	send := func(body io.Reader) (int, string) {
		resp, err := nethttp.Post(srv.URL+"/hooks/deploy/blog", "application/json", body)
		s.Require().NoError(err)
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		s.Require().NoError(err)
		return resp.StatusCode, string(out)
	}
	big := `{"ref":"` + strings.Repeat("a", 1<<20) + `"}`

	status, body := send(strings.NewReader(big))
	s.Equal(nethttp.StatusRequestEntityTooLarge, status, "by Content-Length")
	s.Equal(`{"error":"body is over 8 KiB"}`+"\n", body)
	// A reader of unknown length goes chunked, with no Content-Length.
	status, _ = send(struct{ io.Reader }{strings.NewReader(big)})
	s.Equal(nethttp.StatusRequestEntityTooLarge, status, "chunked")
	s.Empty(reached, "the router never saw either")

	status, _ = send(struct{ io.Reader }{strings.NewReader(`{"ref":"v1"}`)})
	s.Equal(nethttp.StatusAccepted, status)
	s.Equal([]int{len(`{"ref":"v1"}`)}, reached, "a small chunked body reaches it whole")
}

func (s *HooksTestSuite) TestTheHooksListenerStartsAndStopsWithEchowa() {
	original := facades.Config().GetString("echowa.hooks_addr")
	s.T().Cleanup(func() { facades.Config().Add("echowa.hooks_addr", original) })
	facades.Config().Add("echowa.hooks_addr", "")
	s.False(routes.NewHooksListener().ShouldRun(), "no listener unless ECHOWA_HOOKS_ADDR is set")

	free, err := net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err)
	addr := free.Addr().String()
	s.Require().NoError(free.Close())
	facades.Config().Add("echowa.hooks_addr", addr)
	listener := routes.NewHooksListener()
	s.Require().True(listener.ShouldRun())

	app, secret := s.app("blog")
	ran := make(chan error, 1)
	go func() { ran <- listener.Run() }()
	base := "http://" + addr
	s.Require().Eventually(func() bool {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	status, _ := s.get(base, nethttp.MethodGet, "/login")
	s.Equal(nethttp.StatusNotFound, status)
	s.Equal(nethttp.StatusAccepted, s.post(base, hook{app: "blog", secret: secret, body: `{"ref":"v1"}`}).StatusCode)
	s.Len(s.deployments(app.ID), 1)

	busy := routes.NewHooksListener()
	s.Error(busy.Run(), "an address already taken stops Echowa rather than leaving CI with nothing")

	s.Require().NoError(listener.Shutdown())
	select {
	case err := <-ran:
		s.NoError(err, "a shutdown isn't a failure")
	case <-time.After(5 * time.Second):
		s.Fail("Run didn't return after Shutdown")
	}
}
