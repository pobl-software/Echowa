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
	// Version prints the server's version, which ServerVersion reads.
	Version []string
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
var postgresBackup = &Backup{
	Version: []string{"postgres", "-V"},
	Dump:    []string{"sh", "-c", `exec pg_dump -Fc -w -U "$POSTGRES_USER" -d "$POSTGRES_DB"`},
	Restore: []string{"sh", "-c", `exec pg_restore -w -U "$POSTGRES_USER" -d "$POSTGRES_DB" ` +
		`--clean --if-exists --no-owner --no-privileges --single-transaction --exit-on-error`},
	Extension:  "dump",
	Atomic:     true,
	Compatible: sameOrNewer(1),
}

// MySQL's connect over the socket as root, whose password is in the env.
// The dump names no database, so it restores into another app's.
var mysqlBackup = &Backup{
	Version: []string{"mysqld", "--version"},
	Dump: []string{"sh", "-c", `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysqldump -u root ` +
		`--single-transaction --routines --triggers --events --set-gtid-purged=OFF --no-tablespaces --hex-blob "$MYSQL_DATABASE"`},
	Restore:    []string{"sh", "-c", `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -u root "$MYSQL_DATABASE"`},
	Extension:  "sql.gz",
	Gzip:       true,
	Compatible: sameOrNewer(2),
}
