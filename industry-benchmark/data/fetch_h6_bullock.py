"""
H6 Data Fetcher — Michele Bullock (Reserve Bank of Australia)
==============================================================
Scans GDELT 2.0 GKG files for Michele Bullock mentions, splits them into
pre-reversal (stable belief) and post-reversal (contradiction) article sets,
fetches article text, and saves structured JSON ready for the H6 ingestor.

Output:
    data/h6/belief_building/  -- up to 20 stable articles (pre-reversal)
    data/h6/contradiction/    -- best 1 reversal article
    data/h6/manifest.json     -- summary of what was collected

Usage:
    python fetch_h6_bullock.py
"""

import csv
import gzip
import io
import json
import os
import re
import time
import zipfile
from collections import defaultdict
from datetime import datetime, timedelta, timezone
from pathlib import Path

import requests

# ── Config ────────────────────────────────────────────────────────────────────

GDELT_MASTER   = "http://data.gdeltproject.org/gdeltv2/masterfilelist.txt"
TARGET_ENTITY  = "Michele Bullock"

# Date range from scanner results
SCAN_START     = "20260311"
FIRST_REVERSAL = "20260315"   # stable = before this, reversal = from this

TARGET_STABLE_COUNT   = 20
TARGET_REVERSAL_COUNT = 1

OUTPUT_DIR = Path(__file__).parent / "h6"

# GKG column indices (V2.1, tab-delimited, 0-based)
GKG_RECORDID   = 0   # GKGRECORDID — first 14 chars = timestamp
GKG_SOURCECURL = 4   # V2DOCUMENTIDENTIFIER (article URL)
GKG_THEMES     = 7   # V1THEMES
GKG_PERSONS    = 11  # V1PERSONS
GKG_ENH_PERSONS= 12  # V2ENHANCEDPERSONS
GKG_TONE       = 15  # V1.5TONE

csv.field_size_limit(10 * 1024 * 1024)

HEADERS = {
    "User-Agent": (
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
    )
}

# ── Helpers ───────────────────────────────────────────────────────────────────

def get_gkg_urls(start_date: str) -> list[str]:
    print("Fetching GDELT master file list...", flush=True)
    resp = requests.get(GDELT_MASTER, timeout=30)
    resp.raise_for_status()
    cutoff = datetime.strptime(start_date, "%Y%m%d").replace(tzinfo=timezone.utc)
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
    print(f"Found {len(urls)} GKG files from {start_date} onwards.")
    return sorted(urls)


def download_gkg_rows(url: str) -> list[list[str]]:
    try:
        resp = requests.get(url, timeout=60)
        resp.raise_for_status()
        content = resp.content
        if content[:2] == b'PK':
            with zipfile.ZipFile(io.BytesIO(content)) as zf:
                with zf.open(zf.namelist()[0]) as f:
                    text = f.read().decode("utf-8", errors="replace")
        else:
            with gzip.open(io.BytesIO(content), "rt", encoding="utf-8", errors="replace") as f:
                text = f.read()
        return list(csv.reader(io.StringIO(text), delimiter="\t"))
    except Exception as e:
        print(f"  [WARN] {url.split('/')[-1]}: {e}")
        return []


def entity_in_row(row: list[str]) -> bool:
    """Check if TARGET_ENTITY appears in the persons fields."""
    persons_raw = ""
    if len(row) > GKG_PERSONS:
        persons_raw += row[GKG_PERSONS].lower()
    if len(row) > GKG_ENH_PERSONS:
        persons_raw += row[GKG_ENH_PERSONS].lower()
    target = TARGET_ENTITY.lower()
    # Match "michele bullock" with optional variations
    return target in persons_raw or "bullock" in persons_raw and "michele" in persons_raw


def parse_tone(field: str) -> float:
    try:
        return float(field.split(",")[0])
    except (ValueError, IndexError, AttributeError):
        return 0.0


def get_date_from_row(row: list[str]) -> str:
    """Extract YYYYMMDD from GKGRECORDID (first field, first 8 chars)."""
    try:
        return row[GKG_RECORDID][:8]
    except IndexError:
        return "00000000"


def strip_html(html: str) -> str:
    """Very basic HTML stripping — removes tags and collapses whitespace."""
    text = re.sub(r"<script[^>]*>.*?</script>", " ", html, flags=re.DOTALL | re.IGNORECASE)
    text = re.sub(r"<style[^>]*>.*?</style>", " ", text, flags=re.DOTALL | re.IGNORECASE)
    text = re.sub(r"<[^>]+>", " ", text)
    text = re.sub(r"&nbsp;", " ", text)
    text = re.sub(r"&amp;", "&", text)
    text = re.sub(r"&lt;", "<", text)
    text = re.sub(r"&gt;", ">", text)
    text = re.sub(r"\s+", " ", text)
    return text.strip()


