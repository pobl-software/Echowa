package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
)

// The Webhooks section (ADR 0047): the URL, the secret behind Reveal and
// Rotate, the CI steps and the refused calls.

func (s *HooksTestSuite) hooksURL(url string) {
	original := facades.Config().GetString("echowa.hooks_url")
	facades.Config().Add("echowa.hooks_url", url)
	s.T().Cleanup(func() { facades.Config().Add("echowa.hooks_url", original) })
}

func (s *HooksTestSuite) ciProp(page map[string]any) map[string]any {
	ci, ok := page["ci"].(map[string]any)
	s.Require().True(ok, "ci is %v", page["ci"])

	return ci
}

func (s *HooksTestSuite) TestTheCISectionNeverHasTheSecret() {
	app, secret := s.app("blog")
	s.hooksURL("https://echowa.example.com/")
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(secret)

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=ci", app.ID))

	ci := s.ciProp(page.Props)
	s.Equal("https://echowa.example.com/hooks/deploy/blog", ci["url"])
	s.Equal([]any{}, ci["calls"], "an empty list, not null")
	var ids []string
	for _, snippet := range ci["snippets"].([]any) {
		snippet := snippet.(map[string]any)
		ids = append(ids, snippet["id"].(string))
		code := snippet["code"].(string)
		s.Contains(code, `"https://echowa.example.com/hooks/deploy/blog"`, snippet["id"])
		s.Contains(code, `"blog"`, snippet["id"])
		s.Contains(code, "ECHOWA_SECRET", snippet["id"])
	}
	s.Equal([]string{"github", "gitlab", "shell"}, ids)
	postman := ci["postman"].(map[string]any)
	s.Equal("https://echowa.example.com/hooks/deploy/blog", postman["url"])
	s.Contains(postman["script"], "'.blog.'")
	s.Contains(postman["script"], "ECHOWA_SECRET")
}

func (s *HooksTestSuite) TestATemplateAppHasNoCISection() {
	db, _ := s.app("db")
	_, err := facades.Orm().Query().Model(db).Update("template", "postgres")
	s.Require().NoError(err)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(fmt.Sprintf("/apps/%d", db.ID))

	s.Nil(page.Props["ci"])
	headers := map[string]string{"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie), "Accept": "application/json"}
	b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/webhook/reveal", db.ID), nil, false, headers).AssertNotFound()
	b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/webhook/rotate", db.ID), nil, false, headers).AssertNotFound()
}

func (s *HooksTestSuite) TestRevealAnswersTheSecretUncached() {
	app, secret := s.app("blog")
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d", app.ID))
	url := fmt.Sprintf("/apps/%d/webhook/reveal", app.ID)

	resp := b.request(nethttp.MethodPost, url, nil, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})

	resp.AssertOk()
	resp.AssertHeader("Cache-Control", "no-store")
	resp.AssertJson(map[string]any{"secret": secret})
	b.request(nethttp.MethodPost, url, nil, false, nil).AssertStatus(419)

	guest := newBrowser(s.T(), &s.TestCase)
	guest.visit("/login")
	guest.request(nethttp.MethodPost, url, nil, false, map[string]string{
		"X-XSRF-TOKEN": guest.cookieValue(middlewareXsrfCookie),
	}).AssertHeader("Location", "/login")
}

func (s *HooksTestSuite) TestRotateRetiresTheOldSecret() {
	app, old := s.app("blog")
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(old)
	showURL := fmt.Sprintf("/apps/%d?tab=ci", app.ID)
	b.visit(showURL)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/webhook/rotate", app.ID), showURL, nil).AssertHeader("Location", showURL)

	var rotated models.App
	s.Require().NoError(facades.Orm().Query().Find(&rotated, app.ID))
	secret := string(rotated.WebhookSecret)
	s.NotEmpty(secret)
	s.NotEqual(old, secret)
	b.forbid(secret)
	_, page := b.visit(showURL)
	s.Equal("New webhook secret for blog. Update ECHOWA_SECRET in your CI.", page.Flash["success"])

	resp := s.send(hook{app: "blog", secret: old, body: `{"ref":"v1"}`})
	resp.AssertStatus(401)
	s.send(hook{app: "blog", secret: secret, body: `{"ref":"v1"}`}).AssertStatus(202)
	s.Len(s.deployments(app.ID), 1)
}

