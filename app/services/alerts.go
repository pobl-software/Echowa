package services

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/release"
	"goravel/app/update"
)

// NotifyRetries are the waits before a failed notification's second and
// third tries (ADR 0040). Tests shorten them.
var NotifyRetries = []time.Duration{30 * time.Second, 2 * time.Minute}

const (
	// NotifyBurst is how many notifications a provider gets in
	// NotifyWindow. The rest are held back, and one message says how many
	// once the window has room.
	NotifyBurst  = 20
	NotifyWindow = 10 * time.Minute
	// NotificationsKept is how long a notification's record stays.
	NotificationsKept = 30 * 24 * time.Hour
	// maxRetryAfter caps how long a provider's Retry-After holds a
	// notification back.
	maxRetryAfter = 10 * time.Minute
	// heldBackTrigger marks the message that says how many were held back.
	heldBackTrigger = "held_back"
)

// notifyStart separates this process's notifications from those an
// earlier one left pending. Stored times have second precision.
var notifyStart = time.Now().Truncate(time.Second)

// deliveries makes the storm guard's count and the row it decides one
// step, and waits for sends in tests.
var deliveries struct {
	sync.Mutex
	wg sync.WaitGroup
}

// WaitForNotifications waits until nothing is being sent. For tests.
func WaitForNotifications() {
	deliveries.wg.Wait()
}

// Notify sends e to the providers of the enabled rules on its trigger:
// the server's rules for a server event, and for an app's, the app's own
// and the every-app ones. It returns once the sends have started, and
// only logs what goes wrong: the work that raised the event goes on.
func Notify(e alerts.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rules, err := rulesFor(ctx, e.Trigger, e.App)
	if err != nil {
		facades.Log().Error(fmt.Errorf("notify %s: %w", e.Trigger, err))
		return
	}
	deliver(ctx, rules, e)
}

func rulesFor(ctx context.Context, trigger string, app *models.App) ([]models.AlertRule, error) {
	query := facades.Orm().WithContext(ctx).Query().With("Providers").Where("trigger", trigger).Where("enabled", true)
	if app == nil {
		query = query.Where("scope", models.AlertServer)
	} else {
		query = query.Where("(scope = ? OR (scope = ? AND app_id = ?))", models.AlertAllApps, models.AlertApp, app.ID)
	}
	var rules []models.AlertRule
	err := query.Order("id").Get(&rules)

	return rules, err
}

