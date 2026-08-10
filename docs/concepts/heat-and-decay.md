# Heat & Decay

Every cognitive chain carries a **heat score**: a number in $[0, 1]$ that decays with neglect and recovers with use. Heat is the single signal that drives the memory lifecycle: hot chains get [promoted](promotion-and-knowledge-graph.md) to the knowledge graph, cold chains get [archived](archival-and-lifecycle.md).

The model is a direct implementation of the **Ebbinghaus forgetting curve** with spaced-repetition reinforcement.

## The formula

$$
H(t) \;=\; I_{\text{base}} \times e^{-\Delta T \,/\, (\tau \cdot S)}
$$

| Symbol | Meaning | Source |
|---|---|---|
| $H(t)$ | Heat now | computed on every promoter pass |
| $I_{\text{base}}$ | Base importance of the chain | see below |
| $\Delta T$ | Hours since the chain was last accessed | `last_accessed_at`, falling back to `max(last_event_at, started_at)`; clamped ≥ 0 |
| $\tau$ | Decay constant, hours | `HEAT_DECAY_TAU_HOURS` (default **24**) |
| $S$ | Recall strength (the spaced-repetition multiplier) | grows with well-spaced accesses |

## Base importance

$$
I_{\text{base}} = \min\bigl(1.0,\;\; w_1 \cdot \text{Intrinsic} + w_2 \cdot \text{Density}\bigr)
$$

with $w_1 =$ `HEAT_WEIGHT_INTRINSIC` (default **0.7**) and $w_2 =$ `HEAT_WEIGHT_DENSITY` (default **0.3**).

**Intrinsic** is the LLM's judgment of the chain's importance ($0$–$1$), produced during [chain formation](chain-formation.md).

**Density** rewards structurally rich segments, accumulated additively and capped at 1.0:

| Signal | Bonus |
|---|---|
| Entities present | +0.15 |
| Contains a `thought` or `action` event | +0.20 |
| Observation with `workflow_id` / `execution_id` / blob | +0.25 |
| Each distinct `origin_service` (capped) | +0.15 |
| More than one turn | +0.05 |

A multi-service automation trace with entities and reasoning scores far higher density than two lines of chit-chat. That is by design.

## Recall strength: memories that are used, harden

$S$ starts at 1.0 and never drops below it. When a chain is accessed (retrieved by search or context blending), `RecordAccess` applies:

```
if hours since previous access > HEAT_COOLDOWN_HOURS (12):
    S ×= HEAT_RECALL_GROWTH (1.5)
```

The 12-hour cooldown is what makes this *spaced* repetition: ten retrievals in one afternoon count once, but returning to a topic across days multiplies $S$. And since $S$ divides $\Delta T$ in the exponent, a hardened memory cools dramatically slower.

## The reference curve

With $\tau = 24h$ and $S = 1$: heat falls to $e^{-1} \approx 36.8\%$ of $I_{\text{base}}$ after 24 hours.

| Chain ($I_{base}=0.8$) | After 12h | 24h | 48h | 7d |
|---|---|---|---|---|
| Never re-accessed ($S=1$) | 0.49 | 0.29 | 0.11 | 0.0007 |
| Recalled twice, spaced ($S=2.25$) | 0.64 | 0.51 | 0.33 | 0.036 |

## The two thresholds

Heat is compared against two gates, creating three lifecycle bands:

```
H ≥ 0.3   → PROMOTE     knowledge extracted into the LTM graph
0.1–0.3   → DORMANT     stays searchable in MTM, keeps decaying
H < 0.1   → FREEZE      archiver candidate ("freezing point")
```

- Promotion threshold: `MEMORY_OS_MTM_HEAT_THRESHOLD`, pipeline default **0.3** (the `.env.example` ships **0.8** for conservative production promotion)
- Freezing point: `MTM_FREEZING_POINT` (default **0.1**), evaluated by the archiver only for chains idle longer than `MTM_ARCHIVE_SCAN_DAYS` (default **7 days**)

The promoter recomputes every active chain's heat on each pass (30-minute ticker), so heat is never stored stale for the promotion decision; the persisted `heat_score` is a snapshot for observability and archiving.

!!! tip "Tuning intuition"
    - Raise `HEAT_DECAY_TAU_HOURS` to make *all* memory stickier; raise the promotion threshold to make the graph more selective. They pull in opposite directions on graph write volume.
    - `HEAT_WEIGHT_INTRINSIC` vs `HEAT_WEIGHT_DENSITY` trades the LLM's semantic judgment against structural evidence. Automation-heavy tenants often deserve more density weight.
    - Watch the `memos_heat_score_distribution` histogram before and after any change ([Metrics](../reference/metrics.md)).
