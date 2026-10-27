package docker

import (
	"fmt"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
)

func TestTailBufferKeepsEverythingUnderItsLimit(t *testing.T) {
	b := &TailBuffer{Limit: 10}
	fmt.Fprint(b, "one\n")
	fmt.Fprint(b, "two\n")

	assert.Equal(t, "one\ntwo\n", b.String())
}

func TestTailBufferKeepsTheEndAndSaysWhatItDropped(t *testing.T) {
	b := &TailBuffer{Limit: 8}
	for i := range 100 {
		fmt.Fprintf(b, "%03d\n", i)
	}

	assert.Equal(t, "[392 earlier bytes dropped]\n098\n099\n", b.String())
	assert.LessOrEqual(t, len(b.buf), 2*8+4, "memory stays near twice the limit")
}

func TestTailBufferTakesAWriteLargerThanItsLimit(t *testing.T) {
	b := &TailBuffer{Limit: 4}
	n, err := b.Write([]byte("abcdefghij"))

	assert.NoError(t, err)
	assert.Equal(t, 10, n, "a write is never short, so the command's output isn't cut off")
	assert.Equal(t, "[6 earlier bytes dropped]\nghij", b.String())
}

func TestTailBufferStartsAtAWholeCharacter(t *testing.T) {
	b := &TailBuffer{Limit: 5}
	fmt.Fprint(b, "aé€b") // a, 2 bytes, 3 bytes, b: the last 5 bytes start inside é

	assert.Equal(t, "[3 earlier bytes dropped]\n€b", b.String())
}

func TestTailBufferReplacesInvalidUTF8(t *testing.T) {
	b := &TailBuffer{Limit: 10}
	b.Write([]byte{'o', 'k', 0xff, '\n'})

	assert.Equal(t, "ok�\n", b.String())
}

func TestNewestContainerIsTheLastCreated(t *testing.T) {
	items := []container.Summary{
		{ID: "old", Created: 100},
		{ID: "new", Created: 160},
		{ID: "older", Created: 40},
	}

	assert.Equal(t, "new", newestContainer(items))
}
