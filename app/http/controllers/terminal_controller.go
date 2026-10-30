package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/gorilla/websocket"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
)

const (
	// terminalPing keeps the WebSocket busy enough that proxies don't close
	// it as idle: Cloudflare does after 100 s.
	terminalPing = 25 * time.Second
	// terminalPongWait is how long the browser has to answer a ping.
	terminalPongWait  = 60 * time.Second
	terminalWriteWait = 10 * time.Second
	terminalStart     = 15 * time.Second
	// terminalMaxMessage bounds what one message from the browser can be:
	// keystrokes, a paste or a resize.
	terminalMaxMessage = 64 << 10
	maxTerminalSize    = 1000
)

var terminalUpgrader = websocket.Upgrader{CheckOrigin: sameOrigin}

// sameOrigin is the WebSocket's CSRF check. A browser sends the session
// cookie with a WebSocket from any same-site page, such as an app Echowa
// deploys on a sibling subdomain, and always says which page in Origin.
func sameOrigin(r *nethttp.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))

	return err == nil && origin.Host != "" && strings.EqualFold(origin.Host, r.Host)
}

// terminalMessage is a control message, as JSON in a text frame. The
// terminal's bytes go in binary frames, both ways.
type terminalMessage struct {
	// Type is "ready", "exit" or "closed" from Echowa, and "resize" from
	// the browser.
	Type      string `json:"type"`
	Container string `json:"container,omitempty"`
	Code      *int   `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Cols      uint   `json:"cols,omitempty"`
	Rows      uint   `json:"rows,omitempty"`
}

// TerminalController is a custom app's Terminal tab: a shell in its newest
// running task, over a WebSocket (ADR 0038). Its route is in
// routes/streams.go, past the request timeout, and never saves the session.
type TerminalController struct{}

func NewTerminalController() *TerminalController {
	return &TerminalController{}
}

// Show upgrades to a WebSocket and opens a shell of ?cols= and ?rows=.
func (r *TerminalController) Show(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	if app.Template != nil {
		return notFound(ctx)
	}
	size, ok := parseTerminalSize(ctx.Request().Query("cols"), ctx.Request().Query("rows"))
	if !ok {
		return ctx.Response().String(http.StatusUnprocessableEntity, fmt.Sprintf("cols and rows must be 1 to %d", maxTerminalSize))
	}
	conn, err := terminalUpgrader.Upgrade(ctx.Response().Writer(), ctx.Request().Origin(), nil)
	if err != nil {
		// Upgrade has answered with the reason.
		return nil
	}
	serveTerminal(conn, &app, size)

	return nil
}

func parseTerminalSize(cols, rows string) (docker.TerminalSize, bool) {
	c, err1 := strconv.ParseUint(cols, 10, 0)
	r, err2 := strconv.ParseUint(rows, 10, 0)
	size := docker.TerminalSize{Rows: uint(r), Cols: uint(c)}

	return size, err1 == nil && err2 == nil && validTerminalSize(size)
}

func validTerminalSize(size docker.TerminalSize) bool {
	return size.Cols >= 1 && size.Cols <= maxTerminalSize && size.Rows >= 1 && size.Rows <= maxTerminalSize
}

// serveTerminal runs the shell until it exits, the browser goes, or
// Echowa ends it, and then tells the browser which.
func serveTerminal(conn *websocket.Conn, app *models.App, size docker.TerminalSize) {
	defer func() { _ = conn.Close() }()
	conn.SetReadLimit(terminalMaxMessage)

	started, cancel := context.WithTimeout(context.Background(), terminalStart)
	term, err := services.OpenTerminal(started, app, size)
	cancel()
	if err != nil {
		var refused *services.TerminalRefusal
		if !errors.As(err, &refused) {
			facades.Log().Error(err)
			refused = &services.TerminalRefusal{Reason: "Echowa couldn't open a terminal. The server log has the reason."}
		}
		endTerminal(conn, terminalMessage{Type: "closed", Reason: refused.Reason})
		return
	}
	defer term.Close()
	if err := writeTerminalMessage(conn, terminalMessage{Type: "ready", Container: term.Container()[:min(12, len(term.Container()))]}); err != nil {
		return
	}

	idle := time.AfterFunc(services.TerminalIdle, func() {
		term.End(fmt.Sprintf("Closed after %d minutes with nothing typed or shown.", int(services.TerminalIdle.Minutes())))
	})
	defer idle.Stop()

	browserGone := make(chan struct{})
	go func() {
		defer close(browserGone)
		// The browser going is the end of the shell.
		defer term.Close()
		readTerminalInput(conn, term, func() { idle.Reset(services.TerminalIdle) })
	}()
	go pingTerminal(conn, browserGone)

	buf := make([]byte, 32<<10)
	for {
		n, err := term.Read(buf)
		if n > 0 {
			idle.Reset(services.TerminalIdle)
			_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteWait))
			if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				break
			}
		}
		if errors.Is(err, io.EOF) {
			code, cerr := exitCode(term)
			if cerr != nil {
				facades.Log().Error(cerr)
				endTerminal(conn, terminalMessage{Type: "closed", Reason: "The shell ended."})
				break
			}
			endTerminal(conn, terminalMessage{Type: "exit", Code: &code})
			break
		}
		if err != nil {
			reason := term.Reason()
			if reason == "" {
				reason = "The terminal was closed."
			}
			endTerminal(conn, terminalMessage{Type: "closed", Reason: reason})
			break
		}
	}
	_ = conn.Close()
	<-browserGone
}

func exitCode(term *services.Terminal) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), terminalWriteWait)
	defer cancel()

	return term.ExitCode(ctx)
}

// readTerminalInput types what the browser sends into the shell and
// applies its resizes, until the browser goes or stops answering pings.
func readTerminalInput(conn *websocket.Conn, term *services.Terminal, active func()) {
	_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	})
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch kind {
		case websocket.BinaryMessage:
			active()
			if _, err := term.Write(data); err != nil {
				return
			}
		case websocket.TextMessage:
			var msg terminalMessage
			if json.Unmarshal(data, &msg) != nil || msg.Type != "resize" {
				continue
			}
			size := docker.TerminalSize{Rows: msg.Rows, Cols: msg.Cols}
			if !validTerminalSize(size) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), terminalWriteWait)
			if err := term.Resize(ctx, size); err != nil {
				facades.Log().Error(err)
			}
			cancel()
		}
	}
}

// pingTerminal pings the browser until it goes.
func pingTerminal(conn *websocket.Conn, browserGone <-chan struct{}) {
	ticker := time.NewTicker(terminalPing)
	defer ticker.Stop()
	for {
		select {
		case <-browserGone:
			return
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(terminalWriteWait)); err != nil {
				return
			}
		}
	}
}

// endTerminal tells the browser how the terminal ended, then closes the
// WebSocket.
func endTerminal(conn *websocket.Conn, msg terminalMessage) {
	if writeTerminalMessage(conn, msg) != nil {
		return
	}
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(terminalWriteWait))
}

func writeTerminalMessage(conn *websocket.Conn, msg terminalMessage) error {
	_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteWait))

	return conn.WriteJSON(msg)
}
