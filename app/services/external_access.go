package services

import (
	"context"
	"errors"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// EnableExternalAccess saves the host port the app is published on. For a
// template that serves TLS, it issues the app's certificate first, unless
// all of it is already in the env (ADR 0026). Keeping it means a client's
// copy of the CA stays good when access is turned off and on again.
func EnableExternalAccess(ctx context.Context, app *models.App, port int, tmpl *templates.Template) error {
	return facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if tmpl.TLS != nil {
			if err := ensureTLS(tx, app); err != nil {
				return err
			}
		}
		if _, err := tx.Model(app).Update("external_port", port); err != nil {
			return err
		}
		app.ExternalPort = &port
		return nil
	})
}

func ensureTLS(tx orm.Query, app *models.App) error {
	count, err := tx.Model(&models.AppEnvVar{}).Where("app_id", app.ID).WhereIn("key", toAny(templates.TLSEnvKeys)).Count()
	if err != nil || count == int64(len(templates.TLSEnvKeys)) {
		return err
	}
	env, err := templates.IssueTLS(app.Name, time.Now())
	if err != nil {
		return err
	}
	if _, err := tx.Where("app_id", app.ID).WhereIn("key", toAny(templates.TLSEnvKeys)).Delete(&models.AppEnvVar{}); err != nil {
		return err
	}

	return createEnvVars(tx, app.ID, env)
}

func toAny(keys []string) []any {
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}

	return out
}

// SetExternalPort saves the host port the app is published on, or nil for
// none (ADR 0026). It takes effect on the next deploy.
func SetExternalPort(ctx context.Context, app *models.App, port *int) error {
	if _, err := facades.Orm().WithContext(ctx).Query().Model(app).Update("external_port", port); err != nil {
		return err
	}
	app.ExternalPort = port

	return nil
}

// HostPortInUse reports whether something on this machine already listens
// on port, such as sshd or another container. Echowa runs on the host, so
// trying to listen is the check. Only "address in use" counts: any other
// failure is left for the deploy to report.
func HostPortInUse(port int) bool {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return errors.Is(err, syscall.EADDRINUSE)
	}
	_ = l.Close()

	return false
}
