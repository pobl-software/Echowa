package commands

import (
	"errors"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"goravel/app/facades"
	"goravel/app/services"
)

// SetupLink prints the link to the first-run setup page (ADR 0023), which
// creates the admin. `echowa setup` prints it at the end of an install;
// this prints it again if it's lost:
//
//	sudo echowa-artisan setup:link
type SetupLink struct{}

func (r *SetupLink) Signature() string {
	return "setup:link"
}

func (r *SetupLink) Description() string {
	return "Print the link to the setup page that creates the admin"
}

func (r *SetupLink) Extend() command.Extend {
	return command.Extend{Category: "user"}
}

func (r *SetupLink) Handle(ctx console.Context) error {
	needed, err := services.NeedsSetup()
	if err != nil {
		return err
	}
	if !needed {
		if err := services.ForgetSetupToken(); err != nil {
			return err
		}
		return errors.New("an admin already exists. To reset its password: sudo echowa-artisan user:create")
	}
	token, err := services.SetupToken()
	if err != nil {
		return err
	}
	ctx.Line(strings.TrimRight(facades.Config().GetString("http.url"), "/") + "/setup?token=" + token)

	return nil
}
