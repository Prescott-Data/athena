# ArangoDB Infrastructure Specification for Athena (Memory OS)

This document outlines the infrastructure, deployment, and operational requirements for the ArangoDB instance backing Athena's Long-Term Personal Memory (LTPM) and Graph Analytics.

## 1. Deployment Topology

### 1.1 Edition
The system uses the **ArangoDB Community Edition**. 
*Note: Because we are on the Community Edition, Enterprise features like SmartGraphs or SatelliteGraphs are unavailable. The application logic is designed to work within Community graph constraints.*

### 1.2 Clustering & Scaling
- The database can be deployed as a single instance or a standard cluster depending on data volume.
- Since SmartGraphs are not available, cross-shard graph traversals in a clustered environment may incur network hops. The `memory-os` application assumes standard Community Edition graph behavior.

## 2. Graph Analytics & Kubernetes Execution

### 2.1 The Pregel Problem
Athena employs ArangoDB's Pregel framework to run complex distributed graph algorithms:
1.  **Label Propagation**: For community detection among loosely related cognitive entities.
2.  **Bridge Entity Calculation**: To identify concepts that connect disparate knowledge communities.

Pregel algorithms are extremely **RAM-intensive** because they load the required graph structures into memory to execute supersteps synchronously.

### 2.2 Execution Strategy (Cron Jobs)
To prevent these heavy analytics from degrading real-time conversation and storage performance:
- The graph analytics are **not** run continuously.
- Analytics are orchestrated via **Kubernetes CronJobs**.
- **Auto-Scaling**: Because Pregel is isolated to these jobs, Kubernetes can spin up heavily-provisioned pods (High RAM requests/limits) specifically for the duration of the daily cron execution, and scale them down afterward. The core ArangoDB operational nodes do not need to be permanently over-provisioned for these transient analytics bursts.

## 3. Mandatory Database Indexes

While `memory-os` handles the creation of these indexes at startup (via `ltm_setup.go:ensureIndexes`), DevOps must monitor and ensure these persistent indexes remain healthy. The system relies entirely on them to achieve $O(\log N)$ or $O(1)$ query complexity.

If these indexes are dropped or corrupted, the analytics queries will revert to $O(N)$ full-collection scans, causing severe CPU spikes and timeout errors.

### Indexed Collections
The following Document Collections represent nodes in our LTPM and must be indexed:
- `Identities`
- `Concepts`
- `Tools`
- `Projects`

### Required Persistent Indexes (RocksDB)
On *each* of the above collections, the following Persistent Indexes are enforced:
1.  **`idx_community_id`**: For fast filtering of nodes within a specific Pregel community (`FILTER doc.community_id == X`).
2.  **`idx_is_bridge`**: To quickly isolate bridge entities (`FILTER doc.is_bridge == true`).
3.  **`idx_bridge_score`**: For sorting bridge nodes by importance.
4.  **`idx_is_bridge_bridge_score` (Composite)**: Critically optimizes the combination of filtering and sorting (`FILTER doc.is_bridge == true SORT doc.bridge_score DESC`), allowing the RocksDB engine to read pre-sorted results directly from disk.

## 4. Monitoring & Alerting

Infrastructure monitoring should track the following specific ArangoDB metrics:
1.  **Memory Usage (RAM)**: Especially during the Kubernetes Pregel CronJob execution window.
2.  **RocksDB Block Cache Hit Rate**: To ensure that hot LTPM entities remain in memory.
3.  **Query Execution Time (Slow Logs)**: Any AQL query taking > 100ms should trigger an alert, as it likely indicates a missing persistent index on an analytics property.
