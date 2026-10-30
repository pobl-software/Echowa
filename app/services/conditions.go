package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/notify"
)

// The triggers each check evaluates (ADR 0040).
var (
	healthTriggers = []string{alerts.Unhealthy, alerts.PoolerDown}
	metricTriggers = []string{alerts.AppCPU, alerts.AppMemory, alerts.ServerCPU, alerts.ServerMemory, alerts.ServerSwap,
		alerts.ServerIOWait, alerts.DiskUsed, alerts.DockerUnreachable}
	databaseTriggers = []string{alerts.DBConnections, alerts.DBSize, alerts.LongestTransaction, alerts.TransactionIDAge,
		alerts.EvictedKeys, alerts.CacheHitRate}
)

// An observation is one condition's figure for one subject at one check.
type observation struct {
	trigger string
	// app is the app it's about, nil for the server's.
	app *models.App
	// mount is the disk's, for DiskUsed.
	mount string
	alerts.Observation
}

// subject is the key alert_states keeps o's state under.
func (o observation) subject() string {
	switch {
	case o.app != nil:
		return models.AppSubject(o.app.ID)
	case o.mount != "":
		return MountSubject(o.mount)
	default:
		return ServerSubject
	}
}

// ServerSubject is the subject the server's own conditions are kept under.
const ServerSubject = "server"

// MountSubject is the subject a disk's state is kept under.
func MountSubject(mount string) string {
	return "mount:" + mount
}

// covers says whether rule is about o's subject.
func covers(rule models.AlertRule, t alerts.Trigger, o observation) bool {
	switch rule.Scope {
	case models.AlertServer:
		return o.app == nil
	case models.AlertAllApps:
		return o.app != nil && t.Fits(o.app)
	case models.AlertApp:
		return o.app != nil && rule.AppID != nil && *rule.AppID == o.app.ID && t.Fits(o.app)
	}

	return false
}

type stateKey struct {
	rule    uint
	subject string
}

// evaluateConditions steps the enabled rules on triggers through what one
// check observed at now, saves the states that changed and sends what
// fired, repeated or resolved. Each rule is its own alert with its own
// threshold, so two rules on one figure each send.
func evaluateConditions(ctx context.Context, triggers []string, observed []observation, now time.Time) error {
	// Stored to the second, so an unchanged state compares equal.
	now = now.Truncate(time.Second)
	var rules []models.AlertRule
	err := facades.Orm().WithContext(ctx).Query().With("Providers").WhereIn("trigger", anySlice(triggers)).
		Where("enabled", true).Order("id").Get(&rules)
	if err != nil || len(rules) == 0 {
		return err
	}
	ids := make([]any, len(rules))
	for i, rule := range rules {
		ids[i] = rule.ID
	}
	var stored []models.AlertState
	if err := facades.Orm().WithContext(ctx).Query().WhereIn("alert_rule_id", ids).Get(&stored); err != nil {
		return err
	}
	states := make(map[stateKey]*models.AlertState, len(stored))
	for i := range stored {
		states[stateKey{stored[i].AlertRuleID, stored[i].Subject}] = &stored[i]
	}

	var errs []error
	for _, rule := range rules {
		t, ok := alerts.Find(rule.Trigger)
		if !ok || t.Kind != alerts.KindCondition {
			continue
		}
		for _, o := range observed {
			if o.trigger != rule.Trigger || !covers(rule, t, o) {
				continue
			}
			row := states[stateKey{rule.ID, o.subject()}]
			if row == nil {
				row = &models.AlertState{AlertRuleID: rule.ID, Subject: o.subject()}
			}
			was := stateFromRow(row)
			next, outcome := alerts.Step(t, rule, was, o.Observation, now)
			if changed(was, next) {
				stateToRow(next, row)
				if err := facades.Orm().WithContext(ctx).Query().Save(row); err != nil {
					// Unsaved, it would send again at the next check.
					errs = append(errs, fmt.Errorf("alert %d, %s: %w", rule.ID, o.subject(), err))
					continue
				}
			}
			if outcome == alerts.Fire || outcome == alerts.Repeat || (outcome == alerts.Resolve && rule.NotifyResolved) {
				deliver(ctx, []models.AlertRule{rule}, conditionEvent(t, rule, o, was, outcome, now))
			}
		}
	}

	return errors.Join(errs...)
}

func stateFromRow(row *models.AlertState) alerts.State {
	return alerts.State{
		Phase:      alerts.Phase(row.State),
		Since:      stdTime(row.Since),
		ClearSince: stdTime(row.ClearSince),
		NotifiedAt: stdTime(row.NotifiedAt),
		Value:      row.Value,
	}
}

