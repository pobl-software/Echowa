package controllers

import (
	"fmt"
	"maps"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// AlertRuleController edits alerts (ADR 0040) in both places they live: an
// app's Alerts section, under /apps/{app}/alerts, for that app's own, and
// Settings, under /settings/alerts, for the server's and every app's.
type AlertRuleController struct{}

func NewAlertRuleController() *AlertRuleController {
	return &AlertRuleController{}
}

// alertPlace is where an alert is edited: an app's section, or Settings
// when app is nil.
type alertPlace struct {
	app *models.App
}

func (p alertPlace) url() string {
	if p.app == nil {
		return "/settings"
	}

	return fmt.Sprintf("/apps/%d?tab=alerts", p.app.ID)
}

func (p alertPlace) triggers() []string {
	if p.app == nil {
		return services.SettingsAlertTriggerIDs()
	}

	return services.AppAlertTriggerIDs(p.app)
}

func findAlertPlace(ctx http.Context) (alertPlace, http.Response) {
	if ctx.Request().Route("app") == "" {
		return alertPlace{}, nil
	}
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return alertPlace{}, resp
	}

	return alertPlace{app: &app}, nil
}

// findAlertRule loads the {rule} route's alert, which must belong to the
// place: the app's own, or on Settings the server's or every app's.
func findAlertRule(ctx http.Context, rule *models.AlertRule) (alertPlace, http.Response) {
	place, resp := findAlertPlace(ctx)
	if resp != nil {
		return place, resp
	}
	query := facades.Orm().WithContext(ctx.Context()).Query().Where("id", ctx.Request().RouteInt("rule"))
	if place.app != nil {
		query = query.Where("scope", models.AlertApp).Where("app_id", place.app.ID)
	} else {
		query = query.WhereIn("scope", []any{models.AlertServer, models.AlertAllApps})
	}
	if err := query.FirstOrFail(rule); err != nil {
		return place, notFound(ctx)
	}

	return place, nil
}

// validated checks the form against set and binds it into in, with the
// providers it names. A nil response means it passed.
func validated(ctx http.Context, set map[string]any, in any, ids func() []uint) ([]models.NotificationProvider, http.Response) {
	validator, err := rules.Validate(ctx, set, rules.AlertMessages())
	if err != nil {
		return nil, inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		return nil, back(ctx, validator)
	}
	if err := validator.Bind(in); err != nil {
		return nil, inertia.ServerError(ctx, err)
	}
	providers, all, err := services.AlertProviders(ctx.Context(), ids())
	if err != nil {
		return nil, inertia.ServerError(ctx, err)
	}
	if !all {
		return nil, failed(ctx, map[string]string{"providers": "That provider doesn't exist any more."})
	}

	return providers, nil
}

func back(ctx http.Context, validator validation.Validator) http.Response {
	inertia.WithValidationErrors(ctx, validator.Errors())

	return inertia.Back(ctx)
}

// alertRuleSet is the form's rules: AlertRule's, plus a condition's
// fields when trigger is one. triggers is nil when the trigger is saved.
func alertRuleSet(triggers []string, trigger string) map[string]any {
	set := rules.AlertRule(triggers)
	if t, ok := alerts.Find(trigger); ok && t.Kind == alerts.KindCondition {
		maps.Copy(set, rules.AlertCondition(t.Unit != alerts.UnitNone, t.Unit == alerts.UnitPercent))
	}

	return set
}

func alertLabel(trigger string) string {
	if t, ok := alerts.Find(trigger); ok {
		return t.Label
	}

	return trigger
}

func (r *AlertRuleController) Store(ctx http.Context) http.Response {
	place, resp := findAlertPlace(ctx)
	if resp != nil {
		return resp
	}
	var in services.AlertRuleInput
	set := alertRuleSet(place.triggers(), ctx.Request().Input("trigger"))
	providers, resp := validated(ctx, set, &in, func() []uint { return in.Providers })
	if resp != nil {
		return resp
	}
	rule := models.AlertRule{Scope: services.SettingsScope(in.Trigger)}
	if place.app != nil {
		rule.Scope, rule.AppID = models.AlertApp, &place.app.ID
	}
	if err := services.SaveAlertRule(ctx.Context(), &rule, in, providers); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Added "+alertLabel(rule.Trigger)+".")

	return inertia.Redirect(ctx, place.url())
}

// Update saves the severity, providers and a condition's fields. The
// trigger never changes.
func (r *AlertRuleController) Update(ctx http.Context) http.Response {
	var rule models.AlertRule
	place, resp := findAlertRule(ctx, &rule)
	if resp != nil {
		return resp
	}
	var in services.AlertRuleInput
	providers, resp := validated(ctx, alertRuleSet(nil, rule.Trigger), &in, func() []uint { return in.Providers })
	if resp != nil {
		return resp
	}
	if err := services.SaveAlertRule(ctx.Context(), &rule, in, providers); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Saved "+alertLabel(rule.Trigger)+".")

	return inertia.Redirect(ctx, place.url())
}

// Enabled turns the alert on or off.
func (r *AlertRuleController) Enabled(ctx http.Context) http.Response {
	var rule models.AlertRule
	place, resp := findAlertRule(ctx, &rule)
	if resp != nil {
		return resp
	}
	validator, err := rules.Validate(ctx, map[string]any{"enabled": "required|boolean"})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		return back(ctx, validator)
	}
	enabled := ctx.Request().InputBool("enabled")
	if err := services.SetAlertRuleEnabled(ctx.Context(), &rule, enabled); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if enabled {
		inertia.Flash(ctx, "success", alertLabel(rule.Trigger)+" sends again.")
	} else {
		inertia.Flash(ctx, "success", alertLabel(rule.Trigger)+" sends nothing until you turn it back on.")
	}

	return inertia.Redirect(ctx, place.url())
}

// Destroy deletes the alert. The notifications it sent stay.
func (r *AlertRuleController) Destroy(ctx http.Context) http.Response {
	var rule models.AlertRule
	place, resp := findAlertRule(ctx, &rule)
	if resp != nil {
		return resp
	}
	if _, err := facades.Orm().WithContext(ctx.Context()).Query().Delete(&rule); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Deleted "+alertLabel(rule.Trigger)+".")

	return inertia.Redirect(ctx, place.url())
}

// Suggested adds the chosen suggestions, each at its suggested severity.
func (r *AlertRuleController) Suggested(ctx http.Context) http.Response {
	place, resp := findAlertPlace(ctx)
	if resp != nil {
		return resp
	}
	var suggested []services.AlertTriggerOption
	if place.app != nil {
		section, err := services.AppAlertsSection(ctx.Context(), place.app)
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		suggested = section.Suggestions
	} else {
		section, err := services.SettingsAlertsSection(ctx.Context())
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		suggested = section.Suggestions
	}
	ids := make([]string, len(suggested))
	for i, t := range suggested {
		ids[i] = t.ID
	}
	var in services.AlertSuggestionsInput
	providers, resp := validated(ctx, rules.AlertSuggestions(ids), &in, func() []uint { return in.Providers })
	if resp != nil {
		return resp
	}
	if err := services.AddSuggestedAlerts(ctx.Context(), place.app, in.Triggers, providers); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if len(in.Triggers) == 1 {
		inertia.Flash(ctx, "success", "Added "+alertLabel(in.Triggers[0])+".")
	} else {
		inertia.Flash(ctx, "success", fmt.Sprintf("Added %d alerts.", len(in.Triggers)))
	}

	return inertia.Redirect(ctx, place.url())
}
