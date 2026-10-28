package services

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"goravel/app/backups"
	"goravel/app/facades"
	"goravel/app/models"
)

// StorageCheckTimeout keeps a location's Test inside the request timeout.
const StorageCheckTimeout = 2500 * time.Millisecond

// StorageLocationInput is a validated storage location form
// (rules.StorageLocation).
type StorageLocationInput struct {
	Name        string `form:"name" json:"name"`
	Endpoint    string `form:"endpoint" json:"endpoint"`
	Region      string `form:"region" json:"region"`
	Bucket      string `form:"bucket" json:"bucket"`
	Prefix      string `form:"prefix" json:"prefix"`
	AccessKeyID string `form:"access_key_id" json:"access_key_id"`
	// SecretKey is blank on update to keep the stored one.
	SecretKey string `form:"secret_key" json:"secret_key"`
	PathStyle bool   `form:"path_style" json:"path_style"`
}

// Apply copies the form onto loc, normalising the endpoint and prefix. A
// blank secret key leaves loc's as it is.
func (in StorageLocationInput) Apply(loc *models.StorageLocation) {
	loc.Name = in.Name
	loc.Endpoint = strings.TrimSuffix(in.Endpoint, "/")
	loc.Region = in.Region
	loc.Bucket = in.Bucket
	loc.Prefix = strings.Trim(in.Prefix, "/")
	loc.AccessKeyID = in.AccessKeyID
	if in.SecretKey != "" {
		loc.SecretKey = models.Secret(in.SecretKey)
	}
	loc.PathStyle = in.PathStyle
}

// StorageLocationSummary is a row of Settings' S3 storage list.
type StorageLocationSummary struct {
	models.StorageLocation
	// Backups is how many backup records use the location, which Echowa
	// forgets if it's deleted.
	Backups int64 `json:"backups"`
	// Schedules is how many backup schedules use it. While any do, it
	// can't be deleted.
	Schedules int64 `json:"schedules"`
}

func StorageLocations(ctx context.Context) ([]StorageLocationSummary, error) {
	var locations []models.StorageLocation
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&locations); err != nil {
		return nil, err
	}
	backups, err := countByLocation(ctx, "backups")
	if err != nil {
		return nil, err
	}
	schedules, err := countByLocation(ctx, "backup_schedules")
	if err != nil {
		return nil, err
	}
	rows := make([]StorageLocationSummary, 0, len(locations))
	for _, loc := range locations {
		rows = append(rows, StorageLocationSummary{StorageLocation: loc, Backups: backups[loc.ID], Schedules: schedules[loc.ID]})
	}

	return rows, nil
}

// countByLocation counts table's rows per storage_location_id.
func countByLocation(ctx context.Context, table string) (map[uint]int64, error) {
	var counts []struct {
		StorageLocationID uint
		Count             int64
	}
	err := facades.Orm().WithContext(ctx).Query().Table(table).
		Select("storage_location_id", "COUNT(*) AS count").Group("storage_location_id").Scan(&counts)
	byID := map[uint]int64{}
	for _, c := range counts {
		byID[c.StorageLocationID] = c.Count
	}

	return byID, err
}

// MovesBackups says the form points loc at another bucket or endpoint,
// which its backups' records don't follow: their keys would name objects
// that aren't there.
func (in StorageLocationInput) MovesBackups(loc *models.StorageLocation) bool {
	return strings.TrimSuffix(in.Endpoint, "/") != loc.Endpoint || in.Bucket != loc.Bucket
}

// StorageLocationBackups counts the backup records that use the location.
func StorageLocationBackups(ctx context.Context, id uint) (int64, error) {
	return facades.Orm().WithContext(ctx).Query().Model(&models.Backup{}).Where("storage_location_id", id).Count()
}

func CreateStorageLocation(ctx context.Context, in StorageLocationInput) (*models.StorageLocation, error) {
	loc := &models.StorageLocation{}
	in.Apply(loc)
	if err := facades.Orm().WithContext(ctx).Query().Create(loc); err != nil {
		return nil, err
	}

	return loc, nil
}

func UpdateStorageLocation(ctx context.Context, loc *models.StorageLocation, in StorageLocationInput) error {
	in.Apply(loc)

	return facades.Orm().WithContext(ctx).Query().Save(loc)
}

func DeleteStorageLocation(ctx context.Context, loc *models.StorageLocation) error {
	_, err := facades.Orm().WithContext(ctx).Query().Delete(loc)

	return err
}

// CheckStorageLocation runs backups.Check against loc, which needn't be
// saved, within StorageCheckTimeout.
func CheckStorageLocation(ctx context.Context, loc *models.StorageLocation) []backups.Step {
	ctx, cancel := context.WithTimeout(ctx, StorageCheckTimeout)
	defer cancel()

	return backups.Check(ctx, backups.Open(loc), loc.Prefix)
}

var notInPrefix = regexp.MustCompile(`[^a-z0-9-]+`)

// SuggestedStoragePrefix is the prefix the form offers: this server's
// short host name, so servers sharing a bucket don't share a folder.
func SuggestedStoragePrefix() string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(strings.ToLower(host), ".")

	return strings.Trim(notInPrefix.ReplaceAllString(host, "-"), "-")
}
