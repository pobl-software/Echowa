package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moby/moby/api/pkg/stdcopy"
)

var errStopped = errors.New("log reader stopped")

// readLogs demultiplexes a Docker log stream (requested with timestamps)
// into lines. stdcopy calls the two writers in stream order on this
// goroutine, so lines come out in the order Docker sent them. The stream is
// closed when iteration ends.
func readLogs(ctx context.Context, stream io.ReadCloser) iter.Seq2[LogLine, error] {
	return func(yield func(LogLine, error) bool) {
		defer stream.Close()

		reader := &logReader{yield: yield}
		stdout := &lineWriter{reader: reader, stream: Stdout}
		stderr := &lineWriter{reader: reader, stream: Stderr}
		_, err := stdcopy.StdCopy(stdout, stderr, stream)
		if reader.stopped {
			return
		}
		if !stdout.flush() || !stderr.flush() {
			return
		}
		if err != nil && ctx.Err() == nil {
			yield(LogLine{}, err)
		}
	}
}

type logReader struct {
	yield   func(LogLine, error) bool
	stopped bool
}

func (r *logReader) emit(stream Stream, raw []byte) bool {
	if r.stopped {
		return false
	}
	if !r.yield(parseLogLine(stream, raw), nil) {
		r.stopped = true
	}

	return !r.stopped
}

// maxLineBytes is the longest log line Echowa passes on in one piece.
const maxLineBytes = 64 << 10

// lineWriter buffers one stream until a newline, since Docker splits
// messages longer than 16 KiB across frames.
type lineWriter struct {
	reader *logReader
	stream Stream
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		if !w.emit(line) {
			return 0, errStopped
		}
	}
	// An app that never writes a newline would otherwise grow the buffer
	// without limit.
	if len(w.buf) > maxLineBytes {
		line := w.buf
		w.buf = nil
		if !w.emit(line) {
			return 0, errStopped
		}
	}

	return len(p), nil
}

func (w *lineWriter) flush() bool {
	if len(w.buf) == 0 {
		return true
	}
	line := w.buf
	w.buf = nil

	return w.emit(line)
}

// emit passes line on in pieces of at most maxLineBytes, cut at character
// boundaries.
func (w *lineWriter) emit(line []byte) bool {
	for len(line) > maxLineBytes {
		cut := maxLineBytes
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if !w.reader.emit(w.stream, line[:cut]) {
			return false
		}
		line = line[cut:]
	}

	return w.reader.emit(w.stream, line)
}

// parseLogLine splits "<RFC3339Nano timestamp> <details> <text>". The
// details are Docker's comma-separated key=value attributes, Swarm's task
// ID among them.
func parseLogLine(stream Stream, raw []byte) LogLine {
	text := strings.TrimSuffix(string(raw), "\r")
	line := LogLine{Stream: stream, Text: text}
	stamp, rest, found := strings.Cut(text, " ")
	if !found {
		return line
	}
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return line
	}
	line.Time, line.Text = t, rest

	details, rest, _ := strings.Cut(rest, " ")
	if task := detail(details, "com.docker.swarm.task.id"); task != "" {
		line.Task, line.Text = task, rest
	}

	return line
}

// detail is key's value in a line's details, or "" if it isn't there.
func detail(details, key string) string {
	for attr := range strings.SplitSeq(details, ",") {
		if k, v, ok := strings.Cut(attr, "="); ok && k == key {
			return v
		}
	}

	return ""
}
