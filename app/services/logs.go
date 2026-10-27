package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/sse"
)

const (
	// DefaultLogTail is how many earlier lines per task an app log stream
	// starts with.
	DefaultLogTail = 200
	MaxLogTail     = 10000
	// MaxLogDeploys is how many deploys the Logs tab offers, newest first.
	MaxLogDeploys = 3
	// deploymentPoll is how often a deployment stream checks for new lines.
	deploymentPoll = 500 * time.Millisecond
)

// LogQuery is what an app log stream sends: Docker's options, the deploy
// whose lines to show, plus where a reconnecting browser left off.
type LogQuery struct {
	docker.LogOptions
	// Deploy is a LogDeploy's Generation. Zero is the current deploy.
	Deploy uint64
	// After skips lines at or before this time. Docker's since is
	// inclusive, so a resumed stream would repeat the last line without it.
	After time.Time
}

// ParseLogQuery reads an app log stream's ?tail= (lines per task), ?since=
// (a duration such as 15m) and ?deploy= (a LogDeploy's Generation), and the
// Last-Event-ID header a reconnecting browser sends: the time of the last
// line it got.
func ParseLogQuery(tail, since, deploy, lastEventID string, now time.Time) (LogQuery, error) {
	q := LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: DefaultLogTail}}
	if tail != "" {
		n, err := strconv.Atoi(tail)
		if err != nil || n < 1 || n > MaxLogTail {
			return LogQuery{}, fmt.Errorf("tail must be a number of lines from 1 to %d", MaxLogTail)
		}
		q.Tail = n
	}
	if since != "" {
		d, err := time.ParseDuration(since)
		if err != nil || d <= 0 {
			return LogQuery{}, errors.New("since must be a duration such as 15m or 24h")
		}
		q.Since = now.Add(-d)
	}
	if deploy != "" {
		n, err := strconv.ParseUint(deploy, 10, 64)
		if err != nil || n == 0 {
			return LogQuery{}, errors.New("deploy must be a deploy's generation")
		}
		q.Deploy = n
	}
	if lastEventID != "" {
		last, err := time.Parse(time.RFC3339Nano, lastEventID)
		if err != nil {
			return LogQuery{}, errors.New("Last-Event-ID must be a log line's time")
		}
		// Still capped: a Last-Event-ID from long ago mustn't replay the
		// whole log.
		q.Since, q.After, q.Tail = last, last, MaxLogTail
	}

	return q, nil
}

// LogLineEvent is a "line" event of an app log stream.
type LogLineEvent struct {
	Time   time.Time     `json:"time"`
	Stream docker.Stream `json:"stream"`
	Text   string        `json:"text"`
}

// StreamEndEvent is an "end" event: the stream is over and the browser
// shouldn't reconnect.
type StreamEndEvent struct {
	Reason  string `json:"reason"` // not_deployed, not_found, ended, stopped, error
	Message string `json:"message"`
}

// LogDeploy is a deploy whose tasks Swarm still keeps, so the Logs tab can
// show their lines.
type LogDeploy struct {
	// Generation identifies it (docker.Task.Generation).
	Generation uint64 `json:"generation"`
	// Current is the deploy Swarm runs now: the newest with a task it
	// wants running, which after a rollback is the one before.
	Current bool `json:"current"`
	// StartedAt is when its oldest task Swarm still keeps was made.
	StartedAt time.Time `json:"started_at"`
	// Deployment made it, or is nil when none matches.
	Deployment *LogDeployment `json:"deployment"`

	image string
	tasks []string
}

// LogDeployment is the deployment row a LogDeploy came from.
type LogDeployment struct {
	ID        uint      `json:"id"`
	Ref       string    `json:"ref"`
	StartedAt time.Time `json:"started_at"`
}

