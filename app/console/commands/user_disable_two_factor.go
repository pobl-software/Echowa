package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"goravel/app/services"
)

// UserDisableTwoFactor turns two-factor authentication off, for an admin
// who has lost their authenticator app (ADR 0042). Like a password reset,
// it takes a shell on the server:
//
//	sudo echowa-artisan user:disable-two-factor
type UserDisableTwoFactor struct{}

func (r *UserDisableTwoFactor) Signature() string {
	return "user:disable-two-factor"
}

func (r *UserDisableTwoFactor) Description() string {
	return "Turn off two-factor authentication, for a lost authenticator app"
}

func (r *UserDisableTwoFactor) Extend() command.Extend {
	return command.Extend{Category: "user"}
}

func (r *UserDisableTwoFactor) Handle(ctx console.Context) error {
	emails, err := services.TurnOffAllTwoFactor(context.Background())
	if err != nil {
		return err
	}
	if len(emails) == 0 {
		ctx.Info("Two-factor authentication isn't on. Nothing to do.")
		return nil
	}
	for _, email := range emails {
		ctx.Success("Turned off two-factor authentication for " + email + ".")
	}
	ctx.Line("Log in with just the password, then set it up again in Settings, Security.")

	return nil
}
