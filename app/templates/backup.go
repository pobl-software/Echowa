package templates

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Backup is how a template's database is dumped and restored (ADR 0034).
// The commands are constants that read secrets from the container's env
// inside sh -c: nothing a request sends goes into one.
type Backup struct {
	// Database is the env var naming the one database Dump covers.
	Database string
	// Version prints the server's version, which ServerVersion reads.
	Version []string
	// Ready prints 1 once the real server takes connections, not the
	// temporary one each image's entrypoint runs, without networking, to
	// set the database up. A restore into a new app waits for it.
	Ready []string
	// Dump writes the app's database to stdout.
	Dump []string
	// Restore reads a dump on stdin into the app's database, and exits
	// with a status other than 0 when any of it fails.
	Restore []string
	// Extension is the dump's, without the dot: "dump", "sql.gz".
	Extension string
	// Gzip has Echowa compress Dump's output, and decompress it for Restore.
	Gzip bool
	// Atomic means Restore applies all of a dump or none of it.
	Atomic bool
	// Compatible says whether a dump from server version from restores
	// into server version to, both as ServerVersion reads them.
	Compatible func(from, to string) bool
	// Exports are the formats Export offers, the first by default.
	Exports []Export
}

// Export is a format the database downloads in, straight to the browser
// with nothing kept (ADR 0034).
type Export struct {
	// Format names it in the export's URL: "custom", "sql".
	Format string `json:"format"`
	Label  string `json:"label"`
	// Hint says what reads it.
	Hint string `json:"hint"`
	// Dump writes the app's database to stdout in this format.
	Dump      []string `json:"-"`
	Extension string   `json:"extension"`
	// Gzip has Echowa compress Dump's output. A gzip stream ends with a
	// checksum, so a dump that fails part way can't pass for a whole one.
	Gzip bool `json:"-"`
}

// FindExport is the export format named, or the first for "".
func (b *Backup) FindExport(format string) (Export, bool) {
	if format == "" && len(b.Exports) > 0 {
		return b.Exports[0], true
	}
	for _, e := range b.Exports {
		if e.Format == format {
			return e, true
		}
	}

	return Export{}, false
}

var (
	versionNumber = regexp.MustCompile(`\d+(?:\.\d+)*`)
	wholeVersion  = regexp.MustCompile(`^\d+(?:\.\d+)*$`)
)

// ServerVersion is the first version number in what Backup.Version
// printed: "18.0" from "postgres (PostgreSQL) 18.0 (Debian 18.0-1)".
func ServerVersion(out []byte) (string, error) {
	v := versionNumber.Find(out)
	if v == nil {
		return "", fmt.Errorf("no version number in %q", out)
	}

	return string(v), nil
}

// sameOrNewer compares the first n parts of two versions: a dump restores
// into the server it came from or a newer one, never an older one.
func sameOrNewer(n int) func(from, to string) bool {
	return func(from, to string) bool {
		f, okFrom := versionParts(from, n)
		t, okTo := versionParts(to, n)
		if !okFrom || !okTo {
			return false
		}
		for i := range n {
			if t[i] != f[i] {
				return t[i] > f[i]
			}
		}

		return true
	}
}

// versionParts is the first n numbers of a dotted version, missing ones
// as 0.
func versionParts(v string, n int) ([]int, bool) {
	if !wholeVersion.MatchString(v) {
		return nil, false
	}
	parts := make([]int, n)
	for i, s := range strings.Split(v, ".") {
		if i == n {
			break
		}
		p, err := strconv.Atoi(s)
		if err != nil {
			return nil, false
		}
		parts[i] = p
	}

	return parts, true
}

// PostgreSQL's commands connect over the container's socket as the
// template's user, which needs no password (ADR 0027). The custom format
// is compressed already, and pg_restore reads it from a pipe.
var postgresDump = []string{"sh", "-c", `exec pg_dump -Fc -w -U "$POSTGRES_USER" -d "$POSTGRES_DB"`}

var postgresBackup = &Backup{
	Database: "POSTGRES_DB",
	Version:  []string{"postgres", "-V"},
	Ready: []string{"sh", "-c", `exec psql -w -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA ` +
		`-c "SELECT (current_setting('listen_addresses') <> '')::int"`},
	Dump: postgresDump,
	Restore: []string{"sh", "-c", `exec pg_restore -w -U "$POSTGRES_USER" -d "$POSTGRES_DB" ` +
		`--clean --if-exists --no-owner --no-privileges --single-transaction --exit-on-error`},
	Extension:  "dump",
	Atomic:     true,
	Compatible: sameOrNewer(1),
	Exports: []Export{
		{
			Format: "custom", Label: "Custom format",
			Hint: "For pg_restore, or Restore in pgAdmin, of this server's major version or newer.",
			Dump: postgresDump, Extension: "dump",
		},
		{
			// Without owners or grants, it loads as whichever user runs it.
			Format: "sql", Label: "Plain SQL",
			Hint:      "For psql, into any database. Owners and privileges are left out.",
			Dump:      []string{"sh", "-c", `exec pg_dump -w -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-privileges`},
			Extension: "sql.gz", Gzip: true,
		},
	},
}

// MySQL's connect over the socket as root, whose password is in the env.
// The dump names no database, so it restores into another app's.
var mysqlDump = []string{"sh", "-c", `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysqldump -u root ` +
	`--single-transaction --routines --triggers --events --set-gtid-purged=OFF --no-tablespaces --hex-blob "$MYSQL_DATABASE"`}

var mysqlBackup = &Backup{
	Database: "MYSQL_DATABASE",
	Version:  []string{"mysqld", "--version"},
	Ready: []string{"sh", "-c", `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -u root -N -B ` +
		`-e "SELECT 1 - @@skip_networking"`},
	Dump:       mysqlDump,
	Restore:    []string{"sh", "-c", `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -u root "$MYSQL_DATABASE"`},
	Extension:  "sql.gz",
	Gzip:       true,
	Compatible: sameOrNewer(2),
	Exports: []Export{{
		Format: "sql", Label: "SQL",
		Hint: "For mysql, or Import in phpMyAdmin.",
		Dump: mysqlDump, Extension: "sql.gz", Gzip: true,
	}},
}
