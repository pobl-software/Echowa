package backups

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPassesAndLeavesNothingBehind(t *testing.T) {
	store := NewFake()

	steps := Check(context.Background(), store, "server-1")

	require.Len(t, steps, 4)
	for _, s := range steps {
		assert.Empty(t, s.Error, s.Name)
	}
	assert.True(t, Passed(steps))
	assert.Empty(t, store.Keys())
}

func TestCheckStopsAtTheFirstFailure(t *testing.T) {
	store := NewFake()
	store.Err = &smithy.GenericAPIError{Code: "SignatureDoesNotMatch", Message: "The request signature we calculated does not match"}

	steps := Check(context.Background(), store, "")

	require.Len(t, steps, 1)
	assert.Equal(t, "Write a test object", steps[0].Name)
	assert.Equal(t, "The secret key doesn't match the access key ID.", steps[0].Error)
	assert.False(t, Passed(steps))
}

// listless writes and reads but never lists, like keys without
// s3:ListBucket.
type listless struct{ *Fake }

func (l listless) List(context.Context, string, string) (Listing, error) {
	return Listing{}, &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"}
}

func TestCheckCleansUpWhenALaterStepFails(t *testing.T) {
	store := listless{NewFake()}

	steps := Check(context.Background(), store, "server-1")

	require.Len(t, steps, 3)
	assert.Empty(t, steps[1].Error)
	assert.Equal(t, "These keys aren't allowed to do this in the bucket.", steps[2].Error)
	assert.Empty(t, store.Keys(), "the test object is deleted after a failed step")
}

func TestDescribe(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&smithy.GenericAPIError{Code: "NoSuchBucket"}, "There's no bucket with this name at this endpoint."},
		{&smithy.GenericAPIError{Code: "AuthorizationHeaderMalformed"}, "The region doesn't match the bucket's."},
		{&smithy.GenericAPIError{Code: "SlowDown", Message: "Reduce your request rate."}, "SlowDown: Reduce your request rate."},
		{fmt.Errorf("send: %w", context.DeadlineExceeded), "The endpoint didn't answer in time."},
		{fmt.Errorf("dial: %w", &net.DNSError{Name: "nope.example", IsNotFound: true}), "The endpoint's host nope.example doesn't resolve."},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), "The endpoint refused the connection."},
		{errors.New("something else"), "something else"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, Describe(tt.err))
	}
}

func TestJoinKey(t *testing.T) {
	assert.Equal(t, "a", JoinKey("", "a"))
	assert.Equal(t, "p/a", JoinKey("p", "a"))
}