func stateToRow(s alerts.State, row *models.AlertState) {
	row.State = string(s.Phase)
	row.Since = dateTime(s.Since)
	row.ClearSince = dateTime(s.ClearSince)
	row.NotifiedAt = dateTime(s.NotifiedAt)
	row.Value = s.Value
}

// changed says whether next needs saving. An OK state's value alone
// doesn't: nothing shows it.
func changed(was, next alerts.State) bool {
	if was.Phase == "" {
		was.Phase = alerts.OK
	}
	sameValue := (was.Value == nil) == (next.Value == nil) && (was.Value == nil || *was.Value == *next.Value)

	return was.Phase != next.Phase || !was.Since.Equal(next.Since) || !was.ClearSince.Equal(next.ClearSince) ||
		!was.NotifiedAt.Equal(next.NotifiedAt) || (next.Phase != alerts.OK && !sameValue)
}

func stdTime(t *carbon.DateTime) time.Time {
	if t == nil {
		return time.Time{}
	}

	return t.StdTime()
}

func dateTime(t time.Time) *carbon.DateTime {
	if t.IsZero() {
		return nil
	}

	return carbon.NewDateTime(carbon.FromStdTime(t))
}

// conditionEvent is the message for a condition that fired, repeated or
// resolved. was is the state before the check, whose Since is when it
// fired for a repeat or a resolve.
func conditionEvent(t alerts.Trigger, rule models.AlertRule, o observation, was alerts.State, outcome alerts.Outcome, now time.Time) alerts.Event {
	e := alerts.Event{Trigger: t.ID, App: o.app, Title: subjectName(o.app, o.mount) + ": " + t.Describe(rule), Path: conditionPath(t, o.app)}
	firing := alerts.Minutes(max(1, int(math.Round(now.Sub(was.Since).Minutes()))))
	switch outcome {
	case alerts.Fire:
		e.Summary = firingSummary(t, o.Value)
	case alerts.Repeat:
		e.Title = "Still firing: " + e.Title
		e.Summary = firingSummary(t, o.Value) + " Firing for " + firing + "."
	case alerts.Resolve:
		e.Severity = notify.Resolved
		e.Title = "Resolved: " + e.Title
		e.Summary = "Clear after firing for " + firing + "."
		// A figure with words of its own, such as "None for 0.2 hours",
		// reads wrong once it's clear.
		if t.Unit != alerts.UnitNone && t.Now == nil && o.Value != nil {
			e.Summary = strings.TrimSuffix(t.NowText(*o.Value), ".") + ", after firing for " + firing + "."
		}
	}

	return e
}

// subjectName names a condition's subject in its message and the firing
// list: the app, the server, or one of its disks.
func subjectName(app *models.App, mount string) string {
	switch {
	case app != nil:
		return app.Name
	case mount != "":
		return "Server, " + mount
	default:
		return "Server"
	}
}

func firingSummary(t alerts.Trigger, value *float64) string {
	if t.Unit == alerts.UnitNone || value == nil {
		return t.Summary
	}

	return t.NowText(*value)
}

// conditionPath is the page that shows a condition's figure.
func conditionPath(t alerts.Trigger, app *models.App) string {
	if app == nil {
		return "/server"
	}
	tab := "metrics"
	switch t.ID {
	case alerts.Unhealthy:
		return fmt.Sprintf("/apps/%d", app.ID)
	case alerts.CertificateExpiry:
		tab = "domains"
	case alerts.BackupAge:
		tab = "backups"
	case alerts.PoolerDown:
		tab = "connection"
	}

	return fmt.Sprintf("/apps/%d?tab=%s", app.ID, tab)
}

// evaluateHealth checks the apps' health conditions against the status
// poll that just ran. Running below the replicas wanted breaches; a
// stopped app or one never deployed resolves silently, and one Docker
// didn't answer for keeps its state. A PostgreSQL app's pooler is read
// here too, only while an alert watches it.
func evaluateHealth(ctx context.Context, apps []models.App, statuses map[uint]LiveStatus, now time.Time) error {
	watchPoolers, err := hasConditionRules(ctx, alerts.PoolerDown)
	if err != nil {
		return err
	}
	observed := make([]observation, 0, len(apps))
	for i := range apps {
		app := &apps[i]
		status := statuses[app.ID]
		observed = append(observed, observation{trigger: alerts.Unhealthy, app: app, Observation: replicasShort(status)})
		if watchPoolers && alerts.AppKind(app) == "postgres" {
			o := observation{trigger: alerts.PoolerDown, app: app, Observation: alerts.Observation{Stopped: true}}
			if app.Pooling && !stopped(status) {
				o.Observation = poolerDown(AppPoolerStatus(ctx, app))
			}
			observed = append(observed, o)
		}
	}

	return evaluateConditions(ctx, healthTriggers, observed, now)
}

