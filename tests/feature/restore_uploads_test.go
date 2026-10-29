package feature

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
)

// pgDump is a custom-format archive's header, as pg_dump 18 writes it, of
// database from server, followed by body.
func pgDump(server, database, body string) []byte {
	return pgDumpBy("18.6 (Debian 18.6-1.pgdg13+2)", server, database, body)
}

// pgDumpBy is pgDump with the version of the pg_dump that wrote it.
func pgDumpBy(pgDumpVersion, server, database, body string) []byte {
	var b bytes.Buffer
	b.WriteString("PGDMP")
	b.Write([]byte{1, 16, 0, 4, 8, 1, 1})
	writeInt := func(n int) {
		b.WriteByte(0)
		_ = binary.Write(&b, binary.LittleEndian, int32(n))
	}
	for _, n := range []int{5, 4, 3, 2, 9, 126, 0} {
		writeInt(n)
	}
	for _, s := range []string{database, server, pgDumpVersion} {
		writeInt(len(s))
		b.WriteString(s)
	}
	b.WriteString(body)

	return b.Bytes()
}

func gzipText(s string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()

	return buf.Bytes()
}

const mysqlDumpText = "-- MySQL dump 10.13  Distrib 9.7.4, for Linux (aarch64)\n--\n-- Host: localhost    Database: orders\n" +
	"-- ------------------------------------------------------\n-- Server version\t9.7.4\n\nDROP TABLE IF EXISTS `t`;\n"

