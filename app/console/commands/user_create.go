package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
)

// UserCreate creates the admin account, or resets its password. There's no
// public registration or password reset email, so this is the only way in.
//
// Interactive by default. With --email it runs unattended for scripts:
//
//	printf '%s\n' "$PASSWORD" | echowa artisan user:create --email admin@example.com --password-stdin
//
// The password is only accepted on stdin, so it never lands in shell
// history or the process list.
type UserCreate struct {
	// Stdin is where --password-stdin reads from. Tests replace it.
	Stdin io.Reader
}

func (r *UserCreate) Signature() string {
	return "user:create"
}

func (r *UserCreate) Description() string {
	return "Create the admin user, or reset an existing user's password"
}

func (r *UserCreate) Extend() command.Extend {
	return command.Extend{
		Category: "user",
		Flags: []command.Flag{
			&command.StringFlag{Name: "email", Usage: "Run without prompts for this email"},
			&command.StringFlag{Name: "name", Usage: "Name to set (default: the existing user's, or Admin)"},
			&command.BoolFlag{Name: "password-stdin", Usage: "Read the password from the first line of stdin"},
		},
	}
}

func (r *UserCreate) Handle(ctx console.Context) error {
	if email := ctx.Option("email"); email != "" {
		return r.unattended(ctx, email)
	}

	return r.interactive(ctx)
}

func (r *UserCreate) unattended(ctx console.Context, email string) error {
	if !ctx.OptionBool("password-stdin") {
		return r.fail(ctx, errors.New("--email needs --password-stdin: pipe the password in"))
	}
	stdin := r.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return r.fail(ctx, fmt.Errorf("read password: %w", err))
	}
	password := strings.TrimRight(line, "\r\n")

	existing, err := services.FindUserByEmail(email)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(ctx.Option("name"))
	if name == "" && existing != nil {
		name = existing.Name
	}
	if name == "" {
		name = "Admin"
	}

	return r.save(ctx, name, email, password)
}

func (r *UserCreate) interactive(ctx console.Context) error {
	// Default to the existing admin, so a reset is just Enter + new password.
	var first models.User
	if err := facades.Orm().Query().Order("id").First(&first); err != nil {
		return err
	}

	email, err := ctx.Ask("Email", console.AskOption{
		Default: first.Email,
		Validate: func(email string) error {
			return services.ValidateEmail(services.NormalizeEmail(email))
		},
	})
	if err != nil {
		return err
	}
	existing, err := services.FindUserByEmail(email)
	if err != nil {
		return err
	}

	defaultName := "Admin"
	if existing != nil {
		defaultName = existing.Name
		ctx.Info("A user with this email exists. Its password will be reset.")
	}
	name, err := ctx.Ask("Name", console.AskOption{
		Default: defaultName,
		Validate: func(name string) error {
			if strings.TrimSpace(name) == "" {
				return errors.New("the name is required")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}

	password, err := ctx.Secret("Password", console.SecretOption{Validate: services.ValidatePassword})
	if err != nil {
		return err
	}
	if _, err := ctx.Secret("Confirm password", console.SecretOption{
		Validate: func(confirmation string) error {
			if confirmation != password {
				return errors.New("the passwords don't match")
			}
			return nil
		},
	}); err != nil {
		return err
	}

	return r.save(ctx, strings.TrimSpace(name), email, password)
}

func (r *UserCreate) save(ctx console.Context, name, email, password string) error {
	user, created, err := services.SaveUser(name, email, password)
	if err != nil {
		return r.fail(ctx, err)
	}
	if created {
		ctx.Success("Created " + user.Email + ". You can log in now.")
	} else {
		ctx.Success("Reset the password for " + user.Email + ".")
	}

	return nil
}

// fail returns err for Goravel to print, which also makes the command exit
// non-zero for scripts.
func (r *UserCreate) fail(_ console.Context, err error) error {
	return err
}
