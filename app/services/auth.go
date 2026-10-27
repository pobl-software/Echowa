// Package services holds application logic shared by controllers and
// middleware.
package services

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/models"
)

const intendedKey = "url.intended"

// dummyHash is checked when a login names an unknown email, so that the
// response takes as long as a wrong password and doesn't reveal which emails
// have accounts. Built lazily: facades aren't ready at package init.
var dummyHash = sync.OnceValue(func() string {
	hash, _ := facades.Hash().Make("dummy-password-for-timing")
	return hash
})

// Attempt returns the user with these credentials, or nil.
func Attempt(ctx http.Context, email, password string) (*models.User, error) {
	var user models.User
	if err := facades.Orm().WithContext(ctx).Query().Where("email", strings.ToLower(email)).First(&user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		facades.Hash().Check(password, dummyHash())
		return nil, nil
	}
	if !facades.Hash().Check(password, user.Password) {
		return nil, nil
	}

	return &user, nil
}

// Login starts an authenticated session (rotating the session ID and CSRF
// token) and, if asked, sets a long-lived "remember me" cookie.
func Login(ctx http.Context, user *models.User, remember bool) error {
	if _, err := facades.Auth(ctx).Login(user); err != nil {
		return err
	}
	if !remember {
		return nil
	}

	token, err := randomToken()
	if err != nil {
		return err
	}
	hashed := hashToken(token)
	if _, err := facades.Orm().WithContext(ctx).Query().Model(user).Update("remember_token", hashed); err != nil {
		return err
	}
	user.RememberToken = &hashed
	writeRememberCookie(ctx, fmt.Sprintf("%d|%s", user.ID, token), rememberDuration())

	return nil
}

// Logout ends the session and forgets the "remember me" token everywhere.
func Logout(ctx http.Context) error {
	if id, err := facades.Auth(ctx).ID(); err == nil {
		if _, err := facades.Orm().WithContext(ctx).Query().Table("users").Where("id", id).Update("remember_token", nil); err != nil {
			return err
		}
	}
	forgetRememberCookie(ctx)

	return facades.Auth(ctx).Logout()
}

// LoginFromRememberCookie logs a guest in from a valid "remember me" cookie.
// An invalid cookie is removed. Called by the AuthenticateFromRememberCookie
// middleware.
func LoginFromRememberCookie(ctx http.Context) {
	cookie := ctx.Request().Cookie(rememberCookieName())
	if cookie == "" || facades.Auth(ctx).Check() {
		return
	}

	idPart, token, found := strings.Cut(cookie, "|")
	id, err := strconv.ParseUint(idPart, 10, 64)
	if !found || err != nil || token == "" {
		forgetRememberCookie(ctx)
		return
	}

	var user models.User
	if err := facades.Orm().WithContext(ctx).Query().Where("id", id).First(&user); err != nil {
		facades.Log().Error(fmt.Errorf("remember cookie: %w", err))
		return
	}
	if user.ID == 0 || user.RememberToken == nil ||
		subtle.ConstantTimeCompare([]byte(*user.RememberToken), []byte(hashToken(token))) != 1 {
		forgetRememberCookie(ctx)
		return
	}

	if _, err := facades.Auth(ctx).Login(&user); err != nil {
		facades.Log().Error(fmt.Errorf("remember cookie: %w", err))
	}
}

// RememberIntendedURL saves where a guest was going, so login can send them
// there afterwards.
func RememberIntendedURL(ctx http.Context) {
	if session := ctx.Request().Session(); session != nil {
		session.Put(intendedKey, ctx.Request().FullUrl())
	}
}

// IntendedURL returns (and forgets) the saved URL, or fallback. Only local
// paths are returned, so it can't be used as an open redirect.
func IntendedURL(ctx http.Context, fallback string) string {
	session := ctx.Request().Session()
	if session == nil {
		return fallback
	}
	intended, _ := session.Pull(intendedKey, "").(string)
	if path := localPath(intended); path != "" {
		return path
	}

	return fallback
}

func localPath(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rest := rawURL[i+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "/"
		}
		rawURL = rest[slash:]
	}
	if !strings.HasPrefix(rawURL, "/") || strings.HasPrefix(rawURL, "//") {
		return ""
	}

	return rawURL
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

// hashToken is SHA-256, not bcrypt: tokens are 256 random bits, so a fast
// hash is enough and lets us look them up and compare cheaply.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

func rememberCookieName() string {
	return facades.Config().GetString("auth.remember.cookie", "remember_web")
}

func rememberDuration() time.Duration {
	return time.Duration(facades.Config().GetInt("auth.remember.days", 30)) * 24 * time.Hour
}

func writeRememberCookie(ctx http.Context, value string, lifetime time.Duration) {
	config := facades.Config()
	cookie := http.Cookie{
		Name:     rememberCookieName(),
		Value:    value,
		Path:     config.GetString("session.path", "/"),
		Domain:   config.GetString("session.domain"),
		Secure:   config.GetBool("session.secure"),
		HttpOnly: true,
		SameSite: config.GetString("session.same_site", "lax"),
	}
	if lifetime > 0 {
		cookie.Expires = carbon.Now().StdTime().Add(lifetime)
	} else {
		cookie.MaxAge = -1
	}
	ctx.Response().Cookie(cookie)
}

func forgetRememberCookie(ctx http.Context) {
	writeRememberCookie(ctx, "", 0)
}
