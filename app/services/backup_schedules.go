package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/tasks"
)

// KeepFailedBackups is how long the records of backups that didn't succeed
// are kept. They have nothing in the bucket.
const KeepFailedBackups = 30 * 24 * time.Hour

// unsuccessful are the statuses of a backup that ended without a dump.
var unsuccessful = []any{models.BackupFailed, models.BackupSkipped, models.BackupInterrupted}

// RunDueBackups starts, each in the background, the enabled schedules of
// apps with backups that are due in the minute holding now (ADR 0034). A
// schedule whose last backup is still going records the minute as
// skipped. Two schedules of one app both start, and the second waits for
// the app's lock.
func RunDueBackups(ctx context.Context, now time.Time) error {
	var due []models.BackupSchedule
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").Where("enabled", true).Order("id").Get(&due)
	if err != nil {
		return err
	}
	apps := map[uint]*models.App{}
	for i := range due {
		schedule := &due[i]
		parsed, err := tasks.Parse(schedule.Schedule, schedule.Timezone)
		if err != nil {
			facades.Log().Error(fmt.Errorf("backup schedule %d: %w", schedule.ID, err))
			continue
		}
		if !parsed.Due(now) {
			continue
		}
		app, ok := apps[schedule.AppID]
		if !ok {
			app = &models.App{}
			if err := facades.Orm().WithContext(ctx).Query().FindOrFail(app, schedule.AppID); err != nil {
				return err
			}
			apps[schedule.AppID] = app
		}
		if AppBackup(app) == nil || schedule.StorageLocation == nil {
			continue
		}
		_, err = startBackup(app, schedule.StorageLocation, models.BackupOnSchedule, schedule)
		if errors.Is(err, ErrScheduleRunning) {
			facades.Log().Info(fmt.Sprintf("backup schedule %d (%s): skipped, its last backup is still going", schedule.ID, app.Name))
		} else if err != nil {
			facades.Log().Error(fmt.Errorf("backup schedule %d: %w", schedule.ID, err))
		}
	}

	return nil
}

// pruneSchedule deletes the schedule's successful backups past its newest
// Keep: objects first, then the record. An error stops it, and the next
// success tries again. Backups made by hand or before a restore have no
// schedule, so they're never pruned.
func pruneSchedule(ctx context.Context, schedule *models.BackupSchedule) error {
	var old []models.Backup
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").
		Where("backup_schedule_id", schedule.ID).Where("status", models.BackupSucceeded).
		Order("id desc").Offset(schedule.Keep).Limit(1000).Get(&old)
	if err != nil {
		return err
	}
	for i := range old {
		if err := DeleteBackup(ctx, &old[i], old[i].StorageLocation); err != nil {
			return fmt.Errorf("backup %d: %w", old[i].ID, err)
		}
	}

	return nil
}

