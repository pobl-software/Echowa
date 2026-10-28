package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
)

func (s *BackupsTestSuite) addSchedule(app *models.App, schedule, zone string, keep int) *models.BackupSchedule {
	row := &models.BackupSchedule{
		AppID: app.ID, StorageLocationID: s.loc.ID, Schedule: schedule, Timezone: zone, Keep: keep, Enabled: true,
	}
	s.Require().NoError(facades.Orm().Query().Create(row))

	return row
}

func (s *BackupsTestSuite) runDueBackups(now string) {
	at, err := time.Parse("2006-01-02 15:04", now)
	s.Require().NoError(err)
	s.Require().NoError(services.RunDueBackups(context.Background(), at))
	services.WaitForBackups()
}

func (s *BackupsTestSuite) scheduled(schedule *models.BackupSchedule) []models.Backup {
	var rows []models.Backup
	s.Require().NoError(facades.Orm().Query().Where("backup_schedule_id", schedule.ID).Order("id").Get(&rows))

	return rows
}

func (s *BackupsTestSuite) statuses(rows []models.Backup) []models.BackupStatus {
	list := []models.BackupStatus{}
	for _, row := range rows {
		list = append(list, row.Status)
	}

	return list
}

func (s *BackupsTestSuite) TestASchedulesBackupsAreDueInItsZone() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	utc := s.addSchedule(s.app, "0 3 * * *", "UTC", 7)
	newYork := s.addSchedule(s.app, "0 3 * * *", "America/New_York", 7)
	kathmandu := s.addSchedule(s.app, "0 3 * * *", "Asia/Kathmandu", 7)

	s.runDueBackups("2027-01-04 21:15")
	s.runDueBackups("2027-01-05 03:00")
	s.runDueBackups("2027-01-05 03:01")
	s.runDueBackups("2027-01-05 08:00")

	s.Len(s.scheduled(kathmandu), 1, "03:00 in Kathmandu is 21:15 UTC the day before")
	s.Len(s.scheduled(utc), 1)
	s.Len(s.scheduled(newYork), 1, "03:00 in New York is 08:00 UTC in January")
	for _, rec := range s.scheduled(newYork) {
		s.Equal(models.BackupSucceeded, rec.Status)
		s.Equal(models.BackupOnSchedule, rec.Trigger)
		s.Equal(s.loc.ID, rec.StorageLocationID)
	}
}

func (s *BackupsTestSuite) TestOffSchedulesAndAppsWithoutBackupsNeverRun() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	off := s.addSchedule(s.app, "* * * * *", "UTC", 7)
	_, err := facades.Orm().Query().Model(off).Update("enabled", false)
	s.Require().NoError(err)
	cache := s.templateApp("cache", "redis")
	s.addSchedule(cache, "* * * * *", "UTC", 7)

	s.runDueBackups("2027-01-05 10:00")

	n, err := facades.Orm().Query().Model(&models.Backup{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
	s.Zero(s.dumpCount())
}

func (s *BackupsTestSuite) TestAScheduleNeverOverlapsItself() {
	gate := make(chan struct{})
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", gate)
	schedule := s.addSchedule(s.app, "* * * * *", "UTC", 7)
	ctx := context.Background()

	s.Require().NoError(services.RunDueBackups(ctx, time.Now()))
	s.Eventually(func() bool { return s.dumpCount() == 1 }, 5*time.Second, 10*time.Millisecond)
	s.Require().NoError(services.RunDueBackups(ctx, time.Now().Add(time.Minute)))
	close(gate)
	services.WaitForBackups()

	rows := s.scheduled(schedule)
	s.Equal([]models.BackupStatus{models.BackupSucceeded, models.BackupSkipped}, s.statuses(rows))
	s.Equal("This schedule's last backup was still running.", *rows[1].Message)
	s.NotNil(rows[1].FinishedAt)
	s.Equal(1, s.dumpCount(), "the skipped minute never dumped")
}

func (s *BackupsTestSuite) TestTwoSchedulesDueTogetherBothRun() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	first := s.addSchedule(s.app, "0 * * * *", "UTC", 7)
	second := s.addSchedule(s.app, "0 * * * *", "Europe/London", 7)

	s.runDueBackups("2027-01-05 10:00")

	s.Equal([]models.BackupStatus{models.BackupSucceeded}, s.statuses(s.scheduled(first)))
	s.Equal([]models.BackupStatus{models.BackupSucceeded}, s.statuses(s.scheduled(second)), "the second waited for the app's lock")
}

