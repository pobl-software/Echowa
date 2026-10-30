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
	"goravel/app/templates"
)

// AppDatabasesController is an app's Databases tab: the template apps it's
// linked to (ADR 0028). Only a custom app has one; a database doesn't link
// databases.
type AppDatabasesController struct{}

func NewAppDatabasesController() *AppDatabasesController {
	return &AppDatabasesController{}
}

// Store adds databases to the app: new ones from templates, each deployed
// straight away, and existing ones. Everything is checked before anything
// is made.
func (r *AppDatabasesController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	if app.Template != nil {
		return notFound(ctx)
	}

	validator, err := rules.Validate(ctx, rules.AppDatabases(), rules.AppDatabasesFilters(), rules.AppDatabasesMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in struct {
		Databases []struct {
			Template string `form:"template" json:"template"`
			Name     string `form:"name" json:"name"`
		} `form:"databases" json:"databases"`
		Link []uint `form:"link" json:"link"`
	}
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if len(in.Databases) == 0 && len(in.Link) == 0 {
		return failed(ctx, map[string]string{"databases": "Pick a database to add."})
	}

	news := make([]templates.Template, len(in.Databases))
	for i, d := range in.Databases {
		if d.Name == "" {
			return failed(ctx, map[string]string{fmt.Sprintf("databases.%d.name", i): "Give it a name."})
		}
		tmpl, ok := templates.Find(d.Template)
		if !ok {
			return failed(ctx, map[string]string{fmt.Sprintf("databases.%d.template", i): "There's no template called " + d.Template + "."})
		}
		leftover, err := services.LeftoverVolume(ctx.Context(), templates.VolumeNames(&tmpl, d.Name))
		if err != nil {
			return failed(ctx, map[string]string{fmt.Sprintf("databases.%d.name", i): "Couldn't check Docker for its volume: " + err.Error()})
		}
		if leftover != "" {
			return failed(ctx, map[string]string{fmt.Sprintf("databases.%d.name", i): fmt.Sprintf(
				"The volume %s is left from an earlier app with this name and may hold its data. Pick another name.", leftover)})
		}
		news[i] = tmpl
	}

	var linked []models.App
	if len(in.Link) > 0 {
		ids := make([]any, len(in.Link))
		for i, id := range in.Link {
			ids[i] = id
		}
		if err := facades.Orm().WithContext(ctx.Context()).Query().WhereIn("id", ids).Where("template IS NOT NULL").Order("name").Get(&linked); err != nil {
			return inertia.ServerError(ctx, err)
		}
		if len(linked) != len(in.Link) {
			return failed(ctx, map[string]string{"link": "Only databases made from a template can be linked."})
		}
	}

	var created []string
	for i, d := range in.Databases {
		db, err := services.CreateApp(ctx.Context(), services.TemplateInput(news[i], d.Name), &news[i])
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		if err := services.LinkApps(ctx.Context(), app.ID, db.ID); err != nil {
			return inertia.ServerError(ctx, err)
		}
		if _, err := deploy.Request(ctx.Context(), db, models.TriggerManual, db.DefaultTag); err != nil {
			return inertia.ServerError(ctx, err)
		}
		created = append(created, db.Name)
	}
	var names []string
	for _, db := range linked {
		if err := services.LinkApps(ctx.Context(), app.ID, db.ID); err != nil {
			return inertia.ServerError(ctx, err)
		}
		names = append(names, db.Name)
	}

	var message []string
	if len(created) > 0 {
		message = append(message, fmt.Sprintf("Created %s, deploying now.", strings.Join(created, ", ")))
	}
	if len(names) > 0 {
		message = append(message, fmt.Sprintf("Linked %s.", strings.Join(names, ", ")))
	}
	inertia.Flash(ctx, "success", strings.Join(message, " "))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=databases", app.ID))
}

// Destroy unlinks a database. The database itself keeps running.
func (r *AppDatabasesController) Destroy(ctx http.Context) http.Response {
	var app, db models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	if resp := findOr404(ctx, "database", &db); resp != nil {
		return resp
	}

	if err := services.UnlinkApp(ctx.Context(), app.ID, db.ID); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Unlinked %s. It keeps running.", db.Name))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=databases", app.ID))
}
