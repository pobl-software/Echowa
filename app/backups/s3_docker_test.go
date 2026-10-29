//go:build docker

// Integration test against real S3: the dev machine's S3 server
// (deploy/s3), which deploy/dev-vm.sh up starts, and, when .env.s3-test
// at the repo root (or the environment) names a bucket, that one too.
// Run with deploy/dev-vm.sh test-docker, or on the Mac with the dev
// server's port forwarded:
//
//	go test -tags docker ./app/backups/...
//
// Each test writes under a prefix of its own and deletes what it wrote.
package backups

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/models"
)

// target is a bucket the tests run against. local is the dev machine's
// server, where a closed port can be tried.
type target struct {
	name  string
	loc   models.StorageLocation
	local bool
}

func targets(t *testing.T) []target {
	list := []target{{
		name:  "dev",
		local: true,
		loc: models.StorageLocation{
			Endpoint: "http://localhost:7070", Region: "us-east-1", Bucket: "echowa-dev",
			AccessKeyID: "echowa-dev", SecretKey: "echowa-dev-secret", PathStyle: true,
		},
	}}

	env := testEnv(t)
	if env["ECHOWA_TEST_S3_BUCKET"] != "" {
		list = append(list, target{name: "external", loc: models.StorageLocation{
			Endpoint:    env["ECHOWA_TEST_S3_ENDPOINT"],
			Region:      env["ECHOWA_TEST_S3_REGION"],
			Bucket:      env["ECHOWA_TEST_S3_BUCKET"],
			Prefix:      strings.Trim(env["ECHOWA_TEST_S3_PREFIX"], "/"),
			AccessKeyID: env["ECHOWA_TEST_S3_ACCESS_KEY_ID"],
			SecretKey:   models.Secret(env["ECHOWA_TEST_S3_SECRET_ACCESS_KEY"]),
			PathStyle:   env["ECHOWA_TEST_S3_PATH_STYLE"] == "true",
		}})
	}

	return list
}

