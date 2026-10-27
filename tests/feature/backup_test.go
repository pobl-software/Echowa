package feature

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/services"
	"goravel/tests"
)

type BackupTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestBackupTestSuite(t *testing.T) {
	suite.Run(t, new(BackupTestSuite))
}

func (s *BackupTestSuite) SetupTest() {
	s.RefreshDatabase()
}

func (s *BackupTestSuite) TestBackupCopiesTheDatabaseToANewFile() {
	_, _, err := services.SaveUser("Admin", "admin@example.test", "password123")
	s.Require().NoError(err)
	dest := filepath.Join(s.T().TempDir(), "echowa-backup.sqlite")

	s.Require().NoError(facades.Artisan().Call("--no-ansi backup " + dest))

	db, err := sql.Open("sqlite3", "file:"+dest+"?mode=ro")
	s.Require().NoError(err)
	defer db.Close()
	var email string
	s.Require().NoError(db.QueryRow("SELECT email FROM users").Scan(&email))
	s.Equal("admin@example.test", email)
	var check string
	s.Require().NoError(db.QueryRow("PRAGMA integrity_check").Scan(&check))
	s.Equal("ok", check)

	s.Error(facades.Artisan().Call("--no-ansi backup "+dest), "never overwrites")
}
