# STM Unit Test Suite Review

## Abstract

This document contains a critical review of the unit test suite created for the refactored Short-Term Memory (STM) system. The goal is to ensure that the tests are robust and provide explicit coverage for the new architectural patterns, including the hot/cold path, the "Smart Trigger" mechanism, and the "save-then-trim" data safety protocol.

---

## 1. `stm_cache_test.go` Review

**Overall Assessment:** Good, but there is a conceptual misunderstanding in the test plan leading to a coverage gap.

#### 1.1. Correct Use of `STMEvent` Struct

*   **Finding:** Yes, the tests in this file correctly and exclusively use the new `STMEvent` struct, including the `Role` and `Type` fields. The test `Test_AddAndGet_STMEvent` properly validates the storage and retrieval of this new struct from the mock Redis cache.

#### 1.2. Testing the "Smart Trigger" (`Test_AddSTMEvent_TriggersWorker_On_UserMessage`)

*   **Finding:** This is the most significant finding for this file. The requested test, `Test_AddSTMEvent_TriggersWorker_On_UserMessage`, **does not exist in `stm_cache_test.go`, and it should not.**
*   **Analysis:** There is a misunderstanding of where the "Smart Trigger" logic resides. The `STMCache` (`stm_cache.go`) is a simple data access layer; its only job is to `LPUSH` and `LTRIM` events in Redis. It is, and should be, unaware of the task queue.
*   The "Smart Trigger" logic was implemented in the gRPC handler in `internal/server/server.go`. This is the correct location for this application-level logic.
*   **Identified Gap:** Because the trigger logic is in `server.go`, and there are currently **no unit tests for `server.go`**, this critical piece of functionality is **not covered by any unit tests.**

---

## 2. `task_queue_test.go` Review

**Overall Assessment:** Excellent.

#### 2.1. Validation of `TaskEnvelope` and Lightweight Payload

*   **Finding:** Yes, the tests in this file are perfectly designed for the new architecture.
*   **Analysis:**
    *   `TestEnqueueCognitiveCheckTask` correctly mocks the `redis.LPush` call and asserts that the data pushed to the queue is a `TaskEnvelope` containing a marshalled `CognitiveChainCheckTask` payload. This confirms the lightweight nature of the task.
    *   `TestDequeueTask` correctly mocks the `redis.BRPop` call, provides a marshalled `TaskEnvelope`, and asserts that the function correctly deserializes it and returns the right object.
*   **Conclusion:** The test coverage for the task queue mechanism is robust and complete.

---

## 3. `worker_test.go` Review

**Overall Assessment:** Good, but the most critical test can be made more robust.

#### 3.1. Coverage of "Cosine Gate" Scenarios

*   **Finding:** Yes, all four scenarios are explicitly tested:
    1.  `TestProcessCognitiveChainCheck_ChainContinues_HighSim`
    2.  `TestProcessCognitiveChainCheck_ChainBreaks_LowSim`
    3.  `TestProcessCognitiveChainCheck_GrayArea_LLMFallback_Continues`
    4.  `TestProcessCognitiveChainCheck_GrayArea_LLMFallback_Breaks`
*   **Conclusion:** The test coverage for the decision-making logic of the Cosine Gate is excellent.

#### 3.2. Verification of "Save-Then-Trim" Logic

*   **Finding:** The tests for the chain-break scenarios (`..._ChainBreaks_LowSim` and `..._LLMFallback_Breaks`) do assert that `stmStore.ProcessMTMFormation` and `redis.LTrim` are called. However, they only test the "happy path."
*   **Critical Gap:** The tests **do not verify what happens if `ProcessMTMFormation` fails.** A robust test of the "save-then-trim" logic must include a negative test case.
*   **Recommendation:** A new test case should be added where the mock `stmStore.ProcessMTMFormation` is configured to return an error. The test must then assert that `redis.LTrim` is **NEVER** called. This would definitively prove that the data is not deleted from the STM cache when the persistence step fails, which is the entire point of the "save-then-trim" protocol.

---

## 4. `stm_store_test.go` Review

**Overall Assessment:** Excellent.

#### 4.1. `ProcessMTMFormation` Pipeline Test

*   **Finding:** Yes, the `TestProcessMTMFormation_OrchestratesPipeline` test is comprehensive.
*   **Analysis:** It correctly sets up mocks for all dependencies (`CreateSegmentSummary`, `CreateEmbedding`, `InsertOne`, `StoreCognitiveEvent`) and asserts that each one is called exactly once with the expected arguments. This confirms the orchestration logic is sound.

#### 4.2. "MTM Richness" Prompt Test

*   **Finding:** Yes, the `TestCreateSegmentSummary_BuildsRichPrompt` test correctly validates the fix from the review.
*   **Analysis:** The test uses `mock.MatchedBy` to capture the prompt string that is sent to the mock LLM client. It then performs `assert.Contains` on that string to ensure the `[thought]` and `[action]` keywords are present. This is a perfect validation of the "MTM Richness" requirement.

---

## Final Summary & Actionable Gaps

The unit test suite is strong but has two specific, high-value gaps that need to be addressed:

1.  **Critical Gap 1 (Coverage):** There are no unit tests for the application entry point in `internal/server/server.go`. This means the "Smart Trigger" logic (only enqueueing tasks for `user/message` events) is currently not tested at all.
2.  **Critical Gap 2 (Robustness):** The `worker_test.go` is missing a negative test case to prove the "save-then-trim" logic holds when MTM persistence fails. A test should be added where `ProcessMTMFormation` returns an error, and the test must assert that `LTrim` is not called.
