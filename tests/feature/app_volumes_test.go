package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
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

type AppVolumesTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestAppVolumesTestSuite(t *testing.T) {
	suite.Run(t, new(AppVolumesTestSuite))
}

func (s *AppVolumesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.docker = s.FakeDocker()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

// deployedApp is a custom app of replicas tasks whose first deploy
// succeeded.
func (s *AppVolumesTestSuite) deployedApp(name string, replicas int) models.App {
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: "ghcr.io/owner/" + name, DefaultTag: "latest", Port: 8080, Replicas: replicas,
	}, nil)
	s.Require().NoError(err)
	_, err = deploy.Request(context.Background(), app, models.TriggerManual, "v1")
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	return s.reload(app.ID)
}

func (s *AppVolumesTestSuite) reload(id uint) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().With("Volumes").Where("id", id).FirstOrFail(&app))

	return app
}

func (s *AppVolumesTestSuite) lastDeployment(appID uint) models.Deployment {
	var d models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", appID).Order("id desc").First(&d))

	return d
}

func (s *AppVolumesTestSuite) deploymentCount(appID uint) int64 {
	n, err := facades.Orm().Query().Model(&models.Deployment{}).Where("app_id", appID).Count()
	s.Require().NoError(err)

	return n
}

func (s *AppVolumesTestSuite) lastSpec() docker.ServiceSpec {
	deploys := s.docker.Deploys()
	s.Require().NotEmpty(deploys)

	return deploys[len(deploys)-1]
}

func (s *AppVolumesTestSuite) errors(b *browser, url string) map[string]any {
	_, page := b.visit(url)
	errs, _ := page.Props["errors"].(map[string]any)

	return errs
}

func (s *AppVolumesTestSuite) TestStoreMountsTheVolumeInADeployOfTheRunningImage() {
	app := s.deployedApp("blog", 3)
	running := s.lastDeployment(app.ID)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	resp := b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/volumes", app.ID), url, map[string]any{"name": "storage", "target": "/app/storage"})

	queued := s.lastDeployment(app.ID)
	s.Greater(queued.ID, running.ID)
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, queued.ID))
	s.Equal(models.DeploymentQueued, queued.Status)
	s.Equal(*running.Digest, "ghcr.io/owner/blog@"+queued.Ref, "the image it runs now, not the default tag")
	reloaded := s.reload(app.ID)
	s.Equal(1, reloaded.Replicas, "a volume has one writer")
	s.Require().Len(reloaded.Volumes, 1)
	s.Equal(templates.VolumeName("blog", "storage"), reloaded.Volumes[0].Name)
	s.Equal("echowa-blog-storage", reloaded.Volumes[0].Name)
	s.Equal("/app/storage", reloaded.Volumes[0].Target)
	s.False(reloaded.Volumes[0].Removing())

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	spec := s.lastSpec()
	s.Equal([]docker.Volume{{Name: "echowa-blog-storage", Target: "/app/storage"}}, spec.Volumes)
	s.Equal(uint64(1), spec.Replicas)
}

func (s *AppVolumesTestSuite) TestStoreOnAnAppThatWasNeverDeployedWaitsForItsFirstDeploy() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/volumes", app.ID), url, map[string]any{"name": "data", "target": "/data"}).
		AssertHeader("Location", url)

	_, page := b.visit(url)
	s.Contains(page.Flash["success"], "It applies on the first deploy.")
	s.Zero(s.deploymentCount(app.ID))
	s.Len(s.reload(app.ID).Volumes, 1)
}

func (s *AppVolumesTestSuite) TestAStoppedAppSavesTheChangeAndQueuesNothing() {
	app := s.deployedApp("blog", 1)
	s.Require().NoError(services.StopApp(context.Background(), &app))
	before := s.deploymentCount(app.ID)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/volumes", app.ID), url, map[string]any{"name": "data", "target": "/data"})

	s.Len(s.reload(app.ID).Volumes, 1)
	s.Equal(before, s.deploymentCount(app.ID), "a deployment would start it")
	_, page := b.visit(url)
	s.Contains(page.Flash["success"], "It applies when you start blog.")
}

func (s *AppVolumesTestSuite) TestStoreRefusesBadInput() {
	app := s.deployedApp("blog", 1)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	post := fmt.Sprintf("/apps/%d/volumes", app.ID)
	b.visit(url)

	for name, tc := range map[string]struct {
		form  map[string]any
		field string
		want  string
	}{
		"a hyphen in the name":   {map[string]any{"name": "my-data", "target": "/data"}, "name", "Use lowercase letters, digits and underscores"},
		"a name with capitals":   {map[string]any{"name": "Data", "target": "/data"}, "name", "Use lowercase letters, digits and underscores"},
		"no name":                {map[string]any{"name": "", "target": "/data"}, "name", "Give the volume a name."},
		"a relative path":        {map[string]any{"name": "data", "target": "data"}, "target", "Use an absolute path"},
		"a path with a dot dot":  {map[string]any{"name": "data", "target": "/app/../etc"}, "target", "Use an absolute path"},
		"a trailing slash":       {map[string]any{"name": "data", "target": "/data/"}, "target", "Use an absolute path"},
		"the root":               {map[string]any{"name": "data", "target": "/"}, "target", "Use an absolute path"},
		"a path under /proc":     {map[string]any{"name": "data", "target": "/proc/1"}, "target", "Use an absolute path"},
		"no path":                {map[string]any{"name": "data", "target": ""}, "target", "Enter the path in the container."},
		"a path with whitespace": {map[string]any{"name": "data", "target": "/my data"}, "target", "Use an absolute path"},
	} {
		b.submit(nethttp.MethodPost, post, url, tc.form).AssertHeader("Location", url)
		s.Contains(s.errors(b, url)[tc.field], tc.want, name)
	}

	s.Empty(s.reload(app.ID).Volumes)
}

