package controllers

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/deploy"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// AppServicesController is an app's Services tab: the custom apps that
// support it, such as a queue worker or a PDF renderer (ADR 0037). Only a
// custom app that isn't itself a service has one.
type AppServicesController struct{}

func NewAppServicesController() *AppServicesController {
	return &AppServicesController{}
}

// ServiceChoices is what the Services tab can add. PartOf lists the apps
// that have this one as a service; while it isn't empty, it can't have
// services of its own.
type ServiceChoices struct {
	Existing []services.ServiceChoice `json:"existing"`
	PartOf   []services.AppRef        `json:"part_of"`
}

// Store creates a service, links it and deploys it straight away. It can
// start with a copy of the app's environment, which a worker on the app's
// own image needs.
func (r *AppServicesController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := r.findParent(ctx, &app); resp != nil {
		return resp
	}
	if resp := r.refuseIfService(ctx, &app, "name"); resp != nil {
		return resp
	}

	validator, err := ctx.Request().Validate(rules.AppService(), rules.AppFilters(), rules.AppMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in struct {
		services.AppInput
		CopyEnv bool `form:"copy_env" json:"copy_env"`
	}
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if resp := NewAppController().checkRegistry(ctx, in.AppInput); resp != nil {
		return resp
	}

	service, err := services.CreateApp(ctx, in.AppInput, nil)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if in.CopyEnv {
		env, err := services.EnvVars(ctx, app.ID)
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		if err := services.SetEnvVars(ctx, service.ID, env); err != nil {
			return inertia.ServerError(ctx, err)
		}
	}
	if err := services.LinkApps(ctx, app.ID, service.ID); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if _, err := deploy.Request(ctx, service, models.TriggerManual, service.DefaultTag); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Created %s, deploying now.", service.Name))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=services", app.ID))
}

// Link links existing custom apps as services. Each must have no services
// of its own, so services stay one level deep.
func (r *AppServicesController) Link(ctx http.Context) http.Response {
	var app models.App
	if resp := r.findParent(ctx, &app); resp != nil {
		return resp
	}
	if resp := r.refuseIfService(ctx, &app, "link"); resp != nil {
		return resp
	}

	validator, err := ctx.Request().Validate(map[string]any{"link": "array|max:100", "link.*": "integer|distinct"})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in struct {
		Link []uint `form:"link" json:"link"`
	}
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if len(in.Link) == 0 {
		return failed(ctx, map[string]string{"link": "Pick a service to link."})
	}

	ids := make([]any, len(in.Link))
	for i, id := range in.Link {
		ids[i] = id
	}
	var linked []models.App
	if err := facades.Orm().WithContext(ctx).Query().WhereIn("id", ids).Where("template IS NULL").Order("name").Get(&linked); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if len(linked) != len(in.Link) {
		return failed(ctx, map[string]string{"link": "Only apps of your own image can be linked as services. Databases go on the Databases tab."})
	}
	var names []string
	for _, service := range linked {
		if service.ID == app.ID {
			return failed(ctx, map[string]string{"link": "An app can't be its own service."})
		}
		nested, err := services.HasServices(ctx, service.ID)
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		if nested {
			return failed(ctx, map[string]string{"link": fmt.Sprintf("%s has services of its own, so it can't be one.", service.Name)})
		}
		names = append(names, service.Name)
	}
	for _, service := range linked {
		if err := services.LinkApps(ctx, app.ID, service.ID); err != nil {
			return inertia.ServerError(ctx, err)
		}
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Linked %s.", strings.Join(names, ", ")))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=services", app.ID))
}

// Destroy unlinks a service. The service itself keeps running.
func (r *AppServicesController) Destroy(ctx http.Context) http.Response {
	var app, service models.App
	if resp := r.findParent(ctx, &app); resp != nil {
		return resp
	}
	if resp := findOr404(ctx, "service", &service); resp != nil {
		return resp
	}

	if err := services.UnlinkApp(ctx, app.ID, service.ID); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Unlinked %s. It keeps running.", service.Name))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=services", app.ID))
}

// findParent finds the app whose Services tab this is: a custom app.
func (r *AppServicesController) findParent(ctx http.Context, app *models.App) http.Response {
	if resp := findOr404(ctx, "app", app); resp != nil {
		return resp
	}
	if app.Template != nil {
		return notFound(ctx)
	}

	return nil
}

// refuseIfService keeps an app that's another app's service from having
// services of its own, so the tree stays one level deep (ADR 0037).
func (r *AppServicesController) refuseIfService(ctx http.Context, app *models.App, field string) http.Response {
	parents, err := services.ServiceOf(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if len(parents) > 0 {
		return failed(ctx, map[string]string{field: fmt.Sprintf("%s is a service of %s, so it can't have services of its own.", app.Name, parents[0].Name)})
	}

	return nil
}
