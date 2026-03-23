"""
H4 — Structural Coherence: Community Detection via NetworkX
============================================================
Workaround for ArangoDB 3.12 removing Pregel (/_api/control_pregel).

Steps:
  1. Export nodes + edges from ArangoDB
  2. Build NetworkX graph
  3. Run Label Propagation community detection
  4. Write community_id back to each node in ArangoDB
  5. Compute Newman's Modularity Q-Score
  6. Compute Cluster Silhouette Score

Usage:
    python community_detection.py \
        --url http://localhost:8529 \
        --pass <password> \
        [--dry-run]
"""

import argparse
import json
import sys
import time

import networkx as nx
from networkx.algorithms import community as nx_community
from arango import ArangoClient
from sklearn.metrics import silhouette_score
import numpy as np

NODE_COLLECTIONS  = ["Concepts", "Identities", "Projects", "Tools"]
EDGE_COLLECTION   = "MemoryEdges"
DATABASE          = "athena_ltm"
BATCH_SIZE        = 5000


# ── ArangoDB helpers ──────────────────────────────────────────────────────────

def connect(url: str, password: str):
    client = ArangoClient(hosts=url)
    return client.db(DATABASE, username="root", password=password)


def _export_collection_paginated(db_factory, col: str, path: str, fields: str = "doc",
                                  max_retries: int = 20, retry_delay: int = 60):
    """Export a single collection to JSONL using key-based pagination with auto-retry.
    On connection failure, waits retry_delay seconds and reconnects — ArangoDB may have
    been OOM-killed and is restarting. Continues from the last successfully written _key.
    """
    PAGE = 10_000
    last_key = ""
    count = 0
    retries = 0
    db = db_factory()
    with open(path, "w") as f:
        while True:
            try:
                cursor = db.aql.execute(
                    f"FOR doc IN {col} FILTER doc._key > @lk SORT doc._key "
                    f"LIMIT {PAGE} RETURN {fields}",
                    bind_vars={"lk": last_key},
                    batch_size=PAGE,
                )
                batch = list(cursor)
                if not batch:
                    break
                for doc in batch:
                    f.write(json.dumps(doc) + "\n")
                last_key = batch[-1]["_key"]
                count += len(batch)
                retries = 0
                if count % 500_000 == 0:
                    print(f"      {count:,} written...", flush=True)
            except Exception as e:
                retries += 1
                if retries > max_retries:
                    raise RuntimeError(f"Gave up after {max_retries} retries at {count:,} docs") from e
                print(f"      [{count:,} written] Error: {e}. "
                      f"Retry {retries}/{max_retries} — waiting {retry_delay}s for ArangoDB to recover...",
                      flush=True)
                time.sleep(retry_delay)
                try:
                    db = db_factory()
                except Exception:
                    pass  # will retry on next loop iteration
    return count


def export_to_dir(url: str, password: str, output_dir: str):
    """Export node + edge collections to local JSONL using key-based pagination.
    Passes a db_factory so each retry gets a fresh connection.
    """
    import os
    db_factory = lambda: connect(url, password)
    os.makedirs(output_dir, exist_ok=True)
    for col in NODE_COLLECTIONS:
        path = os.path.join(output_dir, f"{col}.jsonl")
        print(f"    Exporting {col} → {path}")
        count = _export_collection_paginated(db_factory, col, path)
        print(f"      {count:,} documents written")
    edge_path = os.path.join(output_dir, "MemoryEdges.jsonl")
    print(f"    Exporting MemoryEdges → {edge_path}")
    count = _export_collection_paginated(
        db_factory, EDGE_COLLECTION, edge_path,
        fields="{_key: doc._key, _from: doc._from, _to: doc._to}",
    )
    print(f"      {count:,} edges written")


