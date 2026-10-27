// Package update is the hand-off between Echowa and the root updater
// (ADR 0021). Echowa, which can't replace its own binary, asks for an
// update by writing a request file. echowa-update.path sees it and starts
// echowa-update.service, which takes the request, updates, and reports
// progress in a status file that Echowa's Settings page reads.
//
// Both files live in Dir, inside Echowa's data directory, which the echowa
// user owns. So the root side treats everything there as untrusted: it opens
// Dir through its parent and goes through os.Root, which never follows a
// symlink out of either, and accepts only a well-formed version as a
// request.
package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"goravel/app/release"
)

// Dir holds the request and the status. Tests point it elsewhere.
var Dir = "/var/lib/echowa/update"

const (
	requestFile = "request"
	statusFile  = "status.json"
)

// ErrRequested means a request is already waiting for the updater.
var ErrRequested = errors.New("an update has already been requested")

// State is where an update is.
type State string

const (
	Running   State = "running"
	Succeeded State = "succeeded"
	// RolledBack means the new version didn't come up, and the previous
	// one is running again.
	RolledBack State = "rolled_back"
	// Failed means the update stopped before changing anything, or its
	// rollback failed too.
	Failed State = "failed"
)

// Status is the last update's progress or outcome.
type Status struct {
	State      State      `json:"state"`
	From       string     `json:"from"`
	To         string     `json:"to"`
	Message    string     `json:"message"`
	Log        []string   `json:"log"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Request asks the updater for version. Echowa calls it.
func Request(version string) error {
	if !release.IsRelease(version) {
		return fmt.Errorf("%q isn't a release version", version)
	}
	if err := os.MkdirAll(Dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(Dir+"/"+requestFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if errors.Is(err, os.ErrExist) {
		return ErrRequested
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(version + "\n"); err != nil {
		f.Close()
		return err
	}

	return f.Close()
}

// Requested returns the version waiting for the updater, if any.
func Requested() (string, bool) {
	data, err := readLimited(Dir+"/"+requestFile, 64)
	if err != nil {
		return "", false
	}
	version := string(bytes.TrimSpace(data))

	return version, release.IsRelease(version)
}

// Take removes the request and returns the version it asks for. The root
// updater calls it. os.ErrNotExist means there's no request.
func Take() (string, error) {
	root, err := openDir()
	if err != nil {
		return "", err
	}
	defer root.Close()

	f, err := root.OpenFile(requestFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Removed before it's read, so a request can't run twice.
	if err := root.Remove(requestFile); err != nil {
		return "", err
	}
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("the update request isn't a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64))
	if err != nil {
		return "", err
	}
	version := string(bytes.TrimSpace(data))
	if !release.IsRelease(version) {
		return "", fmt.Errorf("the update request %q isn't a release version", version)
	}

	return version, nil
}

// ReadStatus returns the last update's status, or nil if there's none.
func ReadStatus() (*Status, error) {
	data, err := readLimited(Dir+"/"+statusFile, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", statusFile, err)
	}
	if s.Log == nil {
		s.Log = []string{}
	}

	return &s, nil
}

// WriteStatus replaces the status file, by rename so Echowa never reads
// half of it. The root updater calls it.
func WriteStatus(s Status) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	root, err := openDir()
	if err != nil {
		return err
	}
	defer root.Close()

	tmp := fmt.Sprintf("%s.%d.tmp", statusFile, os.Getpid())
	_ = root.Remove(tmp)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return root.Rename(tmp, statusFile)
}

// openDir opens Dir for the root updater. Echowa could put a symlink in
// Dir's place, so it's opened from its parent, which os.Root won't let it
// lead out of.
func openDir() (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(Dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()

	return parent.OpenRoot(filepath.Base(Dir))
}

func readLimited(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return io.ReadAll(io.LimitReader(f, limit))
}
