package tests

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/goravel/framework/testing"

	"goravel/app/backups"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
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
	// Nor to S3. Suites that care call FakeStorage.
	backups.Open = func(loc *models.StorageLocation) backups.Store {
		// Backups open their location in the background.
		openedMu.Lock()
		defer openedMu.Unlock()
		OpenedStorage = append(OpenedStorage, *loc)
		return fakeStorage
	}
	// Nor to a notification provider. Suites that care call FakeNotify.
	notify.Open = func(p *models.NotificationProvider) (notify.Sender, error) {
		return fakeNotify.Open(p)
	}
}

var (
	// realStorage is the S3 store, kept for the docker integration tests
	// (real.go) before init swaps in the fake.
	realStorage = backups.Open
	fakeDocker  = &docker.Fake{}
	fakeStorage = backups.NewFake()
	fakeNotify  = &notify.Fake{}
	openedMu    sync.Mutex
)

// OpenedStorage lists, oldest first, the locations backups.Open was given
// since the last FakeStorage.
var OpenedStorage []models.StorageLocation

type TestCase struct {
	testing.TestCase
}

// FakeDocker gives the test a fresh in-memory Docker: a healthy Swarm
// where everything succeeds until its func fields say otherwise.
func (tc *TestCase) FakeDocker() *docker.Fake {
	fakeDocker = &docker.Fake{}

	return fakeDocker
}

// FakeStorage gives the test a fresh in-memory bucket, which every storage
// location opens, and forgets which locations were opened.
func (tc *TestCase) FakeStorage() *backups.Fake {
	fakeStorage = backups.NewFake()
	OpenedStorage = nil

	return fakeStorage
}

// FakeNotify gives the test a fresh recorder of what would have been sent
// to notification providers.
func (tc *TestCase) FakeNotify() *notify.Fake {
	fakeNotify = &notify.Fake{}

	return fakeNotify
}

// useTestDatabase points the default connection, the metrics file and the
// session files at a temp directory, so RefreshDatabase can never wipe the
// development database, nor a test sign the dev machine's browser out. It runs after the config files' init() (they're imported) but
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
	// Sessions too: resetting the admin's password deletes every session
	// file, and the dev machine's Echowa keeps its own in the repo's
	// storage/framework/sessions.
	facades.Config().Add("session.files", filepath.Join(dir, "sessions"))
}
