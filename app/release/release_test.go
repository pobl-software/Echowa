package release

import (
	"context"
	"crypto/rand"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aead.dev/minisign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsReleaseAndNewer(t *testing.T) {
	assert.True(t, IsRelease("v1.3.0"))
	assert.True(t, IsRelease("v0.0.1"))
	for _, v := range []string{"dev", "1.3.0", "v1.3", "v01.3.0", "v1.3.0-rc1", "5322fcd-dirty", "v1.3.0\n"} {
		assert.False(t, IsRelease(v), v)
	}

	assert.True(t, Newer("v1.3.0", "v1.2.9"))
	assert.True(t, Newer("v1.10.0", "v1.9.0"), "numeric, not string, order")
	assert.True(t, Newer("v2.0.0", "v1.99.99"))
	assert.False(t, Newer("v1.3.0", "v1.3.0"))
	assert.False(t, Newer("v1.2.0", "v1.3.0"))
	assert.True(t, Newer("v0.1.0", "dev"), "any release is newer than a dev build")
	assert.False(t, Newer("dev", "v0.1.0"))
}

func testKey(t *testing.T) (minisign.PublicKey, minisign.PrivateKey) {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	require.NoError(t, err)

	return pub, priv
}

func sign(priv minisign.PrivateKey, data []byte, version, name string) []byte {
	return minisign.SignWithComments(priv, data, TrustedComment(version, name), "signature from the test key")
}

func TestVerify(t *testing.T) {
	pub, priv := testKey(t)
	other, _ := testKey(t)
	data := []byte("binary")
	sig := sign(priv, data, "v1.3.0", "echowa-linux-amd64")

	assert.NoError(t, Verify(pub, data, sig, "v1.3.0", "echowa-linux-amd64"))
	assert.ErrorIs(t, Verify(pub, []byte("tampered"), sig, "v1.3.0", "echowa-linux-amd64"), ErrSignature)
	assert.ErrorIs(t, Verify(other, data, sig, "v1.3.0", "echowa-linux-amd64"), ErrSignature, "another key")
	assert.ErrorIs(t, Verify(pub, data, sig, "v1.4.0", "echowa-linux-amd64"), ErrSignature, "signed for another release")
	assert.ErrorIs(t, Verify(pub, data, sig, "v1.3.0", "echowa-linux-arm64"), ErrSignature, "signed for another file")
	assert.ErrorIs(t, Verify(pub, data, []byte("garbage"), "v1.3.0", "echowa-linux-amd64"), ErrSignature)
}

// make publish signs with the minisign CLI, which prehashes by default.
func TestVerifiesSignaturesFromTheMinisignCLI(t *testing.T) {
	if _, err := exec.LookPath("minisign"); err != nil {
		t.Skip("minisign isn't installed")
	}
	dir := t.TempDir()
	pubFile, secFile, file := filepath.Join(dir, "test.pub"), filepath.Join(dir, "test.key"), filepath.Join(dir, "latest.json")
	run := func(args ...string) {
		out, err := exec.Command("minisign", args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("-G", "-W", "-p", pubFile, "-s", secFile)
	require.NoError(t, os.WriteFile(file, []byte(`{"version":"v1.3.0"}`), 0o600))
	run("-S", "-s", secFile, "-m", file, "-t", TrustedComment("v1.3.0", "latest.json"))

	pub, err := minisign.PublicKeyFromFile(pubFile)
	require.NoError(t, err)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	sig, err := os.ReadFile(file + ".minisig")
	require.NoError(t, err)

	assert.NoError(t, Verify(pub, data, sig, "v1.3.0", "latest.json"))
}

func TestTheEmbeddedKeyParsesOrSaysThereIsNone(t *testing.T) {
	_, err := PublicKey()
	if err != nil {
		assert.ErrorIs(t, err, ErrNoKey)
	}
}

// channel serves files like the R2 bucket.
func channel(t *testing.T, files map[string][]byte) string {
	t.Helper()
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		data, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			nethttp.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func manifest(t *testing.T, m Manifest) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	require.NoError(t, err)

	return data
}

func goodManifest() Manifest {
	return Manifest{
		Version:     "v1.3.0",
		PublishedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		Notes:       "releases/v1.3.0/notes.md",
		Binaries: map[string]Binary{
			"linux/amd64": {Path: "releases/v1.3.0/echowa-linux-amd64", SHA256: strings.Repeat("a", 64)},
		},
	}
}

func TestLatest(t *testing.T) {
	pub, priv := testKey(t)
	ctx := context.Background()
	client := &nethttp.Client{Timeout: 5 * time.Second}
	published := func(data, sig []byte) string {
		return channel(t, map[string][]byte{"latest.json": data, "latest.json.minisig": sig})
	}

	data := manifest(t, goodManifest())
	got, err := Latest(ctx, client, published(data, sign(priv, data, "v1.3.0", "latest.json")), pub)
	require.NoError(t, err)
	assert.Equal(t, goodManifest(), got)

	t.Run("a version changed after signing", func(t *testing.T) {
		m := goodManifest()
		m.Version = "v9.9.9"
		_, err := Latest(ctx, client, published(manifest(t, m), sign(priv, data, "v1.3.0", "latest.json")), pub)
		assert.ErrorIs(t, err, ErrSignature)
	})

	t.Run("another release's genuine latest.json, relabelled", func(t *testing.T) {
		m := goodManifest()
		m.Version = "v1.2.0"
		old := manifest(t, m)
		_, err := Latest(ctx, client, published(old, sign(priv, old, "v1.3.0", "latest.json")), pub)
		assert.ErrorIs(t, err, ErrSignature)
	})

	t.Run("signed but pointing outside its release", func(t *testing.T) {
		m := goodManifest()
		m.Binaries["linux/amd64"] = Binary{Path: "releases/v1.3.0/../v0.1.0/echowa-linux-amd64", SHA256: strings.Repeat("a", 64)}
		bad := manifest(t, m)
		_, err := Latest(ctx, client, published(bad, sign(priv, bad, "v1.3.0", "latest.json")), pub)
		assert.ErrorContains(t, err, "outside releases/v1.3.0/")
	})

	t.Run("no signature", func(t *testing.T) {
		_, err := Latest(ctx, client, channel(t, map[string][]byte{"latest.json": data}), pub)
		assert.ErrorContains(t, err, "404")
	})

	t.Run("an endless body", func(t *testing.T) {
		huge := []byte(strings.Repeat(" ", 65<<10))
		_, err := Latest(ctx, client, published(huge, nil), pub)
		assert.ErrorContains(t, err, "more than")
	})
}

func TestDownload(t *testing.T) {
	pub, priv := testKey(t)
	ctx := context.Background()
	client := &nethttp.Client{Timeout: 5 * time.Second}
	name := "releases/v1.3.0/echowa-linux-amd64"
	data := []byte("the binary")
	sum := "be7a2e2a0e1ac54e9a5d10b0b1c1b7a7ce49f1d41c5df4cf4b4d1b1d2a2e0e45"
	serve := func(sig []byte) string {
		return channel(t, map[string][]byte{name: data, name + ".minisig": sig})
	}

	got, err := Download(ctx, client, serve(sign(priv, data, "v1.3.0", "echowa-linux-amd64")), pub, "v1.3.0", name, "", 1<<20)
	require.NoError(t, err)
	assert.Equal(t, data, got)

	_, err = Download(ctx, client, serve(sign(priv, data, "v1.3.0", "echowa-linux-arm64")), pub, "v1.3.0", name, "", 1<<20)
	assert.ErrorIs(t, err, ErrSignature, "signed for another file")

	_, err = Download(ctx, client, serve(sign(priv, data, "v1.3.0", "echowa-linux-amd64")), pub, "v1.3.0", name, sum, 1<<20)
	assert.ErrorContains(t, err, "SHA-256", "genuine but not the build latest.json names")

	_, err = Download(ctx, client, serve(sign(priv, data, "v1.3.0", "echowa-linux-amd64")), pub, "v1.3.0", name, "", 4)
	assert.ErrorContains(t, err, "more than")
}
