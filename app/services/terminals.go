package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
)

// terminalShell is what an app's terminal runs: bash if the image has it,
// sh otherwise. Not a login shell, since Debian's /etc/profile resets
// PATH and would hide what the image's ENV added to it.
var terminalShell = []string{"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}

// MaxTerminals is how many terminals an app may have open at once.
const MaxTerminals = 5

// TerminalIdle is how long a terminal stays open with nothing typed or
// shown. Tests shorten it.
var TerminalIdle = 30 * time.Minute

// TerminalRefusal is a terminal that can't open, with the reason for the
// admin.
type TerminalRefusal struct {
	Reason string
}

func (e *TerminalRefusal) Error() string {
	return e.Reason
}

// Terminal is a shell open in an app's container (OpenTerminal).
type Terminal struct {
	docker.Terminal
	app    string
	opened time.Time

	ending sync.Once
	reason string
}

var terminals = struct {
	sync.Mutex
	open   map[*Terminal]uint
	perApp map[uint]int
}{open: map[*Terminal]uint{}, perApp: map[uint]int{}}

// OpenTerminal starts a shell of size in the app's newest running task.
// The shell lasts until it exits or the terminal ends: End or Close it when
// the page goes. ctx bounds the start only. The error is a
// *TerminalRefusal when the app isn't running or has MaxTerminals open.
func OpenTerminal(ctx context.Context, app *models.App, size docker.TerminalSize) (*Terminal, error) {
	terminals.Lock()
	if terminals.perApp[app.ID] >= MaxTerminals {
		terminals.Unlock()
		return nil, &TerminalRefusal{Reason: fmt.Sprintf("%s has %d terminals open already. Close one, then reconnect.", app.Name, MaxTerminals)}
	}
	terminals.perApp[app.ID]++
	terminals.Unlock()

	term, err := Docker().ExecTTY(ctx, app.ServiceName(), terminalShell, size)
	if err != nil {
		releaseTerminal(app.ID)
		if errors.Is(err, docker.ErrNotRunning) {
			return nil, &TerminalRefusal{Reason: app.Name + " isn't running. Start or deploy it, then reconnect."}
		}
		return nil, fmt.Errorf("open a terminal in %s: %w", app.Name, err)
	}

	t := &Terminal{Terminal: term, app: app.Name, opened: time.Now()}
	terminals.Lock()
	terminals.open[t] = app.ID
	terminals.Unlock()
	facades.Log().Infof("terminal: opened in %s, container %.12s", app.Name, term.Container())

	return t, nil
}

func releaseTerminal(appID uint) {
	terminals.Lock()
	defer terminals.Unlock()
	if terminals.perApp[appID]--; terminals.perApp[appID] <= 0 {
		delete(terminals.perApp, appID)
	}
}

// End closes the terminal, for reason, which Reason then gives. Only the
// first End or Close counts.
func (t *Terminal) End(reason string) {
	t.ending.Do(func() {
		terminals.Lock()
		t.reason = reason
		appID := terminals.open[t]
		delete(terminals.open, t)
		terminals.Unlock()
		releaseTerminal(appID)

		err := t.Terminal.Close()
		took := time.Since(t.opened).Round(time.Second)
		if err != nil {
			facades.Log().Error(fmt.Errorf("terminal: closed in %s after %s: %w", t.app, took, err))
			return
		}
		facades.Log().Infof("terminal: closed in %s after %s", t.app, took)
	})
}

// Close ends the terminal with no reason.
func (t *Terminal) Close() error {
	t.End("")

	return nil
}

// Reason is why the terminal was ended, once it has been. It's empty when
// the shell exited or the page went.
func (t *Terminal) Reason() string {
	terminals.Lock()
	defer terminals.Unlock()

	return t.reason
}

// CloseTerminals ends every open terminal for reason, at logout (there's
// one admin) and when the server stops, so no shell outlives its page.
func CloseTerminals(reason string) {
	terminals.Lock()
	open := make([]*Terminal, 0, len(terminals.open))
	for t := range terminals.open {
		open = append(open, t)
	}
	terminals.Unlock()

	var ending sync.WaitGroup
	for _, t := range open {
		ending.Go(func() { t.End(reason) })
	}
	ending.Wait()
}