func (s *BackupsTestSuite) TestRetentionKeepsExactlyTheNewestN() {
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	manual, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	schedule := s.addSchedule(s.app, "0 * * * *", "UTC", 2)

	for hour := 10; hour < 14; hour++ {
		s.runDueBackups(fmt.Sprintf("2027-01-05 %d:00", hour))
	}

	rows := s.scheduled(schedule)
	s.Require().Len(rows, 2)
	s.Equal([]models.BackupStatus{models.BackupSucceeded, models.BackupSucceeded}, s.statuses(rows))
	var kept models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&kept, manual.ID))
	s.Equal(manual.ID, kept.ID, "a backup made by hand is never pruned")

	want := []string{}
	for _, rec := range append([]models.Backup{kept}, rows...) {
		want = append(want, rec.Key, rec.Key[:len(rec.Key)-len(".dump")]+".json")
	}
	s.ElementsMatch(want, s.storage.Keys(), "the pruned backups' dumps and manifests are gone")
}

func (s *BackupsTestSuite) TestAFailureDoesntPrune() {
	schedule := s.addSchedule(s.app, "0 * * * *", "UTC", 1)
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	s.runDueBackups("2027-01-05 10:00")
	s.script("postgres (PostgreSQL) 18.6", "", 1, "pg_dump: error: connection failed", nil)
	s.runDueBackups("2027-01-05 11:00")

	rows := s.scheduled(schedule)
	s.Equal([]models.BackupStatus{models.BackupSucceeded, models.BackupFailed}, s.statuses(rows))
	s.Len(s.storage.Keys(), 2, "the one good backup stays")

	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	s.runDueBackups("2027-01-05 12:00")

	rows = s.scheduled(schedule)
	s.Equal([]models.BackupStatus{models.BackupFailed, models.BackupSucceeded}, s.statuses(rows),
		"a success prunes the older success, and leaves the failure to the record prune")
	s.Len(s.storage.Keys(), 2)
}

func (s *BackupsTestSuite) TestAFailureShowsOnOverviewUntilDismissed() {
	schedule := s.addSchedule(s.app, "0 * * * *", "UTC", 7)
	other := s.addSchedule(s.app, "30 * * * *", "UTC", 7)
	s.script("postgres (PostgreSQL) 18.6", "", 1, "pg_dump: error", nil)
	s.runDueBackups("2027-01-05 10:00")
	s.runDueBackups("2027-01-05 10:30")
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", s.app.ID)

	failures := func() []any {
		_, page := b.visit(url)
		return page.Props["backups"].(map[string]any)["failures"].([]any)
	}
	list := failures()
	s.Require().Len(list, 2)
	failure := list[0].(map[string]any)
	failed := s.scheduled(schedule)[0]
	s.Equal(float64(schedule.ID), failure["schedule_id"])
	s.Equal(float64(failed.ID), failure["backup"].(map[string]any)["id"])
	s.Equal("The dump exited with status 1.", failure["backup"].(map[string]any)["message"])
	s.Nil(failure["succeeded_at"])

	dismiss := fmt.Sprintf("/apps/%d/backup-schedules/%d/dismiss", s.app.ID, schedule.ID)
	b.submit(nethttp.MethodPost, dismiss, url, map[string]any{"backup": s.scheduled(other)[0].ID}).AssertNotFound()
	b.submit(nethttp.MethodPost, dismiss, url, map[string]any{"backup": failed.ID})
	s.Require().Len(failures(), 1)
	s.Equal(float64(other.ID), failures()[0].(map[string]any)["schedule_id"])

	s.runDueBackups("2027-01-05 11:00")
	s.Require().Len(failures(), 2, "a newer failure shows again")
	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	s.runDueBackups("2027-01-05 12:00")
	s.NotNil(failures()[0].(map[string]any)["succeeded_at"], "and says it has succeeded since")

	_, page := b.visit(url)
	last := page.Props["backups"].(map[string]any)["last_succeeded"].(map[string]any)
	s.Equal("R2", last["location"])
}

