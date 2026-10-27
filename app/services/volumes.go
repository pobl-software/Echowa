package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"goravel/app/docker"
	"goravel/app/facades"
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
