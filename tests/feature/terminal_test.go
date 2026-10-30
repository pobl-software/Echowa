package feature

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"time"

	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type TerminalTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
	server *httptest.Server
	app    *models.App
}

func TestTerminalTestSuite(t *testing.T) {
	suite.Run(t, new(TerminalTestSuite))
}

func (s *TerminalTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	// A real listener: a WebSocket takes over the connection, which the
	// test client's recorder can't hand over.
	s.server = httptest.NewServer(facades.Route())
	s.T().Cleanup(s.server.Close)
	s.T().Cleanup(func() { services.CloseTerminals("") })
	s.app = createTestApp(s.T(), "blog", nil)
}

// shell is one ExecTTY call, answered with a fake terminal.
type shell struct {
	service string
	cmd     []string
	size    docker.TerminalSize
	term    *docker.FakeTerminal
}

// shells makes ExecTTY open fake terminals, and sends each as it opens.
func (s *TerminalTestSuite) shells() <-chan shell {
	opened := make(chan shell, 10)
	s.docker.ExecTTYFunc = func(_ context.Context, service string, cmd []string, size docker.TerminalSize) (docker.Terminal, error) {
		term := docker.NewFakeTerminal("0123456789abcdef0123")
		opened <- shell{service: service, cmd: cmd, size: size, term: term}
		return term, nil
	}

	return opened
}

func (s *TerminalTestSuite) path(app *models.App, query string) string {
	return "/apps/" + id(app.ID) + "/terminal?" + query
}

// dial opens the WebSocket as the browser b would from a page at origin,
// or with no Origin when it's empty.
func (s *TerminalTestSuite) dial(b *browser, path, origin string) (*websocket.Conn, *nethttp.Response, error) {
	header := nethttp.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	var cookies []string
	for _, cookie := range b.cookies {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	header.Set("Cookie", strings.Join(cookies, "; "))
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.server.URL, "http")+path, header)
	if conn != nil {
		s.T().Cleanup(func() { _ = conn.Close() })
	}

	return conn, resp, err
}

// open opens a terminal on the app as b, and waits for its shell.
func (s *TerminalTestSuite) open(b *browser, opened <-chan shell) (*websocket.Conn, shell) {
	conn, _, err := s.dial(b, s.path(s.app, "cols=100&rows=30"), s.server.URL)
	s.Require().NoError(err)
	s.JSONEq(`{"type":"ready","container":"0123456789ab"}`, s.text(conn))
	select {
	case sh := <-opened:
		return conn, sh
	case <-time.After(5 * time.Second):
		s.FailNow("no shell opened")
		return nil, shell{}
	}
}

func (s *TerminalTestSuite) read(conn *websocket.Conn, want int) string {
	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(5 * time.Second)))
	kind, data, err := conn.ReadMessage()
	s.Require().NoError(err)
	s.Require().Equal(want, kind, "message: %s", data)

	return string(data)
}

func (s *TerminalTestSuite) text(conn *websocket.Conn) string {
	return s.read(conn, websocket.TextMessage)
}

func (s *TerminalTestSuite) output(conn *websocket.Conn) string {
	return s.read(conn, websocket.BinaryMessage)
}

func (s *TerminalTestSuite) closedNormally(conn *websocket.Conn) {
	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(5 * time.Second)))
	_, _, err := conn.ReadMessage()
	s.True(websocket.IsCloseError(err, websocket.CloseNormalClosure), "closed with %v", err)
}

