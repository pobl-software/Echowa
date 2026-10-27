// Package sse writes Server-Sent Events responses.
package sse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
)

// Event is one Server-Sent Event.
type Event struct {
	Name string // the event type the page listens for
	// ID, when set, is what the browser sends back as Last-Event-ID when it
	// reconnects, so the stream can carry on after it.
	ID   string
	Data any // sent as JSON
}

const (
	// flushEvery batches writes: a busy log sends many lines per flush.
	flushEvery = 100 * time.Millisecond
	// keepAlive is how long a stream may stay silent before it sends a
	// comment, so proxies don't close it as idle.
	keepAlive = 15 * time.Second
	// retry is how long the browser waits before reconnecting.
	retry = 2 * time.Second
)

// MaxLifetime ends every stream after a while, without an "end" event. The
// browser reconnects and resumes from Last-Event-ID, and the new request
// passes AuthenticateStream again, so a stream outlives a logout or an
// expired session by at most this long. Tests shorten it.
var MaxLifetime = 15 * time.Minute

// Serve answers with an event stream. run sends events until it returns or
// ctx ends, which happens when the client disconnects.
//
// The route must skip Goravel's global Timeout middleware, which buffers
// the whole response (routes/streams.go).
func Serve(ctx http.Context, run func(ctx context.Context, send func(Event) error) error) http.Response {
	ctx.Response().Header("Content-Type", "text/event-stream")
	ctx.Response().Header("Cache-Control", "no-cache")
	ctx.Response().Header("X-Accel-Buffering", "no")

	return ctx.Response().Stream(http.StatusOK, func(w http.StreamWriter) error {
		reqCtx := ctx.Request().Origin().Context()
		path := ctx.Request().Path()
		started := time.Now()
		facades.Log().Infof("sse: %s opened", path)

		s := &stream{w: w, lastWrite: started}
		if err := s.write(fmt.Appendf(nil, "retry: %d\n\n", retry.Milliseconds())); err != nil {
			return nil
		}
		// Send the headers now, so the browser's EventSource opens before
		// the first event.
		s.flush()

		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() { s.flushLoop(done) })

		runCtx, cancel := context.WithTimeout(reqCtx, MaxLifetime)
		defer cancel()
		err := run(runCtx, s.send)

		close(done)
		wg.Wait()
		s.flush()

		took := time.Since(started).Round(time.Second)
		switch {
		case reqCtx.Err() != nil:
			facades.Log().Infof("sse: %s closed after %s: the client went away", path, took)
		case runCtx.Err() != nil:
			facades.Log().Infof("sse: %s closed after %s, so the browser reconnects and signs in again", path, took)
		case err != nil && !errors.Is(err, errWrite):
			facades.Log().Errorf("sse: %s failed after %s: %v", path, took, err)
		default:
			facades.Log().Infof("sse: %s finished after %s", path, took)
		}

		return nil
	})
}

var errWrite = errors.New("sse: write failed")

// stream serialises writes from run and from the flush loop.
type stream struct {
	mu        sync.Mutex
	w         http.StreamWriter
	dirty     bool
	lastWrite time.Time
	failed    bool
}

func (s *stream) send(e Event) error {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("sse: event %s: %w", e.Name, err)
	}

	var b bytes.Buffer
	if e.ID != "" {
		b.WriteString("id: " + e.ID + "\n")
	}
	b.WriteString("event: " + e.Name + "\n")
	b.WriteString("data: ")
	b.Write(data) // JSON has no raw newlines, so one data line is enough
	b.WriteString("\n\n")

	return s.write(b.Bytes())
}

func (s *stream) write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return errWrite
	}
	if _, err := s.w.Write(p); err != nil {
		s.failed = true
		return errWrite
	}
	s.dirty = true
	s.lastWrite = time.Now()

	return nil
}

func (s *stream) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty && !s.failed {
		_ = s.w.Flush()
		s.dirty = false
	}
}

func (s *stream) flushLoop(done <-chan struct{}) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.mu.Lock()
			idle := time.Since(s.lastWrite) >= keepAlive
			s.mu.Unlock()
			if idle {
				_ = s.write([]byte(": keep-alive\n\n"))
			}
			s.flush()
		}
	}
}
