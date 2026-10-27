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

type AppDatabasesTestSuite struct {
	suite.Suite
	tests.TestCase
	docker     *docker.Fake
	dispatched []uint
}

func TestAppDatabasesTestSuite(t *testing.T) {
	suite.Run(t, new(AppDatabasesTestSuite))
}

func (s *AppDatabasesTestSuite) SetupTest() {
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

func (s *AppDatabasesTestSuite) createDatabase(slug, name string) *models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.TemplateInput(tmpl, name), &tmpl)
	s.Require().NoError(err)

	return app
}

func (s *AppDatabasesTestSuite) appNamed(name string) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().With("Volumes").Where("name", name).FirstOrFail(&app))

	return app
}

func (s *AppDatabasesTestSuite) linkedNames(b *browser, app *models.App) []string {
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=databases", app.ID))
	names := []string{}
	for _, row := range page.Props["linkedDatabases"].([]any) {
		names = append(names, row.(map[string]any)["name"].(string))
	}

	return names
}

func (s *AppDatabasesTestSuite) countApps() int64 {
	n, err := facades.Orm().Query().Model(&models.App{}).Count()
	s.Require().NoError(err)

	return n
}

func (s *AppDatabasesTestSuite) TestAddCreatesEachDatabaseLinksItAndDeploysIt() {
	shop := createTestApp(s.T(), "shop", nil)
	tab := fmt.Sprintf("/apps/%d?tab=databases", shop.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(tab)

	resp := b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/databases", shop.ID), tab, map[string]any{
		"databases": []map[string]any{{"template": "postgres", "name": " Shop-Postgres "}, {"template": "redis", "name": "shop-redis"}},
		"link":      []uint{},
	})

	resp.AssertHeader("Location", tab)
	postgres, redis := s.appNamed("shop-postgres"), s.appNamed("shop-redis")
	s.Equal("postgres", *postgres.Template)
	s.Equal("echowa-shop-postgres-data", postgres.Volumes[0].Name, "made as the New app page makes it, volume and all")
	s.Equal(1, postgres.Replicas)
	s.Nil(postgres.Domain)
	s.ElementsMatch([]uint{postgres.ID, redis.ID}, s.dispatched, "each new database deploys at once")
	for _, db := range []models.App{postgres, redis} {
		var deployment models.Deployment
		s.Require().NoError(facades.Orm().Query().Where("app_id", db.ID).FirstOrFail(&deployment))
		s.Equal(db.DefaultTag, deployment.Ref)
	}

	_, page := b.visit(tab)
	s.Equal("Created shop-postgres, shop-redis, deploying now.", page.Flash["success"])
	rows := page.Props["linkedDatabases"].([]any)
	s.Require().Len(rows, 2)
	row := rows[0].(map[string]any)
	s.Equal(float64(postgres.ID), row["id"])
	s.Equal("shop-postgres", row["name"])
	s.Equal("postgres", row["template"])
	s.Equal("echowa-shop-postgres", row["host"])
	s.Equal(float64(5432), row["port"])
	s.Equal("not_deployed", row["status"].(map[string]any)["state"])
	s.Len(page.Props["databaseChoices"].(map[string]any)["templates"], 3)
	s.Empty(page.Props["databaseChoices"].(map[string]any)["existing"], "linked databases aren't offered again")
}

func (s *AppDatabasesTestSuite) TestAddLinksExistingDatabases() {
	shop := createTestApp(s.T(), "shop", nil)
	createTestApp(s.T(), "blog", nil)
	main := s.createDatabase("postgres", "main-db")
	cache := s.createDatabase("redis", "cache")
	tab := fmt.Sprintf("/apps/%d?tab=databases", shop.ID)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(tab)
	existing := page.Props["databaseChoices"].(map[string]any)["existing"].([]any)
	s.Require().Len(existing, 2, "only template apps are offered")
	s.Equal(map[string]any{"id": float64(cache.ID), "name": "cache", "template": "redis"}, existing[0])

	resp := b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/databases", shop.ID), tab, map[string]any{
		"link": []uint{main.ID, cache.ID},
	})

	resp.AssertHeader("Location", tab)
	s.Equal([]string{"cache", "main-db"}, s.linkedNames(b, shop))
	s.Empty(s.dispatched, "linking deploys nothing")

	// Linking again is harmless.
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/databases", shop.ID), tab, map[string]any{"link": []uint{main.ID}})
	n, err := facades.Orm().Query().Model(&models.AppLink{}).Count()
	s.Require().NoError(err)
	s.EqualValues(2, n)
}

