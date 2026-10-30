package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// volumeRetry is how often RemoveVolumes asks Docker again, and
// volumeTimeout how long it keeps asking.
const (
	volumeRetry   = time.Second
	volumeTimeout = time.Minute
)

// RemoveVolumes removes a deleted app's volumes, and with them its data
// (ADR 0025). Swarm stops the app's tasks after RemoveService returns, and
// Docker won't remove a volume a container still uses, so it retries for up
// to a minute. A volume it can't remove is logged and left; LeftoverVolume
// then stops a new app from mounting it.
func RemoveVolumes(ctx context.Context, appName string, names []string) {
	ctx, cancel := context.WithTimeout(ctx, volumeTimeout)
	defer cancel()

	for _, name := range names {
		if err := removeVolume(ctx, name); err != nil {
			facades.Log().Error(fmt.Errorf("delete app %s: %w", appName, err))
		}
	}
}

// VolumeRemoval is what RemoveDetached did with one volume. Err is nil when
// the Docker volume and its row are both gone.
type VolumeRemoval struct {
	Volume models.AppVolume
	Err    error
}

// RemoveDetached removes the Docker volumes a successful deploy left out of
// its service, and then their rows (ADR 0039). volumes are the Removing
// rows that deploy excluded from its spec, never ones marked since, which
// its task may still mount. Each volume gets up to a minute for Swarm to
// stop the task that used it. One Docker refuses stays Removing, and the
// next deploy tries it again. The row goes only after the volume, so a
// failure never loses track of data.
func RemoveDetached(ctx context.Context, volumes []models.AppVolume) []VolumeRemoval {
	removals := make([]VolumeRemoval, 0, len(volumes))
	for _, v := range volumes {
		removals = append(removals, VolumeRemoval{Volume: v, Err: removeDetached(ctx, v)})
	}

	return removals
}

func removeDetached(ctx context.Context, v models.AppVolume) error {
	volumeCtx, cancel := context.WithTimeout(ctx, volumeTimeout)
	defer cancel()
	if err := removeVolume(volumeCtx, v.Name); err != nil {
		return err
	}
	_, err := facades.Orm().WithContext(ctx).Query().Where("id", v.ID).Where("removed_at IS NOT NULL").Delete(&models.AppVolume{})

	return err
}

func removeVolume(ctx context.Context, name string) error {
	for {
		err := Docker().RemoveVolume(ctx, name)
		if !errors.Is(err, docker.ErrVolumeInUse) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(volumeRetry):
		}
	}
}

// LeftoverVolume returns the first of names, the Docker volumes a new app
// or volume would mount, that already exists, or "" if none do. Such a
// volume is left from a deleted app, and mounting it would be worse than
// refusing: PostgreSQL only reads POSTGRES_PASSWORD when its data directory
// is empty, so the new app's password wouldn't work.
func LeftoverVolume(ctx context.Context, names []string) (string, error) {
	for _, name := range names {
		exists, err := Docker().VolumeExists(ctx, name)
		if err != nil {
			return "", err
		}
		if exists {
			return name, nil
		}
	}

	return "", nil
}

// VolumeClash says why a volume called dockerName at target can't join an
// app's volumes, as the form's field and what to tell the admin, or returns
// nil when it can. Removing rows count: they still hold their name and
// path until a deploy deletes them. except is the volume being moved, which
// can't clash with itself, and dockerName is "" when it keeps its name.
func VolumeClash(volumes []models.AppVolume, dockerName, target string, except uint) map[string]string {
	clash := map[string]string{}
	for _, v := range volumes {
		if v.ID == except {
			continue
		}
		if dockerName != "" && v.Name == dockerName {
			clash["name"] = "This app already has a volume with this name."
			if v.Removing() {
				clash["name"] = "A volume with this name is still being removed. Add it again after the next deployment."
			}
		}
		if v.Target == target {
			clash["target"] = "Another volume is already mounted at this path."
			if v.Removing() {
				clash["target"] = "A volume at this path is still being removed. Use the path again after the next deployment."
			}
		}
	}
	if len(clash) == 0 {
		return nil
	}

	return clash
}

// AddVolume saves a volume called name at target for the app, and sets its
// replicas to one in the same transaction, since a volume has one writer
// (ADR 0025). The Docker volume is echowa-<app>-<name>. The caller has
// checked VolumeClash and LeftoverVolume.
func AddVolume(ctx context.Context, app *models.App, name, target string) (*models.AppVolume, error) {
	volume := &models.AppVolume{AppID: app.ID, Name: templates.VolumeName(app.Name, name), Target: target}
	err := facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if err := tx.Create(volume); err != nil {
			return err
		}
		_, err := tx.Model(app).Update("replicas", 1)

		return err
	})
	if err != nil {
		return nil, err
	}
	app.Replicas = 1

	return volume, nil
}

// MoveVolume mounts the volume at target. Its data goes with it: the name,
// which is the Docker volume, never changes.
func MoveVolume(ctx context.Context, volume *models.AppVolume, target string) error {
	_, err := facades.Orm().WithContext(ctx).Query().Model(volume).Update("target", target)

	return err
}

// MarkVolumeRemoved turns the volume into Removing. The next deploy leaves
// it out and deletes it (RemoveDetached).
func MarkVolumeRemoved(ctx context.Context, volume *models.AppVolume) error {
	_, err := facades.Orm().WithContext(ctx).Query().Model(volume).Update("removed_at", carbon.NewDateTime(carbon.Now()))

	return err
}
