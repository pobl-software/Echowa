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
	// Two-factor authentication is on once it's confirmed (ADR 0042). A
	// secret without a confirmation is a setup that wasn't finished.
	TwoFactorSecret      Secret           `json:"-"`
	TwoFactorConfirmedAt *carbon.DateTime `json:"-"`
	// The time step of the last code accepted, so no code works twice.
	TwoFactorLastStep *int64 `json:"-"`
}
