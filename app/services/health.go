package services

import (
	"context"
	"sync"
	"time"

	"goravel/app/facades"
	"goravel/app/models"
)

// UnhealthyAfter is how long an app may run fewer replicas than it wants
// before it counts as unhealthy (docs/mvp.md, "App status").
const UnhealthyAfter = 2 * time.Minute

// short maps each app the poller last saw running fewer replicas than
// desired to when it first saw that. It lives in memory: after a restart,
// the poller needs UnhealthyAfter again to call an app unhealthy.
var short = struct {
	sync.Mutex
	since map[uint]time.Time
}{since: map[uint]time.Time{}}

// PollAppStatus reads every app's service and records which are short of
// replicas, and since when. The scheduler calls it every 30 seconds; now
// is when the poll ran. An app Docker didn't answer for keeps its record,
// since nothing is known about it.
func PollAppStatus(ctx context.Context, now time.Time) error {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().Get(&apps); err != nil {
		return err
	}
	statuses := make(map[uint]LiveStatus, len(apps))
	for _, app := range apps {
		statuses[app.ID] = serviceStatus(ctx, &app)
	}

	short.Lock()
	defer short.Unlock()
	since := map[uint]time.Time{}
	for id, status := range statuses {
		previous, seen := short.since[id]
		switch {
		case status.State == StateUnknown && seen:
			since[id] = previous
		case status.Running < status.Desired && seen:
			since[id] = previous
		case status.Running < status.Desired:
			since[id] = now
		}
	}
	short.since = since

	return nil
}

// unhealthySince returns when the app fell short of replicas, if the
// poller has seen it short for at least UnhealthyAfter before now.
func unhealthySince(appID uint, now time.Time) *time.Time {
	short.Lock()
	defer short.Unlock()
	since, ok := short.since[appID]
	if !ok || now.Sub(since) < UnhealthyAfter {
		return nil
	}

	return &since
}
