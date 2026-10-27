package feature

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	gonertia "github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/sse"
	"goravel/tests"
)

type StreamTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
	server *httptest.Server
}

func TestStreamTestSuite(t *testing.T) {
	suite.Run(t, new(StreamTestSuite))
}

func (s *StreamTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	// A real listener rather than the test client's recorder, so the test
	// sees events as the server flushes them and can hang up mid-stream.
	s.server = httptest.NewServer(facades.Route())
	s.T().Cleanup(s.server.Close)

	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

func (s *StreamTestSuite) deployed(app *models.App, lines ...docker.LogLine) {
	s.Require().NoError(s.docker.DeployService(context.Background(), deploy.ServiceSpec(app, "sha256:abc", nil, services.ResourceLimits{})))
	for _, line := range lines {
		s.docker.WriteLog(app.ServiceName(), line)
	}
}

func (s *StreamTestSuite) TestAppLogsFollowUntilTheClientHangsUp() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	s.deployed(app,
		docker.LogLine{Time: start, Stream: docker.Stdout, Text: "booting"},
		docker.LogLine{Time: start.Add(time.Second), Stream: docker.Stdout, Text: "listening on :8080"},
		docker.LogLine{Time: start.Add(2 * time.Second), Stream: docker.Stderr, Text: "warning: no cache"},
	)

	stream := s.open(b, "/apps/"+id(app.ID)+"/logs/stream?tail=2", nil)
	s.Equal("text/event-stream", stream.contentType)

	first := stream.next()
	s.Equal("line", first.name)
	s.Equal("2026-10-24T09:00:01Z", first.id)
	s.JSONEq(`{"time":"2026-10-24T09:00:01Z","stream":"stdout","text":"listening on :8080"}`, first.data)
	s.JSONEq(`{"time":"2026-10-24T09:00:02Z","stream":"stderr","text":"warning: no cache"}`, stream.next().data)

	// A line written now arrives while the request is still open: nothing
	// buffers the response.
	s.docker.WriteLog(app.ServiceName(), docker.LogLine{Time: start.Add(3 * time.Second), Stream: docker.Stdout, Text: "GET /"})
	s.JSONEq(`{"time":"2026-10-24T09:00:03Z","stream":"stdout","text":"GET /"}`, stream.next().data)
	s.Equal(1, s.docker.OpenLogStreams())

	// Hanging up ends the Docker log request.
	stream.close()
	s.Eventually(func() bool { return s.docker.OpenLogStreams() == 0 }, 2*time.Second, 10*time.Millisecond)
}

func (s *StreamTestSuite) TestStreamsEndAfterMaxLifetimeSoTheBrowserReauthenticates() {
	original := sse.MaxLifetime
	sse.MaxLifetime = 300 * time.Millisecond
	s.T().Cleanup(func() { sse.MaxLifetime = original })
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app, docker.LogLine{Time: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC), Stream: docker.Stdout, Text: "booting"})

	stream := s.open(b, "/apps/"+id(app.ID)+"/logs/stream", nil)
	s.Equal("booting", lineText(s, stream.next().data))

	s.Empty(stream.drain(), "closed with no end event, so EventSource reconnects")
	s.Eventually(func() bool { return s.docker.OpenLogStreams() == 0 }, 2*time.Second, 10*time.Millisecond)

	// The reconnect is a new request, checked again: after logout it's refused.
	b.visit("/dashboard")
	b.submit(nethttp.MethodPost, "/logout", "/dashboard", nil)
	resp := s.get(b, "/apps/"+id(app.ID)+"/logs/stream", map[string]string{"Last-Event-ID": "2026-10-24T09:00:00Z"})
	s.Equal(nethttp.StatusUnauthorized, resp.StatusCode)
}

func lineText(s *StreamTestSuite, data string) string {
	var line struct{ Text string }
	s.Require().NoError(json.Unmarshal([]byte(data), &line))

	return line.Text
}

func (s *StreamTestSuite) TestAppLogsOutliveTheRequestTimeout() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app)
	timeout := time.Duration(facades.Config().GetInt("http.request_timeout")) * time.Second
	s.Require().Positive(timeout)

	stream := s.open(b, "/apps/"+id(app.ID)+"/logs/stream", nil)
	time.Sleep(timeout + 500*time.Millisecond)
	s.docker.WriteLog(app.ServiceName(), docker.LogLine{Time: time.Now(), Stream: docker.Stdout, Text: "still here"})

	s.Contains(stream.next().data, "still here")
	stream.close()
}

