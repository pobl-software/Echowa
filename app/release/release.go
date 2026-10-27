// Package release is what Echowa knows about its own releases (ADR 0021):
// its version, the channel releases are published to, and how to tell a
// genuine release from anything else.
package release

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aead.dev/minisign"
)

var (
	// Version is set by the release build: "v1.3.0" for a published
	// release, anything else (`git describe`, "dev") for a dev build.
	Version = "dev"
	// URL is the release channel, e.g. "https://get.example.com", set by
	// the release build. ECHOWA_RELEASE_URL overrides it.
	URL = ""
)

// The release signing key's public half. `make release-key` writes it.
//
//go:embed minisign.pub
var publicKeyText []byte

var (
	ErrNoKey     = errors.New("this build has no release key (run `make release-key`)")
	ErrNoChannel = errors.New("this build has no release URL (set RELEASE_URL for make, or ECHOWA_RELEASE_URL)")
	ErrSignature = errors.New("the signature doesn't match the release key")
)

var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// IsRelease reports whether v is a release version, "vMAJOR.MINOR.PATCH".
func IsRelease(v string) bool {
	return releaseVersion.MatchString(v)
}

// Newer reports whether release version a comes after b. Anything that
// isn't a release version is never newer.
func Newer(a, b string) bool {
	pa, pb := releaseVersion.FindStringSubmatch(a), releaseVersion.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return pa != nil && pb == nil
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}

	return false
}

// Channel is the release channel's base URL, without a trailing slash.
func Channel() (string, error) {
	url := URL
	if env := os.Getenv("ECHOWA_RELEASE_URL"); env != "" {
		url = env
	}
	url = strings.TrimRight(url, "/")
	if url == "" {
		return "", ErrNoChannel
	}

	return url, nil
}

// PublicKey is the key every release is signed with.
func PublicKey() (minisign.PublicKey, error) {
	var key minisign.PublicKey
	if err := key.UnmarshalText(publicKeyText); err != nil {
		return key, ErrNoKey
	}

	return key, nil
}

// TrustedComment is what `make publish` signs into each file's signature:
// the version and the file's name. Checking it means a genuine signature
// can't vouch for another file, or for the same file in another release.
func TrustedComment(version, name string) string {
	return "echowa " + version + " " + name
}

// Verify checks that signature is the release key's signature of data,
// signed for the file name in release version.
func Verify(key minisign.PublicKey, data, signature []byte, version, name string) error {
	var sig minisign.Signature
	if err := sig.UnmarshalText(signature); err != nil {
		return fmt.Errorf("%s: %w", name, ErrSignature)
	}
	if !minisign.Verify(key, data, signature) {
		return fmt.Errorf("%s: %w", name, ErrSignature)
	}
	if want := TrustedComment(version, name); sig.TrustedComment != want {
		return fmt.Errorf("%s: signed as %q, not %q: %w", name, sig.TrustedComment, want, ErrSignature)
	}

	return nil
}

// Binary is one platform's build in a release.
type Binary struct {
	Path   string `json:"path"`   // relative to the channel URL
	SHA256 string `json:"sha256"` // hex
}

// Manifest is latest.json: the newest release.
type Manifest struct {
	Version     string            `json:"version"`
	PublishedAt time.Time         `json:"published_at"`
	Notes       string            `json:"notes"` // path of the release notes
	Binaries    map[string]Binary `json:"binaries"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validate checks the manifest is well formed. Its signature says who
// wrote it; this catches a release script that wrote it wrong.
func (m Manifest) validate() error {
	if !IsRelease(m.Version) {
		return fmt.Errorf("latest.json: %q isn't a release version", m.Version)
	}
	prefix := "releases/" + m.Version + "/"
	if m.Notes != "" && !strings.HasPrefix(m.Notes, prefix) {
		return fmt.Errorf("latest.json: notes outside %s", prefix)
	}
	if len(m.Binaries) == 0 {
		return errors.New("latest.json: no binaries")
	}
	for platform, b := range m.Binaries {
		if !strings.HasPrefix(b.Path, prefix) || strings.Contains(b.Path, "..") {
			return fmt.Errorf("latest.json: %s binary outside %s", platform, prefix)
		}
		if !sha256Hex.MatchString(b.SHA256) {
			return fmt.Errorf("latest.json: %s binary has no SHA-256", platform)
		}
	}

	return nil
}

// Latest fetches the channel's latest.json and returns it once its
// signature checks out.
func Latest(ctx context.Context, client *nethttp.Client, channel string, key minisign.PublicKey) (Manifest, error) {
	data, err := fetch(ctx, client, channel+"/latest.json", 64<<10)
	if err != nil {
		return Manifest{}, err
	}
	signature, err := fetch(ctx, client, channel+"/latest.json.minisig", 4<<10)
	if err != nil {
		return Manifest{}, err
	}

	// The version inside is only trusted once the signature, which names
	// it, checks out.
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("latest.json: %w", err)
	}
	if err := Verify(key, data, signature, m.Version, "latest.json"); err != nil {
		return Manifest{}, err
	}

	return m, m.validate()
}

// Download fetches the release file at name (relative to the channel) and
// its signature, and returns it once the signature checks out for version.
// A non-empty sha256 must match too.
func Download(ctx context.Context, client *nethttp.Client, channel string, key minisign.PublicKey, version, name, sha256Hex string, limit int64) ([]byte, error) {
	data, err := fetch(ctx, client, channel+"/"+name, limit)
	if err != nil {
		return nil, err
	}
	signature, err := fetch(ctx, client, channel+"/"+name+".minisig", 4<<10)
	if err != nil {
		return nil, err
	}
	if err := Verify(key, data, signature, version, path.Base(name)); err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); sha256Hex != "" && hex.EncodeToString(sum[:]) != sha256Hex {
		return nil, fmt.Errorf("%s doesn't match latest.json's SHA-256", name)
	}

	return data, nil
}

// fetch GETs url, refusing bodies over limit bytes.
func fetch(ctx context.Context, client *nethttp.Client, url string, limit int64) ([]byte, error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: more than %d bytes", url, limit)
	}

	return data, nil
}
