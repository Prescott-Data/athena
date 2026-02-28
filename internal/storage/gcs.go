package storage

import (
	"context"
	"fmt"
	"io"
)

// GCSBlobStore implements BlobStore for Google Cloud Storage.
type GCSBlobStore struct {
	// TODO: Add GCS client
}

func NewGCSBlobStore() (*GCSBlobStore, error) {
	return nil, fmt.Errorf("GCS blob store not yet implemented")
}

func (s *GCSBlobStore) Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (s *GCSBlobStore) Download(ctx context.Context, uri string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *GCSBlobStore) Delete(ctx context.Context, uri string) error {
	return fmt.Errorf("not implemented")
}