// deliver sends e once to each enabled provider the rules name, at the
// loudest severity among the rules that name it.
func deliver(ctx context.Context, rules []models.AlertRule, e alerts.Event) {
	type target struct {
		provider models.NotificationProvider
		rule     models.AlertRule
	}
	targets := map[uint]*target{}
	for _, rule := range rules {
		for _, p := range rule.Providers {
			if !p.Enabled {
				continue
			}
			t, ok := targets[p.ID]
			if !ok {
				targets[p.ID] = &target{provider: p, rule: rule}
			} else if louder(rule, t.rule) {
				t.rule = rule
			}
		}
	}
	ids := make([]uint, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	for _, id := range ids {
		t := targets[id]
		severity := notify.Severity(t.rule.Severity)
		if e.Severity != "" {
			severity = e.Severity
		}
		n := models.Notification{
			AlertRuleID:            &t.rule.ID,
			NotificationProviderID: t.provider.ID,
			Trigger:                e.Trigger,
			Severity:               string(severity),
			Title:                  e.Title,
			Summary:                e.Summary,
		}
		if e.App != nil {
			n.AppID = &e.App.ID
		}
		enqueue(ctx, t.provider, n, message(severity, e))
	}
}

var severityRank = map[string]int{string(notify.Info): 1, string(notify.Warning): 2, string(notify.Critical): 3}

// louder says whether a's notification should go instead of b's: the
// higher severity, or else the one app's rule over every app's.
func louder(a, b models.AlertRule) bool {
	if severityRank[a.Severity] != severityRank[b.Severity] {
		return severityRank[a.Severity] > severityRank[b.Severity]
	}

	return a.Scope == models.AlertApp && b.Scope != models.AlertApp
}

// message is e as a provider shows it, with the server's name last so an
// admin with several Echowas can tell them apart.
func message(severity notify.Severity, e alerts.Event) notify.Message {
	m := notify.Message{Severity: severity, Title: e.Title, Summary: e.Summary, Fields: slices.Clone(e.Fields), URL: echowaURL(e.Path)}
	if host, err := os.Hostname(); err == nil {
		m.Fields = append(m.Fields, notify.Field{Label: "Server", Value: host})
	}

	return m
}

// echowaURL is path under APP_URL, or "" when either is missing.
func echowaURL(path string) string {
	base := facades.Config().GetString("http.url")
	if base == "" || path == "" {
		return ""
	}

	return strings.TrimSuffix(base, "/") + path
}

// enqueue records n and sends m to p in the background, unless p has had
// NotifyBurst notifications in the last NotifyWindow: then n is recorded
// as suppressed and SendHeldBack counts it later.
func enqueue(ctx context.Context, p models.NotificationProvider, n models.Notification, m notify.Message) {
	deliveries.Lock()
	full, err := providerFull(ctx, p.ID, time.Now())
	if err != nil {
		deliveries.Unlock()
		facades.Log().Error(fmt.Errorf("notify %s to provider %d: %w", n.Trigger, p.ID, err))
		return
	}
	n.Status = models.NotificationPending
	if full {
		n.Status = models.NotificationSuppressed
	}
	err = facades.Orm().WithContext(ctx).Query().Create(&n)
	deliveries.Unlock()
	if err != nil {
		facades.Log().Error(fmt.Errorf("notify %s to provider %d: %w", n.Trigger, p.ID, err))
		return
	}
	if full {
		return
	}

	deliveries.wg.Add(1)
	go func() {
		defer deliveries.wg.Done()
		send(n, p, m)
	}()
}

// providerFull says whether p has had NotifyBurst notifications in the
// NotifyWindow before now.
func providerFull(ctx context.Context, providerID uint, now time.Time) (bool, error) {
	var recent []models.Notification
	err := facades.Orm().WithContext(ctx).Query().Select("id", "created_at").
		Where("notification_provider_id", providerID).Where("status <> ?", models.NotificationSuppressed).
		Order("id desc").Limit(NotifyBurst).Get(&recent)
	if err != nil || len(recent) < NotifyBurst {
		return false, err
	}
	// Compared in Go, as deploy.Recover does.
	oldest := recent[len(recent)-1].CreatedAt

	return oldest != nil && oldest.StdTime().After(now.Add(-NotifyWindow)), nil
}

// send tries m up to 1 + len(NotifyRetries) times, waiting between tries,
// and records how it went in n. A provider that refuses the message
// itself isn't asked again.
func send(n models.Notification, p models.NotificationProvider, m notify.Message) {
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), notify.Timeout)
		err := sendTo(ctx, &p, m)
		cancel()
		if err == nil {
			updateNotification(n.ID, map[string]any{"status": models.NotificationSent, "attempts": attempt, "error": nil, "sent_at": carbon.NewDateTime(carbon.Now())})
			return
		}
		if attempt > len(NotifyRetries) || notify.Permanent(err) {
			updateNotification(n.ID, map[string]any{"status": models.NotificationFailed, "attempts": attempt, "error": err.Error()})
			facades.Log().Warning(fmt.Sprintf("notification %d to %s (%s) failed after %d tries: %v", n.ID, p.Name, p.Kind, attempt, err))
			return
		}
		updateNotification(n.ID, map[string]any{"attempts": attempt, "error": err.Error()})
		time.Sleep(retryWait(NotifyRetries[attempt-1], err))
	}
}

// retryWait is the wait before the next try: the schedule's, or longer if
// the provider asked for longer.
func retryWait(scheduled time.Duration, err error) time.Duration {
	return max(scheduled, min(notify.RetryAfter(err), maxRetryAfter))
}

func updateNotification(id uint, fields map[string]any) {
	if _, err := facades.Orm().Query().Model(&models.Notification{}).Where("id", id).Update(fields); err != nil {
		facades.Log().Error(fmt.Errorf("notification %d: %w", id, err))
	}
}

