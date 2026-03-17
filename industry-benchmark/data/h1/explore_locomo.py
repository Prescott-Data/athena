"""
H1 — LoCoMo Dataset Explorer
==============================================
Run this BEFORE writing the ingestor.
Goal: understand the raw LoCoMo structure and identify:
  - Chain A candidates: conversation threads where the SAME fact appears across >= 10 turns
  - Chain B candidates: throwaway single-turn QA questions (category 3 = commonsense/inference)

Reads directly from GitHub — no download needed.

Run:
  python explore_locomo.py

LoCoMo structure (confirmed from source):
  raw               -> list of 10 conversations
  conversation[i]   -> {
      "qa": [...],
      "conversation": {
          "speaker_a": str,
          "speaker_b": str,
          "session_1_date_time": str,
          "session_1": [ {"speaker": str, "dia_id": str, "text": str}, ... ],
          "session_2_date_time": str,
          "session_2": [...],
          ...
      },
      "session_summary": { "session_1_summary": str, ... },
      "sample_id": str
  }
  qa item -> {"question": str, "answer": str|int, "evidence": [...], "category": int}
  QA categories: 1=factual, 2=temporal, 3=commonsense, 4=multi-hop, 5=adversarial(no answer)
"""

import json
import requests
import pandas as pd

LOCOMO_URL = "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"

# ---------- Load ----------
try:
    print(f"Fetching LoCoMo from {LOCOMO_URL} ...")
    response = requests.get(LOCOMO_URL, timeout=30)
    response.raise_for_status()
    raw = response.json()
    print(f"Loaded {len(raw)} conversations ({len(response.content):,} bytes)\n")
except requests.exceptions.ConnectionError as e:
    raise SystemExit(f"Network error: {e}")
except requests.exceptions.Timeout as e:
    raise SystemExit(f"Timed out after 30s: {e}")
except requests.exceptions.HTTPError as e:
    raise SystemExit(f"HTTP {response.status_code}: {e}")
except json.JSONDecodeError as e:
    raise SystemExit(f"Invalid JSON: {e}")

# ---------- STEP 1: Flatten all turns into a DataFrame ----------
print("=" * 60)
print("STEP 1 — All turns flattened")
print("=" * 60)

rows = []
for conv_idx, entry in enumerate(raw):
    convo = entry["conversation"]
    speaker_a = convo.get("speaker_a", "")
    speaker_b = convo.get("speaker_b", "")

    session_nums = sorted(set(
        int(k.split("_")[1])
        for k in convo
        if k.startswith("session_") and k.split("_")[1].isdigit() and not k.endswith("_time")
    ))

    for snum in session_nums:
        date = convo.get(f"session_{snum}_date_time", "")
        turns = convo.get(f"session_{snum}", [])
        for turn in turns:
            rows.append({
                "conv_idx":    conv_idx,
                "sample_id":   entry.get("sample_id", conv_idx),
                "speaker_a":   speaker_a,
                "speaker_b":   speaker_b,
                "session":     snum,
                "session_date": date,
                "dia_id":      turn.get("dia_id", ""),
                "speaker":     turn.get("speaker", ""),
                "text":        turn.get("text", ""),
            })

df = pd.DataFrame(rows)
print(f"Total turns: {len(df)}")
print(f"Columns: {list(df.columns)}\n")
print("First 5 rows:")
print(df[["conv_idx", "session", "session_date", "speaker", "text"]].head(5).to_string())

# ---------- STEP 2: Sessions and turns per conversation ----------
print()
print("=" * 60)
print("STEP 2 — Sessions and turns per conversation")
print("=" * 60)
summary = (
    df.groupby("conv_idx")
    .agg(
        speaker_a=("speaker_a", "first"),
        speaker_b=("speaker_b", "first"),
        num_sessions=("session", "nunique"),
        total_turns=("text", "count"),
    )
    .reset_index()
)
print(summary.to_string(index=False))

# ---------- STEP 3: QA pairs ----------
print()
print("=" * 60)
print("STEP 3 — QA pairs and categories")
print("=" * 60)

CATEGORY_LABELS = {
    1: "single-hop factual",
    2: "temporal",
    3: "commonsense / inference",
    4: "multi-hop",
    5: "adversarial (no answer)",
}

qa_rows = []
for conv_idx, entry in enumerate(raw):
    for qa in entry.get("qa", []):
        qa_rows.append({
            "conv_idx": conv_idx,
            "category": qa.get("category"),
            "label":    CATEGORY_LABELS.get(qa.get("category"), "unknown"),
            "question": qa.get("question", ""),
            "answer":   str(qa.get("answer", "N/A")),
        })

qa_df = pd.DataFrame(qa_rows)
print(f"Total QA pairs: {len(qa_df)}")
print(f"\nBy category:")
print(qa_df.groupby(["category", "label"]).size().reset_index(name="count").to_string(index=False))

print()
print("Sample from each category (conv 0):")
for cat in sorted(qa_df["category"].unique()):
    sample = qa_df[(qa_df["conv_idx"] == 0) & (qa_df["category"] == cat)].head(2)
    label = CATEGORY_LABELS.get(cat, "")
    print(f"\n  Category {cat} — {label}:")
    for _, row in sample.iterrows():
        print(f"    Q: {row['question']}")
        print(f"    A: {row['answer']}")

# ---------- STEP 4: Chain A candidates ----------
print()
print("=" * 60)
print("STEP 4 — Chain A candidates (conversations with most sessions)")
print("         We need >= 10 turns referencing the SAME persistent fact")
print("=" * 60)

top_convs = summary.sort_values("num_sessions", ascending=False).head(5)
print(top_convs[["conv_idx", "speaker_a", "speaker_b", "num_sessions", "total_turns"]].to_string(index=False))

# Show session summaries for the richest conversation
best_conv_idx = int(top_convs.iloc[0]["conv_idx"])
best_entry = raw[best_conv_idx]
print(f"\nSession summaries for conversation {best_conv_idx} ({best_entry['conversation']['speaker_a']} & {best_entry['conversation']['speaker_b']}):")
for key, summary_text in sorted(best_entry.get("session_summary", {}).items()):
    print(f"\n  [{key}]")
    print(f"  {summary_text[:200]}")

# ---------- STEP 5: Chain B candidates ----------
print()
print("=" * 60)
print("STEP 5 — Chain B candidates (single throwaway questions)")
print("         Best source: category 3 (commonsense/inference) — one-off, no follow-up")
print("=" * 60)
chain_b_candidates = qa_df[qa_df["category"] == 3].head(10)
print(chain_b_candidates[["conv_idx", "question", "answer"]].to_string(index=False))

# ---------- SUMMARY ----------
print()
print("=" * 60)
print("SUMMARY — What to pick for H1")
print("=" * 60)
print(f"  Dataset: {len(raw)} conversations, {len(df)} total turns, {len(qa_df)} QA pairs")
print()
print("  CHAIN A — pick one of these (most sessions = most persistent facts):")
for _, row in top_convs.head(3).iterrows():
    print(f"    conv_idx={int(row['conv_idx'])}: {row['speaker_a']} & {row['speaker_b']} — {int(row['num_sessions'])} sessions, {int(row['total_turns'])} turns")
print()
print("  CHAIN B — pick one category-3 QA pair from the list above")
print("    It becomes a single StoreInteraction: userMessage=question, agentResponse=answer")
print()
print("  NOISE   — 10,000 synthetic turns generated by the ingestor (no dataset needed)")
