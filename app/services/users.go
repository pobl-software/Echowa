package services

import (
	"errors"
	"fmt"
	"io/fs"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"goravel/app/facades"
	"goravel/app/models"
)

// Password length limits. bcrypt ignores everything past 72 bytes.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 72
)

// NormalizeEmail is how emails are stored and looked up.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail rejects anything that isn't a bare address.
func ValidateEmail(email string) error {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return errors.New("enter a valid email address")
	}

	return nil
}

// ValidatePassword enforces the length limits.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return errors.New("use at least 8 characters")
	}
	if len(password) > MaxPasswordLength {
		return errors.New("use at most 72 bytes")
	}

	return nil
}

// FindUserByEmail returns the user with this email, or nil.
func FindUserByEmail(email string) (*models.User, error) {
	var user models.User
	if err := facades.Orm().Query().Where("email", NormalizeEmail(email)).First(&user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		return nil, nil
	}

	return &user, nil
}

// SaveUser creates the user with this email, or resets the name and password
// of the existing one. A reset also signs every browser out: it clears the
// "remember me" token and deletes the session files. After a compromise,
// a reset has to lock the intruder out at once.
func SaveUser(name, email, password string) (user *models.User, created bool, err error) {
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return nil, false, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, false, err
	}
	hashed, err := facades.Hash().Make(password)
	if err != nil {
		return nil, false, err
	}

	user, err = FindUserByEmail(email)
	if err != nil {
		return nil, false, err
	}
	if user == nil {
		user = &models.User{Name: name, Email: email, Password: hashed}
		return user, true, facades.Orm().Query().Create(user)
	}

	_, err = facades.Orm().Query().Model(user).Update(map[string]any{
		"name": name, "password": hashed, "remember_token": nil,
	})
	if err != nil {
		return user, false, err
	}

	return user, false, endAllSessions()
}

// endAllSessions deletes every session file. There's one admin, so every
// session is theirs. It needs the server's working directory (the data
// directory in production), where the files are.
func endAllSessions() error {
	dir := facades.Config().GetString("session.files")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sign out sessions: %w", err)
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("sign out sessions: %w", err)
			}
		}
	}

	return nil
}