func (s *HooksTestSuite) TestRefusedCallsAreKeptForTheirApp() {
	app, secret := s.app("blog")
	db, dbSecret := s.app("db")
	_, err := facades.Orm().Query().Model(db).Update("template", "postgres")
	s.Require().NoError(err)

	s.send(hook{app: "blog", secret: "wrong", body: `{"ref":"v1"}`, ip: "198.51.100.7"}).AssertStatus(401)
	s.send(hook{app: "blog", secret: secret, body: `{"ref":"not a ref"}`}).AssertStatus(422)
	s.send(hook{app: "nope", secret: secret}).AssertStatus(401)
	s.send(hook{app: "db", secret: dbSecret}).AssertStatus(401)
	s.send(hook{app: "blog", secret: secret, body: `{"ref":"v1"}`}).AssertStatus(202)

	calls := services.HookCalls(app.ID)
	s.Require().Len(calls, 2, "only refusals, and only blog's")
	s.Equal(services.HookRefusedRef, calls[0].Reason, "newest first")
	s.Equal("ref must be an image tag or a sha256: digest", calls[0].Message)
	s.Equal("203.0.113.10", calls[0].IP)
	s.Equal(services.HookRefusedSignature, calls[1].Reason)
	s.Equal("198.51.100.7", calls[1].IP)
	s.Empty(services.HookCalls(db.ID), "a template app keeps none")

	_, page := signedIn(s.T(), &s.TestCase).visit(fmt.Sprintf("/apps/%d?tab=ci", app.ID))
	shown := s.ciProp(page.Props)["calls"].([]any)
	s.Require().Len(shown, 2)
	s.Equal("ref", shown[0].(map[string]any)["reason"])
	s.NotEmpty(shown[0].(map[string]any)["at"])

	for i := range 12 {
		s.send(hook{app: "blog", secret: secret, body: fmt.Sprintf(`{"ref":"bad ref %d"}`, i)})
	}
	calls = services.HookCalls(app.ID)
	s.Len(calls, 10)
	s.Equal(`ref must be an image tag or a sha256: digest`, calls[0].Message)

	s.Require().NoError(services.DeleteApp(context.Background(), app))
	s.Empty(services.HookCalls(app.ID), "a new app given its ID starts with none")
}

// The steps the CI section shows, run as CI would run them: each through
// sh with the system's OpenSSL and curl, against a real server.
func (s *HooksTestSuite) TestTheCIStepsDeploy() {
	for _, tool := range []string{"curl", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			s.T().Skipf("no %s to run the steps with", tool)
		}
	}
	app, secret := s.app("blog")
	srv := httptest.NewServer(facades.Route())
	defer srv.Close()
	s.hooksURL(srv.URL)

	scripts := map[string]struct{ script, ref string }{}
	for _, snippet := range services.HookSnippets(app) {
		switch snippet.ID {
		case "github":
			var steps []struct {
				Env map[string]string `yaml:"env"`
				Run string            `yaml:"run"`
			}
			s.Require().NoError(yaml.Unmarshal([]byte(snippet.Code), &steps), snippet.Code)
			s.Require().Len(steps, 1)
			s.Equal(map[string]string{
				"ECHOWA_SECRET": "${{ secrets.ECHOWA_SECRET }}",
				"REF":           "${{ steps.build.outputs.digest }}",
			}, steps[0].Env)
			scripts["github"] = struct{ script, ref string }{steps[0].Run, testDigest}
		case "gitlab":
			var jobs map[string]struct {
				Variables map[string]string `yaml:"variables"`
				Script    []string          `yaml:"script"`
			}
			s.Require().NoError(yaml.Unmarshal([]byte(snippet.Code), &jobs), snippet.Code)
			job := jobs["deploy"]
			s.Equal(map[string]string{"REF": "$CI_COMMIT_SHORT_SHA"}, job.Variables)
			s.Require().Len(job.Script, 2)
			scripts["gitlab"] = struct{ script, ref string }{job.Script[1], "a1b2c3d"}
		case "shell":
			// It sets REF itself, to the default tag.
			scripts["shell"] = struct{ script, ref string }{snippet.Code, ""}
		}
	}
	s.Require().Len(scripts, 3)

	for id, run := range scripts {
		cmd := exec.Command("sh", "-c", run.script)
		cmd.Env = append(os.Environ(), "ECHOWA_SECRET="+secret, "REF="+run.ref)
		out, err := cmd.CombinedOutput()
		s.Require().NoError(err, "%s: %s", id, out)
		var answer struct {
			DeploymentID uint `json:"deployment_id"`
		}
		s.Require().NoError(json.Unmarshal(out, &answer), "%s: %s", id, out)
		var deployment models.Deployment
		s.Require().NoError(facades.Orm().Query().Find(&deployment, answer.DeploymentID))
		want := run.ref
		if want == "" {
			want = "latest"
		}
		s.Equal(want, deployment.Ref, id)
		s.Equal(models.TriggerWebhook, deployment.Trigger, id)
	}
	s.Len(s.deployments(app.ID), 3)
	s.Empty(services.HookCalls(app.ID))
}

