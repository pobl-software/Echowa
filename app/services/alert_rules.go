package services

import (
	"context"
	"slices"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// RecentNotificationsShown is how many of an app's notifications its Alerts
// section lists.
const RecentNotificationsShown = 50

// AlertTriggerOption is a trigger as the alert form lists it.
type AlertTriggerOption struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Label string `json:"label"`
	// Server is a trigger about the server; the rest are about apps, and on
	// Settings make every-app alerts.
	Server bool `json:"server"`
	// Only names the apps it's limited to, such as "PostgreSQL and MySQL
	// apps", or is empty when every app has it.
	Only string `json:"only"`
	// Severity is what a new alert on it starts at.
	Severity  string `json:"severity"`
	Suggested bool   `json:"suggested"`
}

// ProviderRef is a provider as an alert names it: never its config.
type ProviderRef struct {
	ID      uint                    `json:"id"`
	Name    string                  `json:"name"`
	Kind    models.NotificationKind `json:"kind"`
	Enabled bool                    `json:"enabled"`
}

// AlertRuleSummary is a row of an alert list.
type AlertRuleSummary struct {
	ID      uint              `json:"id"`
	Scope   models.AlertScope `json:"scope"`
	Trigger string            `json:"trigger"`
	// Label is the trigger's, or its ID when this release doesn't know it,
	// as after an updater rollback.
	Label     string        `json:"label"`
	Known     bool          `json:"known"`
	Severity  string        `json:"severity"`
	Enabled   bool          `json:"enabled"`
	Providers []ProviderRef `json:"providers"`
}

// NotificationSummary is a sent, failed or held-back notification, as the
// Alerts section lists it.
type NotificationSummary struct {
	ID        uint                      `json:"id"`
	Title     string                    `json:"title"`
	Summary   string                    `json:"summary"`
	Severity  string                    `json:"severity"`
	Status    models.NotificationStatus `json:"status"`
	Attempts  int                       `json:"attempts"`
	Error     *string                   `json:"error"`
	Provider  ProviderRef               `json:"provider"`
	CreatedAt *carbon.DateTime          `json:"created_at"`
	SentAt    *carbon.DateTime          `json:"sent_at"`
}

// AppAlerts is an app's Alerts section.
type AppAlerts struct {
	// Rules are the app's own; EveryApp the every-app ones that cover it,
	// which are edited on Settings.
	Rules       []AlertRuleSummary    `json:"rules"`
	EveryApp    []AlertRuleSummary    `json:"every_app"`
	Triggers    []AlertTriggerOption  `json:"triggers"`
	Suggestions []AlertTriggerOption  `json:"suggestions"`
	Providers   []ProviderRef         `json:"providers"`
	Recent      []NotificationSummary `json:"recent"`
}

// SettingsAlerts is Settings' alerts: the server's and every app's.
type SettingsAlerts struct {
	// Rules lists the server's first.
	Rules       []AlertRuleSummary   `json:"rules"`
	Triggers    []AlertTriggerOption `json:"triggers"`
	Suggestions []AlertTriggerOption `json:"suggestions"`
}

// AlertRuleInput is the alert form, validated by rules.AlertRule.
type AlertRuleInput struct {
	Trigger   string `form:"trigger" json:"trigger"`
	Severity  string `form:"severity" json:"severity"`
	Providers []uint `form:"providers" json:"providers"`
}

// AlertSuggestionsInput is the suggestions' Add, validated by
// rules.AlertSuggestions.
type AlertSuggestionsInput struct {
	Triggers  []string `form:"triggers" json:"triggers"`
	Providers []uint   `form:"providers" json:"providers"`
}

