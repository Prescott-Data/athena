# Build stage
FROM golang:1.26-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application binaries
RUN CGO_ENABLED=0 GOOS=linux go build -o memory-server ./cmd/memory-server/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o init-ltm ./cmd/init-ltm/main.go

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS connections
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -s /bin/sh appuser

WORKDIR /app

# Copy the binaries from builder stage
COPY --from=builder /app/memory-server .
COPY --from=builder /app/init-ltm .

# Change ownership to non-root user
RUN chown appuser:appuser memory-server init-ltm

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
CMD ["./memory-server"]