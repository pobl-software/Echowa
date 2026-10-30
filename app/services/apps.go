package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/templates"
)

// AppInput is a validated app form (rules.App). Name is ignored on update.
type AppInput struct {
	Name           string  `form:"name" json:"name"`
	Repository     string  `form:"repository" json:"repository"`
	DefaultTag     string  `form:"default_tag" json:"default_tag"`
	RegistryID     *uint   `form:"registry_id" json:"registry_id"`
	Domain         *string `form:"domain" json:"domain"`
	Port           int     `form:"port" json:"port"`
	Replicas       int     `form:"replicas" json:"replicas"`
	HealthcheckCmd *string `form:"healthcheck_cmd" json:"healthcheck_cmd"`
	TLS            bool    `form:"tls" json:"tls"`
	// Command replaces the image's CMD (rules.SplitCommand). Empty keeps
	// it. A template app's arguments come from its template instead.
	Command *string `form:"command" json:"command"`
}

// CreateApp saves a new app with a freshly generated webhook secret. With a
// template, it also stores what the template's seed makes (env with
// generated passwords, volumes, args), all or nothing.
func CreateApp(ctx context.Context, in AppInput, tmpl *templates.Template) (*models.App, error) {
	secret, err := NewWebhookSecret()
	if err != nil {
		return nil, err
	}
	app := &models.App{Name: in.Name, WebhookSecret: models.Secret(secret)}
	in.applyDocker(app)
	in.applyDomain(app)
	if tmpl == nil {
		if err := facades.Orm().WithContext(ctx).Query().Create(app); err != nil {
			return nil, err
		}
		return app, nil
	}

	seed, err := tmpl.Seed(in.Name)
	if err != nil {
		return nil, err
	}
	app.Template = &tmpl.Slug
	app.Args = seed.Args
	err = facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if err := tx.Create(app); err != nil {
			return err
		}
		if err := createEnvVars(tx, app.ID, seed.Env); err != nil {
			return err
		}
		for _, volume := range seed.Volumes {
			volume.AppID = app.ID
			if err := tx.Create(&volume); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return app, nil
}

// UpdateDocker changes the app's Docker config: image, registry, port,
// replicas, healthcheck and, for a custom app, command. The name names the Swarm service and can't
// change.
func UpdateDocker(ctx context.Context, app *models.App, in AppInput) error {
	in.applyDocker(app)

	return facades.Orm().WithContext(ctx).Query().Save(app)
}

// UpdateDomain changes the app's domain and whether it uses TLS.
func UpdateDomain(ctx context.Context, app *models.App, in AppInput) error {
	in.applyDomain(app)

	return facades.Orm().WithContext(ctx).Query().Save(app)
}

// RotateWebhookSecret replaces the secret. CI keeps failing to deploy
// until its copy is updated.
func RotateWebhookSecret(ctx context.Context, app *models.App) error {
	secret, err := NewWebhookSecret()
	if err != nil {
		return err
	}
	_, err = facades.Orm().WithContext(ctx).Query().Model(app).Update("webhook_secret", models.Secret(secret))
	if err != nil {
		return err
	}
	app.WebhookSecret = models.Secret(secret)

	return nil
}

// DeleteApp deletes the app with its env vars, deployments, metrics and
// the every-app alerts' states for it. Removing the Swarm service and
// pooler is the caller's job. A pooler sync that was already running could
// start the pooler again, so one more sync runs behind it and removes it.
func DeleteApp(ctx context.Context, app *models.App) error {
	if _, err := facades.Orm().WithContext(ctx).Query().Delete(app); err != nil {
		return err
	}
	deleteAppMetrics(ctx, app.ID)
	// A new app could get the ID again, and with it a firing alert.
	if _, err := facades.Orm().WithContext(ctx).Query().Where("subject", models.AppSubject(app.ID)).Delete(&models.AlertState{}); err != nil {
		facades.Log().Error(fmt.Errorf("deleting app %d's alert states: %w", app.ID, err))
	}
	if poolerSyncing(app.ID) {
		StartPoolerSync(app)
	}

	return nil
}

// HasVolumes reports whether the app keeps data in a volume, which limits
// it to one replica (ADR 0025).
func HasVolumes(ctx context.Context, appID uint) (bool, error) {
	return facades.Orm().WithContext(ctx).Query().Model(&models.AppVolume{}).Where("app_id", appID).Exists()
}

// NewWebhookSecret returns 256 random bits, hex-encoded.
func NewWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

func (in AppInput) applyDocker(app *models.App) {
	app.Repository = in.Repository
	app.DefaultTag = in.DefaultTag
	app.RegistryID = in.RegistryID
	app.Port = in.Port
	app.Replicas = in.Replicas
	app.HealthcheckCmd = blankToNil(in.HealthcheckCmd)
	if app.Template == nil {
		// Validated by rules.AppDocker, so it splits.
		app.Args, _ = rules.SplitCommand(deref(in.Command))
	}
}

func (in AppInput) applyDomain(app *models.App) {
	app.Domain = blankToNil(in.Domain)
	app.TLS = in.TLS
}

// blankToNil stores an empty optional field as NULL, so "no domain" is one
// value and unique(domain) ignores it.
func blankToNil(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(*s)

	return &trimmed
}