func (s *AppDatabasesTestSuite) TestAddRefusesBadChoicesAndMakesNothing() {
	shop := createTestApp(s.T(), "shop", nil)
	blog := createTestApp(s.T(), "blog", nil)
	tab := fmt.Sprintf("/apps/%d?tab=databases", shop.ID)
	s.docker.AddVolume("echowa-old-db-data")

	for _, tt := range []struct {
		name  string
		form  map[string]any
		field string
		want  string
	}{
		{"nothing picked", map[string]any{"databases": []any{}, "link": []any{}}, "databases", "Pick a database to add."},
		{"taken name", map[string]any{"databases": []map[string]any{{"template": "postgres", "name": "blog"}}}, "databases.0.name", "There's already an app with this name."},
		{"same name twice", map[string]any{"databases": []map[string]any{{"template": "postgres", "name": "db"}, {"template": "redis", "name": "db"}}}, "databases.1.name", "Two new databases can't share a name."},
		{"blank name", map[string]any{"databases": []map[string]any{{"template": "postgres", "name": " "}}}, "databases.0.name", "Give it a name."},
		{"bad name", map[string]any{"databases": []map[string]any{{"template": "postgres", "name": "-db"}}}, "databases.0.name", "Use lowercase letters"},
		{"unknown template", map[string]any{"databases": []map[string]any{{"template": "oracle", "name": "db"}}}, "databases.0.template", "There's no template called oracle."},
		{"leftover volume", map[string]any{"databases": []map[string]any{{"template": "postgres", "name": "old-db"}}}, "databases.0.name", "The volume echowa-old-db-data is left"},
		{"not a database", map[string]any{"link": []uint{blog.ID}}, "link", "Only databases made from a template can be linked."},
		{"no such app", map[string]any{"link": []uint{9999}}, "link", "Only databases made from a template can be linked."},
	} {
		s.Run(tt.name, func() {
			b := signedIn(s.T(), &s.TestCase)
			b.visit(tab)

			b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/databases", shop.ID), tab, tt.form).AssertHeader("Location", tab)

			_, page := b.visit(tab)
			s.Contains(page.Props["errors"].(map[string]any)[tt.field], tt.want)
			s.EqualValues(2, s.countApps())
			s.Empty(s.dispatched)
		})
	}
}

func (s *AppDatabasesTestSuite) TestADatabaseHasNoDatabasesSection() {
	db := s.createDatabase("postgres", "db")
	other := s.createDatabase("redis", "cache")
	page := fmt.Sprintf("/apps/%d", db.ID)
	b := signedIn(s.T(), &s.TestCase)

	_, show := b.visit(page)
	s.Empty(show.Props["linkedDatabases"])
	s.Nil(show.Props["databaseChoices"])

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/databases", db.ID), page, map[string]any{"link": []uint{other.ID}}).AssertNotFound()
	n, err := facades.Orm().Query().Model(&models.AppLink{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
}

func (s *AppDatabasesTestSuite) TestUnlinkKeepsTheDatabase() {
	shop := createTestApp(s.T(), "shop", nil)
	db := s.createDatabase("postgres", "db")
	s.Require().NoError(services.LinkApps(context.Background(), shop.ID, db.ID))
	tab := fmt.Sprintf("/apps/%d?tab=databases", shop.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(tab)

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/databases/%d", shop.ID, db.ID), tab, nil).AssertHeader("Location", tab)

	s.Empty(s.linkedNames(b, shop))
	s.appNamed("db")
}

func (s *AppDatabasesTestSuite) TestDeletingEitherAppRemovesTheLink() {
	shop := createTestApp(s.T(), "shop", nil)
	blog := createTestApp(s.T(), "blog", nil)
	db := s.createDatabase("postgres", "db")
	s.Require().NoError(services.LinkApps(context.Background(), shop.ID, db.ID))
	s.Require().NoError(services.LinkApps(context.Background(), blog.ID, db.ID))

	s.Require().NoError(services.DeleteApp(context.Background(), shop))
	n, err := facades.Orm().Query().Model(&models.AppLink{}).Count()
	s.Require().NoError(err)
	s.EqualValues(1, n, "the deleted app's link goes")

	s.Require().NoError(services.DeleteApp(context.Background(), db))
	n, err = facades.Orm().Query().Model(&models.AppLink{}).Count()
	s.Require().NoError(err)
	s.Zero(n, "so does a deleted database's")
}
