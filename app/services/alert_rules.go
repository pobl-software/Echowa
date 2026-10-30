package services

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

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
	// Kind is an event's or a condition's. The rest are a condition's:
	// its unit, what a percentage is of, whether it breaches below, the
	// words before its threshold, whether its figure moves with the clock
	// and it's checked hourly, what a new alert starts at, and that alert
	// in words.
	Kind        alerts.Kind `json:"kind"`
	Unit        alerts.Unit `json:"unit"`
	Of          string      `json:"of"`
	Below       bool        `json:"below"`
	Compare     string      `json:"compare"`
	Clock       bool        `json:"clock"`
	Hourly      bool        `json:"hourly"`
	Threshold   float64     `json:"threshold"`
	ForMinutes  int         `json:"for_minutes"`
	Description string      `json:"description"`
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
	Label string `json:"label"`
	Known bool   `json:"known"`
	// Description is a condition in words, "Memory use above 90% of its
	// limit for 5 minutes", or an event's label.
	Description    string        `json:"description"`
	Kind           alerts.Kind   `json:"kind"`
	Threshold      *float64      `json:"threshold"`
	ForMinutes     int           `json:"for_minutes"`
	RepeatMinutes  int           `json:"repeat_minutes"`
	NotifyResolved bool          `json:"notify_resolved"`
	Severity       string        `json:"severity"`
	Enabled        bool          `json:"enabled"`
	Providers      []ProviderRef `json:"providers"`
	// State is a condition's, at its worst over the subjects the list is
	// about: ok, pending, firing, or off while it's off. Empty for an
	// event.
	State string `json:"state"`
}

// FiringAlert is a condition that's firing, as the Alerts sections and
// Overview list it.
type FiringAlert struct {
	RuleID uint `json:"rule_id"`
	// Subject is the app's name, "Server", or "Server, /var" for a disk.
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	// Now is the last figure in words, "Now 93% of its limit.", or empty
	// for a yes/no condition.
	Now   string           `json:"now"`
	Since *carbon.DateTime `json:"since"`
	Path  string           `json:"path"`
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
	// Firing lists its conditions that are firing now.
	Firing []FiringAlert `json:"firing"`
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
	// Firing lists the server's and every app's conditions that are
	// firing now.
	Firing []FiringAlert `json:"firing"`
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
	// A condition's, and Threshold only one with a unit.
	Threshold      *float64 `form:"threshold" json:"threshold"`
	ForMinutes     int      `form:"for_minutes" json:"for_minutes"`
	RepeatMinutes  int      `form:"repeat_minutes" json:"repeat_minutes"`
	NotifyResolved bool     `form:"notify_resolved" json:"notify_resolved"`
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
	rules = slices.DeleteFunc(rules, func(rule models.AlertRule) bool {
		trigger, known := alerts.Find(rule.Trigger)
		return rule.Scope != models.AlertApp && (!known || !trigger.Fits(app))
	})
	states, err := activeStates(ctx, rules, models.AppSubject(app.ID))
	if err != nil {
		return section, err
	}
	if section.Firing, err = firing(ctx, rules, states); err != nil {
		return section, err
	}
	covered := map[string]bool{}
	for _, rule := range rules {
		if rule.Scope == models.AlertApp {
			section.Rules = append(section.Rules, summarise(rule, states))
		} else {
			section.EveryApp = append(section.EveryApp, summarise(rule, states))
		}
		covered[rule.Trigger] = true
	}

	applies, err := suggestedFor(ctx, app)
	if err != nil {
		return section, err
	}
	section.Suggestions = []AlertTriggerOption{}
	for _, t := range section.Triggers {
		if ok, depends := applies[t.ID]; t.Suggested && !covered[t.ID] && (!depends || ok) {
			section.Suggestions = append(section.Suggestions, t)
		}
	}

	if section.Providers, err = providerRefs(ctx); err != nil {
		return section, err
	}
	section.Recent, err = recentNotifications(ctx, app.ID)

	return section, err
}

