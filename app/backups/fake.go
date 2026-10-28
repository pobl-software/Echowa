package backups

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
)

// Fake is a bucket in memory. Set Err to make every call fail with it, as
// a wrong key or a missing bucket would.
type Fake struct {
	mu      sync.Mutex
	objects map[string][]byte
	Err     error
}

func NewFake() *Fake {
	return &Fake{objects: map[string][]byte{}}
}

// Keys lists the stored keys, sorted.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}

func (f *Fake) Put(ctx context.Context, key string, r io.Reader) error {
	if f.Err != nil {
		return f.Err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = b

	return nil
}

func (f *Fake) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *Fake) List(ctx context.Context, prefix, delimiter string) (Listing, error) {
	if f.Err != nil {
		return Listing{}, f.Err
	}
	listing := Listing{Objects: []Object{}, Prefixes: []string{}}
	for _, key := range f.Keys() {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		if i := strings.Index(rest, delimiter); delimiter != "" && i >= 0 {
			if p := prefix + rest[:i+len(delimiter)]; !slices.Contains(listing.Prefixes, p) {
				listing.Prefixes = append(listing.Prefixes, p)
			}
			continue
		}
		f.mu.Lock()
		size := int64(len(f.objects[key]))
		f.mu.Unlock()
		listing.Objects = append(listing.Objects, Object{Key: key, Size: size, Modified: time.Now()})
	}

	return listing, nil
}

func (f *Fake) Delete(ctx context.Context, key string) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)

	return nil
}
