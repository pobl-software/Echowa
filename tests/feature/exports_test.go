package feature

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"time"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
)

func exportURL(app *models.App, format string) string {
	url := fmt.Sprintf("/apps/%d/export", app.ID)
	if format != "" {
		url += "?format=" + format
	}

	return url
}

func gunzip(s *BackupsTestSuite, body string) string {
	zr, err := gzip.NewReader(strings.NewReader(body))
	s.Require().NoError(err)
	out, err := io.ReadAll(zr)
	s.Require().NoError(err)

	return string(out)
}

func (s *BackupsTestSuite) TestExportStreamsTheDumpWithNoStorageAndNothingKept() {
	_, err := facades.Orm().Query().Delete(&s.loc)
	s.Require().NoError(err)
	const dump = "PGDMP a whole dump"
	s.script("", dump, 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, exportURL(s.app, ""), nil, false, nil)

	resp.AssertOk()
	disposition := resp.Headers().Get("Content-Disposition")
	s.Regexp(`^attachment; filename="shop-db-\d{8}T\d{6}Z\.dump"$`, disposition)
	resp.AssertHeader("Cache-Control", "no-store")
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal(dump, body, "the custom format, as the dump wrote it")
	tmpl, _ := templates.Find("postgres")
	s.Equal(append([]string{s.app.ServiceName()}, tmpl.Backup.Dump...), s.dumps[0], "the same dump a backup takes")

	count, err := facades.Orm().Query().Model(&models.Backup{}).Count()
	s.Require().NoError(err)
	s.Zero(count, "no backup record")
	s.Empty(s.storage.Keys(), "nothing in a bucket")
	s.Empty(services.AppBusy(s.app.ID), "the lock is released")
}

func (s *BackupsTestSuite) TestExportPlainSQLIsGzippedAndPortable() {
	const dump = "--\n-- PostgreSQL database dump\n--\nCREATE TABLE kept (id int);\n"
	s.script("", dump, 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, exportURL(s.app, "sql"), nil, false, nil)

	resp.AssertOk()
	s.Regexp(`filename="shop-db-\d{8}T\d{6}Z\.sql\.gz"$`, resp.Headers().Get("Content-Disposition"))
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal(dump, gunzip(s, body))
	cmd := strings.Join(s.dumps[0], " ")
	s.Contains(cmd, "pg_dump")
	s.NotContains(cmd, "-Fc")
	s.Contains(cmd, "--no-owner --no-privileges", "loads as whoever runs it")
}

