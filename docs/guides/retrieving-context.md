# Retrieving Context

An LLM has no memory of its own: every generation starts from whatever you put in the prompt. When Athena holds an agent's memory, each turn therefore begins with a read. **Context** is what that read returns: the material an agent needs in front of it before it can respond well. In Athena it has two parts:

- **The conversation window**: the last several events of *this* conversation, verbatim, so the agent can follow what is being said right now.
- **Relevant memories**: summaries of *past* topics that bear on the current message, so the agent can say "as we discussed last week..." and mean it.

`GetContext` delivers both in one call. The intended rhythm is one read per turn: fetch context, build the prompt, generate, [store the exchange](storing-memory.md), repeat. This guide covers the call's two modes, what comes back, and how to assemble it into a prompt.

## The call

```bash
curl "http://localhost:8080/api/v1/sessions/$SESSION/context?limit=10&query=database%20migration"
```

| Parameter | Default | Meaning |
|---|---|---|
| `limit` | 10 | Max STM events returned |
| `query` | none | If present, MTM pages are selected semantically instead of by recency |
| `include_segments` | false | Reserved for segment output <span class="nx-badge nx-badge-beta">Planned</span> |

## The response

```json
{
  "stm_events": [ {"role": "user", "type": "message", "content": "...", "timestamp": "..."} ],
  "relevant_pages": [ {"summary": "...", "topic": "...", "similarity": 0.83} ],
  "segments": [],
  "user_persona": "",
  "ltpm": {"status": "not_implemented"}
}
```

- **`stm_events`**: the [STM window](../concepts/memory-tiers.md#stm-short-term-memory) in chronological order. All four event types are included, so tool observations and agent thoughts appear alongside messages.
- **`relevant_pages`**: cognitive chains from MTM. With a `query`, these are semantic matches; without one, the most recent chains.

!!! note "Fields that are not populated yet"
    `ltpm` always returns `status: "not_implemented"`, and `user_persona` is empty <span class="nx-badge nx-badge-beta">Planned</span>. To pull long-term knowledge today, use [`SearchMemory`](semantic-search.md), which enriches results from the LTM graph.

## Recency mode vs. semantic mode

**Without `query`** you get the working set: the live window plus the latest chains. Right for "continue the conversation" reads at the start of each turn.

**With `query`** the MTM selection is a vector search: the query is embedded and matched against chain embeddings in Milvus. Right when the user references something older than the window ("like we discussed last month"). Pass the user's raw message as the query; it embeds well.

## Assembling a prompt

The intended pattern, per turn:

```
1. GetContext(session, limit=10, query=<user message>)
2. System prompt
   + "Relevant past context:"  ← relevant_pages summaries (oldest first)
   + "Current conversation:"   ← stm_events verbatim
   + user message
3. Generate; then StoreInteraction(user message, agent response)
```

Two practical notes:

- **Summaries are prose.** `relevant_pages` carry LLM-written summaries designed to be pasted into prompts as-is, not re-summarized.
- **Reads warm memory.** Retrieval counts as access: returned chains get their heat recalled ([Heat & Decay](../concepts/heat-and-decay.md)), so the memories your agents actually use survive longer. This is a feature, not a side effect; do not "pre-warm" with synthetic reads.

## Reading across agents

`GetContext` is scoped to the session's agent (STM is agent-scoped). If you need memory formed by *other* agents serving the same user, use [`SearchMemory`](semantic-search.md), which searches user-wide.
