// Package alerts is the catalogue of what Echowa can alert on (ADR 0040).
// Each trigger is a Go value, as app/templates lists templates, and a rule
// stores its ID. The pages, the suggestions, validation and
// services.Notify all read it.
package alerts

import (
	"slices"

	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/templates"
)

// Kind is whether a trigger happens once or holds for a while.
type Kind string

const (
	// KindEvent happens once: it notifies and is done.
	KindEvent Kind = "event"
	// KindCondition holds for a while: it fires, may repeat, and
	// resolves.
	KindCondition Kind = "condition"
)

// Scope is what a trigger is about. An app trigger goes in an app's rules
// or the every-app rules, a server trigger in the server's.
type Scope string

const (
	ScopeServer Scope = "server"
	ScopeApp    Scope = "app"
)

// Custom is a custom app's kind. A template app's is its slug.
const Custom = "custom"

// The triggers' IDs, as rules store them. Never rename one: a rule with an
// ID its release doesn't know is skipped.
const (
	DeployFailed     = "deploy.failed"
	DeploySucceeded  = "deploy.succeeded"
	KilledForMemory  = "app.oom"
	BackupFailed     = "backup.failed"
	BackupSucceeded  = "backup.succeeded"
	RestoreFailed    = "restore.failed"
	RestoreSucceeded = "restore.succeeded"
	TaskFailed       = "task.failed"
	EchowaStarted    = "echowa.started"
	ImagesPruned     = "prune.removed"
)

type Trigger struct {
	ID    string
	Kind  Kind
	Scope Scope
	// Apps lists the app kinds it applies to (Custom or a template's
	// slug). Nil means every kind.
	Apps []string
	// Group is the heading it's listed under: Deploys, Health, Backups,
	// Tasks or Server.
	Group string
	Label string
	// Severity is what a new alert on it starts at.
	Severity notify.Severity
	// Suggested says it's offered where it fits and isn't set up yet.
	Suggested bool
}

var catalogue = []Trigger{
	{ID: DeployFailed, Kind: KindEvent, Scope: ScopeApp, Group: "Deploys", Label: "Deploy failed or rolled back", Severity: notify.Critical, Suggested: true},
	{ID: DeploySucceeded, Kind: KindEvent, Scope: ScopeApp, Group: "Deploys", Label: "Deploy succeeded", Severity: notify.Info},
	{ID: KilledForMemory, Kind: KindEvent, Scope: ScopeApp, Group: "Health", Label: "Killed for memory", Severity: notify.Warning, Suggested: true},
	{ID: BackupFailed, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Backup failed or skipped", Severity: notify.Critical, Suggested: true},
	{ID: BackupSucceeded, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Backup succeeded", Severity: notify.Info},
	{ID: RestoreFailed, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Restore failed", Severity: notify.Critical},
	{ID: RestoreSucceeded, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Restore finished", Severity: notify.Info},
	{ID: TaskFailed, Kind: KindEvent, Scope: ScopeApp, Apps: []string{Custom}, Group: "Tasks", Label: "Scheduled task failed or timed out", Severity: notify.Warning, Suggested: true},
	{ID: EchowaStarted, Kind: KindEvent, Scope: ScopeServer, Group: "Server", Label: "Echowa started", Severity: notify.Info, Suggested: true},
	{ID: ImagesPruned, Kind: KindEvent, Scope: ScopeServer, Group: "Server", Label: "Image prune removed images", Severity: notify.Info},
}

// withBackups lists the templates with a Backups section.
func withBackups() []string {
	var kinds []string
	for _, t := range templates.All() {
		if t.Backup != nil {
			kinds = append(kinds, t.Slug)
		}
	}

	return kinds
}

// All lists the triggers in the order the pages show them.
func All() []Trigger {
	return slices.Clone(catalogue)
}

// Find returns the trigger with this ID.
func Find(id string) (Trigger, bool) {
	i := slices.IndexFunc(catalogue, func(t Trigger) bool { return t.ID == id })
	if i < 0 {
		return Trigger{}, false
	}

	return catalogue[i], true
}

// AppKind is Custom, or the app's template.
func AppKind(app *models.App) string {
	if app.Template == nil {
		return Custom
	}

	return *app.Template
}

// Fits says whether the trigger applies to app.
func (t Trigger) Fits(app *models.App) bool {
	return t.Scope == ScopeApp && (t.Apps == nil || slices.Contains(t.Apps, AppKind(app)))
}

// Event is something that happened once, for services.Notify to send to
// the rules on its trigger.
type Event struct {
	Trigger string
	// App is the app it happened to, or nil for the server's own.
	App     *models.App
	Title   string
	Summary string
	Fields  []notify.Field
	// Path is the page it's about, under APP_URL, such as
	// /apps/3?tab=backups.
	Path string
}
