package docker

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// MaxExecOutput is the most stdout Exec keeps. A database's figures or its
// top queries are a few kilobytes.
const MaxExecOutput = 1 << 20

// maxExecStderr is how much stderr an ExecError keeps.
const maxExecStderr = 4 << 10

// ExecError is a command that exited with a status other than 0.
type ExecError struct {
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("command exited with status %d", e.ExitCode)
	}

	return fmt.Sprintf("command exited with status %d: %s", e.ExitCode, e.Stderr)
}

func (e *Engine) Exec(ctx context.Context, service string, cmd []string) ([]byte, error) {
	stdout := &cappedBuffer{limit: MaxExecOutput}
	stderr := &cappedBuffer{limit: maxExecStderr, truncate: true}
	code, err := e.exec(ctx, service, cmd, stdout, stderr)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, &ExecError{ExitCode: code, Stderr: strings.TrimSpace(stderr.String())}
	}

	return stdout.Bytes(), nil
}

func (e *Engine) ExecTo(ctx context.Context, service string, cmd []string, out io.Writer) (int, error) {
	return e.exec(ctx, service, cmd, out, out)
}

// exec runs cmd in the service's newest running task container and returns
// its exit code.
func (e *Engine) exec(ctx context.Context, service string, cmd []string, stdout, stderr io.Writer) (int, error) {
	svc, err := e.inspect(ctx, service)
	if errors.Is(err, ErrServiceNotFound) {
		return 0, fmt.Errorf("%s: %w", service, ErrNotRunning)
	}
	if err != nil {
		return 0, err
	}
	if !managed(svc) {
		return 0, fmt.Errorf("%s: %w", service, ErrNotManaged)
	}
	// Task containers carry Swarm's labels, not the service's.
	list, err := e.api.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add("label", "com.docker.swarm.service.id="+svc.ID).Add("status", "running"),
	})
	if err != nil {
		return 0, fmt.Errorf("list containers of %s: %w", service, err)
	}
	if len(list.Items) == 0 {
		return 0, fmt.Errorf("%s: %w", service, ErrNotRunning)
	}
	id := newestContainer(list.Items)

	created, err := e.api.ExecCreate(ctx, id, client.ExecCreateOptions{AttachStdout: true, AttachStderr: true, Cmd: cmd})
	if err != nil {
		return 0, fmt.Errorf("exec in container %.12s: %w", id, err)
	}
	attached, err := e.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return 0, fmt.Errorf("exec in container %.12s: %w", id, err)
	}
	defer attached.Close()
	// The hijacked connection doesn't watch ctx once it's open.
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()

	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("exec in container %.12s: %w", id, err)
	}

	inspected, err := e.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return 0, fmt.Errorf("exec in container %.12s: %w", id, err)
	}

	return inspected.ExitCode, nil
}

// newestContainer is the most recently created of a service's running
// containers. During a start-first update that's the new task, so a
// command started then isn't cut off when the old one stops. Docker gives
// creation times in seconds, so tasks created together tie, and either
// will do.
func newestContainer(items []container.Summary) string {
	return slices.MaxFunc(items, func(a, b container.Summary) int {
		return cmp.Compare(a.Created, b.Created)
	}).ID
}

// TailBuffer keeps the last Limit bytes written to it, for output whose end
// matters more than its start, such as a scheduled task's (ADR 0032).
type TailBuffer struct {
	Limit   int
	buf     []byte
	dropped int
}

func (b *TailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	// Trimming only past twice the limit keeps the copies rare.
	if over := len(b.buf) - b.Limit; over > b.Limit {
		b.buf = b.buf[:copy(b.buf, b.buf[over:])]
		b.dropped += over
	}

	return len(p), nil
}

// String is the tail as valid UTF-8, after a line saying how much was
// dropped, if anything was.
func (b *TailBuffer) String() string {
	tail, dropped := b.buf, b.dropped
	if over := len(tail) - b.Limit; over > 0 {
		tail, dropped = tail[over:], dropped+over
	}
	if dropped == 0 {
		return strings.ToValidUTF8(string(tail), "\uFFFD")
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail, dropped = tail[1:], dropped+1
	}

	return fmt.Sprintf("[%d earlier bytes dropped]\n", dropped) + strings.ToValidUTF8(string(tail), "\uFFFD")
}

// cappedBuffer fails a write past limit, or with truncate, drops it.
type cappedBuffer struct {
	bytes.Buffer
	limit    int
	truncate bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if len(p) > room {
		if !b.truncate {
			return 0, ErrExecOutputTooLarge
		}
		b.Buffer.Write(p[:max(room, 0)])
		return len(p), nil
	}

	return b.Buffer.Write(p)
}