// AppAlertsSection reads the app's Alerts section.
func AppAlertsSection(ctx context.Context, app *models.App) (AppAlerts, error) {
	section := AppAlerts{Rules: []AlertRuleSummary{}, EveryApp: []AlertRuleSummary{}, Triggers: AppAlertTriggers(app)}
	var rules []models.AlertRule
	err := facades.Orm().WithContext(ctx).Query().With("Providers").
		Where("(scope = ? OR (scope = ? AND app_id = ?))", models.AlertAllApps, models.AlertApp, app.ID).Order("id").Get(&rules)
	if err != nil {
		return section, err
	}
	covered := map[string]bool{}
	for _, rule := range rules {
		trigger, known := alerts.Find(rule.Trigger)
		switch {
		case rule.Scope == models.AlertApp:
			section.Rules = append(section.Rules, summarise(rule))
		case known && trigger.Fits(app):
			section.EveryApp = append(section.EveryApp, summarise(rule))
		default:
			continue
		}
		covered[rule.Trigger] = true
	}

	hasTasks := false
	if app.Template == nil {
		count, err := facades.Orm().WithContext(ctx).Query().Model(&models.ScheduledTask{}).Where("app_id", app.ID).Count()
		if err != nil {
			return section, err
		}
		hasTasks = count > 0
	}
	section.Suggestions = []AlertTriggerOption{}
	for _, t := range section.Triggers {
		// A task alert is noise for an app with no tasks.
		if t.Suggested && !covered[t.ID] && (t.ID != alerts.TaskFailed || hasTasks) {
			section.Suggestions = append(section.Suggestions, t)
		}
	}

	if section.Providers, err = providerRefs(ctx); err != nil {
		return section, err
	}
	section.Recent, err = recentNotifications(ctx, app.ID)

	return section, err
}

// SettingsAlertsSection reads Settings' alerts.
func SettingsAlertsSection(ctx context.Context) (SettingsAlerts, error) {
	section := SettingsAlerts{Rules: []AlertRuleSummary{}, Triggers: SettingsAlertTriggers(), Suggestions: []AlertTriggerOption{}}
	var rules []models.AlertRule
	err := facades.Orm().WithContext(ctx).Query().With("Providers").
		WhereIn("scope", []any{models.AlertServer, models.AlertAllApps}).Order("id").Get(&rules)
	if err != nil {
		return section, err
	}
	slices.SortStableFunc(rules, func(a, b models.AlertRule) int {
		return boolOrder(a.Scope != models.AlertServer) - boolOrder(b.Scope != models.AlertServer)
	})
	covered := map[string]bool{}
	for _, rule := range rules {
		section.Rules = append(section.Rules, summarise(rule))
		covered[rule.Trigger] = true
	}
	for _, t := range section.Triggers {
		if t.Suggested && !covered[t.ID] {
			section.Suggestions = append(section.Suggestions, t)
		}
	}

	return section, nil
}

func boolOrder(b bool) int {
	if b {
		return 1
	}

	return 0
}

// AppAlertTriggers lists the triggers an app's own alerts may use.
func AppAlertTriggers(app *models.App) []AlertTriggerOption {
	options := []AlertTriggerOption{}
	for _, t := range alerts.All() {
		if t.Fits(app) {
			option := triggerOption(t)
			option.Only = ""
			options = append(options, option)
		}
	}

	return options
}

// SettingsAlertTriggers lists the triggers Settings' alerts may use: the
// server's, then every app's.
func SettingsAlertTriggers() []AlertTriggerOption {
	options := []AlertTriggerOption{}
	for _, scope := range []alerts.Scope{alerts.ScopeServer, alerts.ScopeApp} {
		for _, t := range alerts.All() {
			if t.Scope == scope {
				options = append(options, triggerOption(t))
			}
		}
	}

	return options
}

func triggerIDs(options []AlertTriggerOption) []string {
	ids := make([]string, len(options))
	for i, o := range options {
		ids[i] = o.ID
	}

	return ids
}

// AppAlertTriggerIDs and the rest are what the forms validate against.
func AppAlertTriggerIDs(app *models.App) []string { return triggerIDs(AppAlertTriggers(app)) }

func SettingsAlertTriggerIDs() []string { return triggerIDs(SettingsAlertTriggers()) }

func triggerOption(t alerts.Trigger) AlertTriggerOption {
	return AlertTriggerOption{
		ID: t.ID, Group: t.Group, Label: t.Label, Server: t.Scope == alerts.ScopeServer, Only: appKindsText(t.Apps),
		Severity: string(t.Severity), Suggested: t.Suggested,
	}
}

// appKindsText names app kinds for people: "PostgreSQL and MySQL apps".
func appKindsText(kinds []string) string {
	if kinds == nil {
		return ""
	}
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = "custom"
		if tmpl, ok := templates.Find(kind); ok {
			names[i] = tmpl.Name
		}
	}
	text := strings.Join(names, ", ")
	if len(names) > 1 {
		text = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}

	return strings.ToUpper(text[:1]) + text[1:] + " apps"
}

