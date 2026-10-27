package controllers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/templates"
)

// AppController manages apps: the create form, the detail page with its
// tabs, settings, deletion and the Environment tab.
type AppController struct{}

func NewAppController() *AppController {
	return &AppController{}
}

// RegistryOption is a registry in the app form's select.
type RegistryOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
}

func registryOptions(ctx http.Context) ([]RegistryOption, error) {
	// Non-nil, so an empty list reaches React as [] rather than null.
	options := []RegistryOption{}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.Registry{}).
		Select("id", "name", "host").Order("name").Get(&options)

	return options, err
}

// TemplateOption is a template on the New app page. Env vars are keys and
// where their values come from, never values (ADR 0025).
type TemplateOption struct {
	Slug        string              `json:"slug"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Repository  string              `json:"repository"`
	DefaultTag  string              `json:"default_tag"`
	Port        int                 `json:"port"`
	HTTP        bool                `json:"http"`
	Healthcheck string              `json:"healthcheck_cmd"`
	Env         []TemplateEnvOption `json:"env"`
	Volumes     []string            `json:"volumes"`
	// How to connect with TLS while external access is on; empty when the
	// template doesn't serve TLS.
	TLSClient string `json:"tls_client"`
	// SlowQueries is "switch" when the template lists its slow queries once
	// they're switched on, "always" when it lists them anyway, and empty
	// when it can't (ADR 0027).
	SlowQueries string `json:"slow_queries"`
}

type TemplateEnvOption struct {
	Key    string `json:"key"`
	Source string `json:"source"`
}

func templateOptions() []TemplateOption {
	options := []TemplateOption{}
	for _, t := range templates.All() {
		options = append(options, templateOption(t))
	}

	return options
}

func templateOption(t templates.Template) TemplateOption {
	option := TemplateOption{
		Slug: t.Slug, Name: t.Name, Description: t.Description, Repository: t.Repository,
		DefaultTag: t.DefaultTag, Port: t.Port, HTTP: t.HTTP, Healthcheck: t.Healthcheck,
		Env: []TemplateEnvOption{}, Volumes: []string{},
	}
	for _, e := range t.Env {
		option.Env = append(option.Env, TemplateEnvOption{Key: e.Key, Source: e.Value.Source()})
	}
	for _, v := range t.Volumes {
		option.Volumes = append(option.Volumes, v.Target)
	}
	if t.TLS != nil {
		option.TLSClient = t.TLS.Client
	}
	if t.HasSlowQueries(true) {
		option.SlowQueries = "always"
		if t.Inspect.SlowQueryFlags != "" {
			option.SlowQueries = "switch"
		}
	}

	return option
}

// ConnectInfo is how other apps reach a template app. Preview has env keys
// where the values go; ConnectReveal returns the real URL.
type ConnectInfo struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// User isn't a secret, so the page has it before Reveal.
	User    string `json:"user"`
	Preview string `json:"preview"`
}

// DatabaseChoices is what the Databases tab can add: a new database from a
// template, or an existing one not linked yet.
type DatabaseChoices struct {
	Templates []TemplateOption          `json:"templates"`
	Existing  []services.DatabaseChoice `json:"existing"`
}

func (r *AppController) Create(ctx http.Context) http.Response {
	registries, err := registryOptions(ctx)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Apps/Create", gonertia.Props{"registries": registries, "templates": templateOptions()})
}

// Store creates an app. One made from a template has everything it needs,
// so its first deploy is queued at once.
func (r *AppController) Store(ctx http.Context) http.Response {
	var tmpl *templates.Template
	if slug := ctx.Request().Input("template"); slug != "" {
		found, ok := templates.Find(slug)
		if !ok {
			return failed(ctx, map[string]string{"template": "There's no template called " + slug + "."})
		}
		tmpl = &found
	}

	validator, err := ctx.Request().Validate(rules.App(tmpl != nil && len(tmpl.Volumes) > 0), rules.AppFilters(), rules.AppMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.AppInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}

	if resp := checkDomain(ctx, tmpl, in); resp != nil {
		return resp
	}
	if resp := r.checkRegistry(ctx, in); resp != nil {
		return resp
	}
	leftover, err := services.LeftoverVolume(ctx, tmpl, in.Name)
	if err != nil {
		return failed(ctx, map[string]string{"name": "Couldn't check Docker for this app's volume: " + err.Error()})
	}
	if leftover != "" {
		return failed(ctx, map[string]string{"name": fmt.Sprintf(
			"The volume %s is left from an earlier app called %s and may hold its data. Pick another name, or remove it on the server with docker volume rm %s.",
			leftover, in.Name, leftover)})
	}
	app, err := services.CreateApp(ctx, in, tmpl)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if tmpl == nil {
		inertia.Flash(ctx, "success", "Created "+app.Name+". Add its environment, then deploy it.")
		return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=environment", app.ID))
	}

	deployment, err := deploy.Request(ctx, app, models.TriggerManual, app.DefaultTag)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Created %s. Deploying %s %s now.", app.Name, tmpl.Name, app.DefaultTag))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d/deployments/%d", app.ID, deployment.ID))
}

// appTemplate is the template the app was made from, or nil for a custom
// app or one whose template no longer exists.
func appTemplate(app *models.App) *templates.Template {
	if app.Template == nil {
		return nil
	}
	tmpl, ok := templates.Find(*app.Template)
	if !ok {
		return nil
	}

	return &tmpl
}

// inspectable is whether Echowa reads the app's database figures.
func inspectable(app *models.App) bool {
	tmpl := appTemplate(app)

	return tmpl != nil && tmpl.Inspect != nil
}

// checkDomain refuses a domain for an app whose template isn't HTTP:
// Traefik would send HTTP requests to a database port.
func checkDomain(ctx http.Context, tmpl *templates.Template, in services.AppInput) http.Response {
	if tmpl == nil || tmpl.HTTP || in.Domain == nil || strings.TrimSpace(*in.Domain) == "" {
		return nil
	}

	return failed(ctx, map[string]string{
		"domain": fmt.Sprintf("%s doesn't speak HTTP, so it has no domain. Other apps reach it at %s:%d.", tmpl.Name, models.ServicePrefix+in.Name, in.Port),
	})
}

// Show is the app's detail page. The tab is chosen client-side from ?tab=.
func (r *AppController) Show(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app, "Registry", "Volumes"); resp != nil {
		return resp
	}
	env, err := services.EnvVars(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	var templateProp *TemplateOption
	var connect *ConnectInfo
	if tmpl := appTemplate(&app); tmpl != nil {
		option := templateOption(*tmpl)
		templateProp = &option
		if tmpl.Connect != nil {
			user, _ := tmpl.ConnectCredentials(app.ServiceName(), app.Port, env)
			connect = &ConnectInfo{
				Host: app.ServiceName(), Port: app.Port, User: user,
				Preview: tmpl.ConnectPreview(app.ServiceName(), app.Port),
			}
		}
	}
	registries, err := registryOptions(ctx)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	deployments, err := services.RecentDeployments(ctx, app.ID, 30)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	var lastDeployment *services.DeploymentSummary
	if len(deployments) > 0 {
		lastDeployment = &deployments[0]
	}
	rng := metrics.ParseRange(ctx.Request().Query("range"))

	return inertia.Render(ctx, "Apps/Show", gonertia.Props{
		"app":            app,
		"serviceName":    app.ServiceName(),
		"status":         services.AppLiveStatus(ctx, &app),
		"lastDeployment": lastDeployment,
		"deployments":    deployments,
		// Keys only: values are revealed one at a time by EnvReveal.
		"envKeys":    append([]string{}, slices.Sorted(maps.Keys(env))...),
		"registries": registries,
		"resources":  resourcesProps(ctx),
		"template":   templateProp,
		"connect":    connect,
		// The Metrics section's, worked out only when asked for, like the
		// Server page's (ADR 0024).
		"ranges": metricRanges(),
		"series": func(c context.Context) (any, error) {
			return services.AppMetrics(c, &app, rng, time.Now())
		},
		"metrics": func(c context.Context) (any, error) {
			return services.LatestAppMetrics(&app), nil
		},
		// The Database section's, for a template database (ADR 0027).
		"databaseSeries": func(c context.Context) (any, error) {
			if !inspectable(&app) {
				return nil, nil
			}
			return services.DatabaseMetrics(c, &app, rng, time.Now())
		},
		"database": func(c context.Context) (any, error) {
			if !inspectable(&app) {
				return nil, nil
			}
			return services.LatestDatabaseLive(c, &app), nil
		},
		// The Domains section's DNS and certificate, looked up only when it
		// asks.
		// The Logs tab's deploy picker asks for it, so Docker isn't asked on other tabs.
		"logDeploys": gonertia.Optional(func(c context.Context) (any, error) {
			return services.AppLogDeploys(c, &app), nil
		}),
		"domainStatus": gonertia.Optional(func(c context.Context) (any, error) {
			return services.CheckAppDomain(c, &app), nil
		}),
		// Read from the database only when the page asks (ADR 0027).
		"slowQueries": gonertia.Optional(func(c context.Context) (any, error) {
			return services.SlowQueries(c, &app), nil
		}),
		// The Databases section's, for a custom app (ADR 0028).
		"linkedDatabases": func(c context.Context) (any, error) {
			if app.Template != nil {
				return []services.LinkedDatabase{}, nil
			}
			return services.LinkedDatabases(c, app.ID)
		},
		// The Scheduled tasks section's, for a custom app (ADR 0032).
		"scheduledTasks": func(c context.Context) (any, error) {
			if app.Template != nil {
				return []services.ScheduledTaskSummary{}, nil
			}
			return services.ScheduledTasks(c, app.ID, time.Now())
		},
		// Overview's alerts for tasks that failed (ADR 0032).
		"taskFailures": func(c context.Context) (any, error) {
			if app.Template != nil {
				return []services.TaskFailure{}, nil
			}
			return services.TaskFailures(c, app.ID)
		},
		"databaseChoices": func(c context.Context) (any, error) {
			if app.Template != nil {
				return nil, nil
			}
			existing, err := services.UnlinkedDatabases(c, app.ID)
			if err != nil {
				return nil, err
			}
			return DatabaseChoices{Templates: templateOptions(), Existing: existing}, nil
		},
	})
}

// Update saves the Docker config tab.
func (r *AppController) Update(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	hasVolumes, err := services.HasVolumes(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	in, resp := validateApp(ctx, rules.AppDocker(hasVolumes))
	if resp != nil {
		return resp
	}
	if resp := r.checkRegistry(ctx, in); resp != nil {
		return resp
	}
	if err := services.UpdateDocker(ctx, &app, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Saved. Changes apply on the next deploy.")

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=docker", app.ID))
}

// UpdateResources saves the Resource limits tab. A deployed app is
// redeployed with the image it runs, so the limits apply now.
func (r *AppController) UpdateResources(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	host, err := services.DockerHost(ctx)
	if err != nil {
		inertia.Flash(ctx, "error", "Docker didn't say how big this server is, so the limits can't be checked: "+err.Error())
		return inertia.Back(ctx)
	}

	whole := services.HostLimits(host)
	// Before the first metrics reading the swap is unknown, so allow as much
	// as the memory.
	swap, known := services.HostSwapMiB()
	if !known {
		swap = whole.MemoryMiB
	}
	validator, err := ctx.Request().Validate(
		rules.AppResources(whole.CPUMillicores, whole.MemoryMiB, swap),
		rules.AppResourcesMessages(whole.CPUMillicores, whole.MemoryMiB, swap),
	)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.ResourcesInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if err := services.UpdateResources(ctx, &app, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if app.CurrentDeploymentID != nil {
		return redeployCurrent(ctx, &app, "Resource limits saved")
	}
	inertia.Flash(ctx, "success", "Saved. They apply on the first deploy.")

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=resources", app.ID))
}

// UpdateDomain saves the Domains tab.
func (r *AppController) UpdateDomain(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	in, resp := validateApp(ctx, rules.AppDomain(app.ID))
	if resp != nil {
		return resp
	}
	in.Name, in.Port = app.Name, app.Port
	if resp := checkDomain(ctx, appTemplate(&app), in); resp != nil {
		return resp
	}
	if err := services.UpdateDomain(ctx, &app, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Saved. Changes apply on the next deploy.")

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=domains", app.ID))
}

// DestroyDomain removes the app's domain. Traefik stops routing it on the
// next deploy.
func (r *AppController) DestroyDomain(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	if err := services.UpdateDomain(ctx, &app, services.AppInput{TLS: app.TLS}); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Removed. Traefik stops routing it on the next deploy.")

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=domains", app.ID))
}

// CheckDomain looks up a domain's DNS before it's added, so the Domains tab
// can warn when it doesn't point at this server. Plain JSON, like
// EnvReveal: the domain isn't saved.
func (r *AppController) CheckDomain(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	validator, err := ctx.Request().Validate(rules.AppDomainCheck(app.ID), rules.AppFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"message": validator.Errors().One("domain")})
	}
	var in services.AppInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, services.CheckDNS(ctx, *in.Domain))
}

// validateApp checks one of the app's edit tabs against its rules. On
// failure, the response sends the errors back to the form.
// ResourcesProps is the Resource limits tab's: what the defaults and the
// whole server come to, with the server's swap nil until the first metrics
// reading. Nil when Docker doesn't answer.
type ResourcesProps struct {
	Defaults services.ResourceLimits `json:"defaults"`
	Host     services.ResourceLimits `json:"host"`
	HostSwap *int                    `json:"host_swap_mib"`
}

func resourcesProps(ctx http.Context) *ResourcesProps {
	host, err := services.DockerHost(ctx)
	if err != nil {
		return nil
	}

	props := &ResourcesProps{Defaults: services.DefaultLimits(host), Host: services.HostLimits(host)}
	if swap, known := services.HostSwapMiB(); known {
		props.HostSwap = &swap
	}

	return props
}

func validateApp(ctx http.Context, appRules map[string]any) (services.AppInput, http.Response) {
	var in services.AppInput
	validator, err := ctx.Request().Validate(appRules, rules.AppFilters(), rules.AppMessages())
	if err != nil {
		return in, inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return in, inertia.Back(ctx)
	}
	if err := validator.Bind(&in); err != nil {
		return in, inertia.ServerError(ctx, err)
	}

	return in, nil
}

// Deploy queues a deployment of the given tag or digest, or of the app's
// default tag when none is given.
func (r *AppController) Deploy(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	ref := strings.TrimSpace(ctx.Request().Input("ref"))
	if ref == "" {
		ref = app.DefaultTag
	}
	validator, err := facades.Validation().Make(ctx, map[string]any{"ref": ref}, rules.Deploy())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}

	deployment, err := deploy.Request(ctx, &app, models.TriggerManual, ref)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Deployment #%d of %s queued.", deployment.ID, ref))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d/deployments/%d", app.ID, deployment.ID))
}

// Destroy removes the Swarm service first, so a failure there leaves the
// app in place to try again rather than an orphaned service. Its volumes go
// last, in the background, once Swarm has stopped the tasks using them.
func (r *AppController) Destroy(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app, "Volumes"); resp != nil {
		return resp
	}

	if err := services.Docker().RemoveService(ctx, app.ServiceName()); err != nil {
		facades.Log().Error(fmt.Errorf("delete app %s: %w", app.Name, err))
		inertia.Flash(ctx, "error", "Couldn't remove the Swarm service "+app.ServiceName()+", so the app was kept: "+err.Error())
		return inertia.Back(ctx)
	}
	if err := services.DeleteApp(ctx, &app); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if len(app.Volumes) == 0 {
		inertia.Flash(ctx, "success", "Deleted "+app.Name+" and its Swarm service.")
		return inertia.Redirect(ctx, "/dashboard")
	}

	names := make([]string, 0, len(app.Volumes))
	for _, v := range app.Volumes {
		names = append(names, v.Name)
	}
	go func() { services.RemoveVolumes(context.Background(), app.Name, names) }()
	inertia.Flash(ctx, "success", fmt.Sprintf("Deleted %s and its Swarm service. Its data (%s) is deleted once its task has stopped.",
		app.Name, strings.Join(names, ", ")))

	return inertia.Redirect(ctx, "/dashboard")
}

// EnvUpdate saves the Environment tab's editor: the whole set of rows.
func (r *AppController) EnvUpdate(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	validator, err := ctx.Request().Validate(rules.EnvEdits())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in struct {
		Vars []services.EnvEdit `form:"vars" json:"vars"`
	}
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}

	current, err := services.EnvVars(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	env, err := services.ApplyEnvEdits(current, in.Vars)
	var editErr *services.EnvEditError
	if errors.As(err, &editErr) {
		return failed(ctx, map[string]string{fmt.Sprintf("vars.%d.value", editErr.Row): editErr.Message})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	if err := services.SetEnvVars(ctx, app.ID, env); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Environment saved. It applies on the next deploy.")

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=environment", app.ID))
}

// EnvImport merges pasted .env text into the environment. Pasted keys
// overwrite existing ones; others are kept.
func (r *AppController) EnvImport(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	validator, err := ctx.Request().Validate(map[string]any{"text": "required|string|max:200000"})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}

	pairs, err := services.ParseDotEnv(ctx.Request().Input("text"))
	if err != nil {
		return failed(ctx, map[string]string{"text": err.Error()})
	}
	if len(pairs) == 0 {
		return failed(ctx, map[string]string{"text": "No KEY=value lines found."})
	}

	env, err := services.EnvVars(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	for _, pair := range pairs {
		env[pair.Key] = pair.Value
	}
	if err := services.SetEnvVars(ctx, app.ID, env); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Imported %d variable(s). They apply on the next deploy.", len(pairs)))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=environment", app.ID))
}

// EnvReveal returns one value as plain JSON, never as an Inertia page, so
// values only leave the server one at a time on request.
func (r *AppController) EnvReveal(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	key := ctx.Request().Input("key")
	env, err := services.EnvVars(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	value, ok := env[key]
	if !ok {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"message": "No such variable."})
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, http.Json{"value": value})
}

// ConnectReveal returns the URL other apps connect to a template app with,
// password included, and the password on its own. Like EnvReveal, it's
// plain JSON, never a page prop.
func (r *AppController) ConnectReveal(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	tmpl := appTemplate(&app)
	if tmpl == nil || tmpl.Connect == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"message": "This app has no connection URL."})
	}
	env, err := services.EnvVars(ctx, app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	_, password := tmpl.ConnectCredentials(app.ServiceName(), app.Port, env)

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, http.Json{
		"url": tmpl.Connect(app.ServiceName(), app.Port, env), "password": password,
	})
}

// checkRegistry refuses a repository on another host than the chosen
// registry: every pull would send that registry's token to the wrong place.
func (r *AppController) checkRegistry(ctx http.Context, in services.AppInput) http.Response {
	if in.RegistryID == nil {
		return nil
	}
	var registry models.Registry
	if err := facades.Orm().WithContext(ctx).Query().Where("id", *in.RegistryID).FirstOrFail(&registry); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if !docker.SameRegistry(registry.Host, in.Repository) {
		return failed(ctx, map[string]string{
			"repository": fmt.Sprintf("This image isn't on %s, the host of registry %s. Use %s/..., or pick another registry.", registry.Host, registry.Name, registry.Host),
		})
	}

	return nil
}
