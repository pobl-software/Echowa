package services

import (
	"crypto/subtle"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/goravel/framework/support/path"

	"goravel/app/facades"
	"goravel/app/models"
)

// The first run (ADR 0023): until there's an admin, the UI shows a setup
// page that creates one. Echowa is on the internet as soon as it's
// installed, and its admin controls docker.sock, so the page needs a
// one-time token from the server: `echowa-artisan setup:link` prints a link
// with it, and `echowa setup` prints that link at the end of an install.

var (
	ErrSetupDone       = errors.New("an admin already exists")
	ErrBadSetupToken   = errors.New("that setup link isn't valid")
	firstUserMu        sync.Mutex
	setupTokenFileName = "app/setup-token"
)

func setupTokenFile() string {
	return path.Storage(setupTokenFileName)
}

// NeedsSetup reports whether there's no admin yet.
func NeedsSetup() (bool, error) {
	count, err := facades.Orm().Query().Model(&models.User{}).Count()

	return count == 0, err
}

// SetupToken returns the first run's token, creating it if there isn't one.
func SetupToken() (string, error) {
	if token, err := readSetupToken(); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return token, err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	file := setupTokenFile()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return readSetupToken()
	}
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return "", err
	}

	return token, f.Close()
}

func readSetupToken() (string, error) {
	data, err := os.ReadFile(setupTokenFile())
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("the setup token file is empty: delete " + setupTokenFile())
	}

	return token, nil
}

// ValidSetupToken reports whether token is the first run's. With no token
// made yet, nothing is.
func ValidSetupToken(token string) bool {
	want, err := readSetupToken()

	return err == nil && token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

// ForgetSetupToken removes the token, once there's an admin.
func ForgetSetupToken() error {
	if err := os.Remove(setupTokenFile()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// CreateFirstUser creates the admin from the setup page. Two submissions at
// once can't both get in: there's one Echowa process, and the check and
// the insert happen under one lock.
func CreateFirstUser(token, name, email, password string) (*models.User, error) {
	firstUserMu.Lock()
	defer firstUserMu.Unlock()

	needed, err := NeedsSetup()
	if err != nil {
		return nil, err
	}
	if !needed {
		return nil, ErrSetupDone
	}
	if !ValidSetupToken(token) {
		return nil, ErrBadSetupToken
	}
	user, _, err := SaveUser(strings.TrimSpace(name), email, password)
	if err != nil {
		return nil, err
	}

	return user, ForgetSetupToken()
}