def fetch_nodes(db, verbose=True) -> dict:
    """Returns {full_id: {community_id, svm_cluster}} for all node collections."""
    nodes = {}
    for col in NODE_COLLECTIONS:
        cursor = db.aql.execute(
            f"FOR n IN {col} RETURN {{id: n._id, key: n._key, col: '{col}', community_id: n.community_id, svm_cluster: n.svm_cluster}}",
            batch_size=BATCH_SIZE,
        )
        count = 0
        for doc in cursor:
            nodes[doc["id"]] = doc
            count += 1
        if verbose:
            print(f"    Loaded {count:,} nodes from {col}")
    return nodes


def fetch_nodes_from_files(local_dir: str, verbose=True) -> dict:
    """Load nodes from local JSONL files — no ArangoDB connection needed."""
    import os
    nodes = {}
    for col in NODE_COLLECTIONS:
        path = os.path.join(local_dir, f"{col}.jsonl")
        count = 0
        with open(path) as f:
            for line in f:
                doc = json.loads(line)
                full_id = f"{col}/{doc['_key']}"
                nodes[full_id] = {
                    "id":  full_id,
                    "key": doc["_key"],
                    "col": col,
                    "community_id": doc.get("community_id"),
                    "svm_cluster":  doc.get("svm_cluster"),
                }
                count += 1
        if verbose:
            print(f"    Loaded {count:,} nodes from {path}")
    return nodes


def fetch_edges_from_file(local_dir: str, verbose=True) -> list:
    """Load edges from local JSONL file — no ArangoDB connection needed."""
    import os
    path = os.path.join(local_dir, "MemoryEdges.jsonl")
    edges = []
    with open(path) as f:
        for line in f:
            doc = json.loads(line)
            edges.append((doc["_from"], doc["_to"]))
    if verbose:
        print(f"    Loaded {len(edges):,} edges from {path}")
    return edges


def fetch_edges(db, verbose=True) -> list:
    """Returns list of (_from, _to) tuples."""
    cursor = db.aql.execute(
        f"FOR e IN {EDGE_COLLECTION} RETURN {{from: e._from, to: e._to}}",
        batch_size=BATCH_SIZE,
    )
    edges = [(e["from"], e["to"]) for e in cursor]
    if verbose:
        print(f"    Loaded {len(edges):,} edges from {EDGE_COLLECTION}")
    return edges


def write_community_ids(db, node_community: dict, dry_run=False):
    """Batch-writes community_id back to ArangoDB for each node."""
    # Group by collection
    by_col = {col: [] for col in NODE_COLLECTIONS}
    for full_id, cid in node_community.items():
        col = full_id.split("/")[0]
        key = full_id.split("/")[1]
        by_col[col].append({"_key": key, "community_id": cid})

    for col, updates in by_col.items():
        if not updates:
            continue
        if dry_run:
            print(f"    [dry-run] Would update {len(updates):,} nodes in {col}")
            continue
        collection = db.collection(col)
        total = len(updates)
        written = 0
        for i in range(0, total, BATCH_SIZE):
            batch = updates[i:i + BATCH_SIZE]
            collection.update_many(batch)
            written += len(batch)
            print(f"\r    {col}: {written:,}/{total:,}", end="", flush=True)
        print()


# ── Graph building ────────────────────────────────────────────────────────────

def build_graph(nodes: dict, edges: list, sample: int = 0) -> nx.Graph:
    if sample and sample < len(nodes):
        by_col = {}
        for full_id, doc in nodes.items():
            by_col.setdefault(doc["col"], []).append(full_id)
        import random as rng
        rng.seed(42)
        sampled = set()
        for col, ids in by_col.items():
            n = max(1, int(sample * len(ids) / len(nodes)))
            sampled.update(rng.sample(ids, min(n, len(ids))))
        print(f"    Sampled {len(sampled):,} nodes (proportional across collections)")
        nodes = {k: v for k, v in nodes.items() if k in sampled}

    G = nx.Graph()
    G.add_nodes_from(nodes.keys())
    valid = 0
    for src, dst in edges:
        if src in nodes and dst in nodes:
            G.add_edge(src, dst)
            valid += 1
    print(f"    Graph: {G.number_of_nodes():,} nodes, {G.number_of_edges():,} edges ({len(edges)-valid:,} edges skipped)")
    return G


