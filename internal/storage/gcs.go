package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	gcstorage "cloud.google.com/go/storage"
)

// GCSBlobStore implements BlobStore for Google Cloud Storage.
// Authentication uses Application Default Credentials (workload identity,
// attached service account, or GOOGLE_APPLICATION_CREDENTIALS) — no static
// keys are required on GCP.
type GCSBlobStore struct {
	client *gcstorage.Client
	bucket string
}

func NewGCSBlobStore(ctx context.Context, bucket string) (*GCSBlobStore, error) {
	if bucket == "" {
		return nil, fmt.Errorf("gcs blob store: BLOB_BUCKET is required")
	}

	client, err := gcstorage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GCS client (ADC): %w", err)
	}

	return &GCSBlobStore{
		client: client,
		bucket: bucket,
	}, nil
}

func (s *GCSBlobStore) Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error) {
	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	w.ContentType = mimeType

	if _, err := io.Copy(w, data); err != nil {
		w.Close()
		return "", fmt.Errorf("failed to upload object %s to bucket %s: %w", key, s.bucket, err)
	}
	// Close finalizes the upload; errors here mean the object was not written.
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize upload of object %s to bucket %s: %w", key, s.bucket, err)
	}

	uri := fmt.Sprintf("gs://%s/%s", s.bucket, key)
	return uri, nil
}

func (s *GCSBlobStore) Download(ctx context.Context, uri string) (io.ReadCloser, error) {
	bucket, key, err := ParseGCSURI(uri)
	if err != nil {
		return nil, err
	}

	r, err := s.client.Bucket(bucket).Object(key).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to download object %s from bucket %s: %w", key, bucket, err)
	}

	return r, nil
}

func (s *GCSBlobStore) Delete(ctx context.Context, uri string) error {
	bucket, key, err := ParseGCSURI(uri)
	if err != nil {
		return err
	}

	if err := s.client.Bucket(bucket).Object(key).Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete object %s from bucket %s: %w", key, bucket, err)
	}

	return nil
}

// ParseGCSURI breaks a gs://bucket/key URI into its components.
func ParseGCSURI(uri string) (bucket string, key string, err error) {
	if !strings.HasPrefix(uri, "gs://") {
		return "", "", fmt.Errorf("invalid GCS URI: %s", uri)
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse URI %s: %w", uri, err)
	}
	return u.Host, strings.TrimPrefix(u.Path, "/"), nil
}
