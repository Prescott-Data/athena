package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3BlobStore implements BlobStore for S3-compatible APIs (MinIO, AWS S3).
type S3BlobStore struct {
	client *minio.Client
	bucket string
	region string
}

func NewS3BlobStore(endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*S3BlobStore, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize S3/MinIO client: %w", err)
	}

	return &S3BlobStore{
		client: client,
		bucket: bucket,
		region: region,
	}, nil
}

func (s *S3BlobStore) Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error) {
	opts := minio.PutObjectOptions{ContentType: mimeType}
	// Using -1 for object size pushes minio to use multipart upload natively for unknown size streams.
	_, err := s.client.PutObject(ctx, s.bucket, key, data, -1, opts)
	if err != nil {
		return "", fmt.Errorf("failed to upload object %s to bucket %s: %w", key, s.bucket, err)
	}

	uri := fmt.Sprintf("s3://%s/%s", s.bucket, key)
	return uri, nil
}

func (s *S3BlobStore) Download(ctx context.Context, uri string) (io.ReadCloser, error) {
	bucket, key, err := ParseS3URI(uri)
	if err != nil {
		return nil, err
	}

	object, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to download object %s from bucket %s: %w", key, bucket, err)
	}

	return object, nil
}

func (s *S3BlobStore) Size(ctx context.Context, uri string) (int64, error) {
	bucket, key, err := ParseS3URI(uri)
	if err != nil {
		return 0, err
	}

	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return 0, ErrBlobNotFound
		}
		return 0, fmt.Errorf("failed to stat object %s in bucket %s: %w", key, bucket, err)
	}

	return info.Size, nil
}

func (s *S3BlobStore) DownloadRange(ctx context.Context, uri string, offset, length int64) (io.ReadCloser, error) {
	bucket, key, err := ParseS3URI(uri)
	if err != nil {
		return nil, err
	}

	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(offset, offset+length-1); err != nil {
		return nil, fmt.Errorf("invalid range for object %s: %w", key, err)
	}
	// Core.GetObject issues the one ranged GET and reports its error here; the
	// high-level GetObject defers the request to the first Read.
	body, _, _, err := minio.Core{Client: s.client}.GetObject(ctx, bucket, key, opts)
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("failed to download range of object %s from bucket %s: %w", key, bucket, err)
	}

	return body, nil
}

// isS3NotFound reports whether an S3 error means the object does not exist.
func isS3NotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.StatusCode == http.StatusNotFound
}

func (s *S3BlobStore) Delete(ctx context.Context, uri string) error {
	bucket, key, err := ParseS3URI(uri)
	if err != nil {
		return err
	}

	err = s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete object %s from bucket %s: %w", key, bucket, err)
	}

	return nil
}

// ParseS3URI breaks an s3://bucket/key URI into its components.
func ParseS3URI(uri string) (bucket string, key string, err error) {
	if !strings.HasPrefix(uri, "s3://") {
		return "", "", fmt.Errorf("invalid S3 URI: %s", uri)
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse URI %s: %w", uri, err)
	}
	return u.Host, strings.TrimPrefix(u.Path, "/"), nil
}
