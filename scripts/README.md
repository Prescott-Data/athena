# Memory OS Scripts

This directory contains various scripts and utilities for Memory OS development and testing.

## 📁 Directory Structure

```
scripts/
├── tests/                 # Integration and connectivity tests
│   ├── azure_infrastructure_test.go  # Tests Azure infrastructure connectivity
│   ├── memory_os_api_test.go         # Tests Memory OS API endpoints
│   └── go.mod                        # Dependencies for test scripts
└── README.md              # This file
```

## 🧪 Tests

### Infrastructure Test
```bash
cd scripts/tests
go run azure_infrastructure_test.go
```

Tests connectivity to Azure infrastructure services:
- Redis connectivity and operations
- MongoDB connectivity and operations  
- Milvus health and gRPC connectivity
- Network connectivity verification

### API Test
```bash
cd scripts/tests
go run memory_os_api_test.go
```

Tests Memory OS API endpoints (requires running Memory OS server):
- Health endpoint
- Session creation
- Interaction storage
- Context retrieval

## 📋 Usage

1. **Infrastructure Test**: Run this first to verify Azure services are accessible
2. **Start Memory OS**: Build and start the Memory OS server
3. **API Test**: Run this to verify Memory OS APIs work with Azure infrastructure

## 🔧 Requirements

- Go 1.21+
- Access to Azure infrastructure
- Redis, MongoDB, and Milvus services running on Azure VM
