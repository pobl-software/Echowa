package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"
	"github.com/spf13/cast"
	"rsc.io/qr"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/totp"
)

// TwoFactorLoginTTL is how long the code step of a login waits after the
// password before it has to be typed again.
var TwoFactorLoginTTL = 5 * time.Minute

// ErrTwoFactorOn is returned when setting up two-factor authentication
// that's already on: turning it off comes first, with a code.
var ErrTwoFactorOn = errors.New("two-factor authentication is already on")

const (
	pendingUserKey    = "login.two_factor.user"
	pendingExpiresKey = "login.two_factor.expires"
)

// TwoFactorOn reports whether user signs in with a code as well as the
// password.
func TwoFactorOn(user *models.User) bool {
	return user.TwoFactorConfirmedAt != nil
}

// TwoFactorSetup is what an authenticator app needs: the secret, to type
// in, and the otpauth:// link as a QR code, to scan. It's plain JSON for
// the setup dialog, never a page prop, so it isn't kept in the browser's
// history.
type TwoFactorSetup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QR     QRCode `json:"qr"`
}

// QRCode is an SVG path of the dark modules, one unit each, on a Size by
// Size grid. The page draws it with its own quiet zone.
type QRCode struct {
	Size int    `json:"size"`
	Path string `json:"path"`
}

// StartTwoFactor gives user a new secret, unconfirmed, replacing any setup
// that wasn't finished. Nothing changes at login until ConfirmTwoFactor.
func StartTwoFactor(ctx context.Context, user *models.User) (*TwoFactorSetup, error) {
	if TwoFactorOn(user) {
		return nil, ErrTwoFactorOn
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return nil, err
	}
	if err := saveTwoFactor(ctx, user, models.Secret(secret), nil, nil); err != nil {
		return nil, err
	}

	uri := totp.URI(facades.Config().GetString("app.name", "Echowa"), user.Email, secret)
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return nil, fmt.Errorf("two-factor QR code: %w", err)
	}

	return &TwoFactorSetup{Secret: secret, URI: uri, QR: qrCode(code)}, nil
}

// ConfirmTwoFactor turns two-factor authentication on if code comes from
// the secret StartTwoFactor gave out, which proves the app has it.
func ConfirmTwoFactor(ctx context.Context, user *models.User, code string, now time.Time) (bool, error) {
	if TwoFactorOn(user) || user.TwoFactorSecret == "" {
		return false, nil
	}
	step, ok := totp.Verify(string(user.TwoFactorSecret), code, now, 0)
	if !ok {
		return false, nil
	}
	confirmed := carbon.NewDateTime(carbon.FromStdTime(now))
	if err := saveTwoFactor(ctx, user, user.TwoFactorSecret, confirmed, &step); err != nil {
		return false, err
	}

	return true, nil
}

// CheckTwoFactorCode reports whether code is a valid code for user that
// hasn't been used. Accepting it uses it: the step is recorded only if it's
// later than the last one, so of two requests racing with one code, one
// wins.
func CheckTwoFactorCode(ctx context.Context, user *models.User, code string, now time.Time) (bool, error) {
	if !TwoFactorOn(user) {
		return false, nil
	}
	var after int64
	if user.TwoFactorLastStep != nil {
		after = *user.TwoFactorLastStep
	}
	step, ok := totp.Verify(string(user.TwoFactorSecret), code, now, after)
	if !ok {
		return false, nil
	}
	result, err := facades.Orm().WithContext(ctx).Query().Table("users").
		Where("id", user.ID).
		Where("two_factor_last_step IS NULL OR two_factor_last_step < ?", step).
		Update("two_factor_last_step", step)
	if err != nil {
		return false, err
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	user.TwoFactorLastStep = &step

	return true, nil
}

// TurnOffTwoFactor forgets user's secret, confirmed or not.
func TurnOffTwoFactor(ctx context.Context, user *models.User) error {
	return saveTwoFactor(ctx, user, "", nil, nil)
}

func saveTwoFactor(ctx context.Context, user *models.User, secret models.Secret, confirmed *carbon.DateTime, step *int64) error {
	// The secret goes through the model, so it's encrypted. Empty is
	// stored as NULL.
	var stored any
	if secret != "" {
		stored = secret
	}
	if _, err := facades.Orm().WithContext(ctx).Query().Model(user).Update(map[string]any{
		"two_factor_secret":       stored,
		"two_factor_confirmed_at": confirmed,
		"two_factor_last_step":    step,
	}); err != nil {
		return err
	}
	user.TwoFactorSecret, user.TwoFactorConfirmedAt, user.TwoFactorLastStep = secret, confirmed, step

	return nil
}

// BeginTwoFactorLogin remembers, in the session, that user has given the
// right password and is yet to give a code. Nothing is signed in until
// FinishTwoFactorLogin.
func BeginTwoFactorLogin(ctx http.Context, user *models.User) {
	session := ctx.Request().Session()
	session.Put(pendingUserKey, user.ID)
	session.Put(pendingExpiresKey, carbon.Now().StdTime().Add(TwoFactorLoginTTL).Unix())
}

// PendingTwoFactorLogin is the user whose password was right and who is yet
// to give a code. It's nil when there's no such login, or when it took too
// long.
func PendingTwoFactorLogin(ctx http.Context) (*models.User, error) {
	session := ctx.Request().Session()
	if session == nil {
		return nil, nil
	}
	id := cast.ToUint(session.Get(pendingUserKey))
	if id == 0 {
		return nil, nil
	}
	if carbon.Now().StdTime().Unix() > cast.ToInt64(session.Get(pendingExpiresKey)) {
		forgetTwoFactorLogin(ctx)
		return nil, nil
	}

	var user models.User
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", id).First(&user); err != nil {
		return nil, err
	}
	if user.ID == 0 || !TwoFactorOn(&user) {
		forgetTwoFactorLogin(ctx)
		return nil, nil
	}

	return &user, nil
}

// PendingTwoFactorUserID is the ID PendingTwoFactorLogin would look up, or
// 0, without a query: the code limiter counts attempts by it.
func PendingTwoFactorUserID(ctx http.Context) uint {
	if session := ctx.Request().Session(); session != nil {
		return cast.ToUint(session.Get(pendingUserKey))
	}

	return 0
}

// FinishTwoFactorLogin signs in the user PendingTwoFactorLogin returned.
func FinishTwoFactorLogin(ctx http.Context, user *models.User) error {
	forgetTwoFactorLogin(ctx)

	return Login(ctx, user)
}

func forgetTwoFactorLogin(ctx http.Context) {
	session := ctx.Request().Session()
	session.Forget(pendingUserKey, pendingExpiresKey)
}

// qrCode draws each row's runs of dark modules as rectangles.
func qrCode(code *qr.Code) QRCode {
	var path strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			start := x
			for x < code.Size && code.Black(x, y) {
				x++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", start, y, x-start, x-start)
		}
	}

	return QRCode{Size: code.Size, Path: path.String()}
}

// TurnOffAllTwoFactor turns two-factor authentication off for every user
// who had it on, and returns their emails. There's one admin, so that's
// them.
func TurnOffAllTwoFactor(ctx context.Context) ([]string, error) {
	var users []models.User
	if err := facades.Orm().WithContext(ctx).Query().Where("two_factor_confirmed_at IS NOT NULL").Get(&users); err != nil {
		return nil, err
	}
	emails := []string{}
	for i := range users {
		if err := TurnOffTwoFactor(ctx, &users[i]); err != nil {
			return emails, err
		}
		emails = append(emails, users[i].Email)
	}

	return emails, nil
}
