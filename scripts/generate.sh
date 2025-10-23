#!/bin/bash
set -e

# This script generates Go code from the memory.proto file.

# Ensure protoc is installed
if ! command -v protoc &> /dev/null
then
    echo "protoc could not be found. Please install it."
    exit 1
fi

# Install Go plugins for protoc
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.10.0

# Define paths
PROTO_DIR=./api/grpc
OUTPUT_DIR=./api/grpc/gen
PROTO_FILE=$PROTO_DIR/memory.proto

# Get GOPATH for plugin paths
GOPATH=$(go env GOPATH)

# Create output directory if it doesn't exist
mkdir -p $OUTPUT_DIR

# Generate Go code
protoc -I/usr/local/include -I. -I./third_party/googleapis \
  --plugin=protoc-gen-go=$GOPATH/bin/protoc-gen-go \
  --plugin=protoc-gen-go-grpc=$GOPATH/bin/protoc-gen-go-grpc \
  --plugin=protoc-gen-grpc-gateway=$GOPATH/bin/protoc-gen-grpc-gateway \
  --go_out=$OUTPUT_DIR --go_opt=paths=source_relative \
  --go-grpc_out=$OUTPUT_DIR --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=$OUTPUT_DIR --grpc-gateway_opt=paths=source_relative \
  $PROTO_FILE

echo "Successfully generated Go code from $PROTO_FILE"

