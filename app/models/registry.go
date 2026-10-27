package models

import (
	"github.com/goravel/framework/database/orm"
)

// Registry is a login for pulling private images, e.g. a GHCR token.
type Registry struct {
	orm.Model
	Name     string `json:"name"`
	Host     string `json:"host"` // e.g. "ghcr.io", or "registry.example.com:5000"
	Username string `json:"username"`
	// Never serialised: props end up in the page HTML and JSON.
	Token Secret `json:"-"`
}
