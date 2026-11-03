package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/rules"
)

// A deploy webhook (ADR 0047): CI signs "<timestamp>.<app>.<body>" with the
// app's webhook secret and sends it to POST /hooks/deploy/{app}.
const (
	// HookSkew is how far a request's timestamp may be from the server's
	// clock, either way.
	HookSkew = 5 * time.Minute
	// HookMaxBody is the largest body a hook reads.
	HookMaxBody = 8 << 10
	// HookFailureLimit failed verifications from one IP in
	// HookFailureWindow, then that IP gets 429 before any HMAC work.
	HookFailureLimit  = 10
	HookFailureWindow = 10 * time.Minute
	// HookDeployLimit accepted deploys of one app in HookDeployWindow.
	HookDeployLimit  = 20
	HookDeployWindow = time.Minute
)

// hookRepeatWindow is how long an accepted signature is remembered. A
// timestamp is good for 2 × HookSkew, so a request can't be accepted
// again once it's forgotten.
const hookRepeatWindow = 2 * HookSkew

var (
	// ErrHookMalformed is a missing or badly formed timestamp or signature.
	ErrHookMalformed = errors.New("missing or malformed X-Echowa-Timestamp or X-Echowa-Signature")
	// ErrHookClock is a timestamp more than HookSkew from the server's.
	ErrHookClock = errors.New("timestamp is more than 5 minutes from the server's clock; check the CI runner's clock")
	// ErrHookSignature covers a bad signature, an unknown app and a
	// template app alike, so a caller can't tell which apps exist.
	ErrHookSignature = errors.New("invalid signature")
)

// hookDummyKey signs for an app that doesn't exist or isn't custom, so
// the 401 takes as long as a real app's.
var hookDummyKey = []byte("echowa-hook-dummy-key-0000000000")

// HookSignature is the hex HMAC-SHA256 of a hook request, as the client
// sends it after "sha256=".
func HookSignature(secret, timestamp, app string, body []byte) string {
	return hex.EncodeToString(hookMAC([]byte(secret), timestamp, app, body))
}

func hookMAC(key []byte, timestamp, app string, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(timestamp + "." + app + "."))
	mac.Write(body)

	return mac.Sum(nil)
}

// VerifyDeployHook checks a deploy hook and returns its app. The headers'
// format is checked first, then the clock, then the app, so the clock's
// answer says nothing about which apps exist. The error is one of
// ErrHookMalformed, ErrHookClock or ErrHookSignature, or a database error.
func VerifyDeployHook(ctx context.Context, name, timestamp, signature string, body []byte, now time.Time) (*models.App, error) {
	sent, ok := parseHookSignature(signature)
	if !ok {
		return nil, ErrHookMalformed
	}
	at, ok := parseHookTimestamp(timestamp)
	if !ok {
		return nil, ErrHookMalformed
	}
	if skew := now.Sub(time.Unix(at, 0)); skew > HookSkew || skew < -HookSkew {
		return nil, ErrHookClock
	}

	app, err := findHookApp(ctx, name)
	if err != nil {
		return nil, err
	}
	// An empty secret is a key anyone could sign with, so an app without
	// one is refused like an unknown app.
	usable := app.ID != 0 && app.Template == nil && app.WebhookSecret != ""
	key := hookDummyKey
	if usable {
		key = []byte(string(app.WebhookSecret))
	}
	good := hmac.Equal(hookMAC(key, timestamp, name, body), sent)
	if !good || !usable {
		return nil, ErrHookSignature
	}

	return &app, nil
}

// findHookApp is the app named in a hook's URL, or a zero App. A name that
// can't be an app's isn't looked up, so a caller's long name never reaches
// a query or its log.
func findHookApp(ctx context.Context, name string) (models.App, error) {
	var app models.App
	if !rules.IsSlug(name) {
		return app, nil
	}
	err := facades.Orm().WithContext(ctx).Query().Where("name", name).First(&app)

	return app, err
}

// parseHookSignature reads "sha256=" and exactly 64 hex characters.
func parseHookSignature(header string) ([]byte, bool) {
	digest, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(digest) != hex.EncodedLen(sha256.Size) {
		return nil, false
	}
	sum, err := hex.DecodeString(digest)

	return sum, err == nil
}

