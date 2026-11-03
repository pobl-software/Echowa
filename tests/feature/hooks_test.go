package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/support/path"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type HooksTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestHooksTestSuite(t *testing.T) {
	suite.Run(t, new(HooksTestSuite))
}

const testDigest = "sha256:3f1c6a1e9b0d4c2f8e7a5b3d1c9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e"

func (s *HooksTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	s.Require().True(facades.Cache().Flush())
	services.ResetHookCalls()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	// The test client isn't on loopback, so trust it to name the client's
	// IP: the limits are per IP.
	header, proxies := facades.Config().GetString("echowa.client_ip_header"), facades.Config().GetString("echowa.trusted_proxies")
	facades.Config().Add("echowa.client_ip_header", "X-Test-Client-IP")
	facades.Config().Add("echowa.trusted_proxies", "0.0.0.0/0,::/0")
	s.T().Cleanup(func() {
		deploy.Dispatch = original
		facades.Config().Add("echowa.client_ip_header", header)
		facades.Config().Add("echowa.trusted_proxies", proxies)
		facades.Cache().Flush()
		// The poller's record is process-wide: leave it empty for other suites.
		_, err := facades.Orm().Query().Exec("DELETE FROM apps")
		s.Require().NoError(err)
		s.Require().NoError(services.PollAppStatus(context.Background(), time.Now()))
	})
}

// hook is a request as CI sends it.
type hook struct {
	app, secret, body, ip string
	// contentType is application/json unless set.
	contentType string
	// signedApp is the app name in the signed bytes, if not app.
	signedApp string
	at        time.Time
}

func (h hook) timestamp() string {
	at := h.at
	if at.IsZero() {
		at = time.Now()
	}

	return strconv.FormatInt(at.Unix(), 10)
}

func (h hook) signature(ts string) string {
	signed := h.signedApp
	if signed == "" {
		signed = h.app
	}

	return "sha256=" + services.HookSignature(h.secret, ts, signed, []byte(h.body))
}

func (s *HooksTestSuite) send(h hook) contractstesting.Response {
	ts := h.timestamp()

	return s.sendSigned(h, ts, h.signature(ts))
}

func (s *HooksTestSuite) sendSigned(h hook, ts, signature string) contractstesting.Response {
	ip := h.ip
	if ip == "" {
		ip = "203.0.113.10"
	}
	contentType := h.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	resp, err := s.Http(s.T()).WithHeaders(map[string]string{
		"Content-Type":       contentType,
		"X-Echowa-Timestamp": ts,
		"X-Echowa-Signature": signature,
		"X-Test-Client-IP":   ip,
	}).Post("/hooks/deploy/"+h.app, strings.NewReader(h.body))
	s.Require().NoError(err)

	return resp
}

func (s *HooksTestSuite) json(resp contractstesting.Response) map[string]any {
	content, err := resp.Content()
	s.Require().NoError(err)
	var out map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &out), content)

	return out
}

func (s *HooksTestSuite) deployments(appID uint) []models.Deployment {
	var ds []models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", appID).Order("id").Find(&ds))

	return ds
}

func (s *HooksTestSuite) app(name string) (*models.App, string) {
	app := createTestApp(s.T(), name, nil)

	return app, string(app.WebhookSecret)
}

func (s *HooksTestSuite) TestQueuesAWebhookDeploymentOfTheRef() {
	app, secret := s.app("blog")

	resp := s.send(hook{app: "blog", secret: secret, body: `{"ref":"` + testDigest + `"}`})

	resp.AssertStatus(202)
	ds := s.deployments(app.ID)
	s.Require().Len(ds, 1)
	s.Equal(models.TriggerWebhook, ds[0].Trigger)
	s.Equal(testDigest, ds[0].Ref)
	s.Equal(float64(ds[0].ID), s.json(resp)["deployment_id"])
}

func (s *HooksTestSuite) TestAnEmptyBodyDeploysTheDefaultTag() {
	app, secret := s.app("blog")

	s.send(hook{app: "blog", secret: secret}).AssertStatus(202)

	ds := s.deployments(app.ID)
	s.Require().Len(ds, 1)
	s.Equal("latest", ds[0].Ref)
}

