package models

import (
	"github.com/goravel/framework/database/orm"
)

// AppVolume is a Docker named volume mounted into an app's tasks. An app
// with one runs a single replica (ADR 0025).
type AppVolume struct {
	orm.Model
	AppID uint `json:"app_id"`
	// Name is the Docker volume's name, unique across apps.
	Name string `json:"name"`
	// Target is the absolute path in the container.
	Target string `json:"target"`
}
