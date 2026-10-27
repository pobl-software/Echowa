package models

import (
	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

type User struct {
	orm.Model
	Name            string           `json:"name"`
	Email           string           `json:"email"`
	EmailVerifiedAt *carbon.DateTime `json:"email_verified_at"`
	// Never serialised: props end up in the page HTML and JSON.
	Password      string  `json:"-"`
	RememberToken *string `json:"-"`
}
