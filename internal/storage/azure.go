package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
)

// AzureBlobStore implements BlobStore for Azure Blob Storage.
type AzureBlobStore struct {
	client    *azblob.Client
	container string
	account   string
}

// NewAzureBlobStore initializes an AzureBlobStore from environment variables.
// Required env vars:
//   - BLOB_CONNECTION_STRING: full Azure Storage connection string
//   - BLOB_CONTAINER:         target container name (default: dromos-workflow-outputs)
func NewAzureBlobStore() (*AzureBlobStore, error) {
	connStr := os.Getenv("BLOB_CONNECTION_STRING")
	if connStr == "" {
		return nil, fmt.Errorf("BLOB_CONNECTION_STRING env var is required for azure blob provider")
	}

	container := os.Getenv("BLOB_CONTAINER")
	if container == "" {
		container = "dromos-workflow-outputs"
	}

	client, err := azblob.NewClientFromConnectionString(connStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Blob client: %w", err)
	}

	// Extract account name from connection string for URI construction.
	account := extractAccountName(connStr)
	if account == "" {
		return nil, fmt.Errorf("could not parse AccountName from BLOB_CONNECTION_STRING")
	}

	return &AzureBlobStore{
		client:    client,
		container: container,
		account:   account,
	}, nil
}

// Upload streams data to Azure Blob Storage and returns a fully-qualified HTTPS URI.
func (s *AzureBlobStore) Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error) {
	opts := &azblob.UploadStreamOptions{
		HTTPHeaders: &blob.HTTPHeaders{
			BlobContentType: &mimeType,
		},
	}

	_, err := s.client.UploadStream(ctx, s.container, key, data, opts)
	if err != nil {
		return "", fmt.Errorf("azure upload failed for key %s: %w", key, err)
	}

	uri := fmt.Sprintf("https://%s.blob.core.windows.net/%s/%s", s.account, s.container, key)
	return uri, nil
}

// Download returns a ReadCloser for the blob at the given Azure HTTPS URI.
func (s *AzureBlobStore) Download(ctx context.Context, uri string) (io.ReadCloser, error) {
	container, blobName, err := parseAzureURI(uri)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.DownloadStream(ctx, container, blobName, nil)
	if err != nil {
		return nil, fmt.Errorf("azure download failed for %s: %w", uri, err)
	}

	return resp.Body, nil
}

// Delete removes a blob at the given Azure HTTPS URI.
func (s *AzureBlobStore) Delete(ctx context.Context, uri string) error {
	container, blobName, err := parseAzureURI(uri)
	if err != nil {
		return err
	}

	_, err = s.client.DeleteBlob(ctx, container, blobName, nil)
	if err != nil {
		return fmt.Errorf("azure delete failed for %s: %w", uri, err)
	}

	return nil
}

// parseAzureURI breaks https://<account>.blob.core.windows.net/<container>/<blob> into parts.
func parseAzureURI(uri string) (container string, blobName string, err error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid Azure URI %s: %w", uri, err)
	}

	// Path is /<container>/<blob/path...>
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse container/blob from Azure URI: %s", uri)
	}

	return parts[0], parts[1], nil
}

// extractAccountName parses AccountName=... from an Azure connection string.
func extractAccountName(connStr string) string {
	for _, part := range strings.Split(connStr, ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "AccountName") {
			return kv[1]
		}
	}
	return ""
}
