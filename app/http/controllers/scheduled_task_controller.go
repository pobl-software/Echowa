package controllers

import (
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/tasks"
)

// ScheduledTaskController is a custom app's Scheduled tasks section (ADR
// 0032). A template app has none.
type ScheduledTaskController struct{}

func NewScheduledTaskController() *ScheduledTaskController {
	return &ScheduledTaskController{}
}

func (r *ScheduledTaskController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findCustomApp(ctx, &app); resp != nil {
		return resp
	}

	return r.save(ctx, &app, &models.ScheduledTask{AppID: app.ID}, "Added")
}

func (r *ScheduledTaskController) Update(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}

	return r.save(ctx, &app, &task, "Saved")
}

func (r *ScheduledTaskController) save(ctx http.Context, app *models.App, task *models.ScheduledTask, done string) http.Response {
	validator, err := ctx.Request().Validate(rules.ScheduledTask(), rules.ScheduledTaskFilters(), rules.ScheduledTaskMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.ScheduledTaskInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if err := services.SaveScheduledTask(ctx, task, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("%s %s.", done, task.Name))

	return inertia.Redirect(ctx, tasksURL(app))
}

// Enabled turns the task's schedule on or off.
func (r *ScheduledTaskController) Enabled(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}
	validator, err := ctx.Request().Validate(map[string]any{"enabled": "required|boolean"})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	enabled := ctx.Request().InputBool("enabled")
	if _, err := facades.Orm().WithContext(ctx).Query().Model(&task).Update("enabled", enabled); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if enabled {
		inertia.Flash(ctx, "success", task.Name+" runs on its schedule again.")
	} else {
		inertia.Flash(ctx, "success", task.Name+" won't run on its schedule until you turn it back on.")
	}

	return inertia.Redirect(ctx, tasksURL(&app))
}

// Destroy deletes the task and its runs. A run in progress carries on in
// the container.
func (r *ScheduledTaskController) Destroy(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}
	if _, err := facades.Orm().WithContext(ctx).Query().Delete(&task); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Deleted "+task.Name+".")

	return inertia.Redirect(ctx, tasksURL(&app))
}

// Run starts the task now, in the background.
func (r *ScheduledTaskController) Run(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}
	_, err := services.RunTask(&app, &task)
	switch {
	case errors.Is(err, services.ErrTaskRunning):
		inertia.Flash(ctx, "info", task.Name+" is already running.")
	case err != nil:
		return inertia.ServerError(ctx, err)
	default:
		inertia.Flash(ctx, "success", "Started "+task.Name+".")
	}

	return inertia.Back(ctx)
}

// Dismiss hides the Overview's alert for the posted run, which must be
// this task's, and for the task's failures before it.
func (r *ScheduledTaskController) Dismiss(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}
	var run models.ScheduledTaskRun
	err := facades.Orm().WithContext(ctx).Query().
		Where("id", ctx.Request().InputInt("run")).Where("scheduled_task_id", task.ID).FirstOrFail(&run)
	if err != nil {
		return notFound(ctx)
	}
	if err := services.DismissTaskFailure(ctx, &task, &run); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Back(ctx)
}

// Runs lists the task's newest runs, as JSON, without their output.
func (r *ScheduledTaskController) Runs(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}
	runs, err := services.TaskRuns(ctx, task.ID, services.KeepTaskRuns)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, http.Json{"runs": runs})
}

// ShowRun is one run with its output, as JSON.
func (r *ScheduledTaskController) ShowRun(ctx http.Context) http.Response {
	var app models.App
	var task models.ScheduledTask
	if resp := findTask(ctx, &app, &task); resp != nil {
		return resp
	}
	var run models.ScheduledTaskRun
	err := facades.Orm().WithContext(ctx).Query().
		Where("id", ctx.Request().RouteInt("run")).Where("scheduled_task_id", task.ID).FirstOrFail(&run)
	if err != nil {
		return notFound(ctx)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, run)
}

// Preview is the form's next three runs of a schedule, or why it can't
// run, as JSON.
func (r *ScheduledTaskController) Preview(ctx http.Context) http.Response {
	schedule, err := tasks.Parse(ctx.Request().Query("schedule"), ctx.Request().Query("timezone"))
	if err != nil {
		return ctx.Response().Json(http.StatusOK, http.Json{"next": []time.Time{}, "error": err.Error()})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"next": schedule.Next(time.Now(), 3), "error": nil})
}

// findCustomApp loads the {app} route's app, or answers 404 for a missing
// one or a template app, which has no scheduled tasks.
func findCustomApp(ctx http.Context, app *models.App) http.Response {
	if resp := findOr404(ctx, "app", app); resp != nil {
		return resp
	}
	if app.Template != nil {
		return notFound(ctx)
	}

	return nil
}

// findTask loads the {app} route's custom app and its {task}.
func findTask(ctx http.Context, app *models.App, task *models.ScheduledTask) http.Response {
	if resp := findCustomApp(ctx, app); resp != nil {
		return resp
	}
	err := facades.Orm().WithContext(ctx).Query().
		Where("id", ctx.Request().RouteInt("task")).Where("app_id", app.ID).FirstOrFail(task)
	if err != nil {
		return notFound(ctx)
	}

	return nil
}

func tasksURL(app *models.App) string {
	return fmt.Sprintf("/apps/%d?tab=tasks", app.ID)
}