func (s *StreamTestSuite) TestAppLogsResumeAfterLastEventID() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	s.deployed(app,
		docker.LogLine{Time: start, Stream: docker.Stdout, Text: "one"},
		docker.LogLine{Time: start.Add(time.Second), Stream: docker.Stdout, Text: "two"},
		docker.LogLine{Time: start.Add(2 * time.Second), Stream: docker.Stdout, Text: "three"},
	)

	stream := s.open(b, "/apps/"+id(app.ID)+"/logs/stream", map[string]string{"Last-Event-ID": "2026-10-24T09:00:01Z"})

	s.Contains(stream.next().data, `"three"`)
	stream.close()
}

func (s *StreamTestSuite) TestAppLogsShowOneDeploy() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	s.deployed(app, docker.LogLine{Time: start, Stream: docker.Stdout, Text: "first boot"})
	s.deployed(app, docker.LogLine{Time: start.Add(time.Minute), Stream: docker.Stdout, Text: "second boot"})
	tasks, err := s.docker.ServiceTasks(context.Background(), app.ServiceName())
	s.Require().NoError(err)
	s.Require().Len(tasks, 2)

	// The current deploy, followed: the first deploy's lines are left out,
	// even ones it writes now.
	current := s.open(b, "/apps/"+id(app.ID)+"/logs/stream", nil)
	s.Contains(current.next().data, `"second boot"`)
	s.docker.WriteLog(app.ServiceName(), docker.LogLine{Time: start.Add(2 * time.Minute), Stream: docker.Stdout, Text: "first shutting down", Task: tasks[0].ID})
	s.docker.WriteLog(app.ServiceName(), docker.LogLine{Time: start.Add(3 * time.Minute), Stream: docker.Stdout, Text: "GET /"})
	s.Contains(current.next().data, `"GET /"`)
	current.close()

	// The first deploy: its lines, then the end, since its task stopped.
	first := s.open(b, fmt.Sprintf("/apps/%s/logs/stream?deploy=%d", id(app.ID), tasks[0].Generation), nil)
	s.Contains(first.next().data, `"first boot"`)
	s.Contains(first.next().data, `"first shutting down"`)
	end := first.next()
	s.Equal("end", end.name)
	s.Contains(end.data, `"reason":"stopped"`)
	first.closed()

	// A deploy with no lines left says so.
	s.deployed(app)
	s.deployed(app, docker.LogLine{Time: start.Add(4 * time.Minute), Stream: docker.Stdout, Text: "fourth boot"})
	empty := s.open(b, fmt.Sprintf("/apps/%s/logs/stream?deploy=%d", id(app.ID), tasks[1].Generation+1), nil)
	end = empty.next()
	s.Equal("end", end.name)
	s.Contains(end.data, "Docker has no lines from that deploy")
	empty.closed()

	gone := s.open(b, "/apps/"+id(app.ID)+"/logs/stream?deploy=99", nil)
	end = gone.next()
	s.Equal("end", end.name)
	s.Contains(end.data, `"reason":"not_found"`)
	gone.closed()
	s.Equal(0, s.docker.OpenLogStreams())
}

func (s *StreamTestSuite) TestTheLogsTabListsTheNewestDeploys() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	for range services.MaxLogDeploys + 1 {
		s.deployed(app)
	}

	_, page := b.visit("/apps/" + id(app.ID) + "?tab=logs")
	s.NotContains(page.Props, "logDeploys", "Docker isn't asked on a full visit")

	resp := b.request(nethttp.MethodGet, "/apps/"+id(app.ID)+"?tab=logs", nil, true, map[string]string{
		"X-Inertia-Partial-Component": "Apps/Show",
		"X-Inertia-Partial-Data":      "logDeploys",
	})
	resp.AssertOk()
	body, err := resp.Content()
	s.Require().NoError(err)
	deploys, _ := gonertia.AssertFromString(s.T(), body).Props["logDeploys"].([]any)
	s.Require().Len(deploys, services.MaxLogDeploys)
	newest := deploys[0].(map[string]any)
	s.InDelta(services.MaxLogDeploys+1, newest["generation"], 0)
	s.Equal(true, newest["current"])
	s.Equal(false, deploys[1].(map[string]any)["current"])
}

