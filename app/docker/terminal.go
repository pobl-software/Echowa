package docker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// TerminalSize is a terminal's size in characters.
type TerminalSize struct {
	Rows, Cols uint
}

// Terminal is a command running with a TTY (Client.ExecTTY). Read and
// Write may run in different goroutines, and Close in any.
type Terminal interface {
	// Read reads what the command writes to the terminal, including its
	// echo of what's typed. It returns io.EOF once the command has ended.
	io.Reader
	// Write types p into the terminal.
	io.Writer
	// Resize tells the command the terminal's new size.
	Resize(ctx context.Context, size TerminalSize) error
	// ExitCode is the command's exit status, once Read has returned io.EOF.
	ExitCode(ctx context.Context) (int, error)
	// Close hangs up, as closing an SSH session does: the command gets
	// SIGHUP, and so does whatever runs in the terminal's foreground.
	// Docker has no way to end an exec, so without it a shell and what it
	// runs would outlive the page. Closing after the command has ended
	// only closes the connection.
	Close() error
	// Container is the ID of the container the command runs in.
	Container() string
}

// pidMarker starts what ttyWrapper prints first. It's an operating system
// command sequence, which terminals ignore, should one ever get through.
const pidMarker = "\x1b]echowa-pid;"

// ttyWrapper prints its PID in the container, then becomes the command,
// which keeps that PID, so Close knows what to hang up.
const ttyWrapper = `printf '\033]echowa-pid;%s\007' "$$"; exec "$@"`

// hangUpWait bounds Close's hang-up.
const hangUpWait = 10 * time.Second

func (e *Engine) ExecTTY(ctx context.Context, service string, cmd []string, size TerminalSize) (Terminal, error) {
	id, err := e.taskContainer(ctx, service)
	if err != nil {
		return nil, err
	}

	console := client.ConsoleSize{Height: size.Rows, Width: size.Cols}
	created, err := e.api.ExecCreate(ctx, id, client.ExecCreateOptions{
		TTY: true, ConsoleSize: console, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: []string{"TERM=xterm-256color"},
		Cmd: append([]string{"sh", "-c", ttyWrapper, "sh"}, cmd...),
	})
	if err != nil {
		return nil, fmt.Errorf("exec in container %.12s: %w", id, err)
	}
	attached, err := e.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: console})
	if err != nil {
		return nil, fmt.Errorf("exec in container %.12s: %w", id, err)
	}

	t := &engineTerminal{engine: e, container: id, exec: created.ID, conn: attached.Conn, out: attached.Reader}
	// The hijacked connection doesn't watch ctx once it's open.
	stop := context.AfterFunc(ctx, attached.Close)
	t.pid, err = readPID(attached.Reader)
	if !stop() {
		_ = t.Close()
		return nil, ctx.Err()
	}
	if err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("exec in container %.12s: %w", id, err)
	}

	return t, nil
}

// readPID reads the PID ttyWrapper prints. Output that doesn't start with
// it, such as Docker's reason when the image has no sh, is left to be
// read, and the PID is 0: the command has nothing to hang up.
func readPID(r *bufio.Reader) (int, error) {
	head, err := r.Peek(len(pidMarker))
	if string(head) != pidMarker {
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		return 0, nil
	}
	_, _ = r.Discard(len(pidMarker))
	line, err := r.ReadSlice('\a')
	if err != nil {
		return 0, fmt.Errorf("read the shell's PID: %w", err)
	}
	pid, err := strconv.Atoi(string(line[:len(line)-1]))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("read the shell's PID: %q", line)
	}

	return pid, nil
}

type engineTerminal struct {
	engine    *Engine
	container string
	exec      string
	pid       int
	conn      net.Conn
	out       *bufio.Reader

	closing  sync.Once
	closeErr error
}

func (t *engineTerminal) Read(p []byte) (int, error) {
	return t.out.Read(p)
}

func (t *engineTerminal) Write(p []byte) (int, error) {
	return t.conn.Write(p)
}

func (t *engineTerminal) Resize(ctx context.Context, size TerminalSize) error {
	_, err := t.engine.api.ExecResize(ctx, t.exec, client.ExecResizeOptions{Height: size.Rows, Width: size.Cols})
	if err != nil {
		return fmt.Errorf("resize the terminal in container %.12s: %w", t.container, err)
	}

	return nil
}

func (t *engineTerminal) ExitCode(ctx context.Context) (int, error) {
	inspected, err := t.engine.api.ExecInspect(ctx, t.exec, client.ExecInspectOptions{})
	if err != nil {
		return 0, fmt.Errorf("exec in container %.12s: %w", t.container, err)
	}
	if inspected.Running {
		return 0, fmt.Errorf("exec in container %.12s: still running", t.container)
	}

	return inspected.ExitCode, nil
}

func (t *engineTerminal) Close() error {
	t.closing.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), hangUpWait)
		defer cancel()
		t.closeErr = t.hangUp(ctx)
		_ = t.conn.Close()
	})

	return t.closeErr
}

func (t *engineTerminal) Container() string {
	return t.container
}

// hangUp sends SIGHUP to the command, if it's still running. It's the
// terminal's session leader, so when it exits the kernel sends SIGHUP to
// the terminal's foreground job too.
func (t *engineTerminal) hangUp(ctx context.Context) error {
	if t.pid == 0 {
		return nil
	}
	running := func() (bool, error) {
		inspected, err := t.engine.api.ExecInspect(ctx, t.exec, client.ExecInspectOptions{})
		if err != nil {
			return false, fmt.Errorf("hang up the terminal in container %.12s: %w", t.container, err)
		}
		return inspected.Running, nil
	}
	if ok, err := running(); err != nil || !ok {
		return err
	}

	stderr := &cappedBuffer{limit: maxExecStderr, truncate: true}
	code, err := t.engine.execIn(ctx, t.container, []string{"sh", "-c", `kill -HUP "$1"`, "sh", strconv.Itoa(t.pid)}, nil, io.Discard, stderr)
	if err != nil {
		return fmt.Errorf("hang up the terminal in container %.12s: %w", t.container, err)
	}
	if code != 0 {
		// It may have ended between the check and the kill.
		if ok, err := running(); err == nil && !ok {
			return nil
		}
		return &ExecError{ExitCode: code, Stderr: strings.TrimSpace(stderr.String())}
	}

	return nil
}
