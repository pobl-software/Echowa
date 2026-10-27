package docker

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func service(replicas uint64, forceUpdate uint64, image string, status *swarm.UpdateStatus) swarm.Service {
	return swarm.Service{
		Spec: swarm.ServiceSpec{
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: image},
				ForceUpdate:   forceUpdate,
			},
		},
		UpdateStatus: status,
	}
}

func task(forceUpdate uint64, image string, state swarm.TaskState) swarm.Task {
	desired := swarm.TaskStateRunning
	if state == swarm.TaskStateShutdown || state == swarm.TaskStateFailed {
		desired = swarm.TaskStateShutdown
	}

	return swarm.Task{
		Spec:         swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: image}, ForceUpdate: forceUpdate},
		DesiredState: desired,
		Status:       swarm.TaskStatus{State: state},
	}
}

func TestUpdateProgress(t *testing.T) {
	const a, b = "app@sha256:aaa", "app@sha256:bbb"
	running, shutdown, failed := swarm.TaskStateRunning, swarm.TaskStateShutdown, swarm.TaskStateFailed
	status := func(state swarm.UpdateState, message string) *swarm.UpdateStatus {
		return &swarm.UpdateStatus{State: state, Message: message}
	}

	tests := []struct {
		name    string
		svc     swarm.Service
		tasks   []swarm.Task
		done    bool
		outcome Outcome
		message string
	}{
		{
			name:  "new service, task not running yet",
			svc:   service(1, 0, a, nil),
			tasks: []swarm.Task{task(0, a, swarm.TaskStatePreparing)},
		},
		{
			name:    "new service, all replicas running",
			svc:     service(2, 0, a, nil),
			tasks:   []swarm.Task{task(0, a, running), task(0, a, running)},
			done:    true,
			outcome: OutcomeCompleted,
		},
		{
			name:  "new service, one of two replicas running",
			svc:   service(2, 0, a, nil),
			tasks: []swarm.Task{task(0, a, running), task(0, a, failed)},
		},
		{
			name:  "update sent, orchestrator hasn't started: old tasks don't count",
			svc:   service(1, 4, b, nil),
			tasks: []swarm.Task{task(3, a, running)},
		},
		{
			name:  "same image redeployed: the force counter tells tasks apart",
			svc:   service(1, 4, a, nil),
			tasks: []swarm.Task{task(3, a, running)},
		},
		{
			name:  "updating",
			svc:   service(1, 4, b, status(swarm.UpdateStateUpdating, "update in progress")),
			tasks: []swarm.Task{task(3, a, running), task(4, b, swarm.TaskStateStarting)},
		},
		{
			name:    "update completed",
			svc:     service(1, 4, b, status(swarm.UpdateStateCompleted, "update completed")),
			tasks:   []swarm.Task{task(3, a, shutdown), task(4, b, running)},
			done:    true,
			outcome: OutcomeCompleted,
			message: "update completed",
		},
		{
			name:  "rolling back",
			svc:   service(1, 3, a, status(swarm.UpdateStateRollbackStarted, "update rolled back due to failure")),
			tasks: []swarm.Task{task(4, b, failed)},
		},
		{
			name:    "rolled back",
			svc:     service(1, 3, a, status(swarm.UpdateStateRollbackCompleted, "rollback completed")),
			tasks:   []swarm.Task{task(3, a, running), task(4, b, failed)},
			done:    true,
			outcome: OutcomeRolledBack,
			message: "rollback completed",
		},
		{
			name:    "update paused",
			svc:     service(1, 4, b, status(swarm.UpdateStatePaused, "update paused due to failure")),
			done:    true,
			outcome: OutcomePaused,
			message: "update paused due to failure",
		},
		{
			name:    "rollback paused",
			svc:     service(1, 3, a, status(swarm.UpdateStateRollbackPaused, "rollback paused")),
			done:    true,
			outcome: OutcomePaused,
			message: "rollback paused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, result, _ := updateProgress(tt.svc, tt.tasks)
			assert.Equal(t, tt.done, done)
			if tt.done {
				assert.Equal(t, UpdateResult{Outcome: tt.outcome, Message: tt.message}, result)
			}
		})
	}
}

func TestUpdateProgressHintIsLatestTaskError(t *testing.T) {
	older := task(1, "a", swarm.TaskStateFailed)
	older.Status.Err = "older error"
	older.Status.Timestamp = time.Unix(100, 0)
	newer := task(1, "a", swarm.TaskStateRejected)
	newer.Status.Err = "No such image: a"
	newer.Status.Timestamp = time.Unix(200, 0)

	_, _, hint := updateProgress(service(1, 1, "a", nil), []swarm.Task{newer, older})
	assert.Equal(t, "No such image: a", hint)
}

func TestKilledContainersAreTheTasksThatExitedWith137(t *testing.T) {
	task := func(container string, exit int) swarm.Task {
		return swarm.Task{Status: swarm.TaskStatus{ContainerStatus: &swarm.ContainerStatus{ContainerID: container, ExitCode: exit}}}
	}
	tasks := []swarm.Task{task("ok", 0), task("crashed", 1), task("killed", 137), task("", 137), {}}

	assert.Equal(t, []string{"killed"}, killedContainers(tasks))
}