// testEnv reads ECHOWA_TEST_S3_* from .env.s3-test at the repo root, with
// the environment winning. The file is read here rather than passed in,
// so the secret never goes on a command line.
func testEnv(t *testing.T) map[string]string {
	env := map[string]string{}
	if f, err := os.Open(filepath.Join(repoRoot(t), ".env.s3-test")); err == nil {
		defer func() { _ = f.Close() }()
		lines := bufio.NewScanner(f)
		for lines.Scan() {
			line := strings.TrimSpace(lines.Text())
			key, value, ok := strings.Cut(line, "=")
			if ok && !strings.HasPrefix(line, "#") {
				env[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
	}
	for _, kv := range os.Environ() {
		if key, value, _ := strings.Cut(kv, "="); strings.HasPrefix(key, "ECHOWA_TEST_S3_") {
			env[key] = value
		}
	}

	return env
}

// repoRoot is the nearest directory up from here with a go.mod: the
// package's directory under go test, the repo root in the dev machine.
func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// each runs test once per target, with a location under a fresh prefix.
func each(t *testing.T, test func(t *testing.T, tg target, loc *models.StorageLocation)) {
	for _, tg := range targets(t) {
		t.Run(tg.name, func(t *testing.T) {
			loc := tg.loc
			loc.Prefix = JoinKey(loc.Prefix, "integration-"+randomHex(4))
			test(t, tg, &loc)
		})
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// stream hides any Seek or Len from the uploader, as a dump's pipe does,
// and fails with err after n bytes when err is set.
type stream struct {
	r   io.Reader
	n   int64
	err error
}

func (s *stream) Read(p []byte) (int, error) {
	if s.err != nil && s.n <= 0 {
		return 0, s.err
	}
	if s.err != nil && int64(len(p)) > s.n {
		p = p[:s.n]
	}
	n, err := s.r.Read(p)
	s.n -= int64(n)

	return n, err
}

func TestS3CheckPasses(t *testing.T) {
	each(t, func(t *testing.T, _ target, loc *models.StorageLocation) {
		steps := Check(context.Background(), Open(loc), loc.Prefix)

		for _, s := range steps {
			assert.Empty(t, s.Error, s.Name)
		}
		assert.True(t, Passed(steps))
	})
}

func TestS3StreamsAnUploadOfUnknownSizeInParts(t *testing.T) {
	each(t, func(t *testing.T, _ target, loc *models.StorageLocation) {
		ctx := context.Background()
		store := Open(loc)
		key := JoinKey(loc.Prefix, "big.dump")
		const size = 2*PartSize + 12345

		want := sha256.New()
		require.NoError(t, store.Put(ctx, key, &stream{r: io.TeeReader(io.LimitReader(rand.Reader, size), want)}))
		t.Cleanup(func() { _ = store.Delete(ctx, key) })

		listing, err := store.List(ctx, loc.Prefix+"/", "/")
		require.NoError(t, err)
		require.Len(t, listing.Objects, 1)
		assert.Equal(t, key, listing.Objects[0].Key)
		assert.EqualValues(t, size, listing.Objects[0].Size)

		r, err := store.Get(ctx, key)
		require.NoError(t, err)
		got := sha256.New()
		_, err = io.Copy(got, r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, want.Sum(nil), got.Sum(nil))
	})
}

func TestS3AFailingReaderLeavesNothing(t *testing.T) {
	each(t, func(t *testing.T, _ target, loc *models.StorageLocation) {
		ctx := context.Background()
		store := Open(loc)
		key := JoinKey(loc.Prefix, "broken.dump")
		dumpFailed := errors.New("pg_dump exited 1")

		err := store.Put(ctx, key, &stream{r: rand.Reader, n: 2*PartSize + 1000, err: dumpFailed})

		require.ErrorIs(t, err, dumpFailed)
		listing, err := store.List(ctx, loc.Prefix+"/", "")
		require.NoError(t, err)
		assert.Empty(t, listing.Objects, "no object appears")
		uploads, err := store.(*s3Store).client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket: aws.String(loc.Bucket), Prefix: aws.String(key),
		})
		require.NoError(t, err)
		assert.Empty(t, uploads.Uploads, "the multipart upload is aborted")
	})
}

func TestS3GetOfAMissingKey(t *testing.T) {
	each(t, func(t *testing.T, _ target, loc *models.StorageLocation) {
		_, err := Open(loc).Get(context.Background(), JoinKey(loc.Prefix, "nothing-here"))

		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestS3CheckNamesWhatsWrong(t *testing.T) {
	cases := []struct {
		name   string
		change func(*models.StorageLocation)
		want   string
		// localOnly cases need a machine of our own to try them on.
		localOnly bool
		// externalOnly cases need a provider that checks the region.
		externalOnly bool
	}{
		{name: "wrong secret", change: func(l *models.StorageLocation) { l.SecretKey = "not-the-secret" }, want: "The secret key doesn't match the access key ID."},
		{name: "unknown key", change: func(l *models.StorageLocation) { l.AccessKeyID = "AKIANOSUCHKEY0000000" }, want: "The provider doesn't know this access key ID."},
		{name: "missing bucket", change: func(l *models.StorageLocation) { l.Bucket = "echowa-missing-" + randomHex(8) }, want: "There's no bucket with this name at this endpoint."},
		{name: "wrong region", externalOnly: true, change: func(l *models.StorageLocation) {
			if l.Region == "us-west-2" {
				l.Region = "eu-west-1"
			} else {
				l.Region = "us-west-2"
			}
		}, want: "The region doesn't match the bucket's."},
		{name: "nothing listening", localOnly: true, change: func(l *models.StorageLocation) { l.Endpoint = "http://localhost:7079" }, want: "The endpoint refused the connection."},
	}
	each(t, func(t *testing.T, tg target, loc *models.StorageLocation) {
		for _, c := range cases {
			if (c.localOnly && !tg.local) || (c.externalOnly && tg.local) {
				continue
			}
			t.Run(c.name, func(t *testing.T) {
				broken := *loc
				c.change(&broken)

				steps := Check(context.Background(), Open(&broken), broken.Prefix)

				require.Len(t, steps, 1)
				assert.Equal(t, c.want, steps[0].Error)
			})
		}
	})
}
