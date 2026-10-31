package services

import (
	"context"
	"fmt"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/models"
)

// AlarmsPerPage is how many alarms the Alarms page loads at a time.
const AlarmsPerPage = 50

// AlarmSummary is an alarm as the Alarms page lists it.
type AlarmSummary struct {
	ID       uint   `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Severity string `json:"severity"`
	// Path is the page it's about, or empty.
	Path      string           `json:"path"`
	Read      bool             `json:"read"`
	CreatedAt *carbon.DateTime `json:"created_at"`
}

// AlarmPage is a page of alarms, newest first, and the ID to read older
// ones before, or nil after the oldest.
type AlarmPage struct {
	Alarms []AlarmSummary
	Before *uint
}

// recordAlarm keeps e as an alarm, at the severity of the loudest of the
// rules that matched it, whether or not any of them has a provider. With
// no rules, nothing went off.
func recordAlarm(ctx context.Context, rules []models.AlertRule, e alerts.Event) {
	if len(rules) == 0 {
		return
	}
	rule := rules[0]
	for _, r := range rules[1:] {
		if louder(r, rule) {
			rule = r
		}
	}
	alarm := models.Alarm{
		AlertRuleID: &rule.ID, Trigger: e.Trigger, Severity: rule.Severity,
		Title: e.Title, Summary: e.Summary, Path: e.Path,
	}
	if e.Severity != "" {
		alarm.Severity = string(e.Severity)
	}
	if e.App != nil {
		alarm.AppID = &e.App.ID
	}
	if err := facades.Orm().WithContext(ctx).Query().Create(&alarm); err != nil {
		facades.Log().Error(fmt.Errorf("alarm %s: %w", e.Trigger, err))
	}
}

// Alarms reads up to AlarmsPerPage alarms older than before (0 for the
// newest), only the unread ones if unread.
func Alarms(ctx context.Context, unread bool, before uint) (AlarmPage, error) {
	query := facades.Orm().WithContext(ctx).Query()
	if unread {
		query = query.WhereNull("read_at")
	}
	if before > 0 {
		query = query.Where("id < ?", before)
	}
	var rows []models.Alarm
	if err := query.Order("id desc").Limit(AlarmsPerPage + 1).Get(&rows); err != nil {
		return AlarmPage{}, err
	}

	page := AlarmPage{Alarms: []AlarmSummary{}}
	if len(rows) > AlarmsPerPage {
		rows = rows[:AlarmsPerPage]
		page.Before = &rows[len(rows)-1].ID
	}
	for _, a := range rows {
		page.Alarms = append(page.Alarms, AlarmSummary{
			ID: a.ID, Title: a.Title, Summary: a.Summary, Severity: a.Severity, Path: a.Path,
			Read: a.ReadAt != nil, CreatedAt: a.CreatedAt,
		})
	}

	return page, nil
}

// UnreadAlarms counts the alarms not yet read.
func UnreadAlarms(ctx context.Context) (int64, error) {
	return facades.Orm().WithContext(ctx).Query().Model(&models.Alarm{}).WhereNull("read_at").Count()
}

// MarkAlarmRead marks one alarm as read.
func MarkAlarmRead(ctx context.Context, id uint) error {
	_, err := facades.Orm().WithContext(ctx).Query().Model(&models.Alarm{}).Where("id", id).WhereNull("read_at").
		Update("read_at", carbon.NewDateTime(carbon.Now()))

	return err
}

// MarkAlarmsRead marks every alarm up to through as read: the newest the
// page showed, so one that went off since stays unread.
func MarkAlarmsRead(ctx context.Context, through uint) error {
	_, err := facades.Orm().WithContext(ctx).Query().Model(&models.Alarm{}).Where("id <= ?", through).WhereNull("read_at").
		Update("read_at", carbon.NewDateTime(carbon.Now()))

	return err
}

// FiringNow lists every condition firing now, the server's and every
// app's, the most severe and newest first.
func FiringNow(ctx context.Context) ([]FiringAlert, error) {
	var rules []models.AlertRule
	if err := facades.Orm().WithContext(ctx).Query().Where("enabled", true).Order("id").Get(&rules); err != nil {
		return nil, err
	}
	states, err := activeStates(ctx, rules, "")
	if err != nil {
		return nil, err
	}

	return firing(ctx, rules, states)
}