def build_igraph_direct(local_dir: str, sample: int = 0):
    """
    Build igraph directly from JSONL files — skips NetworkX entirely.
    Uses ~half the memory of the NetworkX→igraph conversion path.
    Returns (ig_graph, node_list, node_index).
    """
    import igraph as ig
    import os
    import random

    print("    Reading nodes from JSONL files...")
    node_list = []
    node_meta = []
    for col in NODE_COLLECTIONS:
        path = os.path.join(local_dir, f"{col}.jsonl")
        with open(path) as f:
            for line in f:
                doc = json.loads(line)
                node_list.append(f"{col}/{doc['_key']}")
                node_meta.append({"key": doc["_key"], "col": col,
                                   "community_id": doc.get("community_id"),
                                   "svm_cluster": doc.get("svm_cluster")})

    if sample and sample < len(node_list):
        random.seed(42)
        indices = random.sample(range(len(node_list)), sample)
        indices_set = set(indices)
        node_list  = [node_list[i]  for i in indices]
        node_meta  = [node_meta[i]  for i in indices]
        print(f"    Sampled {len(node_list):,} nodes")

    node_index = {n: i for i, n in enumerate(node_list)}

    print(f"    {len(node_list):,} nodes indexed. Reading edges...")
    path = os.path.join(local_dir, "MemoryEdges.jsonl")
    ig_edges = []
    skipped = 0
    with open(path) as f:
        for line in f:
            doc = json.loads(line)
            src, dst = doc["_from"], doc["_to"]
            si, di = node_index.get(src), node_index.get(dst)
            if si is not None and di is not None:
                ig_edges.append((si, di))
            else:
                skipped += 1

    print(f"    {len(ig_edges):,} edges loaded ({skipped:,} skipped — endpoints outside sample)")
    print("    Building igraph...")
    G = ig.Graph(n=len(node_list), edges=ig_edges, directed=False)
    G.simplify()
    print(f"    igraph ready: {G.vcount():,} vertices, {G.ecount():,} edges")
    return G, node_list, node_meta


# ── Community detection ───────────────────────────────────────────────────────

def run_louvain_igraph(ig_graph, node_list: list):
    """Runs Louvain via igraph C engine and returns list of node-ID sets."""
    print("    Running Louvain (igraph C engine)...")
    t0 = time.time()
    partition = ig_graph.community_multilevel(weights=None)
    elapsed = time.time() - t0
    print(f"    Done in {elapsed:.1f}s — found {len(partition):,} communities")
    return [{node_list[i] for i in cluster} for cluster in partition]


def run_label_propagation(G: nx.Graph):
    """Fallback: NetworkX label propagation (only used if igraph path not taken)."""
    import igraph as ig
    print("    Building igraph from NetworkX graph...")
    t1 = time.time()
    node_list = list(G.nodes())
    node_index = {n: i for i, n in enumerate(node_list)}
    ig_edges = [(node_index[u], node_index[v]) for u, v in G.edges()]
    ig_graph = ig.Graph(n=len(node_list), edges=ig_edges, directed=False)
    print(f"    igraph built in {time.time()-t1:.1f}s")
    return run_louvain_igraph(ig_graph, node_list)


def communities_to_map(communities: list) -> dict:
    """Returns {node_id: community_index} mapping."""
    node_community = {}
    for idx, comm in enumerate(communities):
        for node in comm:
            node_community[node] = idx
    return node_community


# ── Scoring ───────────────────────────────────────────────────────────────────

def compute_modularity(G: nx.Graph, communities: list) -> float:
    return nx_community.modularity(G, communities)


