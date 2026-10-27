package models

import (
	"github.com/goravel/framework/database/orm"
)

// AppEnvVar is one environment variable of an app.
type AppEnvVar struct {
	orm.Model
	AppID uint   `json:"app_id"`
	Key   string `json:"key"`
	// Never serialised. The UI reveals one value on request.
	Value Secret `json:"-"`
}
