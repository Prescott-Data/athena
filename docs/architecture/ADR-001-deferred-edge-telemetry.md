# ADR-001: Deferral of Edge Ontology Telemetry and Analytics

**Status**: Accepted / Deferred

## Context

When designing Athena's Long-Term Memory (LTM) Knowledge Graph in ArangoDB, we faced the classical "Strict Schema vs. Open Graph" dilemma regarding how the LLM extracts relationships (Edges). 

*   **Strict Schema:** Enforcing a rigorous ontology (e.g., `USES`, `WORKS_ON`) keeps graph traversals fast, indexable, and predictable. However, when a relationship emerges that does not fit the static ontology, LLMs tend to hallucinate completely new schema properties, breaking JSON unmarshaling and database constraints.
*   **Open Graph:** Allowing the LLM to invent any relationship verb ensures no loss of semantic meaning, but results in massive fragmentation (e.g., having 50 edges like `is_coding`, `developing`, `building` that all mean the same thing). This makes systematic graph querying nearly impossible.

### The Solution: The Escape Hatch

We resolved the immediate ingestion problem by building a strict ontology accompanied by an "Escape Hatch." 
We introduced a generic `RELATES_TO` edge and a `context_nuance` string field. If the LLM identifies a relationship that cannot be strictly classified, it is forced to use the `RELATES_TO` edge while preserving the true human meaning inside the `context_nuance` property.
Furthermore, we built a "Gravity Bouncer" (an Edge Validator/Interceptor in `ltm_writer.go`) that catches unauthorized hallucinations, auto-corrects the Edge to `RELATES_TO`, and appends the hallucinated verb to the `context_nuance`.

This immediately solves the problem of preserving semantic meaning and context without ever breaking the strict ArangoDB schema constraints.

## The Technical Debt

We discussed building an `EdgeAnomalies` telemetry collection to actively track these hallucinations. By logging every time the "Escape Hatch" is used, we could analytically determine when a new recurring verb pattern justifies permanently expanding the rigid ontology.

However, we decided to **defer** building the telemetry pipeline to avoid premature optimization (YAGNI - You Aren't Gonna Need It).

The risk of deferring this is purely related to future query scale. If the system frequently begins to rely on the `context_nuance` field on `RELATES_TO` edges for category-wide queries (e.g., "list all books I've read"), the database will be forced to perform slow, full-table text scans or string matching over the `context_nuance` field. This sacrifices the primary advantage of ArangoDB: lightning-fast indexed edge lookups based on strict relationship verbs.

## The Trigger Condition (When to fix this)

We will only build the `EdgeAnomalies` telemetry pipeline and promote new verbs to the strict ontology **IF/WHEN** either of the following occurs:

1.  Query latency noticeably degrades during categorical retrievals because the system is forced to rely heavily on text-scanning the `context_nuance` field.
2.  Data analysis shows that the `RELATES_TO` edge usage vastly outnumbers the usage of the specific ontological verbs, indicating our allowed ontology is too restrictive for general operation.
