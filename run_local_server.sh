#!/bin/bash
export MEMORY_OS_MONGODB_URI="mongodb://admin:admin123@localhost:27017/memory_os?authSource=admin"
export MEMORY_OS_MONGODB_DATABASE="memory_os"
export MEMORY_OS_REDIS_HOST="localhost"
export MEMORY_OS_MILVUS_HOST="localhost"
export MEMORY_OS_PROMOTER_INTERVAL_MIN="1"
export MEMORY_OS_PROMOTER_THRESHOLD="0.1"
export MEMORY_OS_ARCHIVER_INTERVAL_MIN="1"
export ARANGODB_URL="http://localhost:8529"
export ARANGODB_USER="root"
export ARANGODB_PASSWORD="athena_dev"
export ARANGODB_DATABASE="athena_ltm"
export LLM_TIMEOUT_SECONDS="60"
export LLM_RATE_LIMIT_PER_MINUTE="200"

export BLOB_PROVIDER="minio"
export BLOB_ENDPOINT="localhost:9000"
export BLOB_BUCKET="athena-blobs"
export BLOB_ACCESS_KEY="minioadmin"
export BLOB_SECRET_KEY="minioadmin"
export BLOB_USE_SSL="false"
export BLOB_REGION="us-east-1"

go run cmd/memory-server/main.go
