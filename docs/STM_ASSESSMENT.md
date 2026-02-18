# STM (Short-Term Memory) System Assessment

**Date:** November 17, 2025  
**Module:** `dromos-core/memory-os`  
**Focus:** `STMStore` architecture, pipeline orchestration, LLM guardrails

---
## 1. Purpose & Scope
The STM layer manages ingestion and transient processing of `CognitiveEvent`s before forming or updating mid‑term cognitive chains. It orchestrates summarization, quality gating, merging decisions (delegated), embedding creation, and cleanup.

---
## 2. Core Responsibilities (Current)
| Responsibility | Implementation | Notes |
|----------------|---------------|-------|
| Event persistence | `StoreCognitiveEvent` | Direct Mongo insert, no batching |
| MTM pipeline orchestration | `ProcessMTMFormation` | Linear sequence; candidate → quality → merge → embedding → cleanup |
| Summary generation | `CreateSegmentSummary` (LLM) | Single prompt; no heuristic fallback here (fallback only sets generic string) |
| Topic continuity (binary) | `analyzeTopicContinuity` | Redundant with richer `ContinuityAnalyzer` logic elsewhere |
| Embedding creation | `CreateEmbedding` | No caching; raw text sent untruncated |
| Vector persistence | `StoreChainEmbedding` | Delegates to Milvus client; non-transactional with chain creation |
| Guardrails | `LLMGuardrails` | Basic rate limit + circuit breaker; global per type key |

---
## 3. Strengths
- Clear linear pipeline; easy to trace execution.  
- Circuit breaker avoids repeated failures hammering external services.  
- Rate limiting per feature user (summary / embedding) prevents uncontrolled spam.  
- Logs provide step visibility (summary created, quality passed, embedding stored).  
- Separation of concerns with injected components (quality validator, session manager, topic analyzer, parallel processor).  

---
## 4. Gaps & Risks
| # | Gap | Impact | Example |
|---|-----|--------|---------|
| 1 | No embedding cache | Redundant API calls, cost & latency | Multiple chains with identical summaries recomputed |
| 2 | Unbounded summary/ prompt length | Higher token usage & slower responses | Large chains produce huge prompts |
| 3 | Redundant continuity method | Divergent logic vs `ContinuityAnalyzer` | Inconsistent merge decisions possible |
| 4 | Rate limit not per real user | Shared limiting reduces fairness | `embedding_user` static key throttles all at once |
| 5 | Single circuit breaker state | Failures in embeddings block summaries | Cross-feature coupling |
| 6 | No idempotency for formation | Duplicate chains on retried calls | Re-run of same event batch forms new chain |
| 7 | No retry/backoff on transient errors | Fragile under network hiccups | Immediate failure on timeout |
| 8 | Weak fallback summary | Low semantic value harms retrieval | Generic "Conversation segment." |
| 9 | PII unfiltered before LLM | Privacy exposure risk | Emails, secrets in event content |
|10 | No metrics/tracing | Limited observability & tuning | Cannot see LLM latency distribution |
|11 | Non-transactional embedding storage | Partial state if embed fails | Chain persists without vector |
|12 | Global circuit breaker threshold fixed | Static resilience, no adaptive recovery | Doesn’t consider success ratio trend |
|13 | Missing config validation | Silent bad values (negative weights) | Quality weight misconfiguration possible |
|14 | Prompt includes all events | Token overuse & performance hit | Long-running sessions escalate cost |

---
## 5. Quick Win Recommendations (P1)
| # | Recommendation | Effort | Benefit |
|---|---------------|--------|---------|
| 1 | Add LRU embedding cache keyed by `sha256(model+summary)` | 1–2h | Reduce duplicate embedding calls 40–60% |
| 2 | Truncate summary & prompt segments (>512 chars) | 0.5h | Cut token usage & latency |
| 3 | Per-user rate limit keys (`llm:summary:<userID>`) | 1h | Fair resource distribution |
| 4 | Distinct circuit breakers per feature | 1.5h | Isolation of failure domains |
| 5 | Heuristic summary fallback (keywords + first/last event) | 1h | Higher semantic quality under LLM failure |
| 6 | Idempotency fingerprint (hash event contents) | 2h | Avoid duplicates & wasted processing |
| 7 | Basic Prometheus metrics (latency, counts, failures) | 2h | Enables monitoring & SLOs |
| 8 | Exponential backoff (max 2 retries) for HTTP 5xx/timeouts | 1.5h | Improved resilience to transient faults |
| 9 | Regex-based PII redaction (email, tokens) pre-prompt | 1.5h | Privacy safeguard |
|10 | Embed-before-chain or mark `embeddingStatus` | 1h | Prevent partial inconsistent state |

