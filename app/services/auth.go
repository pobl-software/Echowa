// Package services holds application logic shared by controllers and
// middleware.
package services

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"

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
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("email", strings.ToLower(email)).First(&user); err != nil {
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

// CurrentUser is the signed-in user, or nil for a guest. It reads the ID
// from the session and queries with the request's own context: Goravel's
// Auth(ctx).User queries with its pooled http.Context.
func CurrentUser(ctx http.Context) (*models.User, error) {
	id, err := facades.Auth(ctx).ID()
	if err != nil {
		return nil, nil
	}
	var user models.User
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", id).First(&user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		return nil, nil
	}

	return &user, nil
}

// Login starts an authenticated session (rotating the session ID and CSRF
// token), active from now.
func Login(ctx http.Context, user *models.User) error {
	if _, err := facades.Auth(ctx).Login(user); err != nil {
		return err
	}
	TouchSession(ctx, carbon.Now().StdTime())

	return nil
}

// Logout ends the session and closes every terminal: there's one admin,
// so they're all theirs.
func Logout(ctx http.Context) error {
	// In the background: a hang-up can take longer than the request may.
	go CloseTerminals("You signed out.")

	return facades.Auth(ctx).Logout()
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