// A step that didn't deploy must fail CI, and say why. -f alone passes a
// redirect, which is what Cloudflare Access answers on a UI hostname.
func (s *HooksTestSuite) TestTheCIStepFailsLoudly() {
	for _, tool := range []string{"curl", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			s.T().Skipf("no %s to run the steps with", tool)
		}
	}
	app, secret := s.app("blog")
	run := func(url, secret string) (string, error) {
		s.hooksURL(url)
		var shell string
		for _, snippet := range services.HookSnippets(app) {
			if snippet.ID == "shell" {
				shell = snippet.Code
			}
		}
		cmd := exec.Command("sh", "-c", shell)
		cmd.Env = append(os.Environ(), "ECHOWA_SECRET="+secret)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	access := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, "https://example.cloudflareaccess.com/cdn-cgi/access/login", nethttp.StatusFound)
	}))
	defer access.Close()
	out, err := run(access.URL, secret)
	var exit *exec.ExitError
	s.Require().ErrorAs(err, &exit, out)
	s.Equal(47, exit.ExitCode(), "curl's too many redirects: %s", out)

	srv := httptest.NewServer(facades.Route())
	defer srv.Close()
	out, err = run(srv.URL, "wrong")
	s.Require().ErrorAs(err, &exit, out)
	s.Equal(22, exit.ExitCode(), out)
	s.Contains(out, `{"error":"invalid signature"}`, "the refusal's message reaches CI's log")
	s.Empty(s.deployments(app.ID))
}

// The Postman request, sent as Postman would: its pre-request script run
// under Node with pm and crypto-js stood in for, then its headers filled
// in from the variables the script set.
func (s *HooksTestSuite) TestThePostmanRequestDeploys() {
	if _, err := exec.LookPath("node"); err != nil {
		s.T().Skip("no node to run the script with")
	}
	app, secret := s.app("blog")
	srv := httptest.NewServer(facades.Route())
	defer srv.Close()
	s.hooksURL(srv.URL)
	postman := services.HookPostmanRequest(app)

	cmd := exec.Command("node", "-e", `
const crypto = require('node:crypto');
const vars = { ECHOWA_SECRET: process.env.ECHOWA_SECRET };
const pm = {
	request: { body: { raw: process.env.BODY } },
	variables: { get: (k) => vars[k], set: (k, v) => { vars[k] = v; } },
};
const cryptoJS = { HmacSHA256: (m, k) => ({ toString: () => crypto.createHmac('sha256', k).update(m).digest('hex') }) };
new Function('pm', 'require', process.env.SCRIPT)(pm, (name) => {
	if (name !== 'crypto-js') throw new Error('Postman has no ' + name);
	return cryptoJS;
});
delete vars.ECHOWA_SECRET;
console.log(JSON.stringify(vars));
`)
	cmd.Env = append(os.Environ(), "ECHOWA_SECRET="+secret, "BODY="+postman.Body, "SCRIPT="+postman.Script)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, "%s", out)
	var vars map[string]string
	s.Require().NoError(json.Unmarshal(out, &vars), "%s", out)

	send := func() (int, string) {
		req, err := nethttp.NewRequest(nethttp.MethodPost, postman.URL, strings.NewReader(postman.Body))
		s.Require().NoError(err)
		for _, h := range postman.Headers {
			value := h.Value
			for name, v := range vars {
				value = strings.ReplaceAll(value, "{{"+name+"}}", v)
			}
			s.NotContains(value, "{{", "%s is filled in", h.Name)
			req.Header.Set(h.Name, value)
		}
		resp, err := nethttp.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	status, body := send()
	s.Equal(nethttp.StatusAccepted, status, body)
	deployments := s.deployments(app.ID)
	s.Require().Len(deployments, 1)
	s.Equal("latest", deployments[0].Ref)
	s.Equal(models.TriggerWebhook, deployments[0].Trigger)
}
