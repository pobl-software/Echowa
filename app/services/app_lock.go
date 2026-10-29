package services

import (
	"context"
	"sync"
	"time"
)

// AppWork is what holds an app's lock. A deploy, a backup, a restore and
// an export of one app never overlap (ADR 0034): a dump during a deploy
// could read from a task that's stopping, and a restore during one could
// write to it.
type AppWork string

const (
	WorkDeploy  AppWork = "deploy"
	WorkBackup  AppWork = "backup"
	WorkRestore AppWork = "restore"
	WorkExport  AppWork = "export"
)

// Describe is the work as the pages name it: "a deploy", "an export".
func (w AppWork) Describe() string {
	if w == WorkExport {
		return "an export"
	}

	return "a " + string(w)
}

// lockPoll is how often WaitLockApp tries again.
var lockPoll = 250 * time.Millisecond

// appLocks holds each locked app's work. One Echowa process runs per
// server (ADR 0013), so memory is enough.
var appLocks = struct {
	sync.Mutex
	held map[uint]AppWork
}{held: map[uint]AppWork{}}

// LockApp takes the app's lock for work, or returns what holds it.
func LockApp(appID uint, work AppWork) (release func(), holder AppWork, ok bool) {
	appLocks.Lock()
	defer appLocks.Unlock()
	if holder, held := appLocks.held[appID]; held {
		return nil, holder, false
	}
	appLocks.held[appID] = work

	var once sync.Once
	return func() {
		once.Do(func() {
			appLocks.Lock()
			defer appLocks.Unlock()
			delete(appLocks.held, appID)
		})
	}, "", true
}

// WaitLockApp tries LockApp until it succeeds or ctx ends, and then
// returns ctx's error.
func WaitLockApp(ctx context.Context, appID uint, work AppWork) (func(), error) {
	for {
		if release, _, ok := LockApp(appID, work); ok {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// AppBusy says what holds the app's lock, or "".
func AppBusy(appID uint) AppWork {
	appLocks.Lock()
	defer appLocks.Unlock()

	return appLocks.held[appID]
}
