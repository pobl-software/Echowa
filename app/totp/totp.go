// Package totp is RFC 6238 time-based one-time passwords, as authenticator
// apps make them: HMAC-SHA1, six digits, a 30-second step (ADR 0042).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Period is how long a code lasts.
	Period = 30 * time.Second
	// Digits is how long a code is.
	Digits = 6
	// skew is how many steps either side of now a code may come from, for
	// a phone's clock that's a little off and a code typed as it changed.
	skew = 1
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret is 160 random bits in base32, RFC 4226's recommended length,
// the form authenticator apps take when it's typed in.
func NewSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return encoding.EncodeToString(b), nil
}

// Step is the time step t falls in.
func Step(t time.Time) int64 {
	return t.Unix() / int64(Period/time.Second)
}

// Code is the code for secret at step.
func Code(secret string, step int64) (string, error) {
	key, err := encoding.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("totp secret: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:]) & 0x7fffffff

	return fmt.Sprintf("%0*d", Digits, value%1_000_000), nil
}

// Verify returns the step code was made for, if it's a code for secret
// within a step of now and later than after: a code is accepted once, so
// one seen over someone's shoulder can't be used again. Spaces in code
// are ignored, as apps show it in two halves.
func Verify(secret, code string, now time.Time, after int64) (int64, bool) {
	code = strings.ReplaceAll(code, " ", "")
	if len(code) != Digits {
		return 0, false
	}
	current := Step(now)
	for step := current - skew; step <= current+skew; step++ {
		if step <= after {
			continue
		}
		want, err := Code(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}

	return 0, false
}

// URI is the otpauth:// link a QR code carries, in the form Google
// Authenticator documents and every app reads.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(Digits))
	query.Set("period", fmt.Sprint(int(Period/time.Second)))

	return "otpauth://totp/" + label + "?" + query.Encode()
}