def compute_silhouette(G: nx.Graph, node_community: dict) -> float:
    """
    Silhouette score using shortest-path distance as the similarity metric.
    Sampled on 2,000 nodes to keep runtime feasible on 1M node graphs.
    """
    print("    Computing Silhouette Score (2,000 node sample)...")
    all_nodes = list(node_community.keys())

    # Only sample from the largest connected component
    lcc = max(nx.connected_components(G), key=len)
    sample_pool = [n for n in all_nodes if n in lcc]
    sample_size = min(2000, len(sample_pool))
    rng = np.random.default_rng(42)
    sample = rng.choice(sample_pool, size=sample_size, replace=False).tolist()

    # Build distance matrix for the sample using BFS
    labels = [node_community[n] for n in sample]
    if len(set(labels)) < 2:
        print("    Only 1 community found — silhouette undefined")
        return float("nan")

    dist_matrix = np.zeros((sample_size, sample_size))
    for i, src in enumerate(sample):
        lengths = nx.single_source_shortest_path_length(G, src, cutoff=5)
        for j, dst in enumerate(sample):
            dist_matrix[i][j] = lengths.get(dst, 6)  # cap at 6 if unreachable

    score = silhouette_score(dist_matrix, labels, metric="precomputed")
    return score


# ── Summary ───────────────────────────────────────────────────────────────────

