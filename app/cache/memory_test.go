package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func clocked() (*Memory, *time.Time) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	m := NewMemory()
	m.now = func() time.Time { return now }

	return m, &now
}

func TestAKeyWrittenAgainLastsFromItsLastWrite(t *testing.T) {
	m, now := clocked()
	assert.NoError(t, m.Put("bucket", "a", time.Minute))
	*now = now.Add(50 * time.Second)
	assert.NoError(t, m.Put("bucket", "b", time.Minute))

	*now = now.Add(50 * time.Second)
	assert.Equal(t, "b", m.Get("bucket"), "the first write's minute is over, the second's isn't")

	*now = now.Add(10 * time.Second)
	assert.False(t, m.Has("bucket"))
	assert.Equal(t, "gone", m.Get("bucket", "gone"))
}

func TestAFailedAddLeavesTheHoldersExpiry(t *testing.T) {
	m, now := clocked()
	assert.True(t, m.Add("lock", 1, time.Minute))
	*now = now.Add(30 * time.Second)
	assert.False(t, m.Add("lock", 1, time.Second))

	*now = now.Add(20 * time.Second)
	assert.True(t, m.Has("lock"))
	*now = now.Add(10 * time.Second)
	assert.True(t, m.Add("lock", 1, time.Minute), "taken again once it expires")
}

func TestALockIsHeldUntilReleased(t *testing.T) {
	m, _ := clocked()
	held := m.Lock("k", time.Minute)
	assert.True(t, held.Get())
	assert.False(t, m.Lock("k", time.Minute).Get())
	assert.True(t, held.Release())
	assert.True(t, m.Lock("k", time.Minute).Get())
}

func TestForeverAndIncrement(t *testing.T) {
	m, now := clocked()
	assert.True(t, m.Forever("k", "v"))
	n, err := m.Increment("count", 2)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), n)
	n, _ = m.Decrement("count")
	assert.Equal(t, int64(1), n)

	*now = now.Add(24 * time.Hour)
	assert.Equal(t, "v", m.GetString("k"))
	assert.Equal(t, 1, m.GetInt("count"))
	_, err = m.Increment("k")
	assert.Error(t, err)
}

func TestWritesDropExpiredEntries(t *testing.T) {
	m, now := clocked()
	assert.NoError(t, m.Put("old", 1, time.Second))
	*now = now.Add(2 * time.Minute)
	assert.NoError(t, m.Put("new", 1, time.Second))
	assert.NotContains(t, m.entries, "old")
}