func (s *TerminalTestSuite) TestTerminalRunsAShellInTheAppsContainer() {
	b := signedIn(s.T(), &s.TestCase)
	opened := s.shells()
	conn, sh := s.open(b, opened)

	s.Equal(s.app.ServiceName(), sh.service)
	s.Equal(docker.TerminalSize{Rows: 30, Cols: 100}, sh.size)
	s.Equal([]string{"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}, sh.cmd)

	s.Require().NoError(conn.WriteMessage(websocket.BinaryMessage, []byte("php artisan migrate\r")))
	s.Eventually(func() bool { return sh.term.Typed() == "php artisan migrate\r" }, 2*time.Second, 10*time.Millisecond)
	sh.term.Print("Migrated: 2026_10_30_create_posts\r\n")
	s.Equal("Migrated: 2026_10_30_create_posts\r\n", s.output(conn))

	for _, msg := range []string{
		`{"type":"resize","cols":120,"rows":40}`,
		`{"type":"resize","cols":0,"rows":40}`,
		`{"type":"resize","cols":1001,"rows":40}`,
		`{"type":"paste"}`,
		`not json`,
		`{"type":"resize","cols":80,"rows":24}`,
	} {
		s.Require().NoError(conn.WriteMessage(websocket.TextMessage, []byte(msg)))
	}
	s.Eventually(func() bool { return len(sh.term.Sizes()) == 2 }, 2*time.Second, 10*time.Millisecond)
	s.Equal([]docker.TerminalSize{{Rows: 40, Cols: 120}, {Rows: 24, Cols: 80}}, sh.term.Sizes(), "only valid resizes reach the shell")

	sh.term.Exit(0)
	s.JSONEq(`{"type":"exit","code":0}`, s.text(conn))
	s.closedNormally(conn)
	s.Eventually(sh.term.Closed, 2*time.Second, 10*time.Millisecond)
}

func (s *TerminalTestSuite) TestTerminalOutlivesTheRequestTimeout() {
	b := signedIn(s.T(), &s.TestCase)
	conn, sh := s.open(b, s.shells())
	timeout := time.Duration(facades.Config().GetInt("http.request_timeout")) * time.Second
	s.Require().Positive(timeout)

	time.Sleep(timeout + 500*time.Millisecond)
	sh.term.Print("still here")
	s.Equal("still here", s.output(conn))
	s.Require().NoError(conn.WriteMessage(websocket.BinaryMessage, []byte("ls\r")))
	s.Eventually(func() bool { return sh.term.Typed() == "ls\r" }, 2*time.Second, 10*time.Millisecond)
}

func (s *TerminalTestSuite) TestTerminalOnlyOpensFromEchowasOwnPages() {
	s.docker.ExecTTYFunc = func(context.Context, string, []string, docker.TerminalSize) (docker.Terminal, error) {
		s.Fail("no shell opens")
		return nil, docker.ErrNotRunning
	}
	path := s.path(s.app, "cols=80&rows=24")

	_, resp, err := s.dial(newBrowser(s.T(), &s.TestCase), path, s.server.URL)
	s.Require().Error(err)
	s.Equal(nethttp.StatusUnauthorized, resp.StatusCode)

	b := signedIn(s.T(), &s.TestCase)
	for _, origin := range []string{"", "http://blog.example.com", "null", s.server.URL + ".evil.test"} {
		_, resp, err = s.dial(b, path, origin)
		s.Require().Error(err, "origin %q", origin)
		s.Equal(nethttp.StatusForbidden, resp.StatusCode, "origin %q", origin)
	}
}

func (s *TerminalTestSuite) TestTerminalIsOnlyForCustomApps() {
	b := signedIn(s.T(), &s.TestCase)
	s.shells()
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)

	_, resp, err := s.dial(b, s.path(db, "cols=80&rows=24"), s.server.URL)
	s.Require().Error(err)
	s.Equal(nethttp.StatusNotFound, resp.StatusCode)
	_, resp, err = s.dial(b, "/apps/999/terminal?cols=80&rows=24", s.server.URL)
	s.Require().Error(err)
	s.Equal(nethttp.StatusNotFound, resp.StatusCode)

	for _, query := range []string{"", "cols=80", "cols=0&rows=24", "cols=80&rows=1001", "cols=-1&rows=24", "cols=x&rows=24"} {
		_, resp, err = s.dial(b, s.path(s.app, query), s.server.URL)
		s.Require().Error(err, "query %q", query)
		s.Equal(nethttp.StatusUnprocessableEntity, resp.StatusCode, "query %q", query)
	}
}

func (s *TerminalTestSuite) TestTerminalSaysWhyItCouldntOpen() {
	b := signedIn(s.T(), &s.TestCase)
	conn, _, err := s.dial(b, s.path(s.app, "cols=80&rows=24"), s.server.URL)
	s.Require().NoError(err)
	s.JSONEq(`{"type":"closed","reason":"blog isn't running. Start or deploy it, then reconnect."}`, s.text(conn))
	s.closedNormally(conn)

	opened := s.shells()
	var conns []*websocket.Conn
	for range services.MaxTerminals {
		conn, _ := s.open(b, opened)
		conns = append(conns, conn)
	}
	conn, _, err = s.dial(b, s.path(s.app, "cols=80&rows=24"), s.server.URL)
	s.Require().NoError(err)
	s.JSONEq(`{"type":"closed","reason":"blog has 5 terminals open already. Close one, then reconnect."}`, s.text(conn))

	// Closing a page frees its place.
	s.Require().NoError(conns[0].Close())
	s.Eventually(func() bool {
		conn, _, err := s.dial(b, s.path(s.app, "cols=80&rows=24"), s.server.URL)
		if err != nil {
			return false
		}
		return strings.Contains(s.text(conn), `"ready"`)
	}, 2*time.Second, 50*time.Millisecond)
}

func (s *TerminalTestSuite) TestClosingThePageHangsUpTheShell() {
	b := signedIn(s.T(), &s.TestCase)
	conn, sh := s.open(b, s.shells())

	s.Require().NoError(conn.Close())
	s.Eventually(sh.term.Closed, 2*time.Second, 10*time.Millisecond)
}

func (s *TerminalTestSuite) TestLoggingOutClosesTerminals() {
	b := signedIn(s.T(), &s.TestCase)
	conn, sh := s.open(b, s.shells())

	b.visit("/dashboard")
	b.submit(nethttp.MethodPost, "/logout", "/dashboard", nil)

	s.JSONEq(`{"type":"closed","reason":"You signed out."}`, s.text(conn))
	s.closedNormally(conn)
	s.True(sh.term.Closed())
}

func (s *TerminalTestSuite) TestIdleTerminalsClose() {
	original := services.TerminalIdle
	services.TerminalIdle = 300 * time.Millisecond
	s.T().Cleanup(func() { services.TerminalIdle = original })
	b := signedIn(s.T(), &s.TestCase)
	conn, sh := s.open(b, s.shells())

	for range 8 {
		s.Require().NoError(conn.WriteMessage(websocket.BinaryMessage, []byte("x")))
		time.Sleep(100 * time.Millisecond)
	}
	s.False(sh.term.Closed(), "typing keeps it open")
	go func() {
		for range 8 {
			sh.term.Print("y")
			time.Sleep(100 * time.Millisecond)
		}
	}()
	for range 8 {
		s.Equal("y", s.output(conn))
	}
	s.False(sh.term.Closed(), "output keeps it open")

	s.JSONEq(`{"type":"closed","reason":"Closed after 0 minutes with nothing typed or shown."}`, s.text(conn))
	s.closedNormally(conn)
	s.True(sh.term.Closed())
}
