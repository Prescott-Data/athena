"""
GDELT 2.0 Scanner — H6 Candidate Finder
========================================
Scans GDELT 2.0 GKG files to find real-world entities that:
  1. Had the same fact repeated consistently across many news sources (entrenched belief)
  2. Then had an explicit public contradiction/reversal (override event)

Run in two modes:

  Mode 1 -- peek at real GDELT theme names before filtering:
      python scan_gdelt_h6.py --peek

  Mode 2 -- full scan using real theme names discovered in peek:
      python scan_gdelt_h6.py --days 7 --top 20

  --days  : how many days of GDELT history to scan (default 7)
  --top   : how many top candidates to show (default 20)
  --peek  : download one GKG file and print the top 100 most frequent theme names
"""

import argparse
import csv
import gzip
import io
import sys
import zipfile
from collections import defaultdict
from datetime import datetime, timedelta, timezone

import requests

# GDELT GKG rows can have very large fields — raise the CSV limit
csv.field_size_limit(10 * 1024 * 1024)  # 10 MB

# ── Config ────────────────────────────────────────────────────────────────────

GDELT_MASTER = "http://data.gdeltproject.org/gdeltv2/masterfilelist.txt"

# Minimum stable mentions before a reversal to qualify as a candidate
MIN_MENTIONS_FOR_BELIEF = 15

# GKG column indices (V2.1 format, tab-delimited)
# Full spec: http://data.gdeltproject.org/documentation/GDELT-Global_Knowledge_Graph_Codebook-V2.1.pdf
GKG_DATE       = 0
GKG_SOURCECURL = 4
GKG_THEMES     = 7
GKG_PERSONS    = 11
GKG_ORGS       = 12
GKG_TONE       = 15

# Real GDELT GKG theme names discovered via --peek
# STABLE: themes that signal a repeated, entrenched fact about a leader/organisation
STABLE_THEMES: set[str] = {
    "LEADER",
    "TAX_FNCACT_PRESIDENT",
    "TAX_FNCACT_MINISTER",
    "TAX_FNCACT_EXECUTIVE",
    "TAX_FNCACT_CHIEF",
    "TAX_FNCACT_DIRECTOR",
    "TAX_FNCACT_LEADER",
    "TAX_FNCACT_LEADERS",
    "GENERAL_GOVERNMENT",
    "ELECTION",
    "EPU_POLICY_GOVERNMENT",
}

# REVERSAL: themes that signal a change, removal, or contradiction of the stable fact
REVERSAL_THEMES: set[str] = {
    "ARREST",
    "TRIAL",
    "PROTEST",
    "DELAY",
    "USPEC_UNCERTAINTY1",
    "EPU_CATS_MIGRATION_FEAR_MIGRATION",
    "KILL",
}


# ── Helpers ───────────────────────────────────────────────────────────────────

def get_latest_gkg_url() -> str:
    """Return the URL of the most recent GKG file in the master list."""
    resp = requests.get(GDELT_MASTER, timeout=30)
    resp.raise_for_status()
    latest = ""
    for line in resp.text.splitlines():
        parts = line.strip().split()
        if len(parts) >= 3 and ".gkg.csv.zip" in parts[2]:
            latest = parts[2]
    if not latest:
        raise RuntimeError("Could not find a GKG file in master list.")
    return latest


def get_gkg_urls_for_days(days: int) -> list[str]:
    """Return GKG file URLs for the last N days from the master list."""
    print("Fetching GDELT master file list...", flush=True)
    resp = requests.get(GDELT_MASTER, timeout=30)
    resp.raise_for_status()
    cutoff = datetime.now(timezone.utc) - timedelta(days=days)
    urls = []
    for line in resp.text.splitlines():
        parts = line.strip().split()
        if len(parts) < 3 or ".gkg.csv.zip" not in parts[2]:
            continue
        fname = parts[2].split("/")[-1]
        ts_str = fname.split(".")[0]
        try:
            ts = datetime.strptime(ts_str, "%Y%m%d%H%M%S").replace(tzinfo=timezone.utc)
        except ValueError:
            continue
        if ts >= cutoff:
            urls.append(parts[2])
    print(f"Found {len(urls)} GKG files for the last {days} day(s).")
    return sorted(urls)