// replicasShort is whether a service runs fewer replicas than it wants.
func replicasShort(status LiveStatus) alerts.Observation {
	switch {
	case status.State == StateUnknown:
		return alerts.Observation{}
	case stopped(status):
		return alerts.Observation{Stopped: true}
	default:
		return alerts.Observation{Value: yes(status.Running < status.Desired)}
	}
}

// poolerDown is whether a pooler that should run doesn't: its last sync
// failed, or its service is short. Unknown while a sync runs.
func poolerDown(pooler PoolerStatus) alerts.Observation {
	switch {
	case pooler.Syncing || pooler.Service == nil || pooler.Service.State == StateUnknown:
		return alerts.Observation{}
	case pooler.Error != "", pooler.Service.State == StateNotDeployed:
		return alerts.Observation{Value: yes(true)}
	default:
		return replicasShort(*pooler.Service)
	}
}

// hasConditionRules says whether an enabled rule is on trigger, so a
// check that costs something can skip when none is.
func hasConditionRules(ctx context.Context, trigger string) (bool, error) {
	return facades.Orm().WithContext(ctx).Query().Model(&models.AlertRule{}).Where("trigger", trigger).Where("enabled", true).Exists()
}

// CertificateCheckTimeout bounds each domain's certificate read in the
// hourly check: longer than the Domains tab's 2 s, since nobody waits.
const CertificateCheckTimeout = 10 * time.Second

// ServedCertificate reads the certificate Traefik serves for a domain.
// Tests replace it.
var ServedCertificate = CheckCertificate

// CheckCertificateAlerts reads the certificate each custom app with a TLS
// domain is served, and checks the days it has left against the
// certificate alerts. An app without a TLS domain resolves silently, and
// one whose certificate can't be read, or isn't issued yet, keeps its
// state. The scheduler calls it hourly.
func CheckCertificateAlerts(ctx context.Context, now time.Time) error {
	if watched, err := hasConditionRules(ctx, alerts.CertificateExpiry); err != nil || !watched {
		return err
	}
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().WhereNull("template").Get(&apps); err != nil {
		return err
	}
	observed := make([]observation, len(apps))
	var wg sync.WaitGroup
	for i := range apps {
		app := &apps[i]
		observed[i] = appObservation(alerts.CertificateExpiry, app, nil)
		if app.Domain == nil || !app.TLS {
			observed[i].Observation = alerts.Observation{Stopped: true}
		}
		if observed[i].Stopped {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, CertificateCheckTimeout)
			defer cancel()
			if cert := ServedCertificate(ctx, *app.Domain, now); cert.NotAfter != nil {
				observed[i].Value = ptr(cert.NotAfter.Sub(now).Hours() / 24)
			}
		})
	}
	wg.Wait()

	return evaluateConditions(ctx, []string{alerts.CertificateExpiry}, observed, now)
}

// CheckBackupAlerts checks how long each app with backups has gone
// without a successful one: since its newest, or since the app was made
// when it has none. The scheduler calls it every minute.
func CheckBackupAlerts(ctx context.Context, now time.Time) error {
	if watched, err := hasConditionRules(ctx, alerts.BackupAge); err != nil || !watched {
		return err
	}
	trigger, _ := alerts.Find(alerts.BackupAge)
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().WhereIn("template", anySlice(trigger.Apps)).Get(&apps); err != nil {
		return err
	}
	observed := make([]observation, 0, len(apps))
	for i := range apps {
		app := &apps[i]
		var newest models.Backup
		err := facades.Orm().WithContext(ctx).Query().Select("id", "finished_at").Where("app_id", app.ID).
			Where("status", models.BackupSucceeded).Order("id desc").First(&newest)
		if err != nil {
			return err
		}
		since := app.CreatedAt
		if newest.FinishedAt != nil {
			since = newest.FinishedAt
		}
		var hours *float64
		if since != nil {
			hours = ptr(now.Sub(since.StdTime()).Hours())
		}
		observed = append(observed, appObservation(alerts.BackupAge, app, hours))
	}

	return evaluateConditions(ctx, []string{alerts.BackupAge}, observed, now)
}

// stopped is an app that wants no replicas: stopped by the admin (ADR
// 0036), or never deployed.
func stopped(status LiveStatus) bool {
	return status.State == StateStopped || status.State == StateStopping || status.State == StateNotDeployed
}

// appObservation is value for app, or Stopped when the last status poll
// saw it stopped.
func appObservation(trigger string, app *models.App, value *float64) observation {
	o := observation{trigger: trigger, app: app, Observation: alerts.Observation{Value: value}}
	if status, ok := polledStatus(app.ID); ok && stopped(status) {
		o.Observation = alerts.Observation{Stopped: true}
	}

	return o
}

