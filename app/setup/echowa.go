package setup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"goravel/app/release"
	"goravel/app/update"
)

// user creates the echowa system user. docker.sock is root-equivalent, and
// membership of the docker group is all the privilege Echowa gets.
func (s *steps) user() error {
	s.ui.begin("User")
	if _, err := s.output("getent", "passwd", User); err != nil {
		s.ui.doing("creating %s", User)
		group := "--user-group"
		if _, err := s.output("getent", "group", User); err == nil {
			group = "--gid=" + User
		}
		if err := s.quiet(nil, "useradd", "--system", group, "--home-dir", DataDir, "--no-create-home", "--shell", "/usr/sbin/nologin", User); err != nil {
			return err
		}
	}
	if err := s.quiet(nil, "usermod", "-aG", "docker", User); err != nil {
		return err
	}
	s.ui.ok("%s, in the docker group", User)

	return nil
}

// config creates the data directory and /etc/echowa/env, once: an existing
// env file is never overwritten.
func (s *steps) config() error {
	s.ui.begin("Config")
	if err := s.dir(DataDir, 0o700, User+":"+User); err != nil {
		return err
	}
	if err := s.dir(EnvDir, 0o750, "root:"+User); err != nil {
		return err
	}
	// As echowa, which owns the data directory and so could have put a
	// symlink where update.Dir goes: root would chown its target.
	if err := s.quiet(nil, "runuser", "-u", User, "--", "install", "-d", "-m", "0700", update.Dir); err != nil {
		return err
	}

	existing, err := os.ReadFile(EnvFile)
	switch {
	case err == nil:
		s.ui.ok("%s, kept", EnvFile)
		if len(parseEnv(string(existing))["APP_KEY"]) != 32 {
			return fmt.Errorf("APP_KEY in %s must be 32 characters", EnvFile)
		}
	case errors.Is(err, os.ErrNotExist):
		template, err := files.ReadFile("files/echowa.env")
		if err != nil {
			return err
		}
		key, err := newAppKey()
		if err != nil {
			return err
		}
		values, err := s.envFor()
		if err != nil {
			return err
		}
		values["APP_KEY"] = key
		existing = []byte(renderEnv(string(template), values))
		if err := writeAtomic(EnvFile, existing, 0o640); err != nil {
			return err
		}
		if err := s.quiet(nil, "chown", "root:"+User, EnvFile); err != nil {
			return err
		}
		s.ui.ok("%s, with a new APP_KEY", EnvFile)
	default:
		return err
	}

	env := parseEnv(string(existing))
	s.url, s.host, s.port = env["APP_URL"], env["APP_HOST"], env["APP_PORT"]
	if s.host == "" || s.host == "0.0.0.0" || s.host == "::" {
		s.host = "127.0.0.1"
	}
	if s.port == "" {
		s.port = innerPort
	}

	return nil
}

func (s *steps) dir(path string, mode os.FileMode, owner string) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}

	return s.quiet(nil, "chown", owner, path)
}

// binary installs this binary as /usr/local/bin/echowa. On an upgrade it
// backs the database up first, with the new binary (the old one may predate
// `echowa backup`). Run as the installed binary, as the updater does after
// swapping it in, there's nothing to copy.
func (s *steps) binary() error {
	s.ui.begin("Binary")
	s.upgrading = s.command("systemctl", "is-active", "--quiet", "echowa").Run() == nil

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	if self != Bin {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(Bin+".new", data, 0o755); err != nil {
			return err
		}
		if s.upgrading {
			s.ui.doing("backing up the database first")
			if _, err := s.backupDatabase(Bin+".new", release.Version); err != nil {
				return err
			}
		}
		if err := swapIn(Bin + ".new"); err != nil {
			return err
		}
	}
	if err := writeFile("files/echowa-artisan", Artisan, 0o755); err != nil {
		return err
	}
	s.ui.ok("%s %s", Bin, release.Version)

	return nil
}

// swapIn renames staged over /usr/local/bin/echowa, keeping the old binary
// as echowa.previous. A running Echowa keeps the old file until it
// restarts.
func swapIn(staged string) error {
	if _, err := os.Stat(Bin); err == nil {
		_ = os.Remove(Bin + ".previous")
		if err := os.Link(Bin, Bin+".previous"); err != nil {
			return err
		}
	}

	return os.Rename(staged, Bin)
}

// backupDatabase copies the database to Backups with bin, before moving to
// version, and returns the copy's path: "" when there's no database yet.
func (s *steps) backupDatabase(bin, version string) (string, error) {
	if _, err := os.Stat(Database); err != nil {
		return "", nil
	}
	if err := s.dir(Backups, 0o700, User+":"+User); err != nil {
		return "", err
	}
	dest := filepath.Join(Backups, fmt.Sprintf("echowa-before-%s-%s.sqlite", version, time.Now().UTC().Format("20060102T150405Z")))
	// As echowa, in its data directory, with its env: like echowa-artisan,
	// so the database's WAL files keep their owner.
	script := `umask 077 && cd "$1" && set -a && . "$2" && set +a && exec "$3" backup "$4"`
	var out bytes.Buffer
	cmd := s.command("runuser", "-u", User, "--", "/bin/sh", "-c", script, "sh", DataDir, EnvFile, bin, dest)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("back up the database: %w\n%s", err, out.String())
	}
	s.ui.say("Backed up the database to %s", dest)

	return dest, nil
}

// restoreScript puts "$1" back as the database "$2", whose write-ahead log
// belongs to the database being replaced: left there, SQLite would replay
// it into the restored copy.
const restoreScript = `umask 077 && cp -- "$1" "$2.restore" && rm -f -- "$2-wal" "$2-shm" && mv -f -- "$2.restore" "$2"`

// restoreDatabase puts backup back as the database. Echowa must be stopped.
// It runs as echowa: the backup and the data directory are echowa's, so a
// symlink there gets no more than echowa could do itself.
func (s *steps) restoreDatabase(backup string) error {
	if err := s.quiet(nil, "runuser", "-u", User, "--", "/bin/sh", "-c", restoreScript, "sh", backup, Database); err != nil {
		return fmt.Errorf("restore the database: %w", err)
	}
	s.ui.say("Restored the database from %s", backup)

	return nil
}

// parseOSRelease reads /etc/os-release (or an env file): KEY=value lines,
// values optionally quoted.
func parseOSRelease(text string) map[string]string {
	values := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}

	return values
}

// parseEnv reads /etc/echowa/env, which has the same shape.
func parseEnv(text string) map[string]string {
	return parseOSRelease(text)
}

// renderEnv sets values in the env template: each replaces its KEY= line,
// or a commented-out #KEY= line, or else is added at the end.
func renderEnv(template string, values map[string]string) string {
	lines := strings.Split(strings.TrimRight(template, "\n"), "\n")
	done := map[string]bool{}
	for i, line := range lines {
		key, _, ok := strings.Cut(strings.TrimPrefix(line, "#"), "=")
		if value, set := values[key]; ok && set && !done[key] {
			lines[i] = key + "=" + value
			done[key] = true
		}
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if !done[key] {
			lines = append(lines, key+"="+values[key])
		}
	}

	return strings.Join(lines, "\n") + "\n"
}

// newAppKey is 32 random letters and digits: Goravel's AES-256 key.
func newAppKey() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	key := make([]byte, 32)
	for i := range key {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		key[i] = alphabet[n.Int64()]
	}

	return string(key), nil
}