// PruneBackupRecords deletes the records of backups that didn't succeed
// and finished more than KeepFailedBackups before now.
func PruneBackupRecords(ctx context.Context, now time.Time) error {
	var ended []models.Backup
	err := facades.Orm().WithContext(ctx).Query().Select("id", "finished_at").WhereIn("status", unsuccessful).Get(&ended)
	if err != nil {
		return err
	}
	// Compared in Go, as deploy.Recover does.
	var ids []any
	for _, rec := range ended {
		if rec.FinishedAt != nil && rec.FinishedAt.StdTime().Before(now.Add(-KeepFailedBackups)) {
			ids = append(ids, rec.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err = facades.Orm().WithContext(ctx).Query().WhereIn("id", ids).Delete(&models.Backup{})

	return err
}

// BackupScheduleInput is the backup schedule form, validated by
// rules.BackupSchedule.
type BackupScheduleInput struct {
	StorageLocationID uint   `form:"storage_location_id" json:"storage_location_id"`
	Schedule          string `form:"schedule" json:"schedule"`
	Timezone          string `form:"timezone" json:"timezone"`
	Keep              int    `form:"keep" json:"keep"`
	Enabled           bool   `form:"enabled" json:"enabled"`
}

// SaveBackupSchedule creates schedule, or updates it when it has an ID,
// from in. A lower Keep applies after the schedule's next success.
func SaveBackupSchedule(ctx context.Context, schedule *models.BackupSchedule, in BackupScheduleInput) error {
	schedule.StorageLocationID = in.StorageLocationID
	schedule.StorageLocation = nil
	schedule.Schedule = strings.Join(strings.Fields(in.Schedule), " ")
	schedule.Timezone = in.Timezone
	schedule.Keep = in.Keep
	schedule.Enabled = in.Enabled

	return facades.Orm().WithContext(ctx).Query().Save(schedule)
}

// BackupScheduleSummary is a schedule as the Backups section lists it.
type BackupScheduleSummary struct {
	models.BackupSchedule
	Location string `json:"location"`
	// NextRunAt is nil while the schedule is off.
	NextRunAt  *time.Time     `json:"next_run_at"`
	LastBackup *BackupSummary `json:"last_backup"`
	// Kept is how many of its successful backups there are now.
	Kept int64 `json:"kept"`
}

// backupSchedules lists an app's schedules, oldest first, each with its
// next run and last backup.
func backupSchedules(ctx context.Context, appID uint, now time.Time) ([]BackupScheduleSummary, error) {
	var list []models.BackupSchedule
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").Where("app_id", appID).Order("id").Get(&list)
	if err != nil {
		return nil, err
	}

	summaries := make([]BackupScheduleSummary, len(list))
	for i, schedule := range list {
		summary := &summaries[i]
		summary.BackupSchedule = schedule
		if schedule.StorageLocation != nil {
			summary.Location = schedule.StorageLocation.Name
		}
		if parsed, err := tasks.Parse(schedule.Schedule, schedule.Timezone); err == nil && schedule.Enabled {
			if next := parsed.Next(now, 1); len(next) == 1 {
				summary.NextRunAt = &next[0]
			}
		}
		var last []models.Backup
		err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").
			Where("backup_schedule_id", schedule.ID).Order("id desc").Limit(1).Get(&last)
		if err != nil {
			return nil, err
		}
		if len(last) == 1 {
			row := backupSummary(&last[0])
			summary.LastBackup = &row
		}
		summary.Kept, err = facades.Orm().WithContext(ctx).Query().Model(&models.Backup{}).
			Where("backup_schedule_id", schedule.ID).Where("status", models.BackupSucceeded).Count()
		if err != nil {
			return nil, err
		}
	}

	return summaries, nil
}

// BackupFailure is a schedule's newest backup that didn't succeed and
// hasn't been dismissed, as the app's Overview shows it.
type BackupFailure struct {
	ScheduleID uint          `json:"schedule_id"`
	Schedule   string        `json:"schedule"`
	Timezone   string        `json:"timezone"`
	Backup     BackupSummary `json:"backup"`
	// SucceededAt is when the schedule's newest later backup that
	// succeeded finished.
	SucceededAt *carbon.DateTime `json:"succeeded_at"`
}

// backupFailures lists, oldest schedule first, each of an app's schedules
// whose newest failed, skipped or interrupted backup is newer than the one
// dismissed. A skipped backup counts: the schedule didn't do its job.
func backupFailures(ctx context.Context, appID uint) ([]BackupFailure, error) {
	var list []models.BackupSchedule
	if err := facades.Orm().WithContext(ctx).Query().Where("app_id", appID).Order("id").Get(&list); err != nil {
		return nil, err
	}

	failures := []BackupFailure{}
	for _, schedule := range list {
		var dismissed uint
		if schedule.DismissedBackupID != nil {
			dismissed = *schedule.DismissedBackupID
		}
		var failed []models.Backup
		err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").Where("backup_schedule_id", schedule.ID).
			Where("id > ?", dismissed).WhereIn("status", unsuccessful).Order("id desc").Limit(1).Get(&failed)
		if err != nil {
			return nil, err
		}
		if len(failed) == 0 {
			continue
		}
		failure := BackupFailure{
			ScheduleID: schedule.ID,
			Schedule:   schedule.Schedule,
			Timezone:   schedule.Timezone,
			Backup:     backupSummary(&failed[0]),
		}

		var since []models.Backup
		err = facades.Orm().WithContext(ctx).Query().Where("backup_schedule_id", schedule.ID).
			Where("id > ?", failed[0].ID).Where("status", models.BackupSucceeded).Order("id desc").Limit(1).Get(&since)
		if err != nil {
			return nil, err
		}
		if len(since) == 1 {
			failure.SucceededAt = since[0].FinishedAt
		}
		failures = append(failures, failure)
	}

	return failures, nil
}

// DismissBackupFailure hides the Overview's alert for rec and the
// schedule's failures before it. An older backup than the one dismissed
// changes nothing, so a stale page can't bring an old failure back.
func DismissBackupFailure(ctx context.Context, schedule *models.BackupSchedule, rec *models.Backup) error {
	_, err := facades.Orm().WithContext(ctx).Query().Model(&models.BackupSchedule{}).
		Where("id", schedule.ID).Where("(dismissed_backup_id IS NULL OR dismissed_backup_id < ?)", rec.ID).
		Update("dismissed_backup_id", rec.ID)

	return err
}

// LocationSchedules names the apps whose backup schedules use the
// location, for refusing its delete.
func LocationSchedules(ctx context.Context, locationID uint) ([]string, error) {
	var names []string
	err := facades.Orm().WithContext(ctx).Query().Model(&models.App{}).
		Where("id IN (SELECT app_id FROM backup_schedules WHERE storage_location_id = ?)", locationID).
		Order("name").Pluck("name", &names)

	return names, err
}
