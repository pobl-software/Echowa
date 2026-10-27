package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useDir(t *testing.T) string {
	t.Helper()
	old := Dir
	Dir = filepath.Join(t.TempDir(), "update")
	t.Cleanup(func() { Dir = old })

	return Dir
}

func TestARequestIsTakenOnce(t *testing.T) {
	useDir(t)
	_, err := Take()
	assert.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, Request("v1.3.0"))
	assert.ErrorIs(t, Request("v1.3.0"), ErrRequested)
	version, ok := Requested()
	assert.True(t, ok)
	assert.Equal(t, "v1.3.0", version)

	version, err = Take()
	require.NoError(t, err)
	assert.Equal(t, "v1.3.0", version)
	_, ok = Requested()
	assert.False(t, ok)
	_, err = Take()
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestTakeRefusesAnythingButAVersion(t *testing.T) {
	dir := useDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	assert.Error(t, Request("v1.3.0; rm -rf /"))

	for _, content := range []string{"v1.3.0 --allow-downgrade", "../../etc/passwd", "", "v1.3.0\nv1.4.0"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, requestFile), []byte(content), 0o600))
		_, err := Take()
		assert.Error(t, err, content)
		assert.NoFileExists(t, filepath.Join(dir, requestFile), "a bad request is removed too")
	}
}

func TestTakeDoesNotFollowSymlinks(t *testing.T) {
	dir := useDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	target := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(target, []byte("v1.3.0"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, requestFile)))

	_, err := Take()
	assert.Error(t, err)
	assert.FileExists(t, target)
}

func TestTheRootSideStaysOutOfASymlinkedDir(t *testing.T) {
	dir := useDir(t)
	elsewhere := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(elsewhere, requestFile), []byte("v1.3.0"), 0o600))
	require.NoError(t, os.Symlink(elsewhere, dir))

	_, err := Take()
	assert.Error(t, err)
	assert.FileExists(t, filepath.Join(elsewhere, requestFile))

	assert.Error(t, WriteStatus(Status{State: Running}))
	assert.NoFileExists(t, filepath.Join(elsewhere, statusFile))
}

func TestStatusRoundTrips(t *testing.T) {
	dir := useDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	s, err := ReadStatus()
	require.NoError(t, err)
	assert.Nil(t, s)

	now := time.Now().UTC().Truncate(time.Second)
	want := Status{State: RolledBack, From: "v1.2.0", To: "v1.3.0", Message: "v1.3.0 didn't start", Log: []string{"one", "two"}, StartedAt: now, FinishedAt: &now}
	require.NoError(t, WriteStatus(want))
	require.NoError(t, WriteStatus(want), "replaces the last one")
	got, err := ReadStatus()
	require.NoError(t, err)
	assert.Equal(t, want, *got)
}

func TestWriteStatusStaysInDir(t *testing.T) {
	dir := useDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	target := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(target, []byte("keep"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, statusFile)))

	require.NoError(t, WriteStatus(Status{State: Running}))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data), "the symlink is replaced, not followed")
}