def download_gkg_rows(url: str) -> list[list[str]]:
    """Download and parse a single GKG zip file into rows."""
    try:
        resp = requests.get(url, timeout=60)
        resp.raise_for_status()
        content = resp.content
        # GDELT files are ZIP archives containing a single CSV
        if content[:2] == b'PK':
            with zipfile.ZipFile(io.BytesIO(content)) as zf:
                inner = zf.namelist()[0]
                with zf.open(inner) as f:
                    text = f.read().decode("utf-8", errors="replace")
        else:
            with gzip.open(io.BytesIO(content), "rt", encoding="utf-8", errors="replace") as f:
                text = f.read()
        return list(csv.reader(io.StringIO(text), delimiter="\t"))
    except Exception as e:
        print(f"  [WARN] {url.split('/')[-1]}: {e}")
        return []


def parse_themes(field: str) -> list[str]:
    """Parse GDELT themes field — each theme looks like THEME_NAME,charoffset."""
    if not field:
        return []
    themes = []
    for part in field.split(";"):
        name = part.split(",")[0].strip()
        if name:
            themes.append(name)
    return themes


def parse_entities(field: str) -> list[str]:
    """Parse GDELT orgs or persons field into entity name list."""
    if not field:
        return []
    return [p.split(",")[0].strip().title() for p in field.split(";")
            if p.split(",")[0].strip() and len(p.split(",")[0].strip()) > 2]


def parse_tone(field: str) -> float:
    """Extract overall tone score (first value in comma-separated tone field)."""
    try:
        return float(field.split(",")[0])
    except (ValueError, IndexError, AttributeError):
        return 0.0


# ── Mode 1: Peek ──────────────────────────────────────────────────────────────

def peek() -> None:
    """Download the latest GKG file and print the top 100 most frequent theme names."""
    print("Fetching latest GKG file URL...", flush=True)
    url = get_latest_gkg_url()
    print(f"Downloading: {url.split('/')[-1]} ...", flush=True)
    rows = download_gkg_rows(url)
    print(f"Parsed {len(rows)} rows. Counting themes...", flush=True)

    theme_counts: dict[str, int] = defaultdict(int)
    for row in rows:
        if len(row) > GKG_THEMES:
            for theme in parse_themes(row[GKG_THEMES]):
                theme_counts[theme] += 1

    top_themes = sorted(theme_counts.items(), key=lambda x: x[1], reverse=True)[:100]

    print()
    print("=" * 70)
    print("TOP 100 GDELT GKG THEME NAMES (from latest file)")
    print("=" * 70)
    print(f"{'Rank':<6} {'Count':<8} Theme")
    print("-" * 70)
    for rank, (theme, count) in enumerate(top_themes, 1):
        print(f"  {rank:<4} {count:<8} {theme}")
    print("=" * 70)
    print()
    print("Next step:")
    print("  1. Look through the list above for themes that signal:")
    print("     - A STABLE repeated fact (e.g. technology use, leadership, policy)")
    print("     - A REVERSAL/CONTRADICTION (e.g. resignation, migration, policy change)")
    print("  2. Update REVERSAL_THEMES and STABLE_THEMES at the top of this script")
    print("  3. Run: python scan_gdelt_h6.py --days 7")
    print()


# ── Mode 2: Full Scan ─────────────────────────────────────────────────────────

