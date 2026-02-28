package storage

import (
	"context"
	"fmt"
	"io"
)

// AzureBlobStore implements BlobStore for Azure Blob Storage.
type AzureBlobStore struct {
	// TODO: Add Azure client
}

func NewAzureBlobStore() (*AzureBlobStore, error) {
	return nil, fmt.Errorf("Azure blob store not yet implemented")
}

func (s *AzureBlobStore) Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (s *AzureBlobStore) Download(ctx context.Context, uri string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *AzureBlobStore) Delete(ctx context.Context, uri string) error {
	return fmt.Errorf("not implemented")
}
