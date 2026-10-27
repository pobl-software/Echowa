package feature

import (
	"context"
	"fmt"
	"net"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type ExternalAccessTestSuite struct {
	suite.Suite
	tests.TestCase
	docker     *docker.Fake
	dispatched []uint
}

func TestExternalAccessTestSuite(t *testing.T) {
	suite.Run(t, new(ExternalAccessTestSuite))
}

func (s *ExternalAccessTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.docker = s.FakeDocker()
	s.dispatched = nil
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, _ time.Duration) error {
		s.dispatched = append(s.dispatched, appID)
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

// deployedDatabase is an app made from the template slug whose first
// deploy succeeded.
func (s *ExternalAccessTestSuite) deployedDatabase(slug, name string) models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	_, err = services.CreateDeployment(context.Background(), app, models.TriggerManual, app.DefaultTag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	return s.reload(app.ID)
}

func (s *ExternalAccessTestSuite) reload(id uint) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&app))

	return app
}

func (s *ExternalAccessTestSuite) lastSpec() docker.ServiceSpec {
	deploys := s.docker.Deploys()
	s.Require().NotEmpty(deploys)

	return deploys[len(deploys)-1]
}

func (s *ExternalAccessTestSuite) TestEnablingPublishesThePortAndRedeploysTheRunningImage() {
	app := s.deployedDatabase("postgres", "db")
	s.Nil(s.lastSpec().HostPort, "nothing is published until asked")
	var running models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("id", *app.CurrentDeploymentID).FirstOrFail(&running))

	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(page)
	resp := b.submit(nethttp.MethodPost, page+"/external-access", page, map[string]any{"external_port": 47040})

	app = s.reload(app.ID)
	s.Require().NotNil(app.ExternalPort)
	s.Equal(47040, *app.ExternalPort)
	var queued models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).Order("id desc").FirstOrFail(&queued))
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, queued.ID))
	_, digest, _ := strings.Cut(*running.Digest, "@")
	s.Equal(digest, queued.Ref, "the running image, not a newer tag")

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal(&docker.HostPort{Published: 47040, Target: 5432}, s.lastSpec().HostPort)
	postgres, _ := templates.Find("postgres")
	s.Equal(postgres.Args(templates.Options{TLS: true}), s.lastSpec().Args, "the server starts with TLS on")

	b.submit(nethttp.MethodDelete, page+"/external-access", page, nil)
	s.Nil(s.reload(app.ID).ExternalPort)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Nil(s.lastSpec().HostPort, "turning it off unpublishes the port")
	s.Nil(s.lastSpec().Args, "and the server starts as before")
}

func (s *ExternalAccessTestSuite) TestEnablingIssuesACertificateOnceAndOffersTheCA() {
	app := s.deployedDatabase("redis", "cache")
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(page)
	b.request(nethttp.MethodGet, page+"/external-access/ca.crt", nil, false, nil).AssertNotFound()

	b.submit(nethttp.MethodPost, page+"/external-access", page, map[string]any{"external_port": 46379})
	env, err := services.EnvVars(context.Background(), app.ID)
	s.Require().NoError(err)
	ca, key := env[templates.EnvTLSCA], env[templates.EnvTLSKey]
	s.Contains(ca, "BEGIN CERTIFICATE")
	s.Contains(env[templates.EnvTLSCert], "BEGIN CERTIFICATE")
	s.Contains(key, "BEGIN PRIVATE KEY")

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	spec := s.lastSpec()
	s.Equal(&docker.HostPort{Published: 46379, Target: 6380}, spec.HostPort, "Redis publishes its TLS-only port")
	s.Equal(key, spec.Env[templates.EnvTLSKey])

	resp := b.request(nethttp.MethodGet, page+"/external-access/ca.crt", nil, false, nil)
	resp.AssertOk()
	resp.AssertHeader("Content-Disposition", `attachment; filename="cache-ca.crt"`)
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal(ca, body)

	// The key never reaches a page.
	b.forbid(key)
	b.visit(page)
	b.visit(page + "?tab=environment")

	// Off and on again keeps the certificate, so a client's CA stays good.
	b.submit(nethttp.MethodDelete, page+"/external-access", page, nil)
	b.visit(page)
	b.submit(nethttp.MethodPost, page+"/external-access", page, map[string]any{"external_port": 46380})
	env, err = services.EnvVars(context.Background(), app.ID)
	s.Require().NoError(err)
	s.Equal(ca, env[templates.EnvTLSCA])
}

func (s *ExternalAccessTestSuite) TestADeployWithoutTheKeyFailsAndSaysHowToFixIt() {
	app := s.deployedDatabase("postgres", "db")
	tmpl, _ := templates.Find("postgres")
	s.Require().NoError(services.EnableExternalAccess(context.Background(), &app, 47040, &tmpl))
	_, err := facades.Orm().Query().Where("app_id", app.ID).Where("key", templates.EnvTLSKey).Delete(&models.AppEnvVar{})
	s.Require().NoError(err)
	deploys := len(s.docker.Deploys())

	deployment, err := services.CreateDeployment(context.Background(), &app, models.TriggerManual, app.DefaultTag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	s.Require().NoError(facades.Orm().Query().Where("id", deployment.ID).FirstOrFail(deployment))
	s.Equal(models.DeploymentFailed, deployment.Status)
	s.Contains(*deployment.Error, "ECHOWA_TLS_KEY is missing from the environment")
	s.Len(s.docker.Deploys(), deploys, "nothing reached Swarm")
}

func (s *ExternalAccessTestSuite) TestRefusesPortsThatWouldClashOrInvite() {
	app := s.deployedDatabase("postgres", "db")
	other := s.deployedDatabase("postgres", "other")
	s.Require().NoError(services.SetExternalPort(context.Background(), &other, ptr(47041)))

	busy, err := net.Listen("tcp", ":0")
	s.Require().NoError(err)
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port

	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	for _, tt := range []struct {
		port  any
		error string
	}{
		{5432, "Pick a port other than 5432, the one PostgreSQL uses by default. Bots scan default ports first."},
		{22, "Pick a port from 1024 to 65535. Lower ones are for the system and Traefik."},
		{70000, ""},
		{"", ""},
		{47041, "Another app already listens on this port."},
		{busyPort, fmt.Sprintf("Something on this server already listens on port %d.", busyPort)},
	} {
		b.visit(page)
		b.submit(nethttp.MethodPost, page+"/external-access", page, map[string]any{"external_port": tt.port}).AssertHeader("Location", page)
		_, shown := b.visit(page)
		errors := shown.Props["errors"].(map[string]any)
		if tt.error == "" {
			s.Contains(errors, "external_port", "port %v", tt.port)
		} else {
			s.Equal(tt.error, errors["external_port"], "port %v", tt.port)
		}
	}

	s.Nil(s.reload(app.ID).ExternalPort)
	s.Len(s.dispatched, 0, "nothing deployed")
}

func (s *ExternalAccessTestSuite) TestOnlyTemplateDatabasesCanBeReachedFromOutside() {
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "blog", Repository: "ghcr.io/owner/blog", DefaultTag: "latest", Port: 3000, Replicas: 1,
	}, nil)
	s.Require().NoError(err)

	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(page)
	b.submit(nethttp.MethodPost, page+"/external-access", page, map[string]any{"external_port": 47040})
	_, shown := b.visit(page)

	s.Equal("Only databases made from a template can be reached from outside the server.", shown.Props["errors"].(map[string]any)["external_port"])
	s.Nil(s.reload(app.ID).ExternalPort)
}