def scan(days: int, top: int) -> None:
    if not REVERSAL_THEMES or not STABLE_THEMES:
        print("[ERROR] REVERSAL_THEMES and STABLE_THEMES are empty.")
        print("  Run --peek first to discover real GDELT theme names,")
        print("  then populate those sets at the top of this script.")
        sys.exit(1)

    urls = get_gkg_urls_for_days(days)
    if not urls:
        print("No GKG files found. Try increasing --days.")
        sys.exit(1)

    # entity -> list of mention dicts
    entity_mentions: dict[str, list[dict]] = defaultdict(list)

    for i, url in enumerate(urls, 1):
        fname = url.split("/")[-1]
        print(f"  [{i}/{len(urls)}] {fname}...", end="\r", flush=True)
        for row in download_gkg_rows(url):
            if len(row) <= GKG_TONE:
                continue
            themes   = set(parse_themes(row[GKG_THEMES]))
            entities = parse_entities(row[GKG_ORGS]) + parse_entities(row[GKG_PERSONS])
            tone     = parse_tone(row[GKG_TONE])
            date     = row[GKG_DATE][:8]
            src_url  = row[GKG_SOURCECURL] if len(row) > GKG_SOURCECURL else ""

            has_stable   = bool(themes & STABLE_THEMES)
            has_reversal = bool(themes & REVERSAL_THEMES)

            for entity in entities:
                entity_mentions[entity].append({
                    "date":         date,
                    "themes":       themes,
                    "tone":         tone,
                    "has_stable":   has_stable,
                    "has_reversal": has_reversal,
                    "url":          src_url,
                })

    print(f"\nScanned {len(urls)} files — {len(entity_mentions)} unique entities found.")
    print("Scoring candidates...", flush=True)

    candidates = []
    for entity, mentions in entity_mentions.items():
        stable_mentions   = [m for m in mentions if m["has_stable"]]
        reversal_mentions = [m for m in mentions if m["has_reversal"]]

        if len(stable_mentions) < MIN_MENTIONS_FOR_BELIEF or not reversal_mentions:
            continue

        reversal_dates = sorted(set(m["date"] for m in reversal_mentions))
        stable_dates   = sorted(set(m["date"] for m in stable_mentions))
        first_reversal = reversal_dates[0]
        pre_reversal   = [m for m in stable_mentions if m["date"] < first_reversal]

        if len(pre_reversal) < MIN_MENTIONS_FOR_BELIEF:
            continue

        stable_tone   = sum(m["tone"] for m in pre_reversal) / len(pre_reversal)
        reversal_tone = sum(m["tone"] for m in reversal_mentions) / len(reversal_mentions)

        stable_theme_counts   = defaultdict(int)
        reversal_theme_counts = defaultdict(int)
        for m in pre_reversal:
            for t in m["themes"] & STABLE_THEMES:
                stable_theme_counts[t] += 1
        for m in reversal_mentions:
            for t in m["themes"] & REVERSAL_THEMES:
                reversal_theme_counts[t] += 1

        sample_url = next((m["url"] for m in reversal_mentions if m["url"]), "N/A")

        candidates.append({
            "entity":            entity,
            "stable_mentions":   len(pre_reversal),
            "reversal_mentions": len(reversal_mentions),
            "stable_range":      f"{stable_dates[0]} -> {stable_dates[-1]}",
            "first_reversal":    first_reversal,
            "stable_themes":     sorted(stable_theme_counts, key=stable_theme_counts.get, reverse=True)[:3],
            "reversal_themes":   sorted(reversal_theme_counts, key=reversal_theme_counts.get, reverse=True)[:3],
            "stable_tone":       round(stable_tone, 2),
            "reversal_tone":     round(reversal_tone, 2),
            "tone_delta":        round(reversal_tone - stable_tone, 2),
            "sample_url":        sample_url,
            "score":             len(pre_reversal) * len(reversal_mentions),
        })

    candidates.sort(key=lambda c: c["score"], reverse=True)

    print()
    print("=" * 80)
    print("H6 CANDIDATE ENTITIES — GDELT 2.0 SCAN RESULTS")
    print("=" * 80)
    print(f"Showing top {min(top, len(candidates))} of {len(candidates)} candidates")
    print()

    for rank, c in enumerate(candidates[:top], 1):
        print(f"  #{rank:02d}  {c['entity']}")
        print(f"        Stable mentions (pre-reversal) : {c['stable_mentions']}")
        print(f"        Reversal mentions              : {c['reversal_mentions']}")
        print(f"        Stable date range              : {c['stable_range']}")
        print(f"        First reversal detected        : {c['first_reversal']}")
        print(f"        Stable themes                  : {c['stable_themes']}")
        print(f"        Reversal themes                : {c['reversal_themes']}")
        print(f"        Tone  stable/reversal/delta    : {c['stable_tone']} / {c['reversal_tone']} / {c['tone_delta']}")
        print(f"        Sample reversal article        : {c['sample_url'][:100]}")
        print()

    if not candidates:
        print("  No candidates found. Try increasing --days or adjusting theme filters.")

    print("=" * 80)


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Scan GDELT 2.0 for H6 candidate entities")
    parser.add_argument("--peek", action="store_true",
                        help="Download one GKG file and print real theme names (run this first)")
    parser.add_argument("--days", type=int, default=7,
                        help="Days of GDELT history to scan (default 7)")
    parser.add_argument("--top",  type=int, default=20,
                        help="Number of top candidates to display (default 20)")
    args = parser.parse_args()

    if args.peek:
        peek()
    else:
        scan(args.days, args.top)


if __name__ == "__main__":
    main()
