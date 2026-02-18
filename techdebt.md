# Technical Debt & Known Gaps

## 1. Unit Test Coverage

### Missing Server Tests
There are currently no unit tests for the application entry point in `internal/server/server.go`. This means critical logic, such as the "Smart Trigger" (which ensures tasks are only enqueued for `user/message` events), is not covered by automated tests.

**Action Item:** Implement unit tests for `server.go`, specifically mocking the gRPC stream/context to verify that `AddSTMEvent` and `EnqueueTask` are called correctly under different conditions.
