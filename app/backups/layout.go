package backups

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// keyTime is the backup's start in a key: 20261027T221734Z.
const keyTime = "20060102T150405Z"

// Key is where a backup's dump goes:
// <prefix>/<app>/<app>-20261027T221734Z-42.<ext>, with the backup's start
// in UTC and its record's ID. An app's backups sort by time, and the name
// alone says what it is after a download.
func Key(prefix, app string, started time.Time, id uint, ext string) string {
	return JoinKey(prefix, fmt.Sprintf("%s/%s-%s-%d.%s", app, app, started.UTC().Format(keyTime), id, ext))
}

// ManifestKey is the manifest's key next to the dump at key. App names
// and the rest of a dump's name have no dots, so its extension, which may
// have two ("sql.gz"), starts at the name's first dot.
func ManifestKey(key string) string {
	dir, name := path.Split(key)
	name, _, _ = strings.Cut(name, ".")

	return dir + name + ".json"
}

// ManifestFormat is the version of Manifest that Echowa writes.
const ManifestFormat = 1

// Manifest describes a dump, so the bucket makes sense without Echowa's
// database (ADR 0034). It's written after the dump: a dump without one is
// incomplete.
type Manifest struct {
	Format        int    `json:"format"`
	App           string `json:"app"`
	Template      string `json:"template"`
	ServerVersion string `json:"server_version"`
	Database      string `json:"database"`
	// Object is the dump's name, next to the manifest.
	Object string `json:"object"`
	// Compression is "gzip" when Echowa compressed the dump, or "none".
	Compression string    `json:"compression"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	// EchowaVersion is the release that wrote it.
	EchowaVersion string `json:"echowa_version"`
}

// ParseManifest reads a manifest, refusing a format this release doesn't
// know or a dump outside the manifest's folder.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}
	if m.Format != ManifestFormat {
		return Manifest{}, fmt.Errorf("the manifest has format %d, and this release of Echowa reads format %d", m.Format, ManifestFormat)
	}
	if m.Object == "" || strings.ContainsAny(m.Object, "/\\") || m.Object == "." || m.Object == ".." {
		return Manifest{}, errors.New("the manifest names no dump next to it")
	}

	return m, nil
}
