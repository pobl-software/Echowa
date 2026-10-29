package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

// pg_restore commits at the archive's end without reading on to EOF, so
// a dump with more appended mustn't let that end out before the check.
func TestCheckedDumpHoldsBackTheEndOfADumpWithMoreAppended(t *testing.T) {
	dump := bytes.Repeat([]byte("d"), 3*checkTail)
	sum := sha256.Sum256(dump)
	appended := append(slices.Clone(dump), bytes.Repeat([]byte("x"), 2*checkTail)...)
	c := &checkedDump{r: bytes.NewReader(appended), size: int64(len(dump)), sha256: hex.EncodeToString(sum[:]), h: sha256.New()}

	got, err := io.ReadAll(c)

	assert.ErrorIs(t, err, errDumpMismatch)
	assert.LessOrEqual(t, len(got), len(dump)-checkTail, "the dump's last checkTail bytes never went out")
	assert.False(t, c.done.Load())
}