func (s *HooksTestSuite) TestEveryWrongSignatureLooksTheSame() {
	_, secret := s.app("blog")
	other, otherSecret := s.app("shop")
	db, dbSecret := s.app("db")
	_, err := facades.Orm().Query().Model(db).Update("template", "postgres")
	s.Require().NoError(err)
	body := `{"ref":"v1"}`

	cases := map[string]hook{
		"wrong secret":            {app: "blog", secret: otherSecret, body: body},
		"another app in the sign": {app: "blog", signedApp: "shop", secret: secret, body: body},
		"unknown app":             {app: "nope", secret: secret, body: body},
		"template app":            {app: "db", secret: dbSecret, body: body},
	}
	for name, h := range cases {
		resp := s.send(h)
		resp.AssertStatus(401)
		s.Equal(map[string]any{"error": "invalid signature"}, s.json(resp), name)
	}
	s.Empty(s.deployments(other.ID))
	s.Empty(s.deployments(db.ID))
}

// An empty secret is a key anyone can sign with. Nothing writes one, but
// a hook must never be the way to find out.
func (s *HooksTestSuite) TestAnAppWithAnEmptySecretIsRefused() {
	app, _ := s.app("blog")
	_, err := facades.Orm().Query().Model(app).Update("webhook_secret", models.Secret(""))
	s.Require().NoError(err)

	resp := s.send(hook{app: "blog", secret: ""})

	resp.AssertUnauthorized()
	s.Equal("invalid signature", s.json(resp)["error"])
	s.Empty(s.deployments(app.ID))
}

func (s *HooksTestSuite) TestAClockOffByMoreThanFiveMinutesSaysSo() {
	_, secret := s.app("blog")

	for _, off := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		resp := s.send(hook{app: "blog", secret: secret, at: time.Now().Add(off)})
		resp.AssertStatus(401)
		s.Contains(s.json(resp)["error"], "clock")
	}
	// Before the app is looked up, so it says nothing about which exist.
	resp := s.send(hook{app: "nope", secret: secret, at: time.Now().Add(-6 * time.Minute)})
	s.Contains(s.json(resp)["error"], "clock")
}

func (s *HooksTestSuite) TestTheSameRequestTwiceIsOneDeployment() {
	app, secret := s.app("blog")
	h := hook{app: "blog", secret: secret, body: `{"ref":"v2"}`}
	ts := h.timestamp()

	sig := h.signature(ts)

	first := s.sendSigned(h, ts, sig)
	again := s.sendSigned(h, ts, sig)
	upper := s.sendSigned(h, ts, "sha256="+strings.ToUpper(strings.TrimPrefix(sig, "sha256=")))

	first.AssertStatus(202)
	again.AssertStatus(200)
	upper.AssertStatus(200)
	s.Len(s.deployments(app.ID), 1)
	s.Equal(s.json(first)["deployment_id"], s.json(again)["deployment_id"])
	s.Equal(s.json(first)["deployment_id"], s.json(upper)["deployment_id"])
}

func (s *HooksTestSuite) TestMalformedHeadersAreABadRequest() {
	_, secret := s.app("blog")
	h := hook{app: "blog", secret: secret}
	ts := h.timestamp()
	sig := h.signature(ts)

	for name, headers := range map[string][2]string{
		"no timestamp":      {"", sig},
		"signed timestamp":  {"+" + ts, sig},
		"no signature":      {ts, ""},
		"no sha256= prefix": {ts, strings.TrimPrefix(sig, "sha256=")},
		"short signature":   {ts, sig[:len(sig)-2]},
		"not hex":           {ts, sig[:len(sig)-2] + "zz"},
		"another algorithm": {ts, "sha1=" + strings.TrimPrefix(sig, "sha256=")},
	} {
		s.Run(name, func() {
			s.sendSigned(h, headers[0], headers[1]).AssertStatus(400)
		})
	}
}

func (s *HooksTestSuite) TestABodyThatIsntTheRefIsABadRequest() {
	app, secret := s.app("blog")

	for _, body := range []string{`not json`, `{"ref":"v1","image":"evil/app"}`, `{"ref":"v1"}{"ref":"v2"}`, `["v1"]`} {
		s.Run(body, func() {
			s.send(hook{app: "blog", secret: secret, body: body}).AssertStatus(400)
		})
	}
	// curl --data without -H sends a form.
	resp := s.send(hook{app: "blog", secret: secret, body: `{"ref":"v1"}`, contentType: "application/x-www-form-urlencoded"})
	resp.AssertStatus(400)
	s.Contains(s.json(resp)["error"], "application/json")
	s.Empty(s.deployments(app.ID))
}

func (s *HooksTestSuite) TestABodyOver8KiBIsRefused() {
	app, secret := s.app("blog")
	body := `{"ref":"v1","pad":"` + strings.Repeat("x", 9<<10) + `"}`

	s.send(hook{app: "blog", secret: secret, body: body}).AssertStatus(413)
	s.Empty(s.deployments(app.ID))
}