func (s *AppVolumesTestSuite) TestStoreTrimsTheInput() {
	app := s.deployedApp("blog", 1)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/volumes", app.ID), url, map[string]any{"name": " data ", "target": " /data "})

	s.Require().Len(s.reload(app.ID).Volumes, 1)
	s.Equal("/data", s.reload(app.ID).Volumes[0].Target)
}

func (s *AppVolumesTestSuite) TestStoreRefusesAPathOrNameTheAppAlreadyUses() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	post := fmt.Sprintf("/apps/%d/volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, post, url, map[string]any{"name": "other", "target": "/data"}).AssertHeader("Location", url)
	s.Equal("Another volume is already mounted at this path.", s.errors(b, url)["target"])

	b.submit(nethttp.MethodPost, post, url, map[string]any{"name": "data", "target": "/other"}).AssertHeader("Location", url)
	s.Equal("This app already has a volume with this name.", s.errors(b, url)["name"])

	s.Len(s.reload(app.ID).Volumes, 1)
}

func (s *AppVolumesTestSuite) TestStoreRefusesWhatARemovingVolumeStillHolds() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	s.Require().NoError(services.MarkVolumeRemoved(context.Background(), &s.reload(app.ID).Volumes[0]))
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/volumes", app.ID), url, map[string]any{"name": "data", "target": "/data"})

	errs := s.errors(b, url)
	s.Contains(errs["name"], "still being removed")
	s.Contains(errs["target"], "still being removed")
}

func (s *AppVolumesTestSuite) TestStoreRefusesALeftoverVolume() {
	app := s.deployedApp("blog", 1)
	s.docker.AddVolume("echowa-blog-data")
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)
	before := s.deploymentCount(app.ID)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/volumes", app.ID), url, map[string]any{"name": "data", "target": "/data"}).
		AssertHeader("Location", url)

	s.Contains(s.errors(b, url)["name"], "The volume echowa-blog-data already exists in Docker")
	s.Empty(s.reload(app.ID).Volumes)
	s.Equal(before, s.deploymentCount(app.ID))
}

func (s *AppVolumesTestSuite) TestTemplateAppsAnswer404() {
	tmpl, _ := templates.Find("postgres")
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	volume := s.reload(app.ID).Volumes[0]
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, url+"/volumes", url, map[string]any{"name": "more", "target": "/more"}).AssertNotFound()
	b.submit(nethttp.MethodPut, fmt.Sprintf("%s/volumes/%d", url, volume.ID), url, map[string]any{"target": "/elsewhere"}).AssertNotFound()
	b.submit(nethttp.MethodDelete, fmt.Sprintf("%s/volumes/%d", url, volume.ID), url, nil).AssertNotFound()

	reloaded := s.reload(app.ID)
	s.Require().Len(reloaded.Volumes, 1)
	s.Equal(volume.Target, reloaded.Volumes[0].Target)
	s.False(reloaded.Volumes[0].Removing())
}

func (s *AppVolumesTestSuite) TestAnotherAppsVolumeAnswers404() {
	mine := s.deployedApp("blog", 1)
	other := s.deployedApp("shop", 1)
	addTestVolume(s.T(), &other, "echowa-shop-data", "/data")
	theirs := s.reload(other.ID).Volumes[0]
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", mine.ID)
	b.visit(url)

	b.submit(nethttp.MethodPut, fmt.Sprintf("%s/volumes/%d", url, theirs.ID), url, map[string]any{"target": "/elsewhere"}).AssertNotFound()
	b.submit(nethttp.MethodDelete, fmt.Sprintf("%s/volumes/%d", url, theirs.ID), url, nil).AssertNotFound()

	unchanged := s.reload(other.ID).Volumes[0]
	s.Equal("/data", unchanged.Target)
	s.False(unchanged.Removing())
}

func (s *AppVolumesTestSuite) TestUpdateMovesTheVolumeAndKeepsItsName() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	volume := s.reload(app.ID).Volumes[0]
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	resp := b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d/volumes/%d", app.ID, volume.ID), url, map[string]any{
		"name": "renamed", "target": "/srv/data",
	})

	moved := s.reload(app.ID).Volumes[0]
	s.Equal("/srv/data", moved.Target)
	s.Equal("echowa-blog-data", moved.Name, "the name is the Docker volume")
	queued := s.lastDeployment(app.ID)
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, queued.ID))
	s.Equal(models.DeploymentQueued, queued.Status)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal([]docker.Volume{{Name: "echowa-blog-data", Target: "/srv/data"}}, s.lastSpec().Volumes)
}

