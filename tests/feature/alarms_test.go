package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/services"
	"goravel/tests"
)

type AlarmsTestSuite struct {
	suite.Suite
	tests.TestCase
	notify *notify.Fake
	app    *models.App
	ops    models.NotificationProvider
}

func TestAlarmsTestSuite(t *testing.T) {
	suite.Run(t, new(AlarmsTestSuite))
}

func (s *AlarmsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	s.notify = s.FakeNotify()
	s.T().Cleanup(services.WaitForNotifications)
	s.app = createTestApp(s.T(), "blog", nil)
	s.ops = createTestProvider(s.T(), "Slack #ops")
}

func (s *AlarmsTestSuite) deployFailed() {
	services.Notify(alerts.Event{
		Trigger: alerts.DeployFailed, App: s.app, Title: "blog couldn't deploy v2", Summary: "manifest unknown",
		Path: fmt.Sprintf("/apps/%d?tab=deployments", s.app.ID),
	})
	services.WaitForNotifications()
}

func (s *AlarmsTestSuite) alarms() []models.Alarm {
	var rows []models.Alarm
	s.Require().NoError(facades.Orm().Query().Order("id").Get(&rows))

	return rows
}

func (s *AlarmsTestSuite) alarm(title string, read bool) models.Alarm {
	a := models.Alarm{Trigger: alerts.DeployFailed, Severity: "warning", Title: title, Summary: "", Path: "/dashboard"}
	if read {
		a.ReadAt = carbon.NewDateTime(carbon.Now())
	}
	s.Require().NoError(facades.Orm().Query().Create(&a))

	return a
}

// listed is the titles a page of the Alarms page lists.
func (s *AlarmsTestSuite) listed(b *browser, uri string) ([]string, any) {
	_, page := b.visit(uri)
	page.AssertComponent("Alarms")
	data := page.Props["alarms"].(map[string]any)["data"].([]any)
	titles := []string{}
	for _, a := range data {
		titles = append(titles, a.(map[string]any)["title"].(string))
	}

	return titles, page.ScrollProps["alarms"].NextPage
}

func (s *AlarmsTestSuite) unread(b *browser) float64 {
	_, page := b.visit("/alarms")

	return page.Props["unreadAlarms"].(float64)
}

func (s *AlarmsTestSuite) TestAnAlertWithNoProviderStillRaisesAnAlarm() {
	createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical)

	s.deployFailed()

	alarms := s.alarms()
	s.Require().Len(alarms, 1)
	s.Equal("blog couldn't deploy v2", alarms[0].Title)
	s.Equal("critical", alarms[0].Severity)
	s.Equal(fmt.Sprintf("/apps/%d?tab=deployments", s.app.ID), alarms[0].Path)
	s.Equal(s.app.ID, *alarms[0].AppID)
	s.Nil(alarms[0].ReadAt)
	s.Empty(s.notify.Sent())
}

func (s *AlarmsTestSuite) TestNoRuleRaisesNoAlarm() {
	s.deployFailed()

	s.Empty(s.alarms())
}

func (s *AlarmsTestSuite) TestSeveralRulesRaiseOneAlarmAtTheLoudest() {
	db := createTestProvider(s.T(), "Slack #db")
	createTestAlertRule(s.T(), alerts.DeployFailed, nil, notify.Warning, s.ops)
	createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical, db)

	s.deployFailed()

	alarms := s.alarms()
	s.Require().Len(alarms, 1)
	s.Equal("critical", alarms[0].Severity)
	s.Len(s.notify.Sent(), 2)
}

func (s *AlarmsTestSuite) TestAResolvedConditionIsAnAlarmOfItsOwn() {
	createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical, s.ops)

	services.Notify(alerts.Event{Trigger: alerts.DeployFailed, App: s.app, Title: "Resolved: blog", Severity: notify.Resolved})
	services.WaitForNotifications()

	alarms := s.alarms()
	s.Require().Len(alarms, 1)
	s.Equal("resolved", alarms[0].Severity)
}

func (s *AlarmsTestSuite) TestThePageListsNewestFirstAndFiltersUnread() {
	s.alarm("first", false)
	s.alarm("second", true)
	s.alarm("third", false)
	b := signedIn(s.T(), &s.TestCase)

	titles, next := s.listed(b, "/alarms")
	s.Equal([]string{"third", "second", "first"}, titles)
	s.Nil(next)

	titles, _ = s.listed(b, "/alarms?filter=unread")
	s.Equal([]string{"third", "first"}, titles)
	s.Equal(float64(2), s.unread(b))
}

func (s *AlarmsTestSuite) TestThePageLoadsOlderAlarmsBeforeTheOldestShown() {
	for i := range services.AlarmsPerPage + 2 {
		s.alarm(fmt.Sprintf("alarm %d", i), false)
	}
	b := signedIn(s.T(), &s.TestCase)

	titles, next := s.listed(b, "/alarms")
	s.Len(titles, services.AlarmsPerPage)
	s.Equal(fmt.Sprintf("alarm %d", services.AlarmsPerPage+1), titles[0])
	s.Require().NotNil(next)

	titles, next = s.listed(b, fmt.Sprintf("/alarms?before=%v", next))
	s.Equal([]string{"alarm 1", "alarm 0"}, titles)
	s.Nil(next)
}

func (s *AlarmsTestSuite) TestOpeningAnAlarmMarksItReadAndGoesWhereItsAbout() {
	a := s.alarm("deploy failed", false)
	s.alarm("other", false)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/alarms")

	b.submit(nethttp.MethodPost, fmt.Sprintf("/alarms/%d/read", a.ID), "/alarms", nil).
		AssertHeader("Location", "/dashboard")

	s.Equal(float64(1), s.unread(b))
	titles, _ := s.listed(b, "/alarms?filter=unread")
	s.Equal([]string{"other"}, titles)
}

func (s *AlarmsTestSuite) TestAnAlarmThatIsGoneIsNotFound() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/alarms")

	b.submit(nethttp.MethodPost, "/alarms/99/read", "/alarms", nil).AssertNotFound()
}

func (s *AlarmsTestSuite) TestMarkAllAsReadLeavesAlarmsNewerThanThePageUnread() {
	s.alarm("first", false)
	seen := s.alarm("second", false)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/alarms")
	s.alarm("since", false)

	b.submit(nethttp.MethodPost, "/alarms/read", "/alarms", map[string]any{"through": seen.ID}).
		AssertHeader("Location", "/alarms")

	titles, _ := s.listed(b, "/alarms?filter=unread")
	s.Equal([]string{"since"}, titles)
}

func (s *AlarmsTestSuite) TestPruneForgetsAlarmsAfter30Days() {
	var kept []uint
	for _, age := range []time.Duration{40 * 24 * time.Hour, 31 * 24 * time.Hour, 29 * 24 * time.Hour, time.Hour} {
		a := s.alarm("old", false)
		_, err := facades.Orm().Query().Model(&a).Update("created_at", carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-age))))
		s.Require().NoError(err)
		if age < services.NotificationsKept {
			kept = append(kept, a.ID)
		}
	}

	s.Require().NoError(services.PruneNotifications(context.Background(), time.Now()))

	var ids []uint
	for _, a := range s.alarms() {
		ids = append(ids, a.ID)
	}
	s.Equal(kept, ids)
}

func (s *AlarmsTestSuite) TestDeletingAnAppForgetsItsAlarms() {
	createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical)
	s.deployFailed()
	s.Require().Len(s.alarms(), 1)

	_, err := facades.Orm().Query().Delete(s.app)
	s.Require().NoError(err)

	s.Empty(s.alarms())
}