func (s *BackupsTestSuite) TestOldRecordsOfFailedBackupsArePruned() {
	now := time.Date(2027, 2, 10, 12, 0, 0, 0, time.UTC)
	add := func(status models.BackupStatus, daysAgo int) *models.Backup {
		at := carbon.NewDateTime(carbon.FromStdTime(now.AddDate(0, 0, -daysAgo)))
		rec := &models.Backup{
			AppID: s.app.ID, StorageLocationID: s.loc.ID, Trigger: models.BackupByHand, Status: status,
			StartedAt: at, FinishedAt: at,
		}
		s.Require().NoError(facades.Orm().Query().Create(rec))
		return rec
	}
	oldFailed := add(models.BackupFailed, 31)
	oldSkipped := add(models.BackupSkipped, 40)
	recentFailed := add(models.BackupFailed, 29)
	oldSucceeded := add(models.BackupSucceeded, 90)

	s.Require().NoError(services.PruneBackupRecords(context.Background(), now))

	var ids []uint
	s.Require().NoError(facades.Orm().Query().Model(&models.Backup{}).Order("id").Pluck("id", &ids))
	s.Equal([]uint{recentFailed.ID, oldSucceeded.ID}, ids)
	s.NotContains(ids, oldFailed.ID)
	s.NotContains(ids, oldSkipped.ID)
}

func (s *BackupsTestSuite) TestTheScheduleForm() {
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d?tab=backups", s.app.ID)
	base := fmt.Sprintf("/apps/%d/backup-schedules", s.app.ID)
	b.visit(page)

	b.submit(nethttp.MethodPost, base, page, map[string]any{
		"storage_location_id": 999, "schedule": "61 * * * *", "timezone": "Mars/Olympus", "keep": 0,
	})
	_, p := b.visit(page)
	errs := p.Props["errors"].(map[string]any)
	s.Equal("That storage location doesn't exist any more.", errs["storage_location_id"])
	s.Contains(errs, "schedule")
	s.Contains(errs, "timezone")
	s.Equal("Keep at least one.", errs["keep"])

	b.submit(nethttp.MethodPost, base, page, map[string]any{
		"storage_location_id": s.loc.ID, "schedule": " 0  3 * * * ", "timezone": "Europe/London", "keep": 14, "enabled": true,
	}).AssertHeader("Location", page)
	var schedule models.BackupSchedule
	s.Require().NoError(facades.Orm().Query().Where("app_id", s.app.ID).FirstOrFail(&schedule))
	s.Equal("0 3 * * *", schedule.Schedule)
	s.Equal(14, schedule.Keep)
	s.True(schedule.Enabled)

	_, p = b.visit(page)
	row := p.Props["backups"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	s.Equal("R2", row["location"])
	s.NotNil(row["next_run_at"])

	one := fmt.Sprintf("%s/%d", base, schedule.ID)
	b.submit(nethttp.MethodPut, one, page, map[string]any{
		"storage_location_id": s.loc.ID, "schedule": "0 4 * * 0", "timezone": "UTC", "keep": 4, "enabled": true,
	})
	b.submit(nethttp.MethodPut, one+"/enabled", page, map[string]any{"enabled": false})
	s.Require().NoError(facades.Orm().Query().Find(&schedule, schedule.ID))
	s.Equal("0 4 * * 0", schedule.Schedule)
	s.Equal(4, schedule.Keep)
	s.False(schedule.Enabled)
	_, p = b.visit(page)
	s.Nil(p.Props["backups"].(map[string]any)["schedules"].([]any)[0].(map[string]any)["next_run_at"], "off")

	s.script("postgres (PostgreSQL) 18.6", "PGDMP", 0, "", nil)
	rec, err := services.BackUpNow(s.app, &s.loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	_, err = facades.Orm().Query().Model(&models.Backup{}).Where("id", rec.ID).Update("backup_schedule_id", schedule.ID)
	s.Require().NoError(err)
	b.submit(nethttp.MethodDelete, one, page, nil).AssertHeader("Location", page)
	var kept models.Backup
	s.Require().NoError(facades.Orm().Query().Find(&kept, rec.ID))
	s.Equal(rec.ID, kept.ID, "deleting a schedule keeps its backups")
	s.Nil(kept.BackupScheduleID)

	cache := s.templateApp("cache", "redis")
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/backup-schedules", cache.ID), page, map[string]any{
		"storage_location_id": s.loc.ID, "schedule": "0 3 * * *", "timezone": "UTC", "keep": 7,
	}).AssertNotFound()
}

func (s *BackupsTestSuite) TestALocationASchedulesUsesCantBeDeleted() {
	s.addSchedule(s.app, "0 3 * * *", "UTC", 7)
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings")
	s.Equal(float64(1), page.Props["storageLocations"].([]any)[0].(map[string]any)["schedules"])

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/settings/storage/%d", s.loc.ID), "/settings", nil)
	_, page = b.visit("/settings")
	s.Equal("R2 is where the backup schedules of shop-db go. Change or delete them first.", page.Flash["error"])
	n, err := facades.Orm().Query().Model(&models.StorageLocation{}).Count()
	s.Require().NoError(err)
	s.EqualValues(1, n)
}