func (s *BackupsTestSuite) TestExportMySQLIsGzippedSQL() {
	app := s.templateApp("shop-mysql", "mysql")
	const dump = "-- MySQL dump 10.13\nCREATE TABLE kept (id int);\n"
	s.script("", dump, 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", app.ID))
	exports := page.Props["backups"].(map[string]any)["exports"].([]any)
	s.Len(exports, 1)
	s.Equal("sql", exports[0].(map[string]any)["format"])

	resp := b.request(nethttp.MethodGet, exportURL(app, ""), nil, false, nil)

	resp.AssertOk()
	s.Regexp(`filename="shop-mysql-\d{8}T\d{6}Z\.sql\.gz"$`, resp.Headers().Get("Content-Disposition"))
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal(dump, gunzip(s, body))
	s.Contains(strings.Join(s.dumps[0], " "), "mysqldump")
}

func (s *BackupsTestSuite) TestExportAnswers404WithoutABackupOrFormat() {
	redis := s.templateApp("cache", "redis")
	custom := createTestApp(s.T(), "blog", nil)
	s.script("", "dump", 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)

	b.request(nethttp.MethodGet, exportURL(redis, ""), nil, false, nil).AssertNotFound()
	b.request(nethttp.MethodGet, exportURL(custom, ""), nil, false, nil).AssertNotFound()
	b.request(nethttp.MethodGet, exportURL(s.app, "tar"), nil, false, nil).AssertNotFound()
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestExportNeedsASignedInUser() {
	s.script("", "dump", 0, "", nil)

	resp := newBrowser(s.T(), &s.TestCase).request(nethttp.MethodGet, exportURL(s.app, ""), nil, false, nil)

	resp.AssertUnauthorized()
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestExportIsRefusedWhileTheAppIsHeld() {
	s.script("", "dump", 0, "", nil)
	release, _, ok := services.LockApp(s.app.ID, services.WorkDeploy)
	s.Require().True(ok)
	defer release()
	b := signedIn(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, exportURL(s.app, ""), nil, false, nil)

	resp.AssertConflict()
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal("A deploy of shop-db is running. Export once it has finished.", body)
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestExportOfAnAppWithNoRunningTask() {
	b := signedIn(s.T(), &s.TestCase)

	resp := b.request(nethttp.MethodGet, exportURL(s.app, ""), nil, false, nil)

	resp.AssertServiceUnavailable()
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Equal("shop-db has no running task to export from.", body)
	s.Empty(services.AppBusy(s.app.ID))
}

func (s *BackupsTestSuite) TestExportThatFailsBeforeWritingShowsWhy() {
	s.script("", "", 1, "pg_dump: error: connection to server failed\n", nil)
	b := signedIn(s.T(), &s.TestCase)

	for _, format := range []string{"custom", "sql"} {
		resp := b.request(nethttp.MethodGet, exportURL(s.app, format), nil, false, nil)

		resp.AssertStatus(nethttp.StatusBadGateway)
		s.Empty(resp.Headers().Get("Content-Disposition"), format)
		body, err := resp.Content()
		s.Require().NoError(err)
		s.Equal("The dump exited with status 1.\n\npg_dump: error: connection to server failed", body, format)
		s.Empty(services.AppBusy(s.app.ID), format)
	}
}

func (s *BackupsTestSuite) TestExportThatFailsPartWayCutsTheDownloadOff() {
	s.script("", "PGDMP"+strings.Repeat("x", 64<<10), 1, "pg_dump: error: Dumping the contents of table \"kept\" failed\n", nil)
	b := signedIn(s.T(), &s.TestCase)

	s.PanicsWithValue(nethttp.ErrAbortHandler, func() {
		b.request(nethttp.MethodGet, exportURL(s.app, ""), nil, false, nil)
	}, "the response ends without its last chunk, so the browser marks the download failed")
	s.Empty(services.AppBusy(s.app.ID))
}

func (s *BackupsTestSuite) TestAGzippedExportThatFailsPartWayHasNoChecksum() {
	// Random bytes don't compress, so more than the held back part is sent.
	written := make([]byte, 256<<10)
	_, _ = rand.Read(written)
	s.script("", string(written), 1, "", nil)
	tmpl, _ := templates.Find("postgres")
	format, ok := tmpl.Backup.FindExport("sql")
	s.Require().True(ok)

	export, err := services.StartExport(context.Background(), s.app, format, time.Now())
	s.Require().NoError(err, "the download started")
	got, err := io.ReadAll(export)
	s.Require().Error(err)
	s.Require().NoError(export.Close())
	s.Require().Error(export.Err())

	zr, err := gzip.NewReader(bytes.NewReader(got))
	s.Require().NoError(err)
	_, err = io.ReadAll(zr)
	s.ErrorIs(err, io.ErrUnexpectedEOF, "a cut-off .sql.gz fails to decompress")
}

func (s *BackupsTestSuite) TestAnExportHoldsTheAppUntilItEnds() {
	gate := make(chan struct{})
	start := "PGDMP" + strings.Repeat("x", 64<<10)
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		if _, err := io.WriteString(stdout, start); err != nil {
			return 0, err
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		_, err := io.WriteString(stdout, " the rest")
		return 0, err
	}
	tmpl, _ := templates.Find("postgres")
	format, _ := tmpl.Backup.FindExport("")

	export, err := services.StartExport(context.Background(), s.app, format, time.Now())
	s.Require().NoError(err)
	s.Equal(services.WorkExport, services.AppBusy(s.app.ID))

	_, err = services.StartExport(context.Background(), s.app, format, time.Now())
	var busy *services.ExportBusy
	s.Require().ErrorAs(err, &busy)
	s.Equal(services.WorkExport, busy.Holder)

	b := signedIn(s.T(), &s.TestCase)
	b.visit(fmt.Sprintf("/apps/%d?tab=danger", s.app.ID))
	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d", s.app.ID), fmt.Sprintf("/apps/%d?tab=danger", s.app.ID), nil)
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=danger", s.app.ID))
	s.Equal("An export of shop-db is running. Wait for it to finish, then delete the app.", page.Flash["error"])

	close(gate)
	body, err := io.ReadAll(export)
	s.Require().NoError(err)
	s.Equal(start+" the rest", string(body))
	s.Require().NoError(export.Close())
	s.NoError(export.Err())
	s.Empty(services.AppBusy(s.app.ID))
}

func (s *BackupsTestSuite) TestClosingAnExportStopsTheDump() {
	stopped := make(chan error, 1)
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		chunk := bytes.Repeat([]byte("x"), 1<<10)
		for {
			if _, err := stdout.Write(chunk); err != nil {
				stopped <- err
				return 0, err
			}
		}
	}
	tmpl, _ := templates.Find("postgres")
	format, _ := tmpl.Backup.FindExport("")

	export, err := services.StartExport(context.Background(), s.app, format, time.Now())
	s.Require().NoError(err)
	s.Require().NoError(export.Close(), "as when the browser goes away")

	s.Error(<-stopped, "the dump's next write failed")
	s.Empty(services.AppBusy(s.app.ID))
}

func (s *BackupsTestSuite) TestExportAnswersHeadWithoutADump() {
	s.script("", "dump", 0, "", nil)
	b := signedIn(s.T(), &s.TestCase)

	b.request(nethttp.MethodHead, exportURL(s.app, ""), nil, false, nil).AssertStatus(nethttp.StatusMethodNotAllowed)

	s.Zero(s.dumpCount())
}

// liveHeap is the heap still in use after a collection, in MiB.
func liveHeap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return m.HeapInuse >> 20
}

func (s *BackupsTestSuite) TestStreamsKeepNoCopyOfWhatTheySent() {
	const total = 128 << 20
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		chunk := make([]byte, 32<<10)
		for range total / len(chunk) {
			if _, err := stdout.Write(chunk); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}
	b := signedIn(s.T(), &s.TestCase)
	// A real server: the test client's recorder keeps the body itself.
	srv := httptest.NewServer(facades.Route())
	defer srv.Close()
	req, err := nethttp.NewRequest(nethttp.MethodGet, srv.URL+exportURL(s.app, ""), nil)
	s.Require().NoError(err)
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	before := liveHeap()

	resp, err := nethttp.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Require().Equal(nethttp.StatusOK, resp.StatusCode)
	_, err = io.CopyN(io.Discard, resp.Body, total*3/4)
	s.Require().NoError(err)
	during := liveHeap()
	rest, err := io.Copy(io.Discard, resp.Body)
	s.Require().NoError(err)

	s.EqualValues(total/4, rest)
	s.Less(during, before+32, "96 MiB sent, and Echowa's heap grew from %d MiB to %d MiB", before, during)
}

func (s *BackupsTestSuite) TestAnExportTheBrowserStopsTakingEnds() {
	services.ExportStall = 100 * time.Millisecond
	s.T().Cleanup(func() { services.ExportStall = time.Minute })
	stopped := make(chan error, 1)
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for {
			if _, err := stdout.Write(chunk); err != nil {
				stopped <- err
				return 0, err
			}
		}
	}
	tmpl, _ := templates.Find("postgres")
	format, _ := tmpl.Backup.FindExport("")

	export, err := services.StartExport(context.Background(), s.app, format, time.Now())
	s.Require().NoError(err)
	_, err = export.Read(make([]byte, 32<<10))
	s.Require().NoError(err)

	s.Error(<-stopped, "the dump stopped while nothing read it")
	s.Eventually(func() bool { return services.AppBusy(s.app.ID) == "" }, time.Second, 10*time.Millisecond, "the app is free")
	_, err = io.ReadAll(export)
	s.EqualError(err, "the browser stopped taking the download")
	s.Require().NoError(export.Close())
}

func (s *BackupsTestSuite) TestAnExportWaitsForASlowDump() {
	services.ExportStall = 100 * time.Millisecond
	s.T().Cleanup(func() { services.ExportStall = time.Minute })
	start := "PGDMP" + strings.Repeat("x", 64<<10)
	s.docker.ExecStreamFunc = func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		if _, err := io.WriteString(stdout, start); err != nil {
			return 0, err
		}
		// pg_dump reading a big table writes nothing for a while.
		time.Sleep(300 * time.Millisecond)
		_, err := io.WriteString(stdout, " the rest")
		return 0, err
	}
	tmpl, _ := templates.Find("postgres")
	format, _ := tmpl.Backup.FindExport("")

	export, err := services.StartExport(context.Background(), s.app, format, time.Now())
	s.Require().NoError(err)
	body, err := io.ReadAll(export)

	s.Require().NoError(err)
	s.Equal(start+" the rest", string(body))
	s.Require().NoError(export.Close())
}
