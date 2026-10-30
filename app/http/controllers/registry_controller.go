package controllers

import (
	"errors"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// RegistryController manages registry logins. Tokens go in, never out.
type RegistryController struct{}

func NewRegistryController() *RegistryController {
	return &RegistryController{}
}

// RegistryRow is a registry in the list, with how many apps use it.
type RegistryRow struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Username  string `json:"username"`
	AppsCount int64  `json:"apps_count"`
}

func (r *RegistryController) Index(ctx http.Context) http.Response {
	rows := []RegistryRow{}
	err := facades.Orm().WithContext(ctx.Context()).Query().Model(&models.Registry{}).
		Select("id", "name", "host", "username", "(SELECT COUNT(*) FROM apps WHERE apps.registry_id = registries.id) AS apps_count").
		Order("name").Get(&rows)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Registries/Index", gonertia.Props{"registries": rows})
}

func (r *RegistryController) Create(ctx http.Context) http.Response {
	return inertia.Render(ctx, "Registries/Form", gonertia.Props{"registry": nil})
}

func (r *RegistryController) Store(ctx http.Context) http.Response {
	validator, err := ctx.Request().Validate(rules.Registry(0), rules.RegistryFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.RegistryInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}

	registry, err := services.CreateRegistry(ctx.Context(), in)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Added "+registry.Name+". Use “Test connection” to check the login.")

	return inertia.Redirect(ctx, "/registries")
}

// Edit never sends the token: the form shows an empty field that replaces
// it when filled.
func (r *RegistryController) Edit(ctx http.Context) http.Response {
	var registry models.Registry
	if resp := findOr404(ctx, "registry", &registry); resp != nil {
		return resp
	}

	return inertia.Render(ctx, "Registries/Form", gonertia.Props{"registry": registry})
}

func (r *RegistryController) Update(ctx http.Context) http.Response {
	var registry models.Registry
	if resp := findOr404(ctx, "registry", &registry); resp != nil {
		return resp
	}

	validator, err := ctx.Request().Validate(rules.Registry(registry.ID), rules.RegistryFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.RegistryInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}

	if err := services.UpdateRegistry(ctx.Context(), &registry, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Saved "+registry.Name+".")

	return inertia.Redirect(ctx, "/registries")
}

func (r *RegistryController) Destroy(ctx http.Context) http.Response {
	var registry models.Registry
	if resp := findOr404(ctx, "registry", &registry); resp != nil {
		return resp
	}

	err := services.DeleteRegistry(ctx.Context(), &registry)
	if errors.Is(err, services.ErrRegistryInUse) {
		inertia.Flash(ctx, "error", "Can't delete "+registry.Name+": apps still pull through it. Change their registry first.")
		return inertia.Back(ctx)
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Deleted "+registry.Name+".")

	return inertia.Redirect(ctx, "/registries")
}

// Test logs in to the registry through the Docker daemon with the stored
// credentials.
func (r *RegistryController) Test(ctx http.Context) http.Response {
	var registry models.Registry
	if resp := findOr404(ctx, "registry", &registry); resp != nil {
		return resp
	}

	if err := services.Docker().RegistryLogin(ctx.Context(), *services.RegistryAuth(&registry)); err != nil {
		inertia.Flash(ctx, "error", "Login to "+registry.Host+" as "+registry.Username+" failed: "+err.Error())
		return inertia.Back(ctx)
	}
	inertia.Flash(ctx, "success", "Logged in to "+registry.Host+" as "+registry.Username+".")

	return inertia.Back(ctx)
}
