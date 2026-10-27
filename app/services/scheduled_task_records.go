package services

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/tasks"
)

// ScheduledTaskInput is the scheduled task form, validated by
// rules.ScheduledTask.
type ScheduledTaskInput struct {
	Name           string `form:"name" json:"name"`
	Schedule       string `form:"schedule" json:"schedule"`
	Timezone       string `form:"timezone" json:"timezone"`
	Command        string `form:"command" json:"command"`
	TimeoutMinutes int    `form:"timeout_minutes" json:"timeout_minutes"`
	Enabled        bool   `form:"enabled" json:"enabled"`
}

// SaveScheduledTask creates task, or updates it when it has an ID, from in.
func SaveScheduledTask(ctx context.Context, task *models.ScheduledTask, in ScheduledTaskInput) error {
	task.Name = in.Name
	task.Schedule = strings.Join(strings.Fields(in.Schedule), " ")
	task.Timezone = in.Timezone
	task.Command = in.Command
	task.TimeoutMinutes = in.TimeoutMinutes
	task.Enabled = in.Enabled

	return facades.Orm().WithContext(ctx).Query().Save(task)
}

// TaskRun is a run without its output, for lists.
type TaskRun struct {
	ID         uint              `json:"id"`
	Trigger    models.RunTrigger `json:"trigger"`
	Status     models.RunStatus  `json:"status"`
	ExitCode   *int              `json:"exit_code"`
	Message    *string           `json:"message"`
	StartedAt  *carbon.DateTime  `json:"started_at"`
	FinishedAt *carbon.DateTime  `json:"finished_at"`
}

var taskRunColumns = []string{"id", "trigger", "status", "exit_code", "message", "started_at", "finished_at"}

// ScheduledTaskSummary is a task as the Scheduled tasks section lists it.
type ScheduledTaskSummary struct {
	models.ScheduledTask
	// NextRunAt is nil while the task is disabled.
	NextRunAt *time.Time `json:"next_run_at"`
	LastRun   *TaskRun   `json:"last_run"`
}

// ScheduledTasks lists an app's tasks by name, each with its next and last
// runs.
func ScheduledTasks(ctx context.Context, appID uint, now time.Time) ([]ScheduledTaskSummary, error) {
	var list []models.ScheduledTask
	if err := facades.Orm().WithContext(ctx).Query().Where("app_id", appID).Order("name").Order("id").Get(&list); err != nil {
		return nil, err
	}

	summaries := make([]ScheduledTaskSummary, len(list))
	for i, task := range list {
		summaries[i].ScheduledTask = task
		if schedule, err := tasks.Parse(task.Schedule, task.Timezone); err == nil && task.Enabled {
			if next := schedule.Next(now, 1); len(next) == 1 {
				summaries[i].NextRunAt = &next[0]
			}
		}
		runs, err := TaskRuns(ctx, task.ID, 1)
		if err != nil {
			return nil, err
		}
		if len(runs) == 1 {
			summaries[i].LastRun = &runs[0]
		}
	}

	return summaries, nil
}

// TaskRuns lists a task's newest runs, newest first, without their output.
func TaskRuns(ctx context.Context, taskID uint, limit int) ([]TaskRun, error) {
	runs := []TaskRun{}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.ScheduledTaskRun{}).Select(taskRunColumns...).
		Where("scheduled_task_id", taskID).Order("id desc").Limit(limit).Get(&runs)

	return runs, err
}

// TaskFailure is a task's newest failed or timed-out run that hasn't been
// dismissed, as the app's Overview shows it (ADR 0032).
type TaskFailure struct {
	TaskID   uint    `json:"task_id"`
	TaskName string  `json:"task_name"`
	Run      TaskRun `json:"run"`
	// Tail is the end of the run's output.
	Tail string `json:"tail"`
	// SucceededAt is when the newest later run that succeeded finished.
	SucceededAt *carbon.DateTime `json:"succeeded_at"`
}

// How much of a failed run's output the Overview shows.
const (
	taskFailureLines = 5
	taskFailureBytes = 1024
)

// TaskFailures lists, by task name, each of an app's tasks whose newest
// failed or timed-out run is newer than the one dismissed. Skipped and
// interrupted runs aren't the command's fault, so they don't count.
func TaskFailures(ctx context.Context, appID uint) ([]TaskFailure, error) {
	var list []models.ScheduledTask
	if err := facades.Orm().WithContext(ctx).Query().Where("app_id", appID).Order("name").Order("id").Get(&list); err != nil {
		return nil, err
	}

	failures := []TaskFailure{}
	for _, task := range list {
		var dismissed uint
		if task.DismissedRunID != nil {
			dismissed = *task.DismissedRunID
		}
		var runs []models.ScheduledTaskRun
		err := facades.Orm().WithContext(ctx).Query().Where("scheduled_task_id", task.ID).Where("id > ?", dismissed).
			WhereIn("status", []any{models.RunFailed, models.RunTimedOut}).Order("id desc").Limit(1).Get(&runs)
		if err != nil {
			return nil, err
		}
		if len(runs) == 0 {
			continue
		}
		run := runs[0]
		failure := TaskFailure{
			TaskID:   task.ID,
			TaskName: task.Name,
			Run: TaskRun{
				ID: run.ID, Trigger: run.Trigger, Status: run.Status, ExitCode: run.ExitCode,
				Message: run.Message, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
			},
			Tail: outputTail(run.Output),
		}

		var since []TaskRun
		err = facades.Orm().WithContext(ctx).Query().Model(&models.ScheduledTaskRun{}).Select(taskRunColumns...).Where("scheduled_task_id", task.ID).
			Where("id > ?", run.ID).Where("status", models.RunSucceeded).Order("id desc").Limit(1).Get(&since)
		if err != nil {
			return nil, err
		}
		if len(since) == 1 {
			failure.SucceededAt = since[0].FinishedAt
		}
		failures = append(failures, failure)
	}

	return failures, nil
}

// DismissTaskFailure hides the Overview's alert for run and the task's
// failures before it. A run older than the one already dismissed changes
// nothing, so a page loaded before a newer dismissal can't bring an old
// failure back.
func DismissTaskFailure(ctx context.Context, task *models.ScheduledTask, run *models.ScheduledTaskRun) error {
	_, err := facades.Orm().WithContext(ctx).Query().Model(&models.ScheduledTask{}).
		Where("id", task.ID).Where("(dismissed_run_id IS NULL OR dismissed_run_id < ?)", run.ID).
		Update("dismissed_run_id", run.ID)

	return err
}

// outputTail is the last few lines of output, cut to a whole character
// when they're long.
func outputTail(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	tail := strings.Join(lines[max(0, len(lines)-taskFailureLines):], "\n")
	if len(tail) <= taskFailureBytes {
		return tail
	}
	tail = tail[len(tail)-taskFailureBytes:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}

	return "…" + tail
}
