package services

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// LeftoverVolume returns the first volume a new app called appName would
// mount that already exists, or "" if none do. Such a volume is left from a
// deleted app, and mounting it would be worse than refusing: PostgreSQL
// only reads POSTGRES_PASSWORD when its data directory is empty, so the new
// app's password wouldn't work.
func LeftoverVolume(ctx context.Context, tmpl *templates.Template, appName string) (string, error) {
	if tmpl == nil {
		return "", nil
	}
	for _, v := range tmpl.Volumes {
		name := templates.VolumeName(appName, v.Name)
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
