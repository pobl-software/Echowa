package models

import (
	"fmt"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

// AlertScope is what an alert rule covers (ADR 0040).
type AlertScope string

const (
	AlertServer  AlertScope = "server"
	AlertAllApps AlertScope = "all_apps"
	AlertApp     AlertScope = "app"
)

// AlertRule says which providers hear about a trigger, for one app, every
// app or the server. Which triggers exist is app/alerts' catalogue.
type AlertRule struct {
	orm.Model
	Scope AlertScope `json:"scope"`
	// AppID is set only for AlertApp.
	AppID   *uint  `json:"app_id"`
	Trigger string `json:"trigger"`
	// Threshold, ForMinutes, RepeatMinutes and NotifyResolved are a
	// condition's.
	Threshold      *float64 `json:"threshold"`
	ForMinutes     int      `json:"for_minutes"`
	RepeatMinutes  int      `json:"repeat_minutes"`
	NotifyResolved bool     `json:"notify_resolved"`
	// Severity is notify.Severity's info, warning or critical.
	Severity string `json:"severity"`
	Enabled  bool   `json:"enabled"`

	Providers []NotificationProvider `gorm:"many2many:alert_rule_providers" json:"-"`
}

// AlertState is a rule's state for one subject: an app, a mount or the
// server. It's in the database so a restart neither forgets a firing alert
// nor sends one again.
type AlertState struct {
	orm.Model
	AlertRuleID uint   `json:"alert_rule_id"`
	Subject     string `json:"subject"`
	// State is a condition's ok, pending or firing.
	State      string           `json:"state"`
	Since      *carbon.DateTime `json:"since"`
	Value      *float64         `json:"value"`
	NotifiedAt *carbon.DateTime `json:"notified_at"`
	// EventAt is when the newest event the rule notified happened, for an
	// event read from a poll, such as a kill for memory.
	EventAt *carbon.DateTime `json:"event_at"`
}

// AppSubject is the subject an app's state is kept under.
func AppSubject(appID uint) string {
	return fmt.Sprintf("app:%d", appID)
}

// NotificationStatus is where one notification to one provider is.
type NotificationStatus string

const (
	// NotificationPending is being sent, or waiting to try again.
	NotificationPending NotificationStatus = "pending"
	NotificationSent    NotificationStatus = "sent"
	NotificationFailed  NotificationStatus = "failed"
	// NotificationSuppressed was held back: its provider had had enough
	// for a while (services.NotifyBurst).
	NotificationSuppressed NotificationStatus = "suppressed"
)

// Notification is one message to one provider, sent or not.
type Notification struct {
	orm.Model
	AlertRuleID            *uint              `json:"alert_rule_id"`
	NotificationProviderID uint               `json:"notification_provider_id"`
	AppID                  *uint              `json:"app_id"`
	Trigger                string             `json:"trigger"`
	Severity               string             `json:"severity"`
	Title                  string             `json:"title"`
	Summary                string             `json:"summary"`
	Status                 NotificationStatus `json:"status"`
	Attempts               int                `json:"attempts"`
	Error                  *string            `json:"error"`
	SentAt                 *carbon.DateTime   `json:"sent_at"`
}
