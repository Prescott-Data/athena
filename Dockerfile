# Build stage
FROM golang:1.26-alpine AS builder

# Install protobuf compiler with well-known types, plus git
RUN apk add --no-cache protobuf protobuf-dev git

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Install Go protobuf plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2 && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.10.0

# Copy source code
COPY . .

# Generate protobuf code (gen/ is gitignored, so we must generate at build time)
RUN mkdir -p api/grpc/gen && \
    TEMP_GEN=$(mktemp -d) && \
    protoc -I/usr/include -I. -I./third_party/googleapis \
      --go_out=$TEMP_GEN \
      --go-grpc_out=$TEMP_GEN \
      --grpc-gateway_out=$TEMP_GEN \
      api/grpc/memory.proto && \
    find $TEMP_GEN -name "*.go" -exec cp {} api/grpc/gen/ \; && \
    rm -rf $TEMP_GEN && \
    echo "✓ Proto generation complete: $(ls api/grpc/gen/*.go | wc -l) files"

# Build the application binaries
RUN CGO_ENABLED=0 GOOS=linux go build -o memory-server ./cmd/memory-server/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o init-ltm ./cmd/init-ltm/main.go

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS connections
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -u 1000 -s /bin/sh appuser

WORKDIR /app

# Copy the binaries from builder stage
COPY --from=builder /app/memory-server .
COPY --from=builder /app/init-ltm .

# Change ownership to non-root user
RUN chown appuser:appuser memory-server init-ltm

# Switch to non-root user
USER 1000

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
CMD ["./memory-server"]