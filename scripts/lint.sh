#!/bin/bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if golangci-lint is installed
check_golangci_lint() {
    if ! command -v golangci-lint &> /dev/null; then
        print_error "golangci-lint is not installed. Please install it first:"
        echo "  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b \$(go env GOPATH)/bin v1.61.0"
        exit 1
    fi
}

# Run Go formatting
run_go_fmt() {
    print_status "Running go fmt..."
    if go fmt ./...; then
        print_success "go fmt completed successfully"
    else
        print_error "go fmt failed"
        exit 1
    fi
}

# Run Go vet
run_go_vet() {
    print_status "Running go vet..."
    if go vet ./...; then
        print_success "go vet completed successfully"
    else
        print_error "go vet failed"
        exit 1
    fi
}

# Run golangci-lint
run_golangci_lint() {
    print_status "Running golangci-lint..."
    if golangci-lint run; then
        print_success "golangci-lint completed successfully"
    else
        print_error "golangci-lint failed"
        exit 1
    fi
}

# Run go mod tidy
run_go_mod_tidy() {
    print_status "Running go mod tidy..."
    if go mod tidy; then
        print_success "go mod tidy completed successfully"
    else
        print_error "go mod tidy failed"
        exit 1
    fi
}

# Run tests
run_tests() {
    print_status "Running tests..."
    if go test -v ./...; then
        print_success "Tests completed successfully"
    else
        print_error "Tests failed"
        exit 1
    fi
}

# Main function
main() {
    local command="${1:-all}"

    case "$command" in
        "fmt")
            run_go_fmt
            ;;
        "vet")
            run_go_vet
            ;;
        "lint")
            check_golangci_lint
            run_golangci_lint
            ;;
        "tidy")
            run_go_mod_tidy
            ;;
        "test")
            run_tests
            ;;
        "all")
            run_go_mod_tidy
            run_go_fmt
            run_go_vet
            check_golangci_lint
            run_golangci_lint
            run_tests
            print_success "All linting checks passed! 🎉"
            ;;
        *)
            echo "Usage: $0 [fmt|vet|lint|tidy|test|all]"
            echo "  fmt   - Run go fmt"
            echo "  vet   - Run go vet"
            echo "  lint  - Run golangci-lint"
            echo "  tidy  - Run go mod tidy"
            echo "  test  - Run tests"
            echo "  all   - Run all checks (default)"
            exit 1
            ;;
    esac
}

main "$@"