// parseHookTimestamp reads Unix seconds as plain decimal digits: no sign,
// no spaces, no more digits than a time needs.
func parseHookTimestamp(header string) (int64, bool) {
	if header == "" || len(header) > 12 {
		return 0, false
	}
	for _, c := range header {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	at, err := strconv.ParseInt(header, 10, 64)

	return at, err == nil
}

// ParseHookBody reads the ref a hook asks for: {"ref": "..."}, with no
// other fields. An empty body, or an empty ref, is "".
func ParseHookBody(body []byte) (string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}
	var in struct {
		Ref string `json:"ref"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return "", fmt.Errorf("body must be {\"ref\": \"<tag or sha256: digest>\"} or empty: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", errors.New("body must be one JSON object")
	}

	return in.Ref, nil
}

// HookLimited reports whether key has had limit hits in its current
// window, and how long until the window ends. Windows start on multiples
// of window, so the wait is exact without storing when one began.
func HookLimited(key string, limit int64, window time.Duration, now time.Time) (time.Duration, bool) {
	count, _ := facades.Cache().Get(hookWindowKey(key, window, now), int64(0)).(int64)
	if count < limit {
		return 0, false
	}

	return now.Truncate(window).Add(window).Sub(now), true
}

// HookHit counts one against key's current window.
func HookHit(key string, window time.Duration, now time.Time) {
	k := hookWindowKey(key, window, now)
	facades.Cache().Add(k, int64(0), window)
	_, _ = facades.Cache().Increment(k)
}

func hookWindowKey(key string, window time.Duration, now time.Time) string {
	return fmt.Sprintf("hook|%s|%d", key, now.Truncate(window).Unix())
}

// hookOnce serialises the repeat check with the deploy it guards, so a
// retry racing the original can't queue a second deploy.
var hookOnce sync.Mutex

// DeployHookOnce runs request for a signature not accepted in the last
// hookRepeatWindow, and remembers the deployment ID it returns. A
// signature already accepted returns that ID with repeat true and runs
// nothing: a CI retry gets the first deployment, and a captured request
// can't be replayed. A request that fails isn't remembered, so it can be
// sent again.
func DeployHookOnce(signature string, request func() (uint, error)) (id uint, repeat bool, err error) {
	hookOnce.Lock()
	defer hookOnce.Unlock()
	key := "hook|" + strings.ToLower(signature)
	if id, ok := facades.Cache().Get(key).(uint); ok {
		return id, true, nil
	}
	if id, err = request(); err != nil {
		return 0, false, err
	}
	if err := facades.Cache().Put(key, id, hookRepeatWindow); err != nil {
		facades.Log().Error(fmt.Errorf("remembering a deploy hook's signature: %w", err))
	}

	return id, false, nil
}

// AppStopped reports whether the last status poll saw the app stopped by
// the admin (ADR 0036). A hook never starts one.
func AppStopped(appID uint) bool {
	status, ok := polledStatus(appID)

	return ok && (status.State == StateStopped || status.State == StateStopping)
}

// HookRefusal is why a hook was refused, as the CI section lists it.
type HookRefusal string

const (
	HookRefusedMalformed HookRefusal = "malformed"
	HookRefusedClock     HookRefusal = "clock"
	HookRefusedSignature HookRefusal = "signature"
	HookRefusedTooLarge  HookRefusal = "too_large"
	HookRefusedBody      HookRefusal = "body"
	HookRefusedRef       HookRefusal = "ref"
	HookRefusedStopped   HookRefusal = "stopped"
	HookRefusedLimited   HookRefusal = "rate_limited"
)

// HookCall is a refused hook, with the error its caller was sent.
type HookCall struct {
	At      *carbon.DateTime `json:"at"`
	IP      string           `json:"ip"`
	Reason  HookRefusal      `json:"reason"`
	Message string           `json:"message"`
}

// hookCallsKept is how many refused calls each app keeps.
const hookCallsKept = 10

// Refused calls live in memory only, so a caller without the secret can't
// write to the database, and only for custom apps that exist, so it can't
// fill memory with names.
var hookCalls = struct {
	sync.Mutex
	byApp map[uint][]HookCall
}{byApp: map[uint][]HookCall{}}

// RecordHookRefusal keeps a refused call to the custom app named name,
// newest first. Calls to any other name are dropped.
func RecordHookRefusal(ctx context.Context, name string, call HookCall) {
	app, err := findHookApp(ctx, name)
	if err != nil || app.ID == 0 || app.Template != nil {
		return
	}
	hookCalls.Lock()
	defer hookCalls.Unlock()
	calls := append([]HookCall{call}, hookCalls.byApp[app.ID]...)
	hookCalls.byApp[app.ID] = calls[:min(len(calls), hookCallsKept)]
}

// HookCalls are the app's refused calls since Echowa started, newest first.
func HookCalls(appID uint) []HookCall {
	hookCalls.Lock()
	defer hookCalls.Unlock()

	return append([]HookCall{}, hookCalls.byApp[appID]...)
}

// forgetHookCalls drops a deleted app's calls, so a new app given its ID
// starts with none.
func forgetHookCalls(appID uint) {
	hookCalls.Lock()
	defer hookCalls.Unlock()
	delete(hookCalls.byApp, appID)
}

// ResetHookCalls forgets every app's refused calls, for tests.
func ResetHookCalls() {
	hookCalls.Lock()
	defer hookCalls.Unlock()
	clear(hookCalls.byApp)
}