func (s *StreamTestSuite) TestAppLogsOfAnUndeployedAppEnd() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)

	stream := s.open(b, "/apps/"+id(app.ID)+"/logs/stream", nil)

	end := stream.next()
	s.Equal("end", end.name)
	s.Contains(end.data, `"reason":"not_deployed"`)
	stream.closed()
}

func (s *StreamTestSuite) TestAppLogsRejectBadQueries() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app)

	for _, query := range []string{"tail=0", "tail=abc", "tail=10001", "since=-1h", "since=yesterday"} {
		resp := s.get(b, "/apps/"+id(app.ID)+"/logs/stream?"+query, nil)
		s.Equal(nethttp.StatusUnprocessableEntity, resp.StatusCode, query)
		resp.Body.Close()
	}
	resp := s.get(b, "/apps/999/logs/stream", nil)
	s.Equal(nethttp.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func (s *StreamTestSuite) TestStreamsNeedASignedInUser() {
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app)
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, "v1")
	s.Require().NoError(err)

	for _, path := range []string{"/apps/" + id(app.ID) + "/logs/stream", "/deployments/" + id(d.ID) + "/stream"} {
		resp := s.get(newBrowser(s.T(), &s.TestCase), path, nil)
		s.Equal(nethttp.StatusUnauthorized, resp.StatusCode, path)
		resp.Body.Close()
	}
	s.Zero(s.docker.OpenLogStreams())
}

// A stream that closes after the user signed out must not write its old
// copy of the session back, which would sign the old cookie in again.
func (s *StreamTestSuite) TestClosingAStreamDoesNotRestoreTheSession() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app)
	b.visit("/dashboard") // picks up the XSRF cookie login rotated
	before := newBrowser(s.T(), &s.TestCase)
	for name, cookie := range b.cookies {
		before.cookies[name] = cookie
	}

	stream := s.open(b, "/apps/"+id(app.ID)+"/logs/stream", nil)
	s.Eventually(func() bool { return s.docker.OpenLogStreams() == 1 }, 2*time.Second, 10*time.Millisecond)
	b.submit(nethttp.MethodPost, "/logout", "/dashboard", nil).AssertHeader("Location", "/")
	stream.close()
	s.Eventually(func() bool { return s.docker.OpenLogStreams() == 0 }, 2*time.Second, 10*time.Millisecond)

	resp := s.get(before, "/dashboard", nil)
	resp.Body.Close()
	s.Equal(nethttp.StatusFound, resp.StatusCode)
	s.Equal("/login", resp.Header.Get("Location"))
}

func (s *StreamTestSuite) TestDeploymentStreamFollowsADeployUntilItFinishes() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	waiting := make(chan struct{})
	release := make(chan struct{})
	s.docker.Wait = func(docker.ServiceSpec) (docker.UpdateResult, error) {
		close(waiting)
		<-release
		return docker.UpdateResult{Outcome: docker.OutcomeCompleted}, nil
	}
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, "v1")
	s.Require().NoError(err)

	stream := s.open(b, "/deployments/"+id(d.ID)+"/stream", nil)
	first := stream.next()
	s.Equal("status", first.name)
	s.Contains(first.data, `"status":"queued"`)

	ran := make(chan error, 1)
	go func() { ran <- deploy.RunQueued(context.Background(), app.ID) }()
	<-waiting

	// While Swarm "updates", the stream already has the log so far.
	var live []string
	for !strings.Contains(strings.Join(live, "\n"), "Waiting for Swarm") {
		event := stream.next()
		if event.name == "line" {
			live = append(live, event.data)
		}
	}
	close(release)
	s.Require().NoError(<-ran)

	rest := stream.drain()
	last := rest[len(rest)-1]
	s.Equal("status", last.name, "the final status comes last")
	s.Contains(last.data, `"status":"succeeded"`)
	var lines []string
	for _, event := range rest {
		if event.name == "line" {
			lines = append(lines, event.data)
		}
	}
	s.Require().NotEmpty(lines)
	s.Contains(lines[len(lines)-1], "Deployment succeeded")

	// Line event IDs are offsets into the log, so a reconnect resumes.
	done := s.loadDeployment(d.ID)
	lastLine := strings.LastIndexByte(strings.TrimSuffix(done.Log, "\n"), '\n') + 1
	resumed := s.open(b, "/deployments/"+id(d.ID)+"/stream", map[string]string{"Last-Event-ID": strconv.Itoa(lastLine)}).drain()
	s.Len(resumed, 2)
	s.Contains(resumed[0].data, "Deployment succeeded")
	s.Contains(resumed[1].data, `"succeeded"`)
}

