package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/alerts"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/tasks"
)

// Limits on what a scheduled task's runs keep (ADR 0032).
const (
	// MaxTaskOutput is how much of a run's output is kept, from its end.
	MaxTaskOutput = 64 << 10
	// KeepTaskRuns is how many of each task's runs pruning keeps.
	KeepTaskRuns = 100
)

// ErrTaskRunning means the task's last run hasn't finished. A task never
// overlaps itself.
var ErrTaskRunning = errors.New("this task is already running")

// taskStart separates this process's runs from those an earlier one left
// running. Stored times have second precision, hence the truncation.
var taskStart = time.Now().Truncate(time.Second)

// runningTasks holds the IDs of tasks with a run in progress, and waits for
// them in tests. One Echowa process runs per server (ADR 0013), so memory
// is enough.
var runningTasks = struct {
	sync.Mutex
	ids map[uint]bool
	wg  sync.WaitGroup
}{ids: map[uint]bool{}}

// RunDueTasks starts, each in the background, the enabled tasks of custom
// apps that are due in the minute holding now. A task still running from
// before is skipped.
func RunDueTasks(ctx context.Context, now time.Time) error {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().WhereNull("template").Get(&apps); err != nil {
		return err
	}
	byID := map[uint]*models.App{}
	ids := make([]any, 0, len(apps))
	for i := range apps {
		byID[apps[i].ID] = &apps[i]
		ids = append(ids, apps[i].ID)
	}
	if len(ids) == 0 {
		return nil
	}

	var due []models.ScheduledTask
	err := facades.Orm().WithContext(ctx).Query().Where("enabled", true).WhereIn("app_id", ids).Order("id").Get(&due)
	if err != nil {
		return err
	}
	for i := range due {
		task := &due[i]
		schedule, err := tasks.Parse(task.Schedule, task.Timezone)
		if err != nil {
			facades.Log().Error(fmt.Errorf("scheduled task %d: %w", task.ID, err))
			continue
		}
		if !schedule.Due(now) {
			continue
		}
		if _, err := startRun(byID[task.AppID], task, models.RunOnSchedule); errors.Is(err, ErrTaskRunning) {
			facades.Log().Info(fmt.Sprintf("scheduled task %d (%s): skipped, its last run is still going", task.ID, task.Name))
		} else if err != nil {
			facades.Log().Error(fmt.Errorf("scheduled task %d: %w", task.ID, err))
		}
	}

	return nil
}

// RunTask starts a run of the task now, in the background, whether or not
// it's enabled. The error is ErrTaskRunning while a run is going.
func RunTask(app *models.App, task *models.ScheduledTask) (*models.ScheduledTaskRun, error) {
	return startRun(app, task, models.RunByHand)
}

// WaitForTaskRuns waits until no run is going. For tests.
func WaitForTaskRuns() {
	runningTasks.wg.Wait()
}

func startRun(app *models.App, task *models.ScheduledTask, trigger models.RunTrigger) (*models.ScheduledTaskRun, error) {
	runningTasks.Lock()
	defer runningTasks.Unlock()
	if runningTasks.ids[task.ID] {
		return nil, ErrTaskRunning
	}

	run := &models.ScheduledTaskRun{
		ScheduledTaskID: task.ID,
		Trigger:         trigger,
		Status:          models.RunRunning,
		StartedAt:       carbon.NewDateTime(carbon.Now()),
	}
	if err := facades.Orm().Query().Create(run); err != nil {
		return nil, err
	}

	runningTasks.ids[task.ID] = true
	runningTasks.wg.Add(1)
	go func() {
		defer func() {
			runningTasks.Lock()
			delete(runningTasks.ids, task.ID)
			runningTasks.Unlock()
			runningTasks.wg.Done()
		}()
		finishRun(run, execRun(app, task))
	}()

	return run, nil
}

// runResult is how a run ended.
type runResult struct {
	status   models.RunStatus
	exitCode *int
	output   string
	message  string
}