func (s *HooksTestSuite) TestARefWithARepositoryIsUnprocessable() {
	app, secret := s.app("blog")

	resp := s.send(hook{app: "blog", secret: secret, body: `{"ref":"evil/app:v1"}`})

	resp.AssertStatus(422)
	s.Contains(s.json(resp)["error"], "tag or a sha256: digest")
	s.Empty(s.deployments(app.ID))
}

func (s *HooksTestSuite) TestAStoppedAppIsAConflict() {
	app, secret := s.app("blog")
	s.docker.Status = func(name string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{Name: name}, nil
	}
	s.Require().NoError(services.PollAppStatus(context.Background(), time.Now()))

	resp := s.send(hook{app: "blog", secret: secret})

	resp.AssertStatus(409)
	s.Equal("app is stopped; start it from Echowa", s.json(resp)["error"])
	s.Empty(s.deployments(app.ID))
}

// clockAt pins the server's clock 10 seconds into a window, so a test's
// requests never straddle two.
func (s *HooksTestSuite) clockAt(window time.Duration) time.Time {
	now := time.Now().Truncate(window).Add(10 * time.Second)
	carbon.SetTestNow(carbon.FromStdTime(now))
	s.T().Cleanup(carbon.ClearTestNow)

	return now
}

func (s *HooksTestSuite) TestTheEleventhFailureFromAnIPIsRateLimited() {
	_, secret := s.app("blog")
	now := s.clockAt(services.HookFailureWindow)
	bad := hook{app: "blog", secret: "wrong", ip: "198.51.100.7", at: now}

	for range services.HookFailureLimit {
		s.send(bad).AssertStatus(401)
	}
	limited := s.send(bad)
	limited.AssertStatus(429)
	limited.AssertHeader("Retry-After", "590")
	// Even with the right secret, before any HMAC work.
	s.send(hook{app: "blog", secret: secret, ip: "198.51.100.7", at: now}).AssertStatus(429)

	bad.ip = "198.51.100.8"
	s.send(bad).AssertStatus(401)
}

func (s *HooksTestSuite) TestAcceptedDeploysAreLimitedPerApp() {
	app, secret := s.app("blog")
	now := s.clockAt(services.HookDeployWindow)

	for i := range services.HookDeployLimit {
		s.send(hook{app: "blog", secret: secret, at: now, body: fmt.Sprintf(`{"ref":"v%d"}`, i)}).AssertStatus(202)
	}
	resp := s.send(hook{app: "blog", secret: secret, at: now, body: `{"ref":"v99"}`})

	resp.AssertStatus(429)
	resp.AssertHeader("Retry-After", "50")
	s.Len(s.deployments(app.ID), services.HookDeployLimit)
}

func (s *HooksTestSuite) TestParseHookBody() {
	for body, want := range map[string]string{"": "", " \n": "", `{"ref":"v1"}`: "v1", `{"ref": "v1"} `: "v1", `{}`: "", `null`: ""} {
		ref, err := services.ParseHookBody([]byte(body))
		s.NoError(err, body)
		s.Equal(want, ref, body)
	}
	for _, body := range []string{`not json`, `["v1"]`, `{"ref":"v1"}{"ref":"v2"}`, `{"ref":"v1"} x`, `{"ref":"v1","image":"evil/app"}`, `{"ref":1}`} {
		_, err := services.ParseHookBody([]byte(body))
		s.Error(err, body)
	}
}

func (s *HooksTestSuite) TestTheSecretNeverReachesAResponseOrTheLog() {
	name := fmt.Sprintf("hooklog%d", time.Now().UnixNano())
	_, secret := s.app(name)
	h := hook{app: name, secret: secret, body: `{"ref":"v1"}`}
	ts := h.timestamp()
	sig := h.signature(ts)
	logs := logsSince(s.T())

	responses := []contractstesting.Response{
		s.sendSigned(h, ts, sig),
		s.sendSigned(h, ts, sig),
		s.send(hook{app: name, secret: "wrong"}),
		s.send(hook{app: name, secret: secret, body: `{"ref":"bad ref"}`}),
	}
	for _, resp := range responses {
		content, err := resp.Content()
		s.Require().NoError(err)
		s.NotContains(content, secret)
		s.NotContains(content, strings.TrimPrefix(sig, "sha256="))
	}

	logged := logs()
	s.Contains(logged, name, "the hook logs its calls")
	s.NotContains(logged, secret)
	s.NotContains(logged, strings.TrimPrefix(sig, "sha256="))
	s.NotContains(logged, `"ref":"v1"`)
}

