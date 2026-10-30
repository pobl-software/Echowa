package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// NotificationProviderController manages where notifications go, on the
// Settings page (ADR 0040). Secrets go in, never out.
type NotificationProviderController struct{}

func NewNotificationProviderController() *NotificationProviderController {
	return &NotificationProviderController{}
}

func (r *NotificationProviderController) Store(ctx http.Context) http.Response {
	var p models.NotificationProvider
	return r.save(ctx, &p, "Added ")
}

func (r *NotificationProviderController) Update(ctx http.Context) http.Response {
	var p models.NotificationProvider
	if resp := findOr404(ctx, "provider", &p); resp != nil {
		return resp
	}

	return r.save(ctx, &p, "Saved ")
}

func (r *NotificationProviderController) save(ctx http.Context, p *models.NotificationProvider, done string) http.Response {
	validator, err := rules.Validate(ctx, rules.NotificationProvider(r.kind(ctx, p), p.ID), rules.NotificationProviderFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.NotificationProviderInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if problem := in.Problem(p); problem != nil {
		return failed(ctx, problem)
	}
	if err := services.SaveNotificationProvider(ctx.Context(), p, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", done+p.Name+".")

	return inertia.Redirect(ctx, "/settings")
}

// kind picks the rules: a saved provider's own kind, which never changes,
// or the one the form names.
func (r *NotificationProviderController) kind(ctx http.Context, p *models.NotificationProvider) string {
	if p.ID != 0 {
		return string(p.Kind)
	}

	return ctx.Request().Input("kind")
}

// Enabled turns the provider on or off.
func (r *NotificationProviderController) Enabled(ctx http.Context) http.Response {
	var p models.NotificationProvider
	if resp := findOr404(ctx, "provider", &p); resp != nil {
		return resp
	}
	validator, err := rules.Validate(ctx, map[string]any{"enabled": "required|boolean"})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	enabled := ctx.Request().InputBool("enabled")
	if _, err := facades.Orm().WithContext(ctx.Context()).Query().Model(&p).Update("enabled", enabled); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if enabled {
		inertia.Flash(ctx, "success", p.Name+" sends notifications again.")
	} else {
		inertia.Flash(ctx, "success", p.Name+" won't send notifications until you turn it back on.")
	}

	return inertia.Redirect(ctx, "/settings")
}

func (r *NotificationProviderController) Destroy(ctx http.Context) http.Response {
	var p models.NotificationProvider
	if resp := findOr404(ctx, "provider", &p); resp != nil {
		return resp
	}
	if err := services.DeleteNotificationProvider(ctx.Context(), &p); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Deleted "+p.Name+".")

	return inertia.Redirect(ctx, "/settings")
}

// Test sends a sample message with the form's values, saved or not, and
// answers with the send's ID as plain JSON. With an id, a secret sent as
// null uses the stored one, so editing a provider can test it without
// retyping it. A send can outlast the request timeout, so the dialog
// polls TestResult.
func (r *NotificationProviderController) Test(ctx http.Context) http.Response {
	var p models.NotificationProvider
	if id := ctx.Request().InputInt("id"); id > 0 {
		if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", id).FirstOrFail(&p); err != nil {
			return ctx.Response().Json(http.StatusNotFound, http.Json{"message": "No such notification provider."})
		}
	}

	validator, err := rules.Validate(ctx, rules.NotificationProviderCheck(r.kind(ctx, &p), p.ID), rules.NotificationProviderFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"errors": inertia.ValidationMessages(validator.Errors())})
	}
	var in services.NotificationProviderInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if problem := in.Problem(&p); problem != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"errors": problem})
	}
	if err := in.Apply(&p); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Json(http.StatusAccepted, http.Json{"id": services.StartNotificationTest(p)})
}

// TestResult says how a Test send is going.
func (r *NotificationProviderController) TestResult(ctx http.Context) http.Response {
	result, ok := services.NotificationTestResult(ctx.Request().Route("test"))
	if !ok {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"message": "That test has expired. Send it again."})
	}

	return ctx.Response().Json(http.StatusOK, result)
}
