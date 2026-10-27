package models

import (
	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

// ScheduledTask runs Command in a custom app's newest running task when
// Schedule is due in Timezone (ADR 0032). Read the schedule with
// tasks.Parse.
type ScheduledTask struct {
	orm.Model
	AppID uint   `json:"app_id"`
	Name  string `json:"name"`
	// Schedule is a five-field cron expression. Presets are expressions
	// the page names, such as "* * * * *" for every minute.
	Schedule string `json:"schedule"`
	// Timezone is an IANA name, such as "Europe/London" or "UTC".
	Timezone string `json:"timezone"`
	// Command is run with sh -c, with the app's env, user and working
	// directory.
	Command string `json:"command"`
	// TimeoutMinutes is how long a run may take before Echowa stops
	// waiting for it.
	TimeoutMinutes int  `json:"timeout_minutes"`
	Enabled        bool `json:"enabled"`
	// DismissedRunID is the newest failed run dismissed on the Overview
	// tab. A failed run with a higher ID shows again.
	DismissedRunID *uint `json:"dismissed_run_id"`
}

// RunStatus is where a scheduled task's run is, or how it ended.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	// RunFailed exited with a status other than 0.
	RunFailed RunStatus = "failed"
	// RunTimedOut went past the task's timeout. The command may still be
	// running in the container.
	RunTimedOut RunStatus = "timed_out"
	// RunSkipped found no running task to run in.
	RunSkipped RunStatus = "skipped"
	// RunInterrupted was running when Echowa stopped.
	RunInterrupted RunStatus = "interrupted"
)

// RunTrigger is what started a run.
type RunTrigger string

const (
	RunOnSchedule RunTrigger = "schedule"
	RunByHand     RunTrigger = "manual"
)

// ScheduledTaskRun is one run of a scheduled task.
type ScheduledTaskRun struct {
	orm.Model
	ScheduledTaskID uint       `json:"scheduled_task_id"`
	Trigger         RunTrigger `json:"trigger"`
	Status          RunStatus  `json:"status"`
	// ExitCode is nil until the command exits, and for a run that never
	// got one: skipped, timed out or interrupted.
	ExitCode *int `json:"exit_code"`
	// Output is the tail of stdout and stderr, in the order written.
	Output string `json:"output"`
	// Message is Echowa's own note, such as why the run was skipped.
	Message    *string          `json:"message"`
	StartedAt  *carbon.DateTime `json:"started_at"`
	FinishedAt *carbon.DateTime `json:"finished_at"`
}