def print_summary(communities: list, node_community: dict, q: float, silhouette: float):
    total_nodes = len(node_community)
    sizes = sorted([len(c) for c in communities], reverse=True)
    largest_pct = sizes[0] / total_nodes * 100 if sizes else 0

    print()
    print("=" * 65)
    print("  H4 — Structural Coherence Results")
    print("=" * 65)
    print(f"  Total nodes           : {total_nodes:,}")
    print(f"  Communities found     : {len(communities):,}")
    print(f"  Largest community     : {sizes[0]:,} nodes ({largest_pct:.1f}%)")
    print(f"  Top-5 community sizes : {sizes[:5]}")
    print()
    print(f"  Newman Modularity Q   : {q:.4f}  {'✓ (>0.4)' if q > 0.4 else '✗ (<0.4)'}")
    print(f"  Silhouette Score      : {silhouette:.4f}  {'✓ (>0.5)' if not np.isnan(silhouette) and silhouette > 0.5 else '✗ (<0.5)'}")
    print()

    q_pass        = q > 0.4
    sil_pass      = not np.isnan(silhouette) and silhouette > 0.5
    comm_pass     = len(communities) >= 3
    hairball_pass = largest_pct < 60.0

    print(f"  Communities ≥ 3       : {'✓' if comm_pass else '✗'} ({len(communities)})")
    print(f"  Largest < 60% nodes   : {'✓' if hairball_pass else '✗'} ({largest_pct:.1f}%)")
    print()

    overall = q_pass and comm_pass and hairball_pass
    verdict = "PASS ✓" if overall else "FAIL ✗"
    print(f"  H4 RESULT: {verdict}")
    print("=" * 65)

    return {
        "total_nodes":       total_nodes,
        "communities_found": len(communities),
        "largest_pct":       round(largest_pct, 2),
        "modularity_q":      round(q, 4),
        "silhouette":        round(silhouette, 4) if not np.isnan(silhouette) else None,
        "q_pass":            q_pass,
        "silhouette_pass":   sil_pass,
        "communities_pass":  comm_pass,
        "hairball_pass":     hairball_pass,
        "overall_pass":      overall,
    }


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="H4 Community Detection via NetworkX")
    parser.add_argument("--url",        default="http://localhost:8529", help="ArangoDB URL")
    parser.add_argument("--pass",       dest="password", default=None,   help="ArangoDB root password (required unless --local-dir + --dry-run)")
    parser.add_argument("--local-dir",  dest="local_dir", default=None,  help="Read nodes+edges from local JSONL files instead of ArangoDB")
    parser.add_argument("--fetch-to-dir", dest="fetch_to_dir", default=None, help="Stream ArangoDB → local JSONL files in this dir, then run igraph Louvain on them (best for large in-cluster runs)")
    parser.add_argument("--sample",     type=int, default=0, help="Run on a proportional sample of N nodes (0 = full graph)")
    parser.add_argument("--dry-run",    action="store_true", help="Skip writing community_id back to ArangoDB")
    parser.add_argument("--no-silhouette", action="store_true", help="Skip silhouette score (slow on large graphs)")
    parser.add_argument("--output",     default="h4_results.json", help="Output file for results")
    args = parser.parse_args()

    print("=" * 65)
    print("H4 — Structural Coherence: Community Detection")
    print("=" * 65)
    print(f"  ArangoDB : {args.url}")
    print(f"  Database : {DATABASE}")
    print(f"  Dry run  : {args.dry_run}")
    print()

    if args.fetch_to_dir:
        # ── Export path: stream ArangoDB → JSONL, then use igraph fast path ──
        print(f"  Connecting to ArangoDB to export graph...")
        db = connect(args.url, args.password)
        print(f"  Streaming collections to {args.fetch_to_dir}/...")
        export_to_dir(db, args.fetch_to_dir)
        print()
        args.local_dir = args.fetch_to_dir  # hand off to igraph path below

    if args.local_dir:
        # ── Fast path: igraph directly from JSONL (no NetworkX, half the RAM) ──
        print(f"  (reading from local files: {args.local_dir})")
        ig_graph, node_list, node_meta = build_igraph_direct(args.local_dir, sample=args.sample)
        print()

        print("  Running community detection (Louvain / igraph C engine)...")
        communities = run_louvain_igraph(ig_graph, node_list)
        node_community = communities_to_map(communities)
        print()

        print("  Computing Modularity Q (NetworkX)...")
        # Build a minimal NetworkX graph just for Q scoring — uses node_list + ig edges
        G = nx.Graph()
        G.add_nodes_from(node_list)
        for e in ig_graph.es:
            G.add_edge(node_list[e.source], node_list[e.target])
        q = compute_modularity(G, communities)
        print(f"    Q = {q:.4f}")
        print()

        silhouette = float("nan")
        if not args.no_silhouette:
            silhouette = compute_silhouette(G, node_community)
            print(f"    Silhouette = {silhouette:.4f}")
            print()

        db = None
        if not args.dry_run:
            print("  Connecting to ArangoDB to write community_id...")
            db = connect(args.url, args.password)
            print("  Writing community_id back to ArangoDB...")
            write_community_ids(db, node_community, dry_run=False)
            print()

    else:
        # ── Live path: fetch from ArangoDB, build NetworkX graph ──
        db = connect(args.url, args.password)

        print("  Fetching nodes...")
        nodes = fetch_nodes(db)
        print(f"    Total: {len(nodes):,} nodes\n")

        print("  Fetching edges...")
        edges = fetch_edges(db)
        print()

        print("  Building NetworkX graph...")
        G = build_graph(nodes, edges, sample=args.sample)
        print()

        print("  Running community detection...")
        communities = run_label_propagation(G)
        node_community = communities_to_map(communities)
        print()

        print("  Computing Modularity Q...")
        q = compute_modularity(G, communities)
        print(f"    Q = {q:.4f}")
        print()

        silhouette = float("nan")
        if not args.no_silhouette:
            silhouette = compute_silhouette(G, node_community)
            print(f"    Silhouette = {silhouette:.4f}")
            print()

        if not args.dry_run:
            print("  Writing community_id back to ArangoDB...")
            write_community_ids(db, node_community, dry_run=False)
            print()

    results = print_summary(communities, node_community, q, silhouette)

    with open(args.output, "w") as f:
        json.dump(results, f, indent=2)
    print(f"\n  Results saved → {args.output}")


if __name__ == "__main__":
    main()
