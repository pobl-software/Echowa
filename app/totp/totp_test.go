package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RFC 6238's SHA-1 test vectors, on its 20-byte ASCII key, to six digits.
func TestCodeMatchesRFC6238(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	} {
		got, err := Code(secret, Step(time.Unix(unix, 0)))
		require.NoError(t, err)
		assert.Equal(t, want, got, "at %d", unix)
	}
}

func TestVerify(t *testing.T) {
	secret, err := NewSecret()
	require.NoError(t, err)
	assert.Len(t, secret, 32)
	now := time.Unix(1_700_000_000, 0)
	step := Step(now)
	code := func(s int64) string {
		c, err := Code(secret, s)
		require.NoError(t, err)
		return c
	}

	got, ok := Verify(secret, code(step), now, 0)
	assert.True(t, ok)
	assert.Equal(t, step, got)

	got, ok = Verify(secret, code(step-1), now, 0)
	assert.True(t, ok, "a step behind is accepted")
	assert.Equal(t, step-1, got)
	_, ok = Verify(secret, code(step+1), now, 0)
	assert.True(t, ok, "a step ahead is accepted")
	_, ok = Verify(secret, code(step-2), now, 0)
	assert.False(t, ok, "two steps behind is refused")

	_, ok = Verify(secret, code(step)[:3]+" "+code(step)[3:], now, 0)
	assert.True(t, ok, "a space between the halves is ignored")
	_, ok = Verify(secret, code(step), now, step)
	assert.False(t, ok, "a code already used is refused")
	_, ok = Verify(secret, "", now, 0)
	assert.False(t, ok)
	_, ok = Verify(strings.ToLower(secret), code(step), now, 0)
	assert.True(t, ok, "a secret typed in lower case still works")
}

func TestURI(t *testing.T) {
	assert.Equal(t,
		"otpauth://totp/Echowa:admin@example.test?algorithm=SHA1&digits=6&issuer=Echowa&period=30&secret=JBSWY3DPEHPK3PXP",
		URI("Echowa", "admin@example.test", "JBSWY3DPEHPK3PXP"))
}
