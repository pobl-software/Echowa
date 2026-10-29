package templates

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first 200 bytes of pg_dump -Fc from the PostgreSQL 18.6 and 17.11
// images, made in the dev machine.
const (
	postgres18Head = "5047444d5001100004080101000b00000000320000000000000000001d0000000009000000007e0000000000000000000600000073686f703138001d00000031382e36202844656269616e2031382e362d312e7067646731332b3229001d00000031382e36202844656269616e2031382e362d312e7067646731332b3229000700000000700d000000000000000001000000300001000000300008000000454e434f44494e470008000000454e434f44494e470002000000001e00000053455420636c69656e745f"
	postgres17Head = "5047444d5001100004080101000b00000000320000000000000000001d0000000009000000007e0000000000000000000600000073686f703137001f00000031372e3131202844656269616e2031372e31312d312e7067646731332b3229001f00000031372e3131202844656269616e2031372e31312d312e7067646731332b32290007000000005f0d000000000000000001000000300001000000300008000000454e434f44494e470008000000454e434f44494e470002000000001e00000053455420636c69"
	mysqlHead      = "-- MySQL dump 10.13  Distrib 9.7.4, for Linux (aarch64)\n--\n-- Host: localhost    Database: mysqldb\n" +
		"-- ------------------------------------------------------\n-- Server version\t9.7.4\n\n/*!40101 SET NAMES utf8mb4 */;\n"
)

func fromHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)

	return b
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return buf.Bytes()
}

func TestReadDumpHeadReadsPostgresCustomFormat(t *testing.T) {
	head, err := ReadDumpHead(fromHex(t, postgres18Head))
	require.NoError(t, err)
	assert.Equal(t, DumpHead{
		Template: "postgres", Database: "shop18", ServerVersion: "18.6",
		Made:    time.Date(2026, time.October, 29, 0, 50, 11, 0, time.UTC),
		Archive: 16, DumpVersion: "18.6",
	}, head)

	head, err = ReadDumpHead(fromHex(t, postgres17Head))
	require.NoError(t, err)
	assert.Equal(t, "17.11", head.ServerVersion)
	assert.Equal(t, "shop17", head.Database)
}

// Archives before 1.15, from PostgreSQL 15 and older, store the
// compression level in an int where newer ones have a byte.
func TestReadDumpHeadReadsAnOlderArchiveVersion(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("PGDMP")
	b.Write([]byte{1, 14, 0, 4, 8, archCustom})
	writeInt := func(n int) {
		b.WriteByte(0)
		_ = binary.Write(&b, binary.LittleEndian, int32(n))
	}
	writeStr := func(s string) {
		writeInt(len(s))
		b.WriteString(s)
	}
	writeInt(-1) // the default compression level
	for _, n := range []int{5, 4, 3, 2, 0, 125, 0} {
		writeInt(n)
	}
	writeStr("orders")
	writeStr("15.8 (Debian 15.8-1.pgdg120+1)")
	writeStr("15.8 (Debian 15.8-1.pgdg120+1)")

	head, err := ReadDumpHead(b.Bytes())
	require.NoError(t, err)
	assert.Equal(t, DumpHead{
		Template: "postgres", Database: "orders", ServerVersion: "15.8",
		Made:    time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC),
		Archive: 14, DumpVersion: "15.8",
	}, head)
}

// No pg_dump writes a year JSON can't hold, but a damaged header can.
func TestReadDumpHeadLeavesOutADateNoDumpHas(t *testing.T) {
	pg := fromHex(t, postgres18Head)
	pg[38] = 0x28 // the year, 1900 + 9000, after the sign byte at 37
	pg[39] = 0x23
	head, err := ReadDumpHead(pg)
	require.NoError(t, err)
	assert.True(t, head.Made.IsZero())
	assert.Equal(t, "18.6", head.ServerVersion)
}