func (s *AppVolumesTestSuite) TestUpdateRefusesBadAndTakenPaths() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	addTestVolume(s.T(), &app, "echowa-blog-logs", "/logs")
	volume := s.reload(app.ID).Volumes[0]
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	put := fmt.Sprintf("/apps/%d/volumes/%d", app.ID, volume.ID)
	b.visit(url)

	b.submit(nethttp.MethodPut, put, url, map[string]any{"target": "/logs"}).AssertHeader("Location", url)
	s.Equal("Another volume is already mounted at this path.", s.errors(b, url)["target"])
	b.submit(nethttp.MethodPut, put, url, map[string]any{"target": "logs"}).AssertHeader("Location", url)
	s.Contains(s.errors(b, url)["target"], "Use an absolute path")
	b.submit(nethttp.MethodPut, put, url, map[string]any{"target": "/data"}).AssertStatus(nethttp.StatusSeeOther)

	s.Equal("/data", s.reload(app.ID).Volumes[0].Target, "a volume can keep its own path")
}

func (s *AppVolumesTestSuite) TestDestroyMarksTheVolumeAndTheNextDeployRemovesIt() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	_, err := deploy.Request(context.Background(), &app, models.TriggerManual, "v1")
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	volume := s.reload(app.ID).Volumes[0]
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	resp := b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/volumes/%d", app.ID, volume.ID), url, nil)

	marked := s.reload(app.ID).Volumes
	s.Require().Len(marked, 1, "the row stays until a deploy removes the volume")
	s.True(marked[0].Removing())
	s.Empty(s.docker.RemovedVolumes(), "the running task still mounts it")
	queued := s.lastDeployment(app.ID)
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, queued.ID))
	s.Equal(models.DeploymentQueued, queued.Status)

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Empty(s.lastSpec().Volumes)
	s.Equal([]string{"echowa-blog-data"}, s.docker.RemovedVolumes())
	s.Empty(s.reload(app.ID).Volumes)
}

func (s *AppVolumesTestSuite) TestDestroyingARemovingVolumeAgainQueuesNothing() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	volume := s.reload(app.ID).Volumes[0]
	s.Require().NoError(services.MarkVolumeRemoved(context.Background(), &volume))
	before := s.deploymentCount(app.ID)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/volumes/%d", app.ID, volume.ID), url, nil).AssertHeader("Location", url)

	s.Equal(before, s.deploymentCount(app.ID))
	s.Equal(*s.reload(app.ID).Volumes[0].RemovedAt, *volume.RemovedAt, "the first removal's time stands")
}

func (s *AppVolumesTestSuite) TestARemovingVolumeCantBeMoved() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	volume := s.reload(app.ID).Volumes[0]
	s.Require().NoError(services.MarkVolumeRemoved(context.Background(), &volume))
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d/volumes/%d", app.ID, volume.ID), url, map[string]any{"target": "/elsewhere"}).
		AssertHeader("Location", url)

	s.Contains(s.errors(b, url)["target"], "is being removed")
	s.Equal("/data", s.reload(app.ID).Volumes[0].Target)
}

func (s *AppVolumesTestSuite) TestTheAppPageListsVolumesWithTheirState() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	addTestVolume(s.T(), &app, "echowa-blog-logs", "/logs")
	s.Require().NoError(services.MarkVolumeRemoved(context.Background(), &s.reload(app.ID).Volumes[1]))
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=volumes", app.ID))

	volumes := page.Props["app"].(map[string]any)["volumes"].([]any)
	s.Require().Len(volumes, 2)
	byName := map[string]map[string]any{}
	for _, v := range volumes {
		row := v.(map[string]any)
		byName[row["name"].(string)] = row
	}
	s.Nil(byName["echowa-blog-data"]["removed_at"])
	s.NotNil(byName["echowa-blog-logs"]["removed_at"])
}

func (s *AppVolumesTestSuite) TestDeletingTheAppRemovesEveryVolumeRemovingOnesToo() {
	app := s.deployedApp("blog", 1)
	addTestVolume(s.T(), &app, "echowa-blog-data", "/data")
	addTestVolume(s.T(), &app, "echowa-blog-logs", "/logs")
	s.Require().NoError(services.MarkVolumeRemoved(context.Background(), &s.reload(app.ID).Volumes[1]))
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodDelete, url, url, nil).AssertHeader("Location", "/dashboard")

	s.Eventually(func() bool { return len(s.docker.RemovedVolumes()) == 2 }, 5*time.Second, 50*time.Millisecond)
	s.ElementsMatch([]string{"echowa-blog-data", "echowa-blog-logs"}, s.docker.RemovedVolumes())
	n, err := facades.Orm().Query().Model(&models.AppVolume{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
}
