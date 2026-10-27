package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// muxed builds a multiplexed log stream the way the daemon sends one: each
// frame is an 8-byte header (stream type, 3 zero bytes, big-endian length)
// followed by the payload.
func muxed(t *testing.T, frames ...any) io.ReadCloser {
	t.Helper()
	var buf bytes.Buffer
	for i := 0; i < len(frames); i += 2 {
		kind := stdcopy.Stdout
		if frames[i] == Stderr {
			kind = stdcopy.Stderr
		}
		payload := frames[i+1].(string)
		header := [8]byte{byte(kind)}
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		buf.Write(header[:])
		buf.WriteString(payload)
	}

	return io.NopCloser(&buf)
}

func collect(t *testing.T, lines func(func(LogLine, error) bool)) []LogLine {
	t.Helper()
	var got []LogLine
	for line, err := range lines {
		require.NoError(t, err)
		got = append(got, line)
	}

	return got
}

func TestReadLogsDemultiplexesInOrder(t *testing.T) {
	stream := muxed(t,
		Stdout, "2026-10-23T20:16:30.123456789Z Starting up on port 80\n",
		Stderr, "2026-10-23T20:16:31Z oops\r\n",
		Stdout, "2026-10-23T20:16:32Z first half ",
		Stdout, "second half\n",
		Stdout, "no timestamp here\n",
		Stderr, "2026-10-23T20:16:33Z unterminated",
	)

	got := collect(t, readLogs(context.Background(), stream))

	assert.Equal(t, []LogLine{
		{Time: time.Date(2026, 10, 23, 20, 16, 30, 123456789, time.UTC), Stream: Stdout, Text: "Starting up on port 80"},
		{Time: time.Date(2026, 10, 23, 20, 16, 31, 0, time.UTC), Stream: Stderr, Text: "oops"},
		{Time: time.Date(2026, 10, 23, 20, 16, 32, 0, time.UTC), Stream: Stdout, Text: "first half second half"},
		{Stream: Stdout, Text: "no timestamp here"},
		{Time: time.Date(2026, 10, 23, 20, 16, 33, 0, time.UTC), Stream: Stderr, Text: "unterminated"},
	}, got)
}

func TestReadLogsTakesTheTaskFromTheDetails(t *testing.T) {
	stream := muxed(t,
		Stdout, "2026-10-27T12:37:12Z com.docker.swarm.node.id=n1,com.docker.swarm.service.id=s1,com.docker.swarm.task.id=t1 GET / 200\n",
		Stdout, "2026-10-27T12:37:13Z com.docker.swarm.task.id=t2 \n",
		Stdout, "2026-10-27T12:37:14Z key=value but no task\n",
	)

	got := collect(t, readLogs(context.Background(), stream))

	assert.Equal(t, []LogLine{
		{Time: time.Date(2026, 10, 27, 12, 37, 12, 0, time.UTC), Stream: Stdout, Text: "GET / 200", Task: "t1"},
		{Time: time.Date(2026, 10, 27, 12, 37, 13, 0, time.UTC), Stream: Stdout, Text: "", Task: "t2"},
		{Time: time.Date(2026, 10, 27, 12, 37, 14, 0, time.UTC), Stream: Stdout, Text: "key=value but no task"},
	}, got)
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestReadLogsStopsAndClosesWhenTheCallerBreaks(t *testing.T) {
	stream := &closeTracker{Reader: muxed(t, Stdout, "one\ntwo\nthree\n")}

	var got []string
	for line, err := range readLogs(context.Background(), stream) {
		require.NoError(t, err)
		got = append(got, line.Text)
		if len(got) == 2 {
			break
		}
	}

	assert.Equal(t, []string{"one", "two"}, got)
	assert.True(t, stream.closed, "breaking out must close the Docker stream")
}

func TestReadLogsReportsStreamErrors(t *testing.T) {
	broken := io.NopCloser(io.MultiReader(muxed(t, Stdout, "ok\n"), &failingReader{}))

	var lines []string
	var lastErr error
	for line, err := range readLogs(context.Background(), broken) {
		if err != nil {
			lastErr = err
			continue
		}
		lines = append(lines, line.Text)
	}

	assert.Equal(t, []string{"ok"}, lines)
	assert.ErrorContains(t, lastErr, "connection reset")
}

func TestReadLogsIsQuietAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	broken := io.NopCloser(&failingReader{})

	for _, err := range readLogs(ctx, broken) {
		t.Fatalf("expected nothing after cancel, got error %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read: connection reset by peer")
}

func TestReadLogsSplitsLinesWithoutNewlines(t *testing.T) {
	long := strings.Repeat("é", maxLineBytes) // two bytes each
	stream := muxed(t, Stdout, long+"\nshort\n")

	got := collect(t, readLogs(context.Background(), stream))

	var joined string
	for _, line := range got[:len(got)-1] {
		assert.LessOrEqual(t, len(line.Text), maxLineBytes)
		assert.True(t, utf8.ValidString(line.Text), "split at a character boundary")
		joined += line.Text
	}
	assert.Equal(t, long, joined, "nothing is lost")
	assert.Equal(t, "short", got[len(got)-1].Text)
}
