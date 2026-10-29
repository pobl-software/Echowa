package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/docker"
	"goravel/app/models"
)

func TestParseLogQuery(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	last := time.Date(2026, 10, 24, 11, 59, 30, 123456789, time.UTC)

	tests := []struct {
		name                                     string
		tail, since, source, deploy, lastEventID string
		want                                     LogQuery
		wantErr                                  string
	}{
		{name: "defaults", want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: DefaultLogTail}}},
		{name: "tail", tail: "500", want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: 500}}},
		{name: "since", since: "15m", want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: DefaultLogTail, Since: now.Add(-15 * time.Minute)}}},
		{
			name: "resume after the last line, up to the cap", tail: "100", lastEventID: last.Format(time.RFC3339Nano),
			want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Since: last, Tail: MaxLogTail}, After: last},
		},
		{name: "deploy", deploy: "11", want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: DefaultLogTail}, Deploy: generation(11)}},
		{name: "a service's first deploy", deploy: "0", want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: DefaultLogTail}, Deploy: generation(0)}},
		{name: "pooler", source: "pooler", want: LogQuery{LogOptions: docker.LogOptions{Follow: true, Tail: DefaultLogTail}, Pooler: true}},
		{name: "tail zero", tail: "0", wantErr: "tail must be"},
		{name: "tail too big", tail: "10001", wantErr: "tail must be"},
		{name: "tail not a number", tail: "all", wantErr: "tail must be"},
		{name: "negative since", since: "-1h", wantErr: "since must be"},
		{name: "since not a duration", since: "yesterday", wantErr: "since must be"},
		{name: "bad last event ID", lastEventID: "42", wantErr: "Last-Event-ID"},
		{name: "negative deploy", deploy: "-1", wantErr: "deploy must be"},
		{name: "deploy not a number", deploy: "latest", wantErr: "deploy must be"},
		{name: "unknown source", source: "server", wantErr: "source must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLogQuery(tt.tail, tt.since, tt.source, tt.deploy, tt.lastEventID, now)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGroupDeploys(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 27, h, 0, 0, 0, time.UTC) }
	tasks := []docker.Task{
		{ID: "a", Generation: 6, Image: "app@sha256:6", CreatedAt: at(1)},
		{ID: "b", Generation: 10, Image: "app@sha256:10", CreatedAt: at(3)},
		// A rollback: Swarm went back to 10 and stopped 11.
		{ID: "c", Generation: 11, Image: "app@sha256:11", CreatedAt: at(5)},
		{ID: "d", Generation: 10, Image: "app@sha256:10", CreatedAt: at(6), Running: true},
		{ID: "e", Generation: 10, Image: "app@sha256:10", CreatedAt: at(2)},
	}

	got := groupDeploys(tasks)

	assert.Equal(t, []LogDeploy{
		{Generation: 11, StartedAt: at(5), image: "app@sha256:11", tasks: []string{"c"}},
		{Generation: 10, Current: true, StartedAt: at(2), image: "app@sha256:10", tasks: []string{"b", "d", "e"}},
		{Generation: 6, StartedAt: at(1), image: "app@sha256:6", tasks: []string{"a"}},
	}, got)
	assert.Empty(t, groupDeploys(nil))

	// Nothing running: the newest is current.
	stopped := groupDeploys([]docker.Task{{ID: "a", Generation: 1}, {ID: "b", Generation: 2}})
	assert.True(t, stopped[0].Current)
}

func TestMatchDeployment(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 27, h, 0, 0, 0, time.UTC) }
	deployment := func(id uint, digest string, started time.Time) models.Deployment {
		return models.Deployment{
			Model:     orm.Model{ID: id},
			Ref:       fmt.Sprintf("v%d", id),
			Digest:    &digest,
			StartedAt: carbon.NewDateTime(carbon.FromStdTime(started)),
		}
	}
	// Newest first, as AppLogDeploys reads them. 3 redeploys 1's image.
	deployments := []models.Deployment{
		deployment(3, "app@sha256:1", at(5)),
		deployment(2, "app@sha256:2", at(3)),
		deployment(1, "app@sha256:1", at(1)),
	}

	tests := []struct {
		name  string
		image string
		start time.Time
		want  *LogDeployment
	}{
		{name: "its first task", image: "app@sha256:2", start: at(3), want: &LogDeployment{ID: 2, Ref: "v2", StartedAt: at(3)}},
		{name: "a restart long after", image: "app@sha256:2", start: at(9), want: &LogDeployment{ID: 2, Ref: "v2", StartedAt: at(3)}},
		{name: "the same image, deployed again", image: "app@sha256:1", start: at(6), want: &LogDeployment{ID: 3, Ref: "v3", StartedAt: at(5)}},
		{name: "the same image, first time", image: "app@sha256:1", start: at(2), want: &LogDeployment{ID: 1, Ref: "v1", StartedAt: at(1)}},
		{name: "no deployment deployed it", image: "app@sha256:9", start: at(6)},
		{name: "older than every deployment", image: "app@sha256:1", start: at(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchDeployment(LogDeploy{image: tt.image, StartedAt: tt.start}, deployments))
		})
	}
}

func generation(n uint64) *uint64 { return &n }

func TestDeployFilter(t *testing.T) {
	// Swarm creates a service with ForceUpdate 0, so its first deploy is
	// generation 0.
	deploys := []LogDeploy{
		{Generation: 2, tasks: []string{"c"}},
		{Generation: 1, Current: true, tasks: []string{"b1", "b2"}},
		{Generation: 0, tasks: []string{"a"}},
	}

	keep, past, found := deployFilter(deploys, nil)
	require.True(t, found)
	assert.False(t, past)
	assert.True(t, keep("b1"))
	assert.True(t, keep("b2"))
	assert.True(t, keep("new"), "a task made after the list is the current deploy's")
	assert.False(t, keep("a"))
	assert.False(t, keep("c"), "a rolled back deploy isn't the current one")

	keep, past, found = deployFilter(deploys, generation(0))
	require.True(t, found)
	assert.True(t, past)
	assert.True(t, keep("a"))
	assert.False(t, keep("b1"))
	assert.False(t, keep("new"))

	_, past, found = deployFilter(deploys, generation(1))
	assert.True(t, found)
	assert.False(t, past, "asking for the current deploy by number follows it")

	_, _, found = deployFilter(deploys, generation(9))
	assert.False(t, found)
	_, _, found = deployFilter(nil, generation(0))
	assert.False(t, found, "generation 0 is a deploy like any other, not the current one")

	keep, past, found = deployFilter(nil, nil)
	assert.True(t, found && !past && keep("anything"), "with no tasks listed, every line is the current deploy's")
}