// startUpload starts an upload of name and size to app, and returns its ID.
func (s *BackupsTestSuite) startUpload(b *browser, app *models.App, name string, size int) string {
	resp := b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/restores/uploads", app.ID),
		map[string]any{"name": name, "size": size}, false,
		map[string]string{"Accept": "application/json", "X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie)})
	resp.AssertStatus(nethttp.StatusCreated)
	body := must(resp.Json())
	s.Equal(float64(services.UploadChunk), body["chunk"])

	return body["id"].(string)
}

// putChunk sends chunk as the bytes from offset, asserts the response's
// status, and returns its body.
func (s *BackupsTestSuite) putChunk(b *browser, app *models.App, id string, offset int, chunk []byte, status int) map[string]any {
	resp := b.send(nethttp.MethodPut, fmt.Sprintf("/apps/%d/restores/uploads/%s?offset=%d", app.ID, id, offset), bytes.NewReader(chunk),
		map[string]string{
			"Accept": "application/json", "Content-Type": "application/octet-stream",
			"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		})
	resp.AssertStatus(status)
	body, _ := resp.Json()

	return body
}

// upload sends file to app as the dialog does: its start, then the rest in
// chunks. It returns the upload's ID.
func (s *BackupsTestSuite) upload(b *browser, app *models.App, name string, file []byte) string {
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", app.ID))
	id := s.startUpload(b, app, name, len(file))
	for sent := 0; sent < len(file); {
		end := min(len(file), sent+services.UploadChunk)
		if sent == 0 {
			end = min(len(file), 64<<10)
		}
		body := s.putChunk(b, app, id, sent, file[sent:end], nethttp.StatusOK)
		s.Require().Equal(float64(end), body["received"])
		sent = end
	}

	return id
}

func uploadsLeft() []string {
	entries, _ := os.ReadDir(services.UploadDir())
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

func (s *BackupsTestSuite) TestARestoreFromAnUploadedFile() {
	file := pgDump("18.6 (Debian 18.6-1.pgdg13+2)", "shop", strings.Repeat("table data ", 1_000_000))
	s.scriptRestore(postgres186, "PGDMP the data it replaces", 0, nil)
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(storageSecret)

	id := s.upload(b, s.app, "shop-db-20261029T005011Z.dump", file)
	s.Equal([]string{id}, uploadsLeft(), "the file waits beside the database")

	preview := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", s.app.ID, id), nethttp.StatusOK)
	s.Equal("shop-db-20261029T005011Z.dump", preview["name"])
	s.Equal(true, preview["uploaded"])
	s.Equal("", preview["location"])
	s.Equal("shop", preview["database"])
	s.Equal("18.6", preview["server_version"])
	s.Equal("18.6", preview["app_version"])
	s.Equal("2026-10-02T03:04:05Z", preview["started_at"])
	s.EqualValues(len(file), preview["size_bytes"])
	s.Empty(preview["refusal"])

	s.restore(b, s.app, map[string]any{"upload": id, "safety": true, "safety_location": s.loc.ID, "confirm": "shop-db"})
	services.WaitForRestores()

	rows := s.restores(s.app.ID)
	s.Require().Len(rows, 1)
	rec := rows[0]
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.Nil(rec.StorageLocationID)
	s.Nil(rec.BackupID)
	s.Equal("shop-db-20261029T005011Z.dump", rec.Key)
	s.EqualValues(len(file), rec.ReadBytes)
	s.Equal([]string{string(file)}, s.restoredSoFar(), "the restore read the whole file")
	s.Empty(uploadsLeft(), "the restore deleted the file")

	s.Require().NotNil(rec.SafetyBackupID)
	var safety models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&safety, *rec.SafetyBackupID))
	s.Equal(models.BackupSucceeded, safety.Status)
	s.Equal(s.loc.ID, safety.StorageLocationID)

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	s.Equal("Restoring shop-db from shop-db-20261029T005011Z.dump.", page.Flash["success"])
	row := page.Props["backups"].(map[string]any)["restores"].([]any)[0].(map[string]any)
	s.Equal(true, row["uploaded"])
	s.Equal("", row["location"])
	detail := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/%d", s.app.ID, rec.ID), nethttp.StatusOK)
	s.Nil(detail["storage_location_id"])

	// A restore's file is no longer an upload.
	s.putChunk(b, s.app, id, len(file), []byte("more"), nethttp.StatusNotFound)
}

func (s *BackupsTestSuite) TestAnUploadedMySQLDumpIsRestoredDecompressed() {
	orders := s.templateApp("orders", "mysql")
	s.scriptRestore(mysql974, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)

	id := s.upload(b, orders, "orders.sql.gz", gzipText(mysqlDumpText))
	preview := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", orders.ID, id), nethttp.StatusOK)
	s.Equal("orders", preview["database"])
	s.Equal("9.7.4", preview["server_version"])
	s.Nil(preview["started_at"], "mysqldump says when it finished, at the end")
	s.Equal(false, preview["atomic"])

	s.restore(b, orders, map[string]any{"upload": id, "safety": false, "confirm": "orders"})
	services.WaitForRestores()
	rec := s.restores(orders.ID)[0]
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.Nil(rec.SafetyBackupID)
	s.Equal([]string{mysqlDumpText}, s.restoredSoFar())

	// As gunzipped to read it.
	id = s.upload(b, orders, "orders.sql", []byte(mysqlDumpText))
	s.restore(b, orders, map[string]any{"upload": id, "confirm": "orders"})
	services.WaitForRestores()
	s.Equal(models.RestoreSucceeded, s.restores(orders.ID)[1].Status)
	s.Equal([]string{mysqlDumpText, mysqlDumpText}, s.restoredSoFar())
}

func (s *BackupsTestSuite) TestAnUploadThatCantRestoreIntoTheAppIsRefused() {
	orders := s.templateApp("orders", "mysql")
	s.scriptRestore("postgres (PostgreSQL) 17.11 (Debian 17.11-1.pgdg13+2)\n", "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)

	refusal := func(app *models.App, name string, file []byte) string {
		id := s.upload(b, app, name, file)
		return s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", app.ID, id), nethttp.StatusUnprocessableEntity)["error"].(string)
	}
	s.Contains(refusal(s.app, "shop.sql.gz", gzipText("--\n-- PostgreSQL database dump\n--\n")), "This is plain SQL from pg_dump.")
	s.Equal("This is a dump of a MySQL database, and shop-db is PostgreSQL.", refusal(s.app, "orders.sql.gz", gzipText(mysqlDumpText)))
	s.Equal("This is a dump of a PostgreSQL database, and orders is MySQL.", refusal(orders, "shop.dump", pgDump("18.6", "shop", "")))
	s.Contains(refusal(s.app, "notes.txt", []byte("hello")), "can't tell what this file is")

	id := s.upload(b, s.app, "shop.dump", pgDump("18.6", "shop", "data"))
	preview := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", s.app.ID, id), nethttp.StatusOK)
	s.Equal("The backup is from PostgreSQL 18.6, and shop-db runs 17.11. A backup never restores into an older server.", preview["refusal"])

	// pg_dump 18 writes archives PostgreSQL 17's pg_restore reads, so a
	// 17 server dumped with it is fine. 16's isn't.
	id = s.upload(b, s.app, "shop.dump", pgDump("17.2", "shop", "data"))
	s.Empty(s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", s.app.ID, id), nethttp.StatusOK)["refusal"])
	s.scriptRestore("postgres (PostgreSQL) 16.4\n", "", 0, nil)
	id = s.upload(b, s.app, "shop.dump", pgDump("16.4", "shop", "data"))
	s.Contains(s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", s.app.ID, id), nethttp.StatusOK)["refusal"],
		"This dump was made by pg_dump 18.6, in an archive format (1.16) that PostgreSQL 16's pg_restore can't read.")
	s.scriptRestore("postgres (PostgreSQL) 17.11 (Debian 17.11-1.pgdg13+2)\n", "", 0, nil)
	id = s.upload(b, s.app, "shop.dump", pgDump("18.6", "shop", "data"))
	s.restore(b, s.app, map[string]any{"upload": id, "confirm": "shop-db"})
	s.Equal(preview["refusal"], s.restoreError(b, s.app, "source"))
	s.Empty(s.restores(s.app.ID))
}

func (s *BackupsTestSuite) TestUploadChunksCarryOnFromWhereTheServerHasGotTo() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	id := s.startUpload(b, s.app, "shop.dump", 10)

	s.Equal(float64(5), s.putChunk(b, s.app, id, 0, []byte("PGDMP"), nethttp.StatusOK)["received"])

	// Sent again after its response was lost.
	s.Equal(float64(5), s.putChunk(b, s.app, id, 0, []byte("PGDMP"), nethttp.StatusConflict)["received"])
	s.Equal(float64(5), s.putChunk(b, s.app, id, 7, []byte("abc"), nethttp.StatusConflict)["received"])

	s.putChunk(b, s.app, id, 5, []byte("too long"), nethttp.StatusBadRequest)
	s.putChunk(b, s.app, id, 5, make([]byte, services.UploadChunk+1), nethttp.StatusRequestEntityTooLarge)

	resp := b.send(nethttp.MethodPut, fmt.Sprintf("/apps/%d/restores/uploads/%s?offset=5", s.app.ID, id), strings.NewReader(`{"a":1}`),
		map[string]string{"Content-Type": "application/json", "X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie)})
	resp.AssertStatus(nethttp.StatusUnsupportedMediaType)
	resp = b.send(nethttp.MethodPut, fmt.Sprintf("/apps/%d/restores/uploads/%s?offset=5", s.app.ID, id), strings.NewReader("12345"),
		map[string]string{"Content-Type": "application/octet-stream"})
	resp.AssertStatus(419)

	s.Equal(float64(10), s.putChunk(b, s.app, id, 5, []byte("12345"), nethttp.StatusOK)["received"])

	// An upload belongs to its app.
	s.putChunk(b, s.templateApp("other-db", "postgres"), id, 10, []byte("x"), nethttp.StatusNotFound)
}

// Chunks never save the session, which ends two hours after its file was
// last written. The upload's status, in the web group, writes it.
func (s *BackupsTestSuite) TestAnUploadsStatusKeepsTheSessionAlive() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	id := s.startUpload(b, s.app, "shop.dump", 10)
	s.putChunk(b, s.app, id, 0, []byte("PGDMP"), nethttp.StatusOK)
	session := filepath.Join(facades.Config().GetString("session.files"), b.cookieValue(facades.Config().GetString("session.cookie")))
	idle := func(d time.Duration) {
		then := time.Now().Add(-d)
		s.Require().NoError(os.Chtimes(session, then, then))
	}

	idle(100 * time.Minute)
	s.putChunk(b, s.app, id, 5, []byte("12"), nethttp.StatusOK)
	s.Equal(float64(7), s.getJSON(b, fmt.Sprintf("/apps/%d/restores/uploads/%s", s.app.ID, id), nethttp.StatusOK)["received"])
	written, err := os.Stat(session)
	s.Require().NoError(err)
	s.WithinDuration(time.Now(), written.ModTime(), time.Minute, "the status saved the session")

	idle(121 * time.Minute)
	s.putChunk(b, s.app, id, 7, []byte("345"), nethttp.StatusUnauthorized)
}

func (s *BackupsTestSuite) TestUploadsNeedASignedInUser() {
	b := newBrowser(s.T(), &s.TestCase)
	resp := b.send(nethttp.MethodPut, fmt.Sprintf("/apps/%d/restores/uploads/abc?offset=0", s.app.ID), strings.NewReader("PGDMP"),
		map[string]string{"Content-Type": "application/octet-stream"})
	resp.AssertStatus(nethttp.StatusUnauthorized)
	b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/restores/uploads", s.app.ID), map[string]any{"name": "a", "size": 1}, false,
		map[string]string{"Accept": "application/json"}).AssertStatus(419)
}

func (s *BackupsTestSuite) TestAnUploadNeedsRoomOnTheDisk() {
	free := services.DiskFree
	s.T().Cleanup(func() { services.DiskFree = free })
	services.DiskFree = func(string) (uint64, error) { return 1536 << 20, nil }
	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))

	resp := b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/restores/uploads", s.app.ID),
		map[string]any{"name": "big.dump", "size": 600 << 20}, false,
		map[string]string{"Accept": "application/json", "X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie)})
	resp.AssertStatus(nethttp.StatusUnprocessableEntity)
	s.Equal("The server has 1.5 GiB of disk free. This 600 MiB file needs that much and 1.0 GiB to spare.", must(resp.Json())["error"])
	s.Empty(uploadsLeft())

	s.startUpload(b, s.app, "small.dump", 400<<20)

	// That upload will take 400 MiB more of the disk.
	orders := s.templateApp("orders", "mysql")
	resp = b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/restores/uploads", orders.ID),
		map[string]any{"name": "orders.sql.gz", "size": 200 << 20}, false,
		map[string]string{"Accept": "application/json", "X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie)})
	resp.AssertStatus(nethttp.StatusUnprocessableEntity)
	s.Equal("The server has 1.5 GiB of disk free, and uploads to other apps still need 400 MiB of it. "+
		"This 200 MiB file needs that much and 1.0 GiB to spare.", must(resp.Json())["error"])
	s.startUpload(b, orders, "orders.sql.gz", 100<<20)
}

func (s *BackupsTestSuite) TestARestoreWaitsForTheWholeFile() {
	s.scriptRestore(postgres186, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)
	file := pgDump("18.6", "shop", strings.Repeat("x", 100<<10))
	b.visit(fmt.Sprintf("/apps/%d?tab=backups", s.app.ID))
	id := s.startUpload(b, s.app, "shop.dump", len(file))
	s.putChunk(b, s.app, id, 0, file[:64<<10], nethttp.StatusOK)

	s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", s.app.ID, id), nethttp.StatusOK)
	s.restore(b, s.app, map[string]any{"upload": id, "confirm": "shop-db"})
	s.Equal("The file hasn't finished uploading.", s.restoreError(b, s.app, "source"))
	s.Empty(s.restores(s.app.ID))

	s.putChunk(b, s.app, id, 64<<10, file[64<<10:], nethttp.StatusOK)
	s.restore(b, s.app, map[string]any{"upload": id, "confirm": "shop-db"})
	services.WaitForRestores()
	s.Equal(models.RestoreSucceeded, s.restores(s.app.ID)[0].Status)
}

func (s *BackupsTestSuite) TestARestoreRefusedWhileTheAppIsHeldKeepsTheUpload() {
	s.scriptRestore(postgres186, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)
	id := s.upload(b, s.app, "shop.dump", pgDump("18.6", "shop", "data"))

	unlock, _, ok := services.LockApp(s.app.ID, services.WorkExport)
	s.Require().True(ok)
	s.restore(b, s.app, map[string]any{"upload": id, "confirm": "shop-db"})
	s.Equal("An export of shop-db is running. Restore once it has finished.", s.restoreError(b, s.app, "source"))
	unlock()
	s.Equal([]string{id}, uploadsLeft())

	s.restore(b, s.app, map[string]any{"upload": id, "confirm": "shop-db"})
	services.WaitForRestores()
	s.Equal(models.RestoreSucceeded, s.restores(s.app.ID)[0].Status)
}

func (s *BackupsTestSuite) TestASafetyBackupOfAnUploadNeedsALocation() {
	s.scriptRestore(postgres186, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)
	id := s.upload(b, s.app, "shop.dump", pgDump("18.6", "shop", "data"))

	s.restore(b, s.app, map[string]any{"upload": id, "safety": true, "safety_location": 999, "confirm": "shop-db"})
	s.Equal("Choose where the safety backup goes.", s.restoreError(b, s.app, "safety_location"))
	s.Empty(s.restores(s.app.ID))
}

func (s *BackupsTestSuite) TestAnUploadIsDeletedWhenCancelledReplacedOrLeft() {
	b := signedIn(s.T(), &s.TestCase)
	first := s.upload(b, s.app, "shop.dump", []byte("PGDMP"))
	b.request(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/restores/uploads/%s", s.app.ID, first), nil, false,
		map[string]string{"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie)}).AssertNoContent()
	s.Empty(uploadsLeft())
	s.putChunk(b, s.app, first, 5, []byte("x"), nethttp.StatusNotFound)

	second := s.upload(b, s.app, "shop.dump", []byte("PGDMP"))
	third := s.upload(b, s.app, "shop.dump", []byte("PGDMP"))
	s.Equal([]string{third}, uploadsLeft(), "an app has one upload at most")
	s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?upload=%s", s.app.ID, second), nethttp.StatusUnprocessableEntity)

	idle := services.UploadIdle
	s.T().Cleanup(func() { services.UploadIdle = idle })
	services.UploadIdle = 50 * time.Millisecond
	s.upload(b, s.app, "shop.dump", []byte("PGDMP"))
	s.Eventually(func() bool { return len(uploadsLeft()) == 0 }, 2*time.Second, 10*time.Millisecond, "an upload left alone is deleted")
}

func (s *BackupsTestSuite) TestUploadsAnEarlierEchowaLeftAreRemoved() {
	b := signedIn(s.T(), &s.TestCase)
	id := s.upload(b, s.app, "shop.dump", []byte("PGDMP"))
	left := filepath.Join(services.UploadDir(), "0123abcd")
	s.Require().NoError(os.WriteFile(left, []byte("left"), 0o600))
	before := time.Now().Add(-time.Hour)
	s.Require().NoError(os.Chtimes(left, before, before))

	s.Require().NoError(services.RemoveUploads())
	s.Equal([]string{id}, uploadsLeft(), "this Echowa's upload stays")
}

// The cleanup at boot runs while requests are served, so a restore may
// have taken an upload by then.
func (s *BackupsTestSuite) TestTheCleanupAtBootKeepsAFileARestoreHasTaken() {
	s.scriptRestore(postgres186, "PGDMP replaced", 0, nil)
	safetyDump := s.docker.ExecStreamFunc
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, errs io.Writer) (int, error) {
		if stdin == nil {
			s.Require().NoError(services.RemoveUploads())
		}
		return safetyDump(ctx, service, cmd, stdin, stdout, errs)
	}
	b := signedIn(s.T(), &s.TestCase)
	file := pgDump("18.6", "shop", "data")
	id := s.upload(b, s.app, "shop.dump", file)

	s.restore(b, s.app, map[string]any{"upload": id, "safety": true, "safety_location": s.loc.ID, "confirm": "shop-db"})
	services.WaitForRestores()
	rec := s.restores(s.app.ID)[0]
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.Equal([]string{string(file)}, s.restoredSoFar())
}

// The file is checked against what arrived as the restore reads it, and
// its end is held back until it matches.
func (s *BackupsTestSuite) TestAnUploadThatChangesOnDiskIsntRestoredWhole() {
	s.scriptRestore(postgres186, "", 0, nil)
	b := signedIn(s.T(), &s.TestCase)
	file := pgDump("18.6", "shop", strings.Repeat("y", 200<<10))
	id := s.upload(b, s.app, "shop.dump", file)
	changed := append([]byte{}, file...)
	changed[len(changed)-1] ^= 0xff
	s.Require().NoError(os.WriteFile(filepath.Join(services.UploadDir(), id), changed, 0o600))

	s.restore(b, s.app, map[string]any{"upload": id, "confirm": "shop-db"})
	services.WaitForRestores()
	rec := s.restores(s.app.ID)[0]
	s.Equal(models.RestoreFailed, rec.Status)
	s.Contains(*rec.Message, "The uploaded file changed on the server's disk")
	s.Less(len(s.restoredSoFar()[0]), len(file), "the restore never read the end")
	s.Empty(uploadsLeft())
}
