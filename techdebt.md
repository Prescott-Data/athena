# Technical Debt: Go Evaluation Framework

This document outlines the current technical debt in the Go-based evaluation framework located in the `/eval` directory.

## Summary

The Go evaluation framework is a partial port of the more feature-complete Python version found in `/MemoryOS/eval/`. The primary goal is to replicate the Python logic in Go, but several key components are currently missing and have been stubbed out to allow for initial scaffolding of the code.

## Missing Functionality

The main pieces of missing functionality are related to vector embeddings and vector search:

1.  **Vector Embeddings:** The Python code uses the `sentence-transformers` library to convert text into numerical vector embeddings that capture semantic meaning. The Go implementation currently has no equivalent for this. The functions that require embedding generation are stubbed.

2.  **Vector Search:** The Python code uses the `faiss` library for efficient similarity search on these vectors to find relevant memories. The Go implementation also lacks a direct equivalent for this, and the search functions are currently stubbed.

## Current State (Stubs)

Placeholder functions (stubs) have been put in place in the Go code so that the program can compile and the overall structure can be worked on. These functions log a message indicating that they are not implemented and return nil or default values. Examples of this can be found in `eval/long_term_memory.go` and `eval/mid_term_memory.go`.

## Proposed Solution

To resolve this technical debt and complete the Go evaluation framework, the following steps are recommended:

1.  **Vector Search:** The project structure contains `infrastructure/milvus/` and `internal/database/milvus.go`, which strongly indicates that **Milvus** is the intended solution for vector search. The next step is to integrate the Milvus client into the `mid_term_memory.go` and `long_term_memory.go` files.

2.  **Vector Embeddings:** A strategy for generating embeddings in Go needs to be decided upon. The main options are:
    *   Find and integrate a native Go library for sentence embeddings.
    *   Create a separate microservice (e.g., in Python) that provides embeddings via an API.
    *   Use a third-party embedding API (e.g., OpenAI, Cohere).

This decision needs to be made before the implementation of the memory tiers can be completed.
