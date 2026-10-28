package backups

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"goravel/app/models"
)

// Upload settings (ADR 0034). Memory per upload is about PartSize times
// UploadConcurrency, and an object can be up to 10,000 parts.
const (
	PartSize          = 16 << 20
	UploadConcurrency = 2
)

type s3Store struct {
	client   *s3.Client
	uploader *transfermanager.Client
	bucket   string
}

func newS3Store(loc *models.StorageLocation) *s3Store {
	client := s3.New(s3.Options{
		Region:       loc.Region,
		BaseEndpoint: aws.String(loc.Endpoint),
		UsePathStyle: loc.PathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(loc.AccessKeyID, string(loc.SecretKey), ""),
		// The SDK sends CRC checksums by default, which R2, B2 and other
		// providers reject on some calls.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	uploader := transfermanager.New(client, func(o *transfermanager.Options) {
		o.PartSizeBytes = PartSize
		o.Concurrency = UploadConcurrency
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		// A fresh context, so a cancelled backup still aborts its upload.
		o.FailTimeout = 30 * time.Second
	})

	return &s3Store{client: client, uploader: uploader, bucket: loc.Bucket}
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader) error {
	_, err := s.uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	})

	return err
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	var missing *types.NoSuchKey
	if errors.As(err, &missing) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return out.Body, nil
}

func (s *s3Store) List(ctx context.Context, prefix, delimiter string) (Listing, error) {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)}
	if delimiter != "" {
		in.Delimiter = aws.String(delimiter)
	}
	listing := Listing{Objects: []Object{}, Prefixes: []string{}}
	pages := s3.NewListObjectsV2Paginator(s.client, in)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return Listing{}, err
		}
		for _, o := range page.Contents {
			listing.Objects = append(listing.Objects, Object{
				Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), Modified: aws.ToTime(o.LastModified),
			})
		}
		for _, p := range page.CommonPrefixes {
			listing.Prefixes = append(listing.Prefixes, aws.ToString(p.Prefix))
		}
	}

	return listing, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})

	return err
}
