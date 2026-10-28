package models

import (
	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

// BackupStatus is where a backup is, or how it ended.
type BackupStatus string

const (
	BackupRunning   BackupStatus = "running"
	BackupSucceeded BackupStatus = "succeeded"
	// BackupFailed left no dump in the bucket.
	BackupFailed BackupStatus = "failed"
	// BackupSkipped never started dumping: the app had no running task,
	// or something else held it too long.
	BackupSkipped BackupStatus = "skipped"
	// BackupInterrupted was running when Echowa stopped.
	BackupInterrupted BackupStatus = "interrupted"
)

// BackupTrigger is what started a backup.
type BackupTrigger string

const (
	BackupOnSchedule    BackupTrigger = "schedule"
	BackupByHand        BackupTrigger = "manual"
	BackupBeforeRestore BackupTrigger = "before_restore"
)

// Backup is one dump of a template app's database to a storage location
// (ADR 0034). The bucket holds the dump at Key and its manifest next to
// it (backups.ManifestKey).
type Backup struct {
	orm.Model
	AppID             uint          `json:"app_id"`
	StorageLocationID uint          `json:"storage_location_id"`
	BackupScheduleID  *uint         `json:"backup_schedule_id"`
	Trigger           BackupTrigger `json:"trigger"`
	Status            BackupStatus  `json:"status"`
	// Key is the dump's whole key, prefix included.
	Key string `json:"key"`
	// SizeBytes is what's been uploaded: the dump's size once it has
	// succeeded, and its progress while it runs.
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	ServerVersion string `json:"server_version"`
	// Output is the end of the dump's stderr.
	Output string `json:"output"`
	// Message is Echowa's own note, such as why the backup failed.
	Message    *string          `json:"message"`
	StartedAt  *carbon.DateTime `json:"started_at"`
	FinishedAt *carbon.DateTime `json:"finished_at"`

	StorageLocation *StorageLocation `json:"-"`
}

// BackupSchedule backs an app up to a storage location when Schedule is
// due in Timezone, and keeps its newest Keep successful backups (ADR 0034).
type BackupSchedule struct {
	orm.Model
	AppID             uint   `json:"app_id"`
	StorageLocationID uint   `json:"storage_location_id"`
	Schedule          string `json:"schedule"`
	Timezone          string `json:"timezone"`
	Keep              int    `json:"keep"`
	Enabled           bool   `json:"enabled"`
	// DismissedBackupID is the newest backup that didn't succeed and was
	// dismissed on Overview. A newer one shows again.
	DismissedBackupID *uint `json:"dismissed_backup_id"`

	StorageLocation *StorageLocation `json:"-"`
}

// RestoreStatus is where a restore is, or how it ended.
type RestoreStatus string

const (
	RestoreRunning   RestoreStatus = "running"
	RestoreSucceeded RestoreStatus = "succeeded"
	RestoreFailed    RestoreStatus = "failed"
	// RestoreInterrupted was running when Echowa stopped.
	RestoreInterrupted RestoreStatus = "interrupted"
)

// Restore is one restore of a dump into a template app's database (ADR
// 0034), from a backup Echowa has a record of or from a manifest found in
// the bucket.
type Restore struct {
	orm.Model
	AppID             uint `json:"app_id"`
	StorageLocationID uint `json:"storage_location_id"`
	// Key is the dump's whole key, prefix included.
	Key string `json:"key"`
	// BackupID is nil for a dump Echowa has no record of.
	BackupID       *uint         `json:"backup_id"`
	SafetyBackupID *uint         `json:"safety_backup_id"`
	NewApp         bool          `json:"new_app"`
	Status         RestoreStatus `json:"status"`
	// SizeBytes is the dump's, from its manifest, and ReadBytes how much of
	// it the restore has read so far.
	SizeBytes int64 `json:"size_bytes"`
	ReadBytes int64 `json:"read_bytes"`
	// Output is the end of the restore command's stderr.
	Output     string           `json:"output"`
	Message    *string          `json:"message"`
	StartedAt  *carbon.DateTime `json:"started_at"`
	FinishedAt *carbon.DateTime `json:"finished_at"`

	StorageLocation *StorageLocation `json:"-"`
}