def fetch_article_text(url: str) -> str | None:
    """Attempt to fetch and extract readable text from an article URL."""
    try:
        resp = requests.get(url, headers=HEADERS, timeout=15, allow_redirects=True)
        if resp.status_code != 200:
            return None
        text = strip_html(resp.text)
        # Keep only paragraphs likely to be article body (> 100 chars)
        sentences = [s.strip() for s in text.split(".") if len(s.strip()) > 80]
        body = ". ".join(sentences[:30])  # first 30 meaningful sentences
        return body if len(body) > 200 else None
    except Exception:
        return None


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    (OUTPUT_DIR / "belief_building").mkdir(exist_ok=True)
    (OUTPUT_DIR / "contradiction").mkdir(exist_ok=True)

    urls = get_gkg_urls(SCAN_START)

    # Pass 1: collect all Michele Bullock mentions with metadata
    stable_candidates   = []  # before FIRST_REVERSAL
    reversal_candidates = []  # from FIRST_REVERSAL onwards

    total = len(urls)
    for i, url in enumerate(urls, 1):
        fname = url.split("/")[-1]
        print(f"  [{i}/{total}] scanning {fname}...", end="\r", flush=True)
        for row in download_gkg_rows(url):
            if len(row) <= GKG_TONE:
                continue
            if not entity_in_row(row):
                continue
            article_url = row[GKG_SOURCECURL] if len(row) > GKG_SOURCECURL else ""
            if not article_url.startswith("http"):
                continue
            date = get_date_from_row(row)
            tone = parse_tone(row[GKG_TONE]) if len(row) > GKG_TONE else 0.0
            themes = row[GKG_THEMES] if len(row) > GKG_THEMES else ""
            record = {
                "date":        date,
                "url":         article_url,
                "tone":        tone,
                "themes":      themes[:200],
                "file":        fname,
            }
            if date < FIRST_REVERSAL:
                stable_candidates.append(record)
            else:
                reversal_candidates.append(record)

    print(f"\nFound {len(stable_candidates)} stable + {len(reversal_candidates)} reversal mentions.")

    # Deduplicate by URL
    def dedup(records):
        seen = set()
        out = []
        for r in records:
            if r["url"] not in seen:
                seen.add(r["url"])
                out.append(r)
        return out

    stable_candidates   = dedup(stable_candidates)
    reversal_candidates = dedup(reversal_candidates)

    # Sort: stable by date asc (spread across time), reversal by tone asc (most negative = sharpest contradiction)
    stable_candidates.sort(key=lambda r: r["date"])
    reversal_candidates.sort(key=lambda r: r["tone"])

    print(f"After dedup: {len(stable_candidates)} stable, {len(reversal_candidates)} reversal unique URLs.")
    print()

    # Pass 2: fetch article text for top candidates
    manifest = {
        "entity":          TARGET_ENTITY,
        "scan_start":      SCAN_START,
        "first_reversal":  FIRST_REVERSAL,
        "belief_building": [],
        "contradiction":   [],
    }

    # -- Stable / belief-building articles
    print(f"Fetching up to {TARGET_STABLE_COUNT} stable articles...")
    collected = 0
    for rec in stable_candidates:
        if collected >= TARGET_STABLE_COUNT:
            break
        print(f"  [{collected+1}/{TARGET_STABLE_COUNT}] {rec['url'][:80]}...", flush=True)
        text = fetch_article_text(rec["url"])
        if not text:
            print(f"    [SKIP] could not fetch")
            continue
        idx = collected + 1
        fname = f"stable_{idx:02d}_{rec['date']}.json"
        payload = {
            "index":    idx,
            "date":     rec["date"],
            "url":      rec["url"],
            "tone":     rec["tone"],
            "themes":   rec["themes"],
            "text":     text,
            "entity":   TARGET_ENTITY,
            "type":     "stable",
        }
        with open(OUTPUT_DIR / "belief_building" / fname, "w") as f:
            json.dump(payload, f, indent=2)
        manifest["belief_building"].append({"file": fname, "date": rec["date"], "url": rec["url"]})
        collected += 1
        time.sleep(0.5)  # be polite to article servers

    print(f"Collected {collected} stable articles.")
    print()

    # -- Reversal / contradiction article
    print(f"Fetching reversal article...")
    for rec in reversal_candidates:
        print(f"  Trying: {rec['url'][:80]}...", flush=True)
        text = fetch_article_text(rec["url"])
        if not text:
            print(f"  [SKIP] could not fetch")
            continue
        payload = {
            "index":  1,
            "date":   rec["date"],
            "url":    rec["url"],
            "tone":   rec["tone"],
            "themes": rec["themes"],
            "text":   text,
            "entity": TARGET_ENTITY,
            "type":   "reversal",
        }
        fname = f"reversal_01_{rec['date']}.json"
        with open(OUTPUT_DIR / "contradiction" / fname, "w") as f:
            json.dump(payload, f, indent=2)
        manifest["contradiction"].append({"file": fname, "date": rec["date"], "url": rec["url"]})
        print(f"  [OK] saved {fname}")
        break

    # -- Save manifest
    with open(OUTPUT_DIR / "manifest.json", "w") as f:
        json.dump(manifest, f, indent=2)

    print()
    print("=" * 70)
    print("H6 DATA FETCH COMPLETE")
    print("=" * 70)
    print(f"  Stable articles   : {len(manifest['belief_building'])}")
    print(f"  Reversal articles : {len(manifest['contradiction'])}")
    print(f"  Output directory  : {OUTPUT_DIR}")
    print()
    if len(manifest["belief_building"]) < TARGET_STABLE_COUNT:
        print(f"  [WARN] Only got {len(manifest['belief_building'])}/{TARGET_STABLE_COUNT} stable articles.")
        print("         Many article sites block scrapers — this is expected.")
        print("         The ingestor will use whatever is available.")
    print("=" * 70)


if __name__ == "__main__":
    main()
