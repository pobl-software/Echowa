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

	Unhealthy          = "app.unhealthy"
	AppCPU             = "app.cpu"
	AppMemory          = "app.memory"
	DBConnections      = "db.connections"
	DBSize             = "db.size"
	LongestTransaction = "db.longest_transaction"
	TransactionIDAge   = "db.transaction_id_age"
	EvictedKeys        = "db.evicted_keys"
	CacheHitRate       = "db.cache_hit"
	ServerCPU          = "server.cpu"
	ServerMemory       = "server.memory"
	ServerSwap         = "server.swap"
	ServerIOWait       = "server.iowait"
	DiskUsed           = "server.disk"
	DockerUnreachable  = "server.docker"
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

	// A condition's. Unit is what its figure and threshold are measured
	// in, and Of what a percentage is of ("of its limit"). Below breaches
	// under the threshold rather than over it. Threshold and ForMinutes
	// are what a new alert on it starts at.
	Unit       Unit
	Of         string
	Below      bool
	Threshold  float64
	ForMinutes int
	// Summary says what a yes/no condition means when it fires.
	Summary string
}

var catalogue = []Trigger{
	{ID: DeployFailed, Kind: KindEvent, Scope: ScopeApp, Group: "Deploys", Label: "Deploy failed or rolled back", Severity: notify.Critical, Suggested: true},
	{ID: DeploySucceeded, Kind: KindEvent, Scope: ScopeApp, Group: "Deploys", Label: "Deploy succeeded", Severity: notify.Info},
	{ID: Unhealthy, Kind: KindCondition, Scope: ScopeApp, Group: "Health", Label: "Unhealthy", Severity: notify.Critical, Suggested: true,
		ForMinutes: 2, Summary: "It runs fewer replicas than it wants."},
	{ID: KilledForMemory, Kind: KindEvent, Scope: ScopeApp, Group: "Health", Label: "Killed for memory", Severity: notify.Warning, Suggested: true},
	{ID: AppCPU, Kind: KindCondition, Scope: ScopeApp, Group: "Resources", Label: "CPU use", Severity: notify.Warning, Suggested: true,
		Unit: UnitPercent, Of: "of its limit", Threshold: 90, ForMinutes: 10},
	{ID: AppMemory, Kind: KindCondition, Scope: ScopeApp, Group: "Resources", Label: "Memory use", Severity: notify.Warning, Suggested: true,
		Unit: UnitPercent, Of: "of its limit", Threshold: 90, ForMinutes: 5},
	{ID: DBConnections, Kind: KindCondition, Scope: ScopeApp, Apps: withInspect(), Group: "Database", Label: "Connections", Severity: notify.Warning, Suggested: true,
		Unit: UnitPercent, Of: "of the maximum", Threshold: 80, ForMinutes: 5},
	{ID: DBSize, Kind: KindCondition, Scope: ScopeApp, Apps: withInspect(), Group: "Database", Label: "Database size", Severity: notify.Warning,
		Unit: UnitGiB, Threshold: 10},
	{ID: LongestTransaction, Kind: KindCondition, Scope: ScopeApp, Apps: []string{"postgres", "mysql"}, Group: "Database", Label: "Longest open transaction", Severity: notify.Warning, Suggested: true,
		Unit: UnitMinutes, Threshold: 15},
	{ID: TransactionIDAge, Kind: KindCondition, Scope: ScopeApp, Apps: []string{"postgres"}, Group: "Database", Label: "Transaction ID age", Severity: notify.Critical, Suggested: true,
		Unit: UnitCount, Threshold: 1_500_000_000},
	{ID: EvictedKeys, Kind: KindCondition, Scope: ScopeApp, Apps: []string{"redis"}, Group: "Database", Label: "Evicted keys", Severity: notify.Warning,
		Unit: UnitPerSecond, Threshold: 0, ForMinutes: 5},
	{ID: CacheHitRate, Kind: KindCondition, Scope: ScopeApp, Apps: []string{"postgres", "mysql"}, Group: "Database", Label: "Cache hit rate", Severity: notify.Warning,
		Unit: UnitPercent, Below: true, Threshold: 90, ForMinutes: 15},
	{ID: BackupFailed, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Backup failed or skipped", Severity: notify.Critical, Suggested: true},
	{ID: BackupSucceeded, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Backup succeeded", Severity: notify.Info},
	{ID: RestoreFailed, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Restore failed", Severity: notify.Critical},
	{ID: RestoreSucceeded, Kind: KindEvent, Scope: ScopeApp, Apps: withBackups(), Group: "Backups", Label: "Restore finished", Severity: notify.Info},
	{ID: TaskFailed, Kind: KindEvent, Scope: ScopeApp, Apps: []string{Custom}, Group: "Tasks", Label: "Scheduled task failed or timed out", Severity: notify.Warning, Suggested: true},
	{ID: EchowaStarted, Kind: KindEvent, Scope: ScopeServer, Group: "Server", Label: "Echowa started", Severity: notify.Info, Suggested: true},
	{ID: ImagesPruned, Kind: KindEvent, Scope: ScopeServer, Group: "Server", Label: "Image prune removed images", Severity: notify.Info},
	{ID: ServerCPU, Kind: KindCondition, Scope: ScopeServer, Group: "Server", Label: "Server CPU use", Severity: notify.Warning, Suggested: true,
		Unit: UnitPercent, Threshold: 90, ForMinutes: 10},
	{ID: ServerMemory, Kind: KindCondition, Scope: ScopeServer, Group: "Server", Label: "Server memory use", Severity: notify.Warning, Suggested: true,
		Unit: UnitPercent, Threshold: 90, ForMinutes: 10},
	{ID: ServerSwap, Kind: KindCondition, Scope: ScopeServer, Group: "Server", Label: "Swap use", Severity: notify.Warning,
		Unit: UnitPercent, Threshold: 50, ForMinutes: 10},
	{ID: ServerIOWait, Kind: KindCondition, Scope: ScopeServer, Group: "Server", Label: "CPU waiting on disk", Severity: notify.Warning,
		Unit: UnitPercent, Threshold: 20, ForMinutes: 10},
	{ID: DiskUsed, Kind: KindCondition, Scope: ScopeServer, Group: "Server", Label: "Disk use", Severity: notify.Critical, Suggested: true,
		Unit: UnitPercent, Threshold: 90, ForMinutes: 5},
	{ID: DockerUnreachable, Kind: KindCondition, Scope: ScopeServer, Group: "Server", Label: "Docker unreachable", Severity: notify.Critical, Suggested: true,
		ForMinutes: 2, Summary: "Echowa can't reach Docker, so it can't deploy, check apps or read their metrics."},
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

// withInspect lists the templates whose health Echowa reads (ADR 0027).
func withInspect() []string {
	var kinds []string
	for _, t := range templates.All() {
		if t.Inspect != nil {
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
	// Severity, when set, is the message's instead of the rule's: a
	// condition's resolve is notify.Resolved.
	Severity notify.Severity
}
