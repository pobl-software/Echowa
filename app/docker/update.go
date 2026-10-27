package docker

import (
	"github.com/moby/moby/api/types/swarm"
)

// updateProgress decides whether the last deploy of svc has finished.
//
// DeployService bumps TaskTemplate.ForceUpdate on every update, and Swarm
// clears UpdateStatus when a new spec arrives. So a set UpdateStatus always
// belongs to the latest deploy. With none (a new service, or an update the
// orchestrator hasn't picked up yet) the deploy is done once every replica
// runs a task built from the current spec. hint is the latest task error,
// for timeout messages.
func updateProgress(svc swarm.Service, tasks []swarm.Task) (done bool, result UpdateResult, hint string) {
	hint = latestTaskError(tasks)

	if svc.UpdateStatus != nil {
		message := svc.UpdateStatus.Message
		switch UpdateState(svc.UpdateStatus.State) {
		case UpdateCompleted:
			return true, UpdateResult{Outcome: OutcomeCompleted, Message: message}, hint
		case UpdateRollbackCompleted:
			return true, UpdateResult{Outcome: OutcomeRolledBack, Message: message}, hint
		case UpdatePaused, UpdateRollbackPaused:
			return true, UpdateResult{Outcome: OutcomePaused, Message: message}, hint
		default:
			return false, UpdateResult{}, hint
		}
	}

	if runningCurrent(svc, tasks) >= desiredReplicas(svc) {
		return true, UpdateResult{Outcome: OutcomeCompleted}, hint
	}

	return false, UpdateResult{}, hint
}

// runningCurrent counts running tasks built from the service's current spec.
func runningCurrent(svc swarm.Service, tasks []swarm.Task) uint64 {
	template := svc.Spec.TaskTemplate
	var n uint64
	for _, task := range tasks {
		if task.DesiredState != swarm.TaskStateRunning || task.Status.State != swarm.TaskStateRunning {
			continue
		}
		if task.Spec.ForceUpdate != template.ForceUpdate || task.Spec.ContainerSpec == nil ||
			template.ContainerSpec == nil || task.Spec.ContainerSpec.Image != template.ContainerSpec.Image {
			continue
		}
		n++
	}

	return n
}

func desiredReplicas(svc swarm.Service) uint64 {
	if svc.Spec.Mode.Replicated == nil || svc.Spec.Mode.Replicated.Replicas == nil {
		return 1
	}

	return *svc.Spec.Mode.Replicated.Replicas
}

func latestTaskError(tasks []swarm.Task) string {
	var latest swarm.Task
	for _, task := range tasks {
		if task.Status.Err != "" && task.Status.Timestamp.After(latest.Status.Timestamp) {
			latest = task
		}
	}

	return latest.Status.Err
}

// killedContainers are the containers of tasks that exited with 137: killed
// by SIGKILL, which is how the kernel ends a task over its memory limit.
func killedContainers(tasks []swarm.Task) []string {
	var ids []string
	for _, task := range tasks {
		status := task.Status.ContainerStatus
		if status != nil && status.ExitCode == 137 && status.ContainerID != "" {
			ids = append(ids, status.ContainerID)
		}
	}

	return ids
}
