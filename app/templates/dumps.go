package templates

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

// DumpHeadSize is how much of the start of a dump ReadDumpHead reads.
const DumpHeadSize = 64 << 10

// DumpHead is what the start of a dump says about it, for a restore from a
// file with no manifest (ADR 0034).
type DumpHead struct {
	// Template is the slug of the template whose Restore reads it.
	Template string
	Database string
	// ServerVersion is the version of the server it came from, as
	// ServerVersion reads it.
	ServerVersion string
	// Made is when it was dumped, or zero when the dump doesn't say.
	Made time.Time
	// Gzip means it's compressed, as Echowa compresses a template's dump
	// when Backup.Gzip is set.
	Gzip bool
	// Archive is a PostgreSQL archive's format version, 1.Archive, and
	// DumpVersion the pg_dump that wrote it. pg_dump can be newer than the
	// server it dumped.
	Archive     int
	DumpVersion string
}

// ReadableBy says why the restore command of a server running version
// can't read the dump, or nil. A pg_restore reads archives up to its own
// format version only, whichever server they came from.
func (h DumpHead) ReadableBy(version string) error {
	if h.Template != "postgres" {
		return nil
	}
	major, ok := versionParts(version, 1)
	if !ok {
		return nil
	}
	newest, known := pgNewestArchive(major[0])
	if !known || h.Archive <= newest {
		return nil
	}

	return fmt.Errorf("This dump was made by pg_dump %s, in an archive format (1.%d) that PostgreSQL %d's pg_restore can't read. "+
		"Dump it with pg_dump %d, or restore it into an app on a newer PostgreSQL.", h.DumpVersion, h.Archive, major[0], major[0])
}

// pgNewestArchive is the newest archive format version a major version's
// pg_restore reads (K_VERS_MAX in pg_backup_archiver.h): 1.14 from
// PostgreSQL 12, 1.15 from 16 and 1.16 from 17. A major after 18 isn't
// known, and reads what 18 writes.
func pgNewestArchive(major int) (int, bool) {
	switch {
	case major > 18:
		return 0, false
	case major >= 17:
		return 16, true
	case major == 16:
		return 15, true
	default:
		return 14, true
	}
}

var (
	pgdmp          = []byte("PGDMP")
	gzipMagic      = []byte{0x1f, 0x8b}
	mysqlDumpStart = regexp.MustCompile(`(?m)^-- (MySQL|MariaDB) dump `)
	mysqlServer    = regexp.MustCompile(`(?m)^-- Server version\s+(\S+)`)
	mysqlDatabase  = regexp.MustCompile(`(?m)^-- Host: .*Database: (\S*)`)
	// mysqldump --databases and --all-databases make each database and
	// switch to it, before any of its tables.
	mysqlSwitch  = regexp.MustCompile(`(?m)^(-- Current Database: |CREATE DATABASE |USE )`)
	pgPlainStart = []byte("-- PostgreSQL database dump")
)

// ReadDumpHead reads which template's dump head starts, and what it says.
// It knows PostgreSQL's custom format and mysqldump's SQL, gzipped or not.
// An error says, in words for the page, why the file can't be restored.
func ReadDumpHead(head []byte) (DumpHead, error) {
	if bytes.HasPrefix(head, pgdmp) {
		return readPostgresCustom(head)
	}
	text, gzipped := head, false
	if bytes.HasPrefix(head, gzipMagic) {
		var err error
		if text, err = gunzipHead(head); err != nil {
			return DumpHead{}, errors.New("This file is gzipped, but it can't be read: " + err.Error() + ".")
		}
		gzipped = true
	}
	start := text[:min(len(text), 1024)]
	switch {
	case mysqlDumpStart.Match(start):
		return readMySQLDump(text, gzipped)
	case bytes.Contains(start, pgPlainStart):
		return DumpHead{}, errors.New("This is plain SQL from pg_dump. Restore takes PostgreSQL's custom format, " +
			"the .dump that Export and backups make. Load plain SQL with psql instead.")
	}

	return DumpHead{}, errors.New("Echowa can't tell what this file is. Restore takes the .dump or .sql.gz " +
		"that Export or a backup's download made.")
}

// gunzipHead decompresses as much of the start of a gzip stream as fits
// in DumpHeadSize. The rest of the stream isn't there, so a short read is
// expected.
func gunzipHead(head []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(head))
	if err != nil {
		return nil, err
	}
	text := make([]byte, DumpHeadSize)
	n, err := io.ReadFull(zr, text)
	if n == 0 && err != nil {
		return nil, err
	}

	return text[:n], nil
}

