# Architectural Evolution: The Orbital Mechanics of Memory

This document outlines the core architectural principles and recent implementations that govern the Short-Term Memory (STM) and Mid-Term Memory (MTM) pipelines. To explain the relationship between velocity, density, and decay, we use **Johannes Kepler’s Laws of Planetary Motion** as a conceptual framework.

---

## 1. STM Ingestion: Dual-Write Architecture
**The "Planetary State" Principle**

### Technical Implementation
We implemented a robust ingestion layer where every incoming cognitive event is simultaneously persisted to two distinct sinks:
*   **Redis (The Active Orbit):** Stores high-velocity events for immediate AI context and continuity analysis.
*   **MongoDB (The Permanent Audit):** Records raw events in the `cognitive_events` collection to provide a permanent, immutable timeline.

### Concept: Memory as Synthesis
Memory is not a static snapshot; it is a synthesis of events over time. By recording raw events instantly, we preserve the true **cognitive trajectory** of user interactions. This allows agents to understand not just "where the conversation is" (state), but "how it got there" (momentum).

---

## 2. STM Cache Protection: Event Coalescing
**Kepler’s Second Law: The Law of Equal Areas**
> *"A line segment joining a planet and the Sun sweeps out equal areas during equal intervals of time."*

### Technical Implementation
*   **Contextual Density:** Replaced the "Cognitive Depth" metric with **Contextual Density**. This measures the information mass of an interaction based on system triggers, entity count, and workflow metadata.
*   **Event Coalescing:** Implemented a deduplication logic in Redis that merges high-frequency automation logs (e.g., repeating observations from the same execution ID) into a single event with a `coalesced_count`.

### Concept: Semantic Area
In our system, a rapid burst of 50 system automation logs sweeps out the exact same **semantic area** as 5 slow, deliberate human chat messages. The coalescer prevents high-velocity "debris" from flooding the active context window, ensuring the AI focuses on the "area" of the conversation rather than the "frequency" of the logs.

---

## 3. MTM Heat Scoring: Spaced Repetition Cooldown
**Kepler’s Third Law: The Law of Harmonies**
> *"The square of the orbital period of a planet is proportional to the cube of the semi-major axis of its orbit."*

### Technical Implementation
*   **Cramming Protection:** Patched an exploit in the Ebbinghaus-based heat scorer by implementing a **12-hour Cooldown Period**. 
*   **Logic:** Rapid, repeated accesses to the same memory segment within the cooldown window will refresh the "Last Accessed" timestamp (resetting the decay curve) but will **not** multiply the `RecallStrength`.

### Concept: Orbital Stability
True memory retention requires spaced intervals. Blindly multiplying heat on every rapid access artificially inflates a memory's importance. The cooldown enforces a natural **orbital decay**, ensuring that only concepts recalled over meaningful spans of time achieve high stability (RecallStrength), while "crammed" information eventually drifts away.

---

## 4. MTM Cost Optimization: Garbage Collection
**The Escape Velocity Principle**

### Technical Implementation
*   **ArchiveColdChains:** A background process that scans the `cognitive_chains` collection for segments older than 7 days.
*   **Freezing Point:** If a chain's Heat Score drops below **0.1 (Absolute Zero)**, the system triggers archival:
    1.  **Milvus Deletion:** The expensive vector embedding is deleted to save costs and maintain search performance.
    2.  **MongoDB Archival:** The raw summary and metadata are flagged as `status: "archived"`.

### Concept: Gravitational Tether
Chains that lose their "gravitational tether" (Heat Score) are no longer part of the active semantic "solar system." To prevent Milvus bloat, these cold chains float out into the **Historical Archive** (MongoDB). They remain available for deep audits and historical reconstruction but no longer consume the high-performance resources required for active recall.

---

## Summary of Impact

| Layer | Implementation | Physical Analogy | Benefit |
| :--- | :--- | :--- | :--- |
| **Ingestion** | Dual-Write (Redis + Mongo) | Planetary Trajectory | Immutable audit + Active context |
| **Protection** | Event Coalescing | Law of Equal Areas | Prevents context window flooding |
| **Retention** | 12h Access Cooldown | Law of Harmonies | Prevents heat inflation (Spaced Repetition) |
| **Optimization**| ArchiveColdChains | Escape Velocity | Milvus cost reduction + Performance |