// SendHeldBack tells each provider whose notifications the storm guard
// held back how many it missed, once its window has room again. The
// scheduler calls it every minute.
func SendHeldBack(ctx context.Context) error {
	var providers []models.NotificationProvider
	if err := facades.Orm().WithContext(ctx).Query().Where("enabled", true).Get(&providers); err != nil {
		return err
	}
	for _, p := range providers {
		// Held back since the last summary that went out.
		var last models.Notification
		err := facades.Orm().WithContext(ctx).Query().Where("notification_provider_id", p.ID).
			Where("trigger", heldBackTrigger).Where("status <> ?", models.NotificationSuppressed).Order("id desc").First(&last)
		if err != nil {
			return err
		}
		held, err := facades.Orm().WithContext(ctx).Query().Model(&models.Notification{}).Where("notification_provider_id", p.ID).
			Where("status", models.NotificationSuppressed).Where("trigger <> ?", heldBackTrigger).Where("id > ?", last.ID).Count()
		if err != nil {
			return err
		}
		if held == 0 {
			continue
		}
		full, err := providerFull(ctx, p.ID, time.Now())
		if err != nil {
			return err
		}
		if full {
			continue
		}
		title := fmt.Sprintf("%d notifications held back", held)
		if held == 1 {
			title = "1 notification held back"
		}
		e := alerts.Event{
			Trigger: heldBackTrigger,
			Title:   title,
			Summary: fmt.Sprintf("Echowa sends %s at most %d notifications in %s, and kept the rest. It sends again now.", p.Name, NotifyBurst, minutes(int(NotifyWindow/time.Minute))),
			Path:    "/settings",
		}
		enqueue(ctx, p, models.Notification{NotificationProviderID: p.ID, Trigger: e.Trigger, Severity: string(notify.Warning), Title: e.Title, Summary: e.Summary}, message(notify.Warning, e))
	}

	return nil
}