// readMySQLDump reads the comments mysqldump starts its output with. A
// dump that switches database would restore beside the app's database,
// not into it, so it's refused.
func readMySQLDump(text []byte, gzipped bool) (DumpHead, error) {
	db := mysqlDatabase.FindSubmatch(text)
	if mysqlSwitch.Match(text) || (db != nil && len(db[1]) == 0) {
		return DumpHead{}, errors.New("This dump makes its own databases and switches to them, as mysqldump --databases and " +
			"--all-databases do, so it wouldn't restore into this app's database. Dump the one database without --databases.")
	}
	server := mysqlServer.FindSubmatch(text)
	if server == nil {
		return DumpHead{}, errors.New("This mysqldump file doesn't say which server it came from. " +
			"Echowa reads the \"-- Server version\" comment, which --skip-comments leaves out.")
	}
	version, err := ServerVersion(server[1])
	if err != nil {
		return DumpHead{}, errors.New("This mysqldump file's server version can't be read.")
	}
	head := DumpHead{Template: "mysql", ServerVersion: version, Gzip: gzipped}
	if db != nil {
		head.Database = string(db[1])
	}

	return head, nil
}

// PostgreSQL's archive versions (pg_backup_archiver.h): 1.10 added the
// server's version to the header, and 1.15 stored the compression
// algorithm in a byte instead of the level in an int.
const (
	archCustom        = 1
	pgMinMinor        = 10
	pgByteCompression = 15
)

// readPostgresCustom reads a custom-format archive's header, as pg_restore's
// ReadHead does.
func readPostgresCustom(head []byte) (DumpHead, error) {
	r := &archiveReader{b: head[len(pgdmp):]}
	major, minor := r.byte(), r.byte()
	if major > 1 || minor > 0 {
		r.byte() // the revision
	}
	if major != 1 || minor < pgMinMinor {
		if r.err != nil {
			return DumpHead{}, errHeaderCut
		}
		return DumpHead{}, fmt.Errorf("This archive's format is version %d.%d, which Echowa can't read.", major, minor)
	}
	r.intSize = int(r.byte())
	r.byte() // the size of an offset
	format := r.byte()
	if minor >= pgByteCompression {
		r.byte()
	} else {
		r.int()
	}
	sec, mins, hour, day, month, year := r.int(), r.int(), r.int(), r.int(), r.int(), r.int()
	r.int() // whether daylight saving time was in force
	database := r.str()
	server := r.str()
	dumpVersion := r.str()
	if r.err != nil {
		return DumpHead{}, r.err
	}
	if format != archCustom {
		return DumpHead{}, errors.New("This pg_dump archive isn't in the custom format, the .dump that Export and backups make.")
	}
	version, err := ServerVersion([]byte(server))
	if err != nil {
		return DumpHead{}, errors.New("This archive's server version can't be read.")
	}

	dump := DumpHead{Template: "postgres", Database: database, ServerVersion: version, Archive: int(minor)}
	if v, err := ServerVersion([]byte(dumpVersion)); err == nil {
		dump.DumpVersion = v
	}
	// pg_dump writes its machine's local time, which in the image is UTC.
	// A year no real dump has is left out, as JSON can't hold some.
	if year := year + 1900; year >= 1970 && year <= 9999 {
		dump.Made = time.Date(year, time.Month(month+1), day, hour, mins, sec, 0, time.UTC)
	}

	return dump, nil
}

var errHeaderCut = errors.New("The file ends before its header does.")

// archiveReader reads the integers and strings of a custom-format header.
// Its first error sticks, and every read after it returns zero.
type archiveReader struct {
	b       []byte
	intSize int
	err     error
}

func (r *archiveReader) byte() byte {
	if r.err != nil || len(r.b) == 0 {
		r.err = errHeaderCut
		return 0
	}
	b := r.b[0]
	r.b = r.b[1:]

	return b
}

// int is a sign byte, then intSize bytes, least significant first.
func (r *archiveReader) int() int {
	if r.err == nil && (r.intSize < 1 || r.intSize > 8) {
		r.err = errors.New("This archive's header can't be read.")
	}
	negative := r.byte() != 0
	n := 0
	for i := range r.intSize {
		n |= int(r.byte()) << (8 * i)
	}
	if r.err != nil {
		return 0
	}
	if negative {
		return -n
	}

	return n
}

// str is a length, then that many bytes. A negative length is no string.
func (r *archiveReader) str() string {
	n := r.int()
	if r.err != nil || n <= 0 {
		return ""
	}
	if n > len(r.b) {
		r.err = errHeaderCut
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]

	return s
}