func (s *StreamTestSuite) TestDeploymentStreamOfAFinishedDeployment() {
	b := signedIn(s.T(), &s.TestCase)
	app := createTestApp(s.T(), "blog", nil)
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, "v1")
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	done := s.loadDeployment(d.ID)

	events := s.open(b, "/deployments/"+id(d.ID)+"/stream", nil).drain()

	var log strings.Builder
	for _, event := range events[:len(events)-1] {
		s.Equal("line", event.name)
		var text string
		s.Require().NoError(json.Unmarshal([]byte(event.data), &text))
		log.WriteString(text + "\n")
	}
	s.Equal(done.Log, log.String())
	s.Equal("status", events[len(events)-1].name)

	resp := s.get(b, "/deployments/999/stream", nil)
	s.Equal(nethttp.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func (s *StreamTestSuite) loadDeployment(id uint) models.Deployment {
	var d models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&d))

	return d
}

func (s *StreamTestSuite) get(b *browser, path string, headers map[string]string) *nethttp.Response {
	return s.request(context.Background(), b, path, headers)
}

func (s *StreamTestSuite) request(ctx context.Context, b *browser, path string, headers map[string]string) *nethttp.Response {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, s.server.URL+path, nil)
	s.Require().NoError(err)
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	for _, cookie := range b.cookies {
		req.AddCookie(&nethttp.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	resp, err := noRedirects.Do(req)
	s.Require().NoError(err)

	return resp
}

// open connects to an event stream as the browser b.
func (s *StreamTestSuite) open(b *browser, path string, headers map[string]string) *eventStream {
	ctx, cancel := context.WithCancel(context.Background())
	resp := s.request(ctx, b, path, headers)
	s.Require().Equal(nethttp.StatusOK, resp.StatusCode)

	stream := &eventStream{s: &s.Suite, contentType: resp.Header.Get("Content-Type"), cancel: cancel, events: make(chan sseEvent, 1000)}
	go stream.read(resp)
	s.T().Cleanup(stream.close)

	return stream
}

var noRedirects = &nethttp.Client{CheckRedirect: func(*nethttp.Request, []*nethttp.Request) error {
	return nethttp.ErrUseLastResponse
}}

type sseEvent struct {
	name, id, data string
}

type eventStream struct {
	s           *suite.Suite
	contentType string
	cancel      context.CancelFunc
	events      chan sseEvent
	buffered    []sseEvent
}

func (e *eventStream) read(resp *nethttp.Response) {
	defer close(e.events)
	defer resp.Body.Close()
	var event sseEvent
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		field, value, _ := strings.Cut(line, ": ")
		switch field {
		case "":
			if event.name != "" {
				e.events <- event
			}
			event = sseEvent{}
		case "event":
			event.name = value
		case "id":
			event.id = value
		case "data":
			event.data = value
		}
	}
}

// next returns the next event, failing the test if none comes within 2s.
func (e *eventStream) next() sseEvent {
	if len(e.buffered) > 0 {
		event := e.buffered[0]
		e.buffered = e.buffered[1:]
		return event
	}
	select {
	case event, ok := <-e.events:
		e.s.Require().True(ok, "the stream closed")
		return event
	case <-time.After(2 * time.Second):
		e.s.FailNow("no event within 2s")
		return sseEvent{}
	}
}

// drain reads events until the server closes the stream.
func (e *eventStream) drain() []sseEvent {
	var events []sseEvent
	for {
		select {
		case event, ok := <-e.events:
			if !ok {
				return append(e.buffered, events...)
			}
			events = append(events, event)
		case <-time.After(5 * time.Second):
			e.s.FailNow("the stream didn't close within 5s")
			return nil
		}
	}
}

// closed asserts the server ends the stream.
func (e *eventStream) closed() {
	e.s.Empty(e.drain())
}

func (e *eventStream) close() {
	e.cancel()
}

func id(n uint) string {
	return strconv.FormatUint(uint64(n), 10)
}
