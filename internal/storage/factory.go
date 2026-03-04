package storage

import (
	"fmt"
	"os"
	"strconv"
)

// NewBlobStoreFromEnv initializes a BlobStore based on environment variables.
func NewBlobStoreFromEnv() (BlobStore, error) {
	provider := os.Getenv("BLOB_PROVIDER")
	if provider == "" {
		return nil, nil // Blob storage disabled
	}

	switch provider {
	case "minio", "s3":
		endpoint := os.Getenv("BLOB_ENDPOINT")
		accessKey := os.Getenv("BLOB_ACCESS_KEY")
		secretKey := os.Getenv("BLOB_SECRET_KEY")
		bucket := os.Getenv("BLOB_BUCKET")
		region := os.Getenv("BLOB_REGION")
		useSSL, _ := strconv.ParseBool(os.Getenv("BLOB_USE_SSL"))

		return NewS3BlobStore(endpoint, accessKey, secretKey, bucket, region, useSSL)
	case "gcs":
		return NewGCSBlobStore()
	case "azure":
		return NewAzureBlobStore()
	default:
		return nil, fmt.Errorf("unknown blob provider: %s", provider)
	}
}
