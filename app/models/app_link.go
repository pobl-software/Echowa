package models

import (
	"github.com/goravel/framework/database/orm"
)

// AppLink records that an app uses another app, such as its database (ADR
// 0028). Nothing reads it to deploy yet: it's there to map how apps fit
// together.
type AppLink struct {
	orm.Model
	AppID       uint `json:"app_id"`
	LinkedAppID uint `json:"linked_app_id"`
}