// RecoverNotifications marks notifications an earlier Echowa left pending
// as failed: their sends stopped with it.
func RecoverNotifications(ctx context.Context) error {
	var pending []models.Notification
	if err := facades.Orm().WithContext(ctx).Query().Select("id", "created_at").Where("status", models.NotificationPending).Get(&pending); err != nil {
		return err
	}
	// Compared in Go, as deploy.Recover does.
	var ids []any
	for _, n := range pending {
		if n.CreatedAt != nil && n.CreatedAt.StdTime().Before(notifyStart) {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := facades.Orm().WithContext(ctx).Query().Model(&models.Notification{}).WhereIn("id", ids).
		Update(map[string]any{"status": models.NotificationFailed, "error": "Echowa restarted before it was sent."})

	return err
}

// PruneNotifications forgets notifications older than NotificationsKept.
// IDs grow with time, so it deletes up to the newest old one, a batch at
// a time.
func PruneNotifications(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-NotificationsKept)
	for {
		var batch []models.Notification
		if err := facades.Orm().WithContext(ctx).Query().Select("id", "created_at").Order("id").Limit(500).Get(&batch); err != nil {
			return err
		}
		// Compared in Go, as deploy.Recover does.
		old := slices.IndexFunc(batch, func(n models.Notification) bool {
			return n.CreatedAt == nil || !n.CreatedAt.StdTime().Before(cutoff)
		})
		if len(batch) == 0 || old == 0 {
			return nil
		}
		if old < 0 {
			old = len(batch)
		}
		if _, err := facades.Orm().WithContext(ctx).Query().Where("id <= ?", batch[old-1].ID).Delete(&models.Notification{}); err != nil {
			return err
		}
		if old < len(batch) {
			return nil
		}
	}
}

// NotifyStarted tells the server's rules that Echowa started, and from
// which version when an update brought it here: the updater still says
// it's running until the new release answers.
func NotifyStarted() {
	e := alerts.Event{
		Trigger: alerts.EchowaStarted,
		Title:   "Echowa started",
		Summary: "Echowa " + release.Version + " is running.",
		Path:    "/",
	}
	if s, err := update.ReadStatus(); err == nil && s != nil && s.State == update.Running && s.To == release.Version && s.From != release.Version {
		e.Title = "Echowa updated to " + release.Version
		e.Summary = "Echowa updated from " + s.From + " and is running."
	}
	Notify(e)
}

// notifyKills sends each app's newest kill for memory to the rules on
// KilledForMemory that haven't sent it. The kill is read from Swarm's
// task history, not seen happen, so each rule remembers the newest it
// sent in alert_states: a restart doesn't send it again, and one while
// Echowa was down still arrives.
func notifyKills(ctx context.Context, apps []models.App, statuses map[uint]LiveStatus) error {
	var rules []models.AlertRule
	err := facades.Orm().WithContext(ctx).Query().With("Providers").Where("trigger", alerts.KilledForMemory).Where("enabled", true).
		WhereIn("scope", []any{models.AlertAllApps, models.AlertApp}).Order("id").Get(&rules)
	if err != nil || len(rules) == 0 {
		return err
	}
	for i := range apps {
		app := &apps[i]
		status := statuses[app.ID]
		if status.OOMKilledAt == nil {
			continue
		}
		at := status.OOMKilledAt.Truncate(time.Second)
		var due []models.AlertRule
		for _, rule := range rules {
			if rule.Scope == models.AlertApp && (rule.AppID == nil || *rule.AppID != app.ID) {
				continue
			}
			// A new or re-enabled rule doesn't report an old kill.
			if rule.UpdatedAt != nil && !at.After(rule.UpdatedAt.StdTime()) {
				continue
			}
			unsent, err := recordEvent(ctx, &rule, models.AppSubject(app.ID), at)
			if err != nil {
				return err
			}
			if unsent {
				due = append(due, rule)
			}
		}
		if len(due) > 0 {
			deliver(ctx, due, killEvent(app, status, at))
		}
	}

	return nil
}

// recordEvent remembers at as the rule's newest event for subject, and
// says whether the rule has yet to send it.
func recordEvent(ctx context.Context, rule *models.AlertRule, subject string, at time.Time) (bool, error) {
	var state models.AlertState
	if err := facades.Orm().WithContext(ctx).Query().Where("alert_rule_id", rule.ID).Where("subject", subject).First(&state); err != nil {
		return false, err
	}
	if state.EventAt != nil && !at.After(state.EventAt.StdTime()) {
		return false, nil
	}
	eventAt := carbon.NewDateTime(carbon.FromStdTime(at))
	if state.ID == 0 {
		state = models.AlertState{AlertRuleID: rule.ID, Subject: subject, State: "ok", EventAt: eventAt}
		return true, facades.Orm().WithContext(ctx).Query().Create(&state)
	}
	_, err := facades.Orm().WithContext(ctx).Query().Model(&state).Update("event_at", eventAt)

	return true, err
}

// notifyUpdate tells the server's rules on UpdateAvailable about a
// release newer than this one, once each: each rule keeps the newest
// release it sent in alert_states, as with kills for memory. A rule made
// after the release still hears of it, since it's still there to install.
func notifyUpdate(latest LatestRelease) {
	if !release.Newer(latest.Version, release.Version) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rules, err := rulesFor(ctx, alerts.UpdateAvailable, nil)
	if err != nil {
		facades.Log().Error(fmt.Errorf("notify %s: %w", alerts.UpdateAvailable, err))
		return
	}
	at := latest.PublishedAt.Truncate(time.Second)
	var due []models.AlertRule
	for _, rule := range rules {
		unsent, err := recordEvent(ctx, &rule, ServerSubject, at)
		if err != nil {
			facades.Log().Error(fmt.Errorf("notify %s: %w", alerts.UpdateAvailable, err))
			return
		}
		if unsent {
			due = append(due, rule)
		}
	}
	if len(due) == 0 {
		return
	}
	deliver(ctx, due, alerts.Event{
		Trigger: alerts.UpdateAvailable,
		Title:   "Echowa " + latest.Version + " is out",
		Summary: "This server runs " + release.Version + ". Update it from Settings, Updates.",
		Fields:  []notify.Field{{Label: "Released", Value: latest.PublishedAt.UTC().Format("2 Jan 2006")}},
		Path:    "/settings",
	})
}

func killEvent(app *models.App, status LiveStatus, at time.Time) alerts.Event {
	e := alerts.Event{
		Trigger: alerts.KilledForMemory,
		App:     app,
		Title:   app.Name + " was killed for memory",
		Summary: "A task went over its memory limit, so Docker killed it and Swarm started another.",
		Fields:  []notify.Field{{Label: "Killed", Value: at.UTC().Format("2 Jan 2006 15:04 UTC")}},
		Path:    fmt.Sprintf("/apps/%d?tab=resources", app.ID),
	}
	if status.Limits.MemoryMiB > 0 {
		e.Fields = append(e.Fields, notify.Field{Label: "Memory limit", Value: sizeText(int64(status.Limits.MemoryMiB) << 20)})
	}

	return e
}
