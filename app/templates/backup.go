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
	// Database is the env var naming the one database Dump covers, or ""
	// when Dump covers the whole server, as Redis's does.
	Database string
	// Version prints the server's version, which ServerVersion reads.
	Version []string
	// Ready prints 1 once the real server takes connections, not the
	// temporary one each image's entrypoint runs, without networking, to
	// set the database up. A restore into a new app waits for it.
	Ready []string
	// Dump writes the app's database to stdout.
	Dump []string
	// Restorable prints 1 when the running server can take Restore, which
	// one started by an older release may not. Nil means it always can.
	Restorable []string
	// Restore reads a dump on stdin into the app's database, and exits
	// with a status other than 0 when any of it fails.
	Restore []string
	// Unchanged is the status Restore exits with when it refused the dump
	// before changing anything. 0 means it never says.
	Unchanged int
	// Extension is the dump's, without the dot: "dump", "sql.gz".
	Extension string
	// Gzip has Echowa compress Dump's output, and decompress it for Restore.
	Gzip bool
	// Atomic means Restore applies all of a dump or none of it.
	Atomic bool
	// Parts are what a dump is made of, in words: "tables", "collections".
	// A restore that isn't Atomic says some may be left half done.
	Parts string
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

// WholeServer means a dump holds every database in the server, and a
// restore replaces all of them.
func (b *Backup) WholeServer() bool {
	return b.Database == ""
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
	Parts:      "tables",
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
	Parts:      "tables",
	Extension:  "sql.gz",
	Gzip:       true,
	Compatible: sameOrNewer(2),
	Exports: []Export{{
		Format: "sql", Label: "SQL",
		Hint: "For mysql, or Import in phpMyAdmin.",
		Dump: mysqlDump, Extension: "sql.gz", Gzip: true,
	}},
}

// MariaDB's are MySQL's with its own names. It has no GTIDs in a dump
// unless asked, so no --set-gtid-purged.
var mariadbDump = []string{"sh", "-c", `export MYSQL_PWD="$MARIADB_ROOT_PASSWORD"; exec mariadb-dump -u root ` +
	`--single-transaction --routines --triggers --events --no-tablespaces --hex-blob "$MARIADB_DATABASE"`}

var mariadbBackup = &Backup{
	Database: "MARIADB_DATABASE",
	Version:  []string{"mariadbd", "--version"},
	Ready: []string{"sh", "-c", `export MYSQL_PWD="$MARIADB_ROOT_PASSWORD"; exec mariadb -u root -N -B ` +
		`-e "SELECT 1 - @@skip_networking"`},
	Dump:       mariadbDump,
	Restore:    []string{"sh", "-c", `export MYSQL_PWD="$MARIADB_ROOT_PASSWORD"; exec mariadb -u root "$MARIADB_DATABASE"`},
	Parts:      "tables",
	Extension:  "sql.gz",
	Gzip:       true,
	Compatible: sameOrNewer(2),
	Exports: []Export{{
		Format: "sql", Label: "SQL",
		Hint: "For mariadb, or Import in phpMyAdmin.",
		Dump: mariadbDump, Extension: "sql.gz", Gzip: true,
	}},
}

// Redis has no dump a client replays, so a backup is the RDB snapshot a
// replica would get, every logical database in it. It ends with the 40
// byte mark that ends a diskless transfer, which redis-cli can't trim from
// stdout and Redis ignores after the RDB's checksum (ADR 0044).
var redisDump = []string{"sh", "-c", `export REDISCLI_AUTH="$REDIS_PASSWORD"; exec redis-cli --rdb -`}