// pg_restore 16 refuses pg_dump 18's archives with "unsupported version
// (1.16) in file header", and pg_restore 17 reads them: checked with the
// postgres:16 and postgres:17 images in the dev machine.
func TestPostgresDumpsAreReadableOnlyByAPgRestoreThatKnowsTheirArchive(t *testing.T) {
	head, err := ReadDumpHead(fromHex(t, postgres18Head))
	require.NoError(t, err)
	for _, version := range []string{"18.6", "17.0", "19.1"} {
		assert.NoError(t, head.ReadableBy(version), version)
	}
	err = head.ReadableBy("16.4")
	if assert.Error(t, err) {
		assert.Equal(t, "This dump was made by pg_dump 18.6, in an archive format (1.16) that PostgreSQL 16's pg_restore "+
			"can't read. Dump it with pg_dump 16, or restore it into an app on a newer PostgreSQL.", err.Error())
	}
	assert.Error(t, head.ReadableBy("15.8"))

	assert.NoError(t, DumpHead{Template: "postgres", Archive: 15}.ReadableBy("16.4"))
	assert.Error(t, DumpHead{Template: "postgres", Archive: 15}.ReadableBy("15.8"))
	assert.NoError(t, DumpHead{Template: "mysql"}.ReadableBy("8.0.36"))
}

func TestReadDumpHeadReadsMySQLDumps(t *testing.T) {
	want := DumpHead{Template: "mysql", Database: "mysqldb", ServerVersion: "9.7.4"}

	head, err := ReadDumpHead([]byte(mysqlHead))
	require.NoError(t, err)
	assert.Equal(t, want, head)

	want.Gzip = true
	head, err = ReadDumpHead(gzipped(t, mysqlHead))
	require.NoError(t, err)
	assert.Equal(t, want, head)

	// Only the start of an upload has arrived when it's first read.
	whole := gzipped(t, mysqlHead+string(bytes.Repeat([]byte("INSERT INTO t VALUES (1);\n"), 10_000)))
	head, err = ReadDumpHead(whole[:200])
	require.NoError(t, err)
	assert.Equal(t, want, head)

	head, err = ReadDumpHead([]byte("-- MariaDB dump 10.19  Distrib 10.11.6-MariaDB\n-- Server version\t10.11.6-MariaDB-0+deb12u1\n"))
	require.NoError(t, err)
	assert.Equal(t, "10.11.6", head.ServerVersion)
}

func TestReadDumpHeadSaysWhyItCantRestoreAFile(t *testing.T) {
	pg := fromHex(t, postgres18Head)
	tar := append([]byte{}, pg...)
	tar[10] = 3
	for name, c := range map[string]struct {
		head []byte
		want string
	}{
		"plain SQL":      {gzipped(t, "--\n-- PostgreSQL database dump\n--\n\n\\restrict abc\n"), "This is plain SQL from pg_dump."},
		"another format": {tar, "isn't in the custom format"},
		"a cut header":   {pg[:60], "ends before its header does"},
		"an old archive": {[]byte("PGDMP\x01\x09\x00"), "version 1.9"},
		"no version":     {[]byte("-- MySQL dump 10.13\n-- Host: localhost    Database: shop\n"), "doesn't say which server"},
		"anything else":  {[]byte("PK\x03\x04 a zip file"), "can't tell what this file is"},
		"empty":          {nil, "can't tell what this file is"},
		"a broken gzip":  {[]byte{0x1f, 0x8b, 0x00}, "gzipped, but it can't be read"},
		"phpMyAdmin's":   {[]byte("-- phpMyAdmin SQL Dump\n-- version 5.2.1\n"), "can't tell what this file is"},
		"a cut version":  {pg[:80], "ends before its header does"},
		// The start of mysqldump --databases and --all-databases, from MySQL 9.7.4.
		"--databases": {gzipped(t, mysqlHead+"\n--\n-- Current Database: `mysqldb`\n--\n\nCREATE DATABASE /*!32312 IF NOT EXISTS*/ `mysqldb`;\n\nUSE `mysqldb`;\n"),
			"switches to them"},
		"--all-databases": {[]byte("-- MySQL dump 10.13  Distrib 9.7.4, for Linux (aarch64)\n--\n-- Host: localhost    Database: \n" +
			"-- ------------------------------------------------------\n-- Server version\t9.7.4\n"), "switches to them"},
	} {
		_, err := ReadDumpHead(c.head)
		if assert.Error(t, err, name) {
			assert.Contains(t, err.Error(), c.want, name)
		}
	}
}
