package services

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"goravel/app/docker"
	"goravel/app/models"
	"goravel/app/templates"
)

// exportHold is how much of a dump is held back before the download
// starts, so one that fails sooner gets an error the page shows.
const exportHold = 32 << 10

// ExportStall is how long the browser can stop taking the download, paused
// or stuck, before the export ends and frees the app. Tests shorten it.
var ExportStall = time.Minute

var (
	errExportStalled  = errors.New("the browser stopped taking the download")
	errExportTimedOut = fmt.Errorf("stopped after %d hours", int(BackupTimeout/time.Hour))
)

// ExportBusy means other work holds the app's lock, so the export didn't
// start.
type ExportBusy struct {
	Holder AppWork
}

func (e *ExportBusy) Error() string {
	return e.Holder.Describe() + " of the app is running"
}

// Export is an app's database as it's dumped, for the browser to download
// (ADR 0034). Nothing is kept: no record, no object, nothing on disk. It
// holds the app's lock until the dump ends.
type Export struct {
	// Filename is what the browser saves it as.
	Filename string

	ctx   context.Context
	first []byte
	body  *io.PipeReader
	err   error
	done  chan struct{}
	// stall ends the export once the browser has taken nothing for
	// ExportStall. It runs only between reads: a dump that's slow to write
	// is waited for.
	stall *time.Timer
}

// StartExport takes the app's lock and starts dumping its database in
// format. It returns once the dump has written exportHold or has ended, so
// a dump that fails before then is an error here, which the page shows,
// rather than a broken download. Close the Export when done with it.
func StartExport(ctx context.Context, app *models.App, format templates.Export, now time.Time) (*Export, error) {
	unlock, holder, ok := LockApp(app.ID, WorkExport)
	if !ok {
		return nil, &ExportBusy{Holder: holder}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, BackupTimeout, errExportTimedOut)
	pr, pw := io.Pipe()
	// A dump blocked writing to a browser that's gone stops here.
	stop := context.AfterFunc(ctx, func() { pr.CloseWithError(context.Cause(ctx)) })
	e := &Export{
		Filename: fmt.Sprintf("%s-%s.%s", app.Name, now.UTC().Format("20060102T150405Z"), format.Extension),
		ctx:      ctx,
		body:     pr,
		done:     make(chan struct{}),
	}
	go func() {
		defer close(e.done)
		defer unlock()
		defer cancel(nil)
		defer cancelTimeout()
		err := exportDump(ctx, app.ServiceName(), format, pw)
		// Closing the reader now would turn the end of the dump into
		// ErrClosedPipe.
		stop()
		pw.CloseWithError(err)
	}()

	first := make([]byte, exportHold)
	n, err := io.ReadAtLeast(pr, first, 1)
	if err != nil {
		_ = e.Close()
		if errors.Is(err, io.EOF) {
			err = errors.New("the dump wrote nothing")
		}
		return nil, err
	}
	e.first = first[:n]
	e.stall = time.AfterFunc(ExportStall, func() { cancel(errExportStalled) })

	return e, nil
}

// exportDump runs the format's dump into w, compressed when it says,
// holding back the first exportHold. The held part and the end of a gzip
// stream, its checksum, are written only when the dump succeeds.
func exportDump(ctx context.Context, service string, format templates.Export, w io.Writer) error {
	held := bufio.NewWriterSize(w, exportHold)
	var out io.Writer = held
	var gz *gzip.Writer
	if format.Gzip {
		gz = gzip.NewWriter(held)
		out = gz
	}
	stderr := &docker.TailBuffer{Limit: MaxBackupOutput}
	code, err := Docker().ExecStream(ctx, service, format.Dump, nil, out, stderr)
	switch {
	case err != nil:
		return err
	case code != 0:
		return &docker.ExecError{ExitCode: code, Stderr: strings.TrimSpace(stderr.String())}
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return err
		}
	}

	return held.Flush()
}

func (e *Export) Read(p []byte) (int, error) {
	if len(e.first) > 0 {
		n := copy(p, e.first)
		e.first = e.first[n:]
		return n, nil
	}
	e.stall.Stop()
	defer e.stall.Reset(ExportStall)
	n, err := e.body.Read(p)
	// The reader is closed only when the export's context ends.
	if errors.Is(err, io.ErrClosedPipe) {
		err = context.Cause(e.ctx)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		e.err = err
	}

	return n, err
}

// Err is why the dump failed after StartExport returned, or nil. A failed
// write to the browser isn't one.
func (e *Export) Err() error {
	return e.err
}

// Close stops the dump if it's still going and waits for it to end, which
// releases the app's lock.
func (e *Export) Close() error {
	if e.stall != nil {
		e.stall.Stop()
	}
	_ = e.body.Close()
	<-e.done

	return nil
}