// suggestedFor says, for the triggers that are noise on an app without
// what they watch, whether app has it: scheduled tasks, a TLS domain, a
// backup schedule or a connection pooler.
func suggestedFor(ctx context.Context, app *models.App) (map[string]bool, error) {
	count := func(model any) (bool, error) {
		n, err := facades.Orm().WithContext(ctx).Query().Model(model).Where("app_id", app.ID).Count()
		return n > 0, err
	}
	tasks, err := count(&models.ScheduledTask{})
	if err != nil {
		return nil, err
	}
	schedules, err := count(&models.BackupSchedule{})
	if err != nil {
		return nil, err
	}

	return map[string]bool{
		alerts.TaskFailed:        tasks,
		alerts.CertificateExpiry: app.Domain != nil && app.TLS,
		alerts.BackupAge:         schedules,
		alerts.PoolerDown:        app.Pooling,
	}, nil
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
	states, err := activeStates(ctx, rules, "")
	if err != nil {
		return section, err
	}
	if section.Firing, err = firing(ctx, rules, states); err != nil {
		return section, err
	}
	covered := map[string]bool{}
	for _, rule := range rules {
		section.Rules = append(section.Rules, summarise(rule, states))
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
	option := AlertTriggerOption{
		ID: t.ID, Group: t.Group, Label: t.Label, Server: t.Scope == alerts.ScopeServer, Only: appKindsText(t.Apps),
		Severity: string(t.Severity), Suggested: t.Suggested, Kind: t.Kind, Description: t.Label,
	}
	if t.Kind == alerts.KindCondition {
		option.Unit, option.Of, option.Below, option.Compare = t.Unit, t.Of, t.Below, t.Compare
		option.Clock, option.Hourly = t.Clock, t.Hourly
		option.Threshold, option.ForMinutes = t.Threshold, t.ForMinutes
		option.Description = t.Describe(suggestedRule(t))
	}

	return option
}

// suggestedRule is a new alert on t as a suggestion makes it.
func suggestedRule(t alerts.Trigger) models.AlertRule {
	rule := models.AlertRule{Trigger: t.ID, Severity: string(t.Severity), Enabled: true, NotifyResolved: true, ForMinutes: t.ForMinutes}
	if t.Kind == alerts.KindCondition && t.Unit != alerts.UnitNone {
		rule.Threshold = &t.Threshold
	}

	return rule
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

func summarise(rule models.AlertRule, states []models.AlertState) AlertRuleSummary {
	s := AlertRuleSummary{
		ID: rule.ID, Scope: rule.Scope, Trigger: rule.Trigger, Label: rule.Trigger, Description: rule.Trigger, Kind: alerts.KindEvent,
		Threshold: rule.Threshold, ForMinutes: rule.ForMinutes, RepeatMinutes: rule.RepeatMinutes, NotifyResolved: rule.NotifyResolved,
		Severity: rule.Severity, Enabled: rule.Enabled, Providers: []ProviderRef{},
	}
	if t, ok := alerts.Find(rule.Trigger); ok {
		s.Label, s.Known, s.Kind, s.Description = t.Label, true, t.Kind, t.Label
		if t.Kind == alerts.KindCondition {
			s.Description, s.State = t.Describe(rule), worstState(rule, states)
		}
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
	if rule.ID == 0 {
		rule.Trigger, rule.Enabled, rule.NotifyResolved = in.Trigger, true, true
	}
	rule.Severity = in.Severity
	if t, ok := alerts.Find(rule.Trigger); ok && t.Kind == alerts.KindCondition {
		rule.ForMinutes, rule.RepeatMinutes, rule.NotifyResolved, rule.Threshold = in.ForMinutes, in.RepeatMinutes, in.NotifyResolved, nil
		if t.Unit != alerts.UnitNone {
			rule.Threshold = in.Threshold
		}
	}
	query := facades.Orm().WithContext(ctx).Query()
	if rule.ID == 0 {
		if err := query.Create(rule); err != nil {
			return err
		}
	} else if err := query.Save(rule); err != nil {
		return err
	}

	return query.Model(rule).Association("Providers").Replace(providers)
}

// SetAlertRuleEnabled turns rule on or off. Off forgets its states without
// a message, so a firing alert turned off and on again starts from OK.
func SetAlertRuleEnabled(ctx context.Context, rule *models.AlertRule, enabled bool) error {
	return facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if _, err := tx.Model(rule).Update("enabled", enabled); err != nil {
			return err
		}
		if enabled {
			return nil
		}
		_, err := tx.Where("alert_rule_id", rule.ID).Delete(&models.AlertState{})
		return err
	})
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
			rule := suggestedRule(t)
			rule.Scope = models.AlertAllApps
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

// activeStates reads the pending and firing states of rules: for one
// subject, or for all of them when subject is empty.
func activeStates(ctx context.Context, rules []models.AlertRule, subject string) ([]models.AlertState, error) {
	states := []models.AlertState{}
	if len(rules) == 0 {
		return states, nil
	}
	ids := make([]any, len(rules))
	for i, rule := range rules {
		ids[i] = rule.ID
	}
	query := facades.Orm().WithContext(ctx).Query().WhereIn("alert_rule_id", ids).
		WhereIn("state", []any{string(alerts.Pending), string(alerts.Firing)})
	if subject != "" {
		query = query.Where("subject", subject)
	}
	err := query.Order("id").Get(&states)

	return states, err
}

// worstState is a condition rule's state at its worst over states.
func worstState(rule models.AlertRule, states []models.AlertState) string {
	if !rule.Enabled {
		return "off"
	}
	worst := alerts.OK
	for _, s := range states {
		if s.AlertRuleID != rule.ID {
			continue
		}
		if s.State == string(alerts.Firing) {
			return s.State
		}
		worst = alerts.Pending
	}

	return string(worst)
}

// firing lists the states that are firing, the most severe and newest
// first, with their subjects named.
func firing(ctx context.Context, rules []models.AlertRule, states []models.AlertState) ([]FiringAlert, error) {
	list := []FiringAlert{}
	byID := make(map[uint]models.AlertRule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}
	var appIDs []any
	for _, s := range states {
		if id, ok := subjectApp(s.Subject); ok {
			appIDs = append(appIDs, id)
		}
	}
	apps := map[uint]*models.App{}
	if len(appIDs) > 0 {
		var rows []models.App
		if err := facades.Orm().WithContext(ctx).Query().WhereIn("id", appIDs).Get(&rows); err != nil {
			return nil, err
		}
		for i := range rows {
			apps[rows[i].ID] = &rows[i]
		}
	}

	for _, s := range states {
		rule, ok := byID[s.AlertRuleID]
		t, known := alerts.Find(rule.Trigger)
		if !ok || !known || !rule.Enabled || s.State != string(alerts.Firing) {
			continue
		}
		var app *models.App
		mount, _ := strings.CutPrefix(s.Subject, "mount:")
		if id, ok := subjectApp(s.Subject); ok {
			if app = apps[id]; app == nil {
				continue
			}
		}
		alert := FiringAlert{
			RuleID: rule.ID, Subject: subjectName(app, mount), Description: t.Describe(rule), Severity: rule.Severity,
			Since: s.Since, Path: conditionPath(t, app),
		}
		if s.Value != nil {
			alert.Now = t.NowText(*s.Value)
		}
		list = append(list, alert)
	}
	slices.SortStableFunc(list, func(a, b FiringAlert) int {
		if rank := severityRank[b.Severity] - severityRank[a.Severity]; rank != 0 {
			return rank
		}
		return strings.Compare(sinceText(b.Since), sinceText(a.Since))
	})

	return list, nil
}

// subjectApp is the app ID in an app's subject, "app:12".
func subjectApp(subject string) (uint, bool) {
	id, ok := strings.CutPrefix(subject, "app:")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(id, 10, 64)

	return uint(n), err == nil
}

func sinceText(t *carbon.DateTime) string {
	if t == nil {
		return ""
	}

	return t.StdTime().UTC().Format(time.RFC3339)
}