// redisRestore loads a snapshot with DEBUG RELOAD, which the server allows
// from inside its container only. It writes to dump.rdb because the file's
// name and folder can't be changed at runtime, so it pauses saves first. A
// file redis-check-rdb refuses never reaches the server. redis-cli exits 0
// on an error reply, so every reply is checked, or a failed reload, which
// empties the server, would be written to the append-only file.
const redisRestore = `set -e
export REDISCLI_AUTH="$REDIS_PASSWORD"
expect() { want=$1; shift; out=$(redis-cli "$@"); [ "$out" = "$want" ] || { echo "$*: $out" >&2; exit 1; }; }
info() { redis-cli INFO persistence | tr -d '\r' | sed -n "s/^$1://p"; }
[ "$(redis-cli --raw CONFIG GET enable-debug-command | sed -n 2p)" = local ] || { echo "DEBUG is off: redeploy the app first" >&2; exit 3; }
saves=$(redis-cli --raw CONFIG GET save | sed -n 2p)
f=/data/echowa-restore.rdb
expect OK CONFIG SET save ""
trap 'rm -f "$f"; redis-cli CONFIG SET save "$saves" >/dev/null' EXIT
while [ "$(info rdb_bgsave_in_progress)" = 1 ]; do sleep 0.1; done
cat > "$f" || exit 3
check=$(redis-check-rdb "$f" 2>&1) || { echo "$check" >&2; exit 3; }
chown redis:redis "$f"
mv "$f" /data/dump.rdb
expect OK DEBUG RELOAD NOSAVE
redis-cli BGREWRITEAOF >/dev/null
sleep 0.1
while [ "$(info aof_rewrite_in_progress)" = 1 ] || [ "$(info aof_rewrite_scheduled)" = 1 ]; do sleep 0.1; done
[ "$(info aof_last_bgrewrite_status)" = ok ] || { echo "The append-only file wasn't rewritten" >&2; exit 1; }`

var redisBackup = &Backup{
	Version: []string{"redis-server", "--version"},
	// The image runs no temporary server, but one loading its data
	// answers LOADING.
	Ready:      []string{"sh", "-c", `[ "$(` + redisCLI + ` ping)" = PONG ] && echo 1 || echo 0`},
	Dump:       redisDump,
	Parts:      "keys",
	Restorable: []string{"sh", "-c", `[ "$(` + redisCLI + ` --raw CONFIG GET enable-debug-command | sed -n 2p)" = local ] && echo 1 || echo 0`},
	Restore:    []string{"sh", "-c", redisRestore},
	Unchanged:  3,
	Extension:  "rdb",
	// The RDB format changes in minor releases: 7.4 can't read 8.0's.
	Compatible: sameOrNewer(2),
	Exports: []Export{{
		Format: "rdb", Label: "RDB snapshot",
		Hint: "For redis-server with appendonly off, as dump.rdb in its dir, or redis-check-rdb.",
		Dump: redisDump, Extension: "rdb",
	}},
}

// mongoTool runs mongodump or mongorestore as root. The password goes in a
// config file on descriptor 3, which sh feeds from a here-document through
// a pipe, so it's neither an argument nor on disk, and stdin stays free
// for a restore.
func mongoTool(command string) []string {
	return []string{"sh", "-c", "exec " + command +
		` --config /dev/fd/3 -u "$MONGO_INITDB_ROOT_USERNAME" --authenticationDatabase admin 3<<EOF
password: $MONGO_INITDB_ROOT_PASSWORD
EOF`}
}

// mongoDump is the app's database as one archive, which Echowa gzips: the
// same file as mongodump --archive --gzip writes.
var mongoDump = mongoTool(`mongodump --quiet --archive --db "$MONGO_INITDB_DATABASE"`)

var mongoBackup = &Backup{
	Database: "MONGO_INITDB_DATABASE",
	Version:  []string{"mongod", "--version"},
	Ready:    mongosh(`print(db.adminCommand({getCmdLineOpts: 1}).parsed.net.bindIp === "127.0.0.1" ? 0 : 1)`),
	Dump:     mongoDump,
	// Into the app's database, whichever the dump came from. Each
	// collection in the dump is dropped first; others are left. The
	// server's own databases are skipped, so a whole server's dump can't
	// replace its users.
	Restore: mongoTool(`mongorestore --archive --drop --stopOnError ` +
		`--nsExclude 'admin.*' --nsExclude 'config.*' --nsExclude 'local.*' ` +
		`--nsFrom '$db$.$coll$' --nsTo "$MONGO_INITDB_DATABASE"'.$coll$'`),
	Parts:     "collections",
	Extension: "archive.gz",
	Gzip:      true,
	// A feature compatibility version is a major and minor release, and a
	// server older than the one dumped may not know a newer one's options.
	Compatible: sameOrNewer(2),
	Exports: []Export{{
		Format: "archive", Label: "Archive",
		Hint: "For mongorestore --gzip --archive=<file>, with --nsFrom and --nsTo to restore into another database.",
		Dump: mongoDump, Extension: "archive.gz", Gzip: true,
	}},
}