// CheckMetricAlerts checks the metric conditions against a closed
// minute's averages. now is the minute's end. ReadMetrics calls it.
func CheckMetricAlerts(ctx context.Context, sample metrics.Sample, now time.Time) error {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().Get(&apps); err != nil {
		return err
	}
	var cores *float64
	if latest := LatestMetrics(); latest != nil && latest.Host.CPUCount != nil {
		cores = ptr(float64(*latest.Host.CPUCount))
	}

	var observed []observation
	for i := range apps {
		app := &apps[i]
		avg := func(figure string) *float64 { return sample.Avg(metrics.AppTable, app.Name, figure) }
		appCores := cores
		if status, ok := polledStatus(app.ID); ok && status.Limits.CPUMillicores > 0 {
			appCores = ptr(float64(status.Limits.CPUMillicores) / 1000)
		}
		// CPU is a percentage of one core, summed over the tasks, as
		// each task's limit is.
		var cpuLimit *float64
		if tasks := avg("tasks"); tasks != nil && appCores != nil {
			cpuLimit = ptr(*tasks * *appCores * 100)
		}
		observed = append(observed,
			appObservation(alerts.AppCPU, app, share(avg("cpu_percent"), cpuLimit)),
			appObservation(alerts.AppMemory, app, share(avg("memory_used_bytes"), avg("memory_limit_bytes"))),
		)
	}

	host := func(figure string) *float64 { return sample.Avg(metrics.HostTable, "", figure) }
	observed = append(observed,
		observation{trigger: alerts.ServerCPU, Observation: alerts.Observation{Value: host("cpu_percent")}},
		observation{trigger: alerts.ServerMemory, Observation: alerts.Observation{Value: share(host("memory_used_bytes"), host("memory_total_bytes"))}},
		observation{trigger: alerts.ServerSwap, Observation: alerts.Observation{Value: share(host("swap_used_bytes"), host("swap_total_bytes"))}},
		observation{trigger: alerts.ServerIOWait, Observation: alerts.Observation{Value: host("iowait_percent")}},
	)
	// The host row is there whenever the minute had a reading. Docker's
	// container count is missing only when no reading reached it.
	if len(sample.Keys(metrics.HostTable)) > 0 {
		observed = append(observed, observation{trigger: alerts.DockerUnreachable, Observation: alerts.Observation{Value: yes(host("containers") == nil)}})
	}
	for _, mount := range sample.Keys(metrics.DiskTable) {
		used, available := sample.Avg(metrics.DiskTable, mount, "used_bytes"), sample.Avg(metrics.DiskTable, mount, "available_bytes")
		// As df counts it, and the Server page shows it: blocks kept for
		// root are neither.
		var usable *float64
		if used != nil && available != nil {
			usable = ptr(*used + *available)
		}
		observed = append(observed, observation{trigger: alerts.DiskUsed, mount: mount, Observation: alerts.Observation{Value: share(used, usable)}})
	}

	return evaluateConditions(ctx, metricTriggers, observed, now)
}

// evaluateDatabases checks the database conditions against the check
// that just ran. An app whose check failed, or whose task isn't running,
// has no figures and keeps its state.
func evaluateDatabases(ctx context.Context, apps []models.App, dbs []metrics.Database, now time.Time) error {
	byName := make(map[string]metrics.Database, len(dbs))
	for _, db := range dbs {
		byName[db.Name] = db
	}
	var observed []observation
	for i := range apps {
		app := &apps[i]
		db := byName[app.Name]
		observed = append(observed,
			appObservation(alerts.DBConnections, app, share(db.Connections, db.MaxConnections)),
			appObservation(alerts.DBSize, app, scaled(db.SizeBytes, 1<<30)),
			appObservation(alerts.LongestTransaction, app, scaled(db.LongestTransactionSeconds, 60)),
			appObservation(alerts.TransactionIDAge, app, db.TransactionIDAge),
			appObservation(alerts.EvictedKeys, app, db.EvictedKeysPerSec),
			appObservation(alerts.CacheHitRate, app, db.CacheHitPercent),
		)
	}

	return evaluateConditions(ctx, databaseTriggers, observed, now)
}

// share is part as a percentage of whole, or nil without both or with
// nothing to divide by.
func share(part, whole *float64) *float64 {
	if part == nil || whole == nil || *whole <= 0 {
		return nil
	}

	return ptr(*part / *whole * 100)
}

func scaled(v *float64, by float64) *float64 {
	if v == nil {
		return nil
	}

	return ptr(*v / by)
}

func ptr[T any](v T) *T {
	return &v
}

// yes is a yes/no condition's figure.
func yes(b bool) *float64 {
	if b {
		return ptr(1.0)
	}

	return ptr(0.0)
}
