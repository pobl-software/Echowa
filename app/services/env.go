package services

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/goravel/framework/contracts/database/orm"

	"goravel/app/facades"
	"goravel/app/models"
)

// EnvVars returns the app's environment, decrypted.
func EnvVars(ctx context.Context, appID uint) (map[string]string, error) {
	var vars []models.AppEnvVar
	if err := facades.Orm().WithContext(ctx).Query().Where("app_id", appID).Get(&vars); err != nil {
		return nil, err
	}
	env := make(map[string]string, len(vars))
	for _, v := range vars {
		env[v.Key] = string(v.Value)
	}

	return env, nil
}

// SetEnvVars replaces the app's whole environment in one transaction.
// Keys must already be valid (rules.EnvVar).
func SetEnvVars(ctx context.Context, appID uint, env map[string]string) error {
	return facades.Orm().WithContext(ctx).Transaction(func(tx orm.Query) error {
		if _, err := tx.Where("app_id", appID).Delete(&models.AppEnvVar{}); err != nil {
			return err
		}
		return createEnvVars(tx, appID, env)
	})
}

func createEnvVars(tx orm.Query, appID uint, env map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if err := tx.Create(&models.AppEnvVar{AppID: appID, Key: key, Value: models.Secret(env[key])}); err != nil {
			return err
		}
	}

	return nil
}

// EnvEdit is one row of the Environment tab's editor. The editor never
// receives stored values, so an unchanged row sends Value nil with the
// key it was loaded under in OriginalKey. Renaming a row keeps its value.
type EnvEdit struct {
	Key         string  `form:"key" json:"key"`
	OriginalKey *string `form:"original_key" json:"original_key"`
	Value       *string `form:"value" json:"value"`
}

// EnvEditError points at a row whose value can't be resolved.
type EnvEditError struct {
	Row     int
	Message string
}

func (e *EnvEditError) Error() string {
	return fmt.Sprintf("row %d: %s", e.Row, e.Message)
}

// ApplyEnvEdits builds the new environment from the editor rows, taking
// unchanged values from current. Rows not sent are removed.
func ApplyEnvEdits(current map[string]string, edits []EnvEdit) (map[string]string, error) {
	env := make(map[string]string, len(edits))
	for i, edit := range edits {
		if _, dup := env[edit.Key]; dup {
			return nil, &EnvEditError{Row: i, Message: "this key is already used above"}
		}
		switch {
		case edit.Value != nil:
			env[edit.Key] = *edit.Value
		case edit.OriginalKey != nil:
			value, ok := current[*edit.OriginalKey]
			if !ok {
				return nil, &EnvEditError{Row: i, Message: "enter a value"}
			}
			env[edit.Key] = value
		default:
			return nil, &EnvEditError{Row: i, Message: "enter a value"}
		}
	}

	return env, nil
}