func summarise(rule models.AlertRule) AlertRuleSummary {
	s := AlertRuleSummary{ID: rule.ID, Scope: rule.Scope, Trigger: rule.Trigger, Label: rule.Trigger, Severity: rule.Severity, Enabled: rule.Enabled, Providers: []ProviderRef{}}
	if t, ok := alerts.Find(rule.Trigger); ok {
		s.Label, s.Known = t.Label, true
	}
	for _, p := range rule.Providers {
		s.Providers = append(s.Providers, providerRef(p))
	}
	slices.SortFunc(s.Providers, func(a, b ProviderRef) int { return strings.Compare(a.Name, b.Name) })

	return s
}

func providerRef(p models.NotificationProvider) ProviderRef {
	return ProviderRef{ID: p.ID, Name: p.Name, Kind: p.Kind, Enabled: p.Enabled}
}

func providerRefs(ctx context.Context) ([]ProviderRef, error) {
	var providers []models.NotificationProvider
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&providers); err != nil {
		return nil, err
	}
	refs := make([]ProviderRef, 0, len(providers))
	for _, p := range providers {
		refs = append(refs, providerRef(p))
	}

	return refs, nil
}

func recentNotifications(ctx context.Context, appID uint) ([]NotificationSummary, error) {
	var rows []models.Notification
	err := facades.Orm().WithContext(ctx).Query().Where("app_id", appID).Order("id desc").Limit(RecentNotificationsShown).Get(&rows)
	if err != nil {
		return nil, err
	}
	refs, err := providerRefs(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[uint]ProviderRef{}
	for _, ref := range refs {
		byID[ref.ID] = ref
	}
	summaries := make([]NotificationSummary, 0, len(rows))
	for _, n := range rows {
		summaries = append(summaries, NotificationSummary{
			ID: n.ID, Title: n.Title, Summary: n.Summary, Severity: n.Severity, Status: n.Status, Attempts: n.Attempts,
			Error: n.Error, Provider: byID[n.NotificationProviderID], CreatedAt: n.CreatedAt, SentAt: n.SentAt,
		})
	}

	return summaries, nil
}

// AlertProviders loads the providers ids name, or says which is missing.
func AlertProviders(ctx context.Context, ids []uint) ([]models.NotificationProvider, bool, error) {
	var providers []models.NotificationProvider
	if err := facades.Orm().WithContext(ctx).Query().WhereIn("id", anySlice(ids)).Get(&providers); err != nil {
		return nil, false, err
	}

	return providers, len(providers) == len(ids), nil
}

func anySlice[T any](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}

	return out
}

// SaveAlertRule saves the form into rule and names its providers. A new
// rule starts on. Only a new rule takes the trigger.
func SaveAlertRule(ctx context.Context, rule *models.AlertRule, in AlertRuleInput, providers []models.NotificationProvider) error {
	rule.Severity = in.Severity
	query := facades.Orm().WithContext(ctx).Query()
	if rule.ID == 0 {
		rule.Trigger, rule.Enabled, rule.NotifyResolved = in.Trigger, true, true
		if err := query.Create(rule); err != nil {
			return err
		}
	} else if err := query.Save(rule); err != nil {
		return err
	}

	return query.Model(rule).Association("Providers").Replace(providers)
}

// AddSuggestedAlerts makes an alert for each trigger, at its suggested
// severity, sending to providers: for app, or on Settings (app nil) for
// the server or every app, as each trigger is about.
func AddSuggestedAlerts(ctx context.Context, app *models.App, triggers []string, providers []models.NotificationProvider) error {
	return facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		for _, id := range triggers {
			t, ok := alerts.Find(id)
			if !ok || !t.Suggested {
				continue
			}
			rule := models.AlertRule{Scope: models.AlertAllApps, Trigger: id, Severity: string(t.Severity), Enabled: true, NotifyResolved: true}
			switch {
			case app != nil:
				rule.Scope, rule.AppID = models.AlertApp, &app.ID
			case t.Scope == alerts.ScopeServer:
				rule.Scope = models.AlertServer
			}
			if err := tx.Create(&rule); err != nil {
				return err
			}
			if err := tx.Model(&rule).Association("Providers").Replace(providers); err != nil {
				return err
			}
		}
		return nil
	})
}

// SettingsScope is the scope a Settings alert on trigger gets.
func SettingsScope(trigger string) models.AlertScope {
	if t, ok := alerts.Find(trigger); ok && t.Scope == alerts.ScopeServer {
		return models.AlertServer
	}

	return models.AlertAllApps
}
