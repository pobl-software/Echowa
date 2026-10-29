package backups

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/aws/smithy-go"
)

// Step is one stage of Check. Error is empty when it passed.
type Step struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// Check does what a backup will: writes a small object under prefix,
// reads it back, finds it in a listing and deletes it. It stops at the
// first step that fails, but still tries to delete what it wrote.
func Check(ctx context.Context, store Store, prefix string) []Step {
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	key := JoinKey(prefix, ".echowa-check-"+hex.EncodeToString(suffix))
	body := []byte("Echowa checks it can write here. Safe to delete.\n")

	var steps []Step
	step := func(name string, err error) bool {
		s := Step{Name: name}
		if err != nil {
			s.Error = Describe(err)
		}
		steps = append(steps, s)

		return err == nil
	}

	if !step("Write a test object", store.Put(ctx, key, bytes.NewReader(body))) {
		return steps
	}
	if !step("Read it back", readBack(ctx, store, key, body)) ||
		!step("Find it in a listing", findInListing(ctx, store, key)) {
		cleanUp(ctx, store, key)
		return steps
	}
	step("Delete it", store.Delete(ctx, key))

	return steps
}

// cleanupTimeout bounds the delete after a step ran out of time.
const cleanupTimeout = 10 * time.Second

// cleanUp deletes key after a failed step. A step that ran out of time
// leaves ctx done, so the delete then gets its own time in the
// background rather than hold up the answer.
func cleanUp(ctx context.Context, store Store, key string) {
	if ctx.Err() == nil {
		_ = store.Delete(ctx, key)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = store.Delete(ctx, key)
	}()
}

// Passed says whether every step of a Check passed.
func Passed(steps []Step) bool {
	for _, s := range steps {
		if s.Error != "" {
			return false
		}
	}

	return len(steps) > 0
}

func readBack(ctx context.Context, store Store, key string, want []byte) error {
	r, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("the object read back isn't the one written")
	}

	return nil
}

func findInListing(ctx context.Context, store Store, key string) error {
	listing, err := store.List(ctx, key, "")
	if err != nil {
		return err
	}
	for _, o := range listing.Objects {
		if o.Key == key {
			return nil
		}
	}

	return errors.New("the listing doesn't show the object just written")
}

// JoinKey puts name under prefix, which may be empty.
func JoinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}

	return prefix + "/" + name
}

// Describe words an S3 or network error for the admin: what's wrong with
// the location, rather than the SDK's operation and request IDs.
func Describe(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "InvalidAccessKeyId":
			return "The provider doesn't know this access key ID."
		case "SignatureDoesNotMatch":
			return "The secret key doesn't match the access key ID."
		case "NoSuchBucket":
			return "There's no bucket with this name at this endpoint."
		case "AccessDenied":
			return "These keys aren't allowed to do this in the bucket."
		case "AuthorizationHeaderMalformed", "PermanentRedirect", "IllegalLocationConstraintException":
			return "The region doesn't match the bucket's."
		}
		if msg := apiErr.ErrorMessage(); msg != "" {
			return apiErr.ErrorCode() + ": " + msg
		}

		return apiErr.ErrorCode()
	}

	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "The endpoint didn't answer in time."
	case errors.As(err, &dnsErr):
		return "The endpoint's host " + dnsErr.Name + " doesn't resolve."
	case errors.Is(err, syscall.ECONNREFUSED):
		return "The endpoint refused the connection."
	case errors.As(err, &certErr):
		return "The endpoint's TLS certificate isn't valid: " + certErr.Err.Error()
	}

	return err.Error()
}