---
## 6. Medium-Term Enhancements (P2)
| Item | Description |
|------|-------------|
| Adaptive circuit breaker | Rate-based & failure ratio moving window evaluation |
| Structured tracing | Propagate correlation IDs across HTTP calls & logs |
| Summary multi-strategy | LLM primary + heuristic fallback + semantic reuse cluster |
| Sliding window rate limiting | Smoother request distribution vs fixed minute bucket |
| Pipeline refactor | Extract smaller services: SegmentBuilder, QualityGate, PersistenceManager |

---
## 7. Proposed Architectural Refactors
1. Replace `analyzeTopicContinuity` calls via unified `ContinuityAnalyzer`.  
2. Introduce `EmbeddingService` wrapper with cache + retry logic.  
3. Add `FormationController` with idempotency verification pre-run.  
4. Implement `PromptBuilder` to sample representative events (first N, last M, top thought events).  

---
## 8. Observability Plan
**Metrics:**
- `stm_llm_requests_total{kind="summary|embedding|continuity",status="success|error"}`
- `stm_llm_request_duration_seconds_bucket{kind=...}`
- `stm_chain_formation_duration_seconds`
- `stm_embedding_cache_hit_ratio`
- `stm_chain_idempotency_rejects_total`

**Logging Improvements:** Add correlation ID (`chainID`, `fingerprint`) + failure category (`timeout`, `decode_error`).

**Alerting:**  
- Circuit breaker open >5 min  
- LLM failure rate >10% over 5 min window  
- Cache hit ratio <30% (indicates poor summarization reuse)  

---
## 9. Security & Privacy
| Concern | Mitigation |
|---------|------------|
| PII leakage to LLM | Regex redaction (emails, 16+ hex tokens, phone numbers) before prompt assembly |
| API Key exposure in logs | Ensure no header/body logging of secrets |
| Replay chain formation | Fingerprint idempotency key with TTL |
| Oversharing raw user content | Prompt sampling (reduce raw content dump) |

---
## 10. Sample Implementation Snippets
**Embedding Cache (LRU skeleton):**
```go
type embeddingCache struct { mu sync.RWMutex; data map[string]*models.EmbeddingData; order []string; max int }
func (c *embeddingCache) Get(k string)(*models.EmbeddingData,bool){ c.mu.RLock(); defer c.mu.RUnlock(); v,ok:=c.data[k]; return v,ok }
func (c *embeddingCache) Put(k string,v *models.EmbeddingData){ c.mu.Lock(); defer c.mu.Unlock(); if _,ok:=c.data[k]; !ok { if len(c.order)==c.max { old:=c.order[0]; c.order=c.order[1:]; delete(c.data,old) }; c.order=append(c.order,k) }; c.data[k]=v }
```
**Idempotency Fingerprint:**
```go
func fingerprint(events []models.CognitiveEvent) string { h:=sha256.New(); for _,e:=range events { h.Write([]byte(e.Role)); h.Write([]byte(e.Content)); }; return hex.EncodeToString(h.Sum(nil)) }
```
**Heuristic Summary Fallback:**
```go
func heuristicSummary(events []models.CognitiveEvent) string {
 if len(events)==0 { return "Empty segment" }
 first, last := events[0].Content, events[len(events)-1].Content
 keywords := extractTopKeywords(events,5)
 return fmt.Sprintf("Discussion about %s; progressed from '%s' to '%s'", strings.Join(keywords, ", "), snippet(first), snippet(last))
}
```
**Prompt Sampling:**
```go
func buildPrompt(events []models.CognitiveEvent) string {
 maxTurns:=parseIntEnv("SUMMARY_MAX_TURNS",20)
 sample:=sampleEvents(events,maxTurns) // head/tail + thought events
 // assemble concise prompt
}
```

---
## 11. Prioritized Delivery Plan
**Sprint 1 (Quick Wins):** Cache + truncation + idempotency + fallback summary + metrics.  
**Sprint 2:** Circuit breaker separation + tracing + prompt sampling.  
**Sprint 3:** Adaptive logic + sliding rate limit + persistence refactor.  

---
## 12. Success KPIs
| KPI | Target |
|-----|--------|
| Avg chain formation latency | < 2.5s |
| Embedding cache hit ratio | > 50% |
| Duplicate chain formations | < 1% of calls |
| LLM failure fallback rate | < 5% normal operation |
| Mean prompt token count | < 600 |

---
## 13. Conclusion
STM is structurally sound but can be materially improved in resilience, efficiency, and observability. Implementing the quick wins will reduce cost and latency while laying foundation for more adaptive behaviors. No critical architectural flaws detected—improvements are evolutionary, not corrective.

---
**Next Suggested Action:** Begin implementation of embedding cache + summary truncation guard.
