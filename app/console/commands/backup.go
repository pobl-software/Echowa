package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"goravel/app/facades"
)

// Backup writes a consistent copy of the database to a new file, safe while
// Echowa runs (SQLite's VACUUM INTO), so servers don't need the sqlite3 CLI:
//
//	echowa backup /var/backups/echowa/echowa-2026-10-24.sqlite
//
// The copy is useless without APP_KEY, which it doesn't contain.
type Backup struct{}

func (r *Backup) Signature() string {
	return "backup"
}

func (r *Backup) Description() string {
	return "Copy the database to a new file, safely while Echowa runs"
}

func (r *Backup) Extend() command.Extend {
	return command.Extend{
		Category:  "db",
		Arguments: []command.Argument{&command.ArgumentString{Name: "dest", Usage: "The file to write; it mustn't exist", Required: true}},
	}
}

func (r *Backup) Handle(ctx console.Context) error {
	dest, err := filepath.Abs(ctx.ArgumentString("dest"))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s already exists", dest)
	}
	if _, err := facades.Orm().Query().Exec("VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("back up to %s: %w", dest, err)
	}
	ctx.Info("Backed up the database to " + dest + ". Keep APP_KEY separately: the copy is useless without it.")

	return nil
}