// AppLogDeploys lists the app's newest deploys, at most MaxLogDeploys,
// newest first, for the Logs tab's picker. An app that isn't deployed, or
// that Docker can't answer for, has none, and the tab shows the current
// logs without a picker.
func AppLogDeploys(ctx context.Context, app *models.App) []LogDeploy {
	tasks, err := Docker().ServiceTasks(ctx, app.ServiceName())
	if err != nil {
		if !errors.Is(err, docker.ErrServiceNotFound) {
			facades.Log().Warningf("tasks of %s: %v", app.ServiceName(), err)
		}
		return []LogDeploy{}
	}
	deploys := groupDeploys(tasks)
	deploys = deploys[:min(len(deploys), MaxLogDeploys)]

	var deployments []models.Deployment
	err = facades.Orm().WithContext(ctx).Query().
		Where("app_id", app.ID).Where("digest IS NOT NULL").Where("started_at IS NOT NULL").
		Order("id desc").Limit(100).Find(&deployments)
	if err != nil {
		facades.Log().Warningf("deployments of %s: %v", app.Name, err)
	}
	for i := range deploys {
		deploys[i].Deployment = matchDeployment(deploys[i], deployments)
	}

	return deploys
}

// groupDeploys groups tasks by generation, newest first, and marks the
// current one.
func groupDeploys(tasks []docker.Task) []LogDeploy {
	byGeneration := map[uint64]*LogDeploy{}
	running := map[uint64]bool{}
	for _, task := range tasks {
		d, ok := byGeneration[task.Generation]
		if !ok {
			d = &LogDeploy{Generation: task.Generation, StartedAt: task.CreatedAt, image: task.Image}
			byGeneration[task.Generation] = d
		}
		if task.CreatedAt.Before(d.StartedAt) {
			d.StartedAt = task.CreatedAt
		}
		d.tasks = append(d.tasks, task.ID)
		running[task.Generation] = running[task.Generation] || task.Running
	}

	deploys := make([]LogDeploy, 0, len(byGeneration))
	for _, d := range byGeneration {
		deploys = append(deploys, *d)
	}
	slices.SortFunc(deploys, func(a, b LogDeploy) int { return cmp.Compare(b.Generation, a.Generation) })
	current := slices.IndexFunc(deploys, func(d LogDeploy) bool { return running[d.Generation] })
	if len(deploys) > 0 {
		deploys[max(current, 0)].Current = true
	}

	return deploys
}

// matchDeployment finds the deployment that made d: the newest that
// deployed its image and started before its oldest task. Swarm may have
// dropped the tasks the deploy first made, so the oldest left can be a
// restart from long after.
func matchDeployment(d LogDeploy, newestFirst []models.Deployment) *LogDeployment {
	for _, deployment := range newestFirst {
		if deployment.Digest == nil || *deployment.Digest != d.image || deployment.StartedAt == nil {
			continue
		}
		if started := deployment.StartedAt.StdTime(); !started.After(d.StartedAt) {
			return &LogDeployment{ID: deployment.ID, Ref: deployment.Ref, StartedAt: started}
		}
	}

	return nil
}

