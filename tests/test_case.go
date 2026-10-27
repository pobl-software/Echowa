package tests

import (
	"os"
	"path/filepath"

	"github.com/goravel/framework/testing"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/services"
	"goravel/bootstrap"
	"goravel/config"
)

// TestDatabaseName is the file name of the throwaway database tests run on.
const TestDatabaseName = "echowa_testing.sqlite"

func init() {
	useTestDatabase()
	bootstrap.Boot()
	// No test talks to a real Docker daemon. Suites that care call
	// FakeDocker for a fresh fake.
	services.Docker = func() docker.Client { return fakeDocker }
}

var fakeDocker = &docker.Fake{}

type TestCase struct {
	testing.TestCase
}

// FakeDocker gives the test a fresh in-memory Docker: a healthy Swarm
// where everything succeeds until its func fields say otherwise.
func (tc *TestCase) FakeDocker() *docker.Fake {
	fakeDocker = &docker.Fake{}

	return fakeDocker
}

// useTestDatabase points the default connection, and the metrics file, at
// fresh SQLite files in a temp directory, so RefreshDatabase can never wipe
// the development database. It runs after the config files' init() (they're imported) but
// before Boot() opens any connections. See ADR 0014.
func useTestDatabase() {
	dir, err := os.MkdirTemp("", "echowa-test-")
	if err != nil {
		panic(err)
	}
	database := filepath.Join(dir, TestDatabaseName)

	key := "database.connections." + facades.Config().GetString("database.default")
	facades.Config().Add(key+".database", database)
	facades.Config().Add(key+".dsn", config.SQLiteDSN(database))
	facades.Config().Add("database.metrics.database", filepath.Join(dir, "metrics.sqlite"))
}