// logsSince returns a function that reads what was logged after it was
// made, so a test doesn't read every earlier run's logs.
func logsSince(t *testing.T) func() string {
	t.Helper()
	sizes := map[string]int64{}
	files, err := filepath.Glob(path.Storage("logs", "*.log"))
	require.NoError(t, err)
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			sizes[f] = info.Size()
		}
	}

	return func() string {
		files, err := filepath.Glob(path.Storage("logs", "*.log"))
		require.NoError(t, err)
		var all strings.Builder
		for _, f := range files {
			file, err := os.Open(f)
			require.NoError(t, err)
			_, err = file.Seek(sizes[f], io.SeekStart)
			require.NoError(t, err)
			_, err = io.Copy(&all, file)
			require.NoError(t, err)
			require.NoError(t, file.Close())
		}

		return all.String()
	}
}

func (s *HooksTestSuite) TestVerifyDeployHook() {
	app, secret := s.app("blog")
	now := time.Unix(1793577600, 0)
	ts := "1793577600"
	body := []byte(`{"ref":"v1"}`)
	sig := "sha256=" + services.HookSignature(secret, ts, "blog", body)
	ctx := context.Background()

	got, err := services.VerifyDeployHook(ctx, "blog", ts, sig, body, now)
	s.Require().NoError(err)
	s.Equal(app.ID, got.ID)

	for _, at := range []time.Time{now.Add(services.HookSkew), now.Add(-services.HookSkew)} {
		_, err = services.VerifyDeployHook(ctx, "blog", ts, sig, body, at)
		s.NoError(err, "exactly 5 minutes off is still good")
	}
	for _, at := range []time.Time{now.Add(services.HookSkew + time.Second), now.Add(-services.HookSkew - time.Second)} {
		_, err = services.VerifyDeployHook(ctx, "blog", ts, sig, body, at)
		s.ErrorIs(err, services.ErrHookClock)
	}
	_, err = services.VerifyDeployHook(ctx, "blog", ts, sig, []byte(`{"ref":"v2"}`), now)
	s.ErrorIs(err, services.ErrHookSignature, "the body is signed")
	_, err = services.VerifyDeployHook(ctx, "blog", "1793577601", sig, body, now)
	s.ErrorIs(err, services.ErrHookSignature, "the timestamp is signed")
	_, err = services.VerifyDeployHook(ctx, "blog", ts, "sha256="+strings.Repeat("0", 64), body, now)
	s.ErrorIs(err, services.ErrHookSignature)
	_, err = services.VerifyDeployHook(ctx, "blog", "1793577600.0", sig, body, now)
	s.ErrorIs(err, services.ErrHookMalformed)
}

// The client side in deploy/README.md, run as written with each OpenSSL
// this machine has: OpenSSL 3 and 4 and LibreSSL print dgst differently,
// and CI images ship all of them.
const hookClientSnippet = `ts=$(date +%s)
body="{\"ref\":\"$DIGEST\"}"
sig=$(printf '%s.%s.%s' "$ts" "$APP" "$body" | openssl dgst -sha256 -hmac "$ECHOWA_SECRET" -r | cut -d' ' -f1)
printf '%s\n%s\n%s' "$ts" "$body" "$sig"`

func (s *HooksTestSuite) TestTheShellSnippetSigns() {
	app, secret := s.app("blog")
	candidates := []string{"/usr/bin/openssl", "/opt/homebrew/opt/openssl@3/bin/openssl", "/opt/homebrew/opt/openssl@4/bin/openssl", "/usr/local/opt/openssl@3/bin/openssl"}
	if p, err := exec.LookPath("openssl"); err == nil {
		candidates = append(candidates, p)
	}
	versions := map[string]bool{}
	for _, bin := range candidates {
		version, err := exec.Command(bin, "version").Output()
		if err != nil || versions[string(version)] {
			continue
		}
		versions[string(version)] = true
		s.Run(strings.TrimSpace(string(version)), func() {
			dir := s.T().TempDir()
			s.Require().NoError(os.Symlink(bin, filepath.Join(dir, "openssl")))
			cmd := exec.Command("sh", "-c", hookClientSnippet)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "DIGEST="+testDigest, "APP=blog", "ECHOWA_SECRET="+secret)
			out, err := cmd.Output()
			s.Require().NoError(err)
			parts := strings.SplitN(string(out), "\n", 3)
			s.Require().Len(parts, 3)
			s.Equal(services.HookSignature(secret, parts[0], "blog", []byte(parts[1])), parts[2])
			// Another OpenSSL in the same second signs the same bytes.
			s.Require().True(facades.Cache().Flush())

			s.sendSigned(hook{app: "blog", body: parts[1]}, parts[0], "sha256="+parts[2]).AssertStatus(202)
		})
	}
	s.Require().NotEmpty(versions, "no openssl to run the snippet with")
	s.Len(s.deployments(app.ID), len(versions))
}
