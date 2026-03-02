# ArangoDB Setup Guide for Athena (Memory OS)

This document provides the necessary details for setting up the ArangoDB infrastructure to support Athena's Long-Term Personal Memory (LTPM) graph. 

## 1. Application-Driven Initialization (`ltm_setup.go`)

The `memory-os` application takes care of the foundational database setup automatically on startup. When the application connects to ArangoDB, it executes the `InitializeLTMGraph` routine, which performs the following actions:

1. **Database Creation**: Creates the `athena_ltm` database if it does not already exist.
2. **Collection Provisioning**: Creates the required document collections (Nodes) and edge collections (Relationships) for the memory graph.
3. **Index Generation**: Applies persistent RocksDB indexes to specific fields to heavily optimize our graph analytics queries.

### 1.1 Collections Managed by the Application
The application ensures the following collections are present:
- **Documents (Nodes)**: `Identities`, `Concepts`, `Tools`, `Projects`, `Communities`
- **Edges (Relationships)**: `MemoryEdges`

### 1.2 Indexes Managed by the Application
To guarantee $O(1)$ or $O(\log N)$ query performance, the application programmatically enforces persistent indexes on all document collections for the following fields:
- `community_id`
- `is_bridge`
- `bridge_score`
- A composite index on `["is_bridge", "bridge_score"]`

**Infrastructure Responsibility**: Ensure the ArangoDB monitoring tracks slow queries. If a query takes > 100ms, it is an indicator that an index may have failed to build or was accidentally dropped.

---

## 2. Infrastructure Requirements
To successfully run Athena's ArangoDB workload, the infrastructure should be provisioned with the following specifications:

### 2.1 Storage & Disk I/O
ArangoDB's RocksDB storage engine is highly sensitive to disk performance, especially during graph traversals.
- **Volume Type**: SSD/NVMe backed Persistent Volumes.
- **IOPS**: Minimum baseline of 3,000 IOPS to support rapid graph traversals.
- **Storage Engine**: `RocksDB` (This is the ArangoDB default).

### 2.2 Memory & CPU Sizing
Athena utilizes ArangoDB's **Pregel** subsystem for graph analytics like Community Detection (Label Propagation). Pregel loads the graph into memory to perform calculations.

To support this without risk of Out-Of-Memory (OOM) errors:
- **Memory**: Provision at least 16Gi RAM per DBServer pod, with limits set up to 32Gi.
- **CPU**: 4 to 8 vCPUs per pod.
- **RocksDB Cache**: The `--rocksdb.block-cache-size` should be configured to use approximately 50% of the pod's available memory limit.

### 2.3 Analytics Execution Strategy
To maintain real-time chat performance in Athena, the heavy Pregel analytics jobs do not run continuously. They are orchestrated as Kubernetes CronJobs that run during off-peak hours (e.g., 03:00 UTC daily). The infrastructure should be prepared to handle burst load during these scheduled windows.

---

## 3. Authentication Setup
Create a dedicated service account for the application to connect:
1. **User**: `athena_svc`
2. **Permissions**: Grant Read/Write (`rw`) access specifically to the `athena_ltm` database.
