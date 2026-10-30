package models

import (
	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

// AppVolume is a Docker named volume mounted into an app's tasks. An app
// with one runs a single replica (ADR 0025).
//
// A row is Mounted (RemovedAt nil) or Removing (RemovedAt set): the next
// deploy leaves a Removing volume out of the service, and once that deploy
// succeeds it removes the Docker volume and then the row (ADR 0039).
type AppVolume struct {
	orm.Model
	AppID uint `json:"app_id"`
	// Name is the Docker volume's name, unique across apps.
	Name string `json:"name"`
	// Target is the absolute path in the container.
	Target string `json:"target"`
	// RemovedAt is when the admin removed the volume, or nil while it's
	// mounted.
	RemovedAt *carbon.DateTime `json:"removed_at"`
}

// Removing reports whether the volume waits for a deploy to remove it.
func (v AppVolume) Removing() bool {
	return v.RemovedAt != nil
}