// FollowAppLogs sends one deploy's service logs as "line" events. The
// current deploy's are followed until ctx ends, and ending ctx cancels the
// Docker log request. An older deploy's tasks have stopped, so its lines
// are sent once, then "end". If the service doesn't exist, or its log
// stream ends on its own (the service was removed), it sends "end". If the
// stream breaks, it returns the error, and the browser reconnects and
// resumes.
func FollowAppLogs(ctx context.Context, app *models.App, q LogQuery, send func(sse.Event) error) error {
	tasks, err := Docker().ServiceTasks(ctx, app.ServiceName())
	if errors.Is(err, docker.ErrServiceNotFound) {
		return send(endEvent("not_deployed", app.Name+" has no running service yet. Deploy it to see its logs."))
	}
	if err != nil {
		return send(endEvent("error", err.Error()))
	}
	keep, past, found := deployFilter(groupDeploys(tasks), q.Deploy)
	if !found {
		return send(endEvent("not_found", "Swarm no longer keeps that deploy's containers, so its logs are gone."))
	}
	opts := q.LogOptions
	opts.Follow = opts.Follow && !past

	lines, err := Docker().StreamServiceLogs(ctx, app.ServiceName(), opts)
	if errors.Is(err, docker.ErrServiceNotFound) {
		return send(endEvent("not_deployed", app.Name+" has no running service yet. Deploy it to see its logs."))
	}
	if err != nil {
		return send(endEvent("error", err.Error()))
	}

	sent := 0
	for line, err := range lines {
		if err != nil {
			return err
		}
		if !keep(line.Task) || (!q.After.IsZero() && !line.Time.After(q.After)) {
			continue
		}
		sent++
		event := sse.Event{
			Name: "line",
			ID:   line.Time.UTC().Format(time.RFC3339Nano),
			Data: LogLineEvent{Time: line.Time, Stream: line.Stream, Text: line.Text},
		}
		if err := send(event); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if past && sent == 0 {
		// Swarm keeps a stopped task after Docker has removed its
		// container, which a restart of Docker does.
		return send(endEvent("stopped", "Docker has no lines from that deploy. Its containers were removed, or wrote nothing."))
	}
	if past {
		return send(endEvent("stopped", "That deploy's containers have stopped, so no new lines will come."))
	}

	return send(endEvent("ended", "Docker closed the log stream. The service may have been removed."))
}

// deployFilter says which tasks' lines belong to the deploy of generation,
// zero for the current one, whether it's a past deploy, and whether it's
// there at all. A task it doesn't know started after the list was read, so
// it's the current deploy's, or a newer one's.
func deployFilter(deploys []LogDeploy, generation uint64) (keep func(task string) bool, past, found bool) {
	byTask := map[string]uint64{}
	target := LogDeploy{Current: true}
	for _, d := range deploys {
		for _, task := range d.tasks {
			byTask[task] = d.Generation
		}
		if d.Generation == generation || generation == 0 && d.Current {
			target = d
		}
	}
	if generation != 0 && target.Generation != generation {
		return nil, false, false
	}

	return func(task string) bool {
		g, known := byTask[task]
		if !known {
			return target.Current
		}
		return g == target.Generation
	}, !target.Current, true
}

// DeploymentStatusEvent is a "status" event of a deployment stream.
type DeploymentStatusEvent struct {
	Status     models.DeploymentStatus `json:"status"`
	Error      *string                 `json:"error"`
	Digest     *string                 `json:"digest"`
	StartedAt  *carbon.DateTime        `json:"started_at"`
	FinishedAt *carbon.DateTime        `json:"finished_at"`
}

type deploymentTail struct {
	DeploymentStatusEvent
	Log string
}

// FollowDeployment sends deployment id's log as "line" events as the deploy
// job appends it, starting after offset characters, and a "status" event
// whenever its status changes. Once the deployment has finished it sends
// the final status and returns. Each line's event ID is the offset after
// it, so a reconnecting browser carries on where it stopped.
//
// The deploy job writes the final status after its last log line, so the
// final status always follows every line.
func FollowDeployment(ctx context.Context, id uint, offset int, send func(sse.Event) error) error {
	var sent models.DeploymentStatus
	for {
		// SQLite's substr counts characters, so offsets are in characters.
		var rows []deploymentTail
		err := facades.Orm().WithContext(ctx).Query().Raw(
			"SELECT status, error, digest, started_at, finished_at, substr(log, ?) AS log FROM deployments WHERE id = ?",
			offset+1, id,
		).Scan(&rows)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return send(endEvent("not_found", "The deployment was deleted."))
		}
		row := rows[0]

		// The job appends whole lines, but only send what ends in a newline.
		complete := row.Log[:strings.LastIndexByte(row.Log, '\n')+1]
		for line := range strings.Lines(complete) {
			offset += utf8.RuneCountInString(line)
			event := sse.Event{Name: "line", ID: strconv.Itoa(offset), Data: strings.TrimSuffix(line, "\n")}
			if err := send(event); err != nil {
				return err
			}
		}

		if row.Status != sent {
			if err := send(sse.Event{Name: "status", Data: row.DeploymentStatusEvent}); err != nil {
				return err
			}
			sent = row.Status
		}
		if !row.Status.Running() {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(deploymentPoll):
		}
	}
}

func endEvent(reason, message string) sse.Event {
	return sse.Event{Name: "end", Data: StreamEndEvent{Reason: reason, Message: message}}
}
