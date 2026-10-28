// Package backups is the S3 storage that database backups go to (ADR
// 0034). Everything reaches a bucket through Store, so tests swap Open for
// a Fake and never touch the network.
package backups

import (
	"context"
	"errors"
	"io"
	"time"

	"goravel/app/models"
)

// ErrNotFound is Get's error for a key with no object.
var ErrNotFound = errors.New("no such object")

// Store is one storage location's bucket. Keys are whole keys, prefix
// included.
type Store interface {
	// Put uploads r, whose size isn't known, in parts. An error from r
	// aborts the upload, so no object appears.
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// List returns every object under prefix. With a delimiter, keys that
	// go on past it are grouped into Prefixes instead.
	List(ctx context.Context, prefix, delimiter string) (Listing, error)
	// Delete removes the object, or does nothing if there's none.
	Delete(ctx context.Context, key string) error
}

type Object struct {
	Key      string
	Size     int64
	Modified time.Time
}

type Listing struct {
	Objects  []Object
	Prefixes []string
}

// Open returns the location's bucket. Tests replace it.
var Open = func(loc *models.StorageLocation) Store {
	return newS3Store(loc)
}