func execRun(app *models.App, task *models.ScheduledTask) runResult {
	timeout := time.Duration(task.TimeoutMinutes) * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out := &docker.TailBuffer{Limit: MaxTaskOutput}
	code, err := Docker().ExecTo(ctx, app.ServiceName(), []string{"sh", "-c", task.Command}, out)
	result := runResult{output: out.String()}
	switch {
	case errors.Is(err, docker.ErrNotRunning):
		result.status = models.RunSkipped
		result.message = "The app has no running task to run in."
	case errors.Is(err, context.DeadlineExceeded):
		result.status = models.RunTimedOut
		result.message = fmt.Sprintf("Stopped waiting after %s. The command may still be running in the container.", minutes(task.TimeoutMinutes))
	case err != nil:
		result.status = models.RunFailed
		result.message = err.Error()
	default:
		result.exitCode = &code
		result.status = models.RunSucceeded
		if code != 0 {
			result.status = models.RunFailed
		}
	}

	return result
}

func finishRun(run *models.ScheduledTaskRun, result runResult) {
	var message *string
	if result.message != "" {
		message = &result.message
	}
	_, err := facades.Orm().Query().Model(&models.ScheduledTaskRun{}).Where("id", run.ID).Update(map[string]any{
		"status":      result.status,
		"exit_code":   result.exitCode,
		"output":      result.output,
		"message":     message,
		"finished_at": carbon.NewDateTime(carbon.Now()),
	})
	if err != nil {
		facades.Log().Error(fmt.Errorf("scheduled task run %d: %w", run.ID, err))
	}
	if result.status == models.RunFailed || result.status == models.RunTimedOut {
		notifyRun(run, result)
	}
}

// notifyRun tells the task rules that run failed or timed out.
func notifyRun(run *models.ScheduledTaskRun, result runResult) {
	var task models.ScheduledTask
	if err := facades.Orm().Query().Find(&task, run.ScheduledTaskID); err != nil || task.ID == 0 {
		return
	}
	var app models.App
	if err := facades.Orm().Query().Find(&app, task.AppID); err != nil || app.ID == 0 {
		return
	}
	e := alerts.Event{
		Trigger: alerts.TaskFailed,
		App:     &app,
		Title:   fmt.Sprintf("Task %s on %s failed", task.Name, app.Name),
		Summary: result.message,
		Path:    fmt.Sprintf("/apps/%d?tab=tasks", app.ID),
	}
	if result.status == models.RunTimedOut {
		e.Title = fmt.Sprintf("Task %s on %s timed out", task.Name, app.Name)
	}
	if result.exitCode != nil {
		e.Fields = append(e.Fields, notify.Field{Label: "Exit code", Value: strconv.Itoa(*result.exitCode)})
		if e.Summary == "" {
			e.Summary = fmt.Sprintf("The command exited with %d.", *result.exitCode)
		}
	}
	Notify(e)
}

func minutes(n int) string {
	if n == 1 {
		return "1 minute"
	}

	return fmt.Sprintf("%d minutes", n)
}

// RecoverTaskRuns marks runs an earlier Echowa left running as
// interrupted: nobody is waiting for them any more.
func RecoverTaskRuns(ctx context.Context) error {
	var running []models.ScheduledTaskRun
	if err := facades.Orm().WithContext(ctx).Query().Where("status", models.RunRunning).Get(&running); err != nil {
		return err
	}
	// Compared in Go, as deploy.Recover does.
	var ids []any
	for _, run := range running {
		if run.StartedAt == nil || run.StartedAt.StdTime().Before(taskStart) {
			ids = append(ids, run.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := facades.Orm().WithContext(ctx).Query().Model(&models.ScheduledTaskRun{}).WhereIn("id", ids).Update(map[string]any{
		"status":      models.RunInterrupted,
		"message":     "Echowa restarted while this ran. The command may have finished in the container.",
		"finished_at": carbon.NewDateTime(carbon.Now()),
	})

	return err
}

// PruneTaskRuns keeps each task's newest KeepTaskRuns runs.
func PruneTaskRuns(ctx context.Context) error {
	_, err := facades.Orm().WithContext(ctx).Query().Exec(`
		DELETE FROM scheduled_task_runs WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY scheduled_task_id ORDER BY id DESC) AS n
				FROM scheduled_task_runs
			) WHERE n > ?
		)`, KeepTaskRuns)

	return err
}
