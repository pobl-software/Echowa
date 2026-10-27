package feature

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"goravel/app/facades"
	"goravel/tests"
)

// TestMain migrates the test database (tests.TestCase points the app at a
// temp SQLite file) before running this package, and deletes it afterwards.
func TestMain(m *testing.M) {
	database := facades.Config().GetString("database.connections.sqlite.database")
	if filepath.Base(database) != tests.TestDatabaseName {
		// Never migrate or refresh a database that isn't the test one.
		fmt.Fprintf(os.Stderr, "refusing to run: database %q is not a test database\n", database)
		os.Exit(1)
	}
	if err := facades.Artisan().Call("--no-ansi migrate"); err != nil {
		fmt.Fprintln(os.Stderr, "migrate test database:", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(filepath.Dir(database))
	os.Exit(code)
}
