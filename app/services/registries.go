package services

import (
	"context"
	"errors"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
)

// ErrRegistryInUse stops deleting a registry that apps still pull through.
var ErrRegistryInUse = errors.New("this registry is used by at least one app")

// RegistryInput is a validated registry form (rules.Registry).
type RegistryInput struct {
	Name     string `form:"name" json:"name"`
	Host     string `form:"host" json:"host"`
	Username string `form:"username" json:"username"`
	// Token is blank on update to keep the stored one.
	Token string `form:"token" json:"token"`
}

func CreateRegistry(ctx context.Context, in RegistryInput) (*models.Registry, error) {
	registry := &models.Registry{Name: in.Name, Host: in.Host, Username: in.Username, Token: models.Secret(in.Token)}
	if err := facades.Orm().WithContext(ctx).Query().Create(registry); err != nil {
		return nil, err
	}

	return registry, nil
}

func UpdateRegistry(ctx context.Context, registry *models.Registry, in RegistryInput) error {
	registry.Name = in.Name
	registry.Host = in.Host
	registry.Username = in.Username
	if in.Token != "" {
		registry.Token = models.Secret(in.Token)
	}

	return facades.Orm().WithContext(ctx).Query().Save(registry)
}

func DeleteRegistry(ctx context.Context, registry *models.Registry) error {
	count, err := facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Where("registry_id", registry.ID).Count()
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrRegistryInUse
	}
	_, err = facades.Orm().WithContext(ctx).Query().Delete(registry)

	return err
}

// RegistryAuth is the login PullImage sends for an app's private images.
// Nil registry means a public image.
func RegistryAuth(registry *models.Registry) *docker.RegistryAuth {
	if registry == nil {
		return nil
	}

	return &docker.RegistryAuth{ServerAddress: registry.Host, Username: registry.Username, Password: string(registry.Token)}
}
