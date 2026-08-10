# Changelog

Releases are cut automatically when the `VERSION` file changes on the default branch; each release tags the repo and publishes a container image to GitHub Container Registry.

<div class="changelog-release" markdown>

## v0.1.4 <span class="changelog-date">June 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.4" target="_blank" rel="noopener">View release</a>
</div>

**Fixed**

- Docker container now runs as numeric UID `1000`, satisfying Kubernetes `runAsNonRoot` admission policies

**Dependencies**

- `go.mongodb.org/mongo-driver` 1.13.1 → 1.17.7 (main module and benchmark harnesses)
- `golang.org/x/crypto` bumps across benchmark and test modules

</div>

<div class="changelog-release" markdown>

## v0.1.3 <span class="changelog-date">June 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.3" target="_blank" rel="noopener">View release</a>
</div>

**Fixed**

- Docker build corrected around protobuf generation (generated files are produced at build time, never committed)

</div>

<div class="changelog-release" markdown>

## v0.1.1 / v0.1.2 <span class="changelog-date">June 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.2" target="_blank" rel="noopener">View release</a>
</div>

**Changed**

- First releases under the automated VERSION-driven pipeline: tag, GitHub Release, and GHCR image per version bump

</div>

<div class="changelog-release" markdown>

## v0.1.0 <span class="changelog-date">October 2025</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.0" target="_blank" rel="noopener">View release</a>
</div>

**Added**

- Initial release of the Memory OS: three-tier memory (STM/MTM/LTM), cognitive pipeline with chain-break detection, Ebbinghaus heat scoring, knowledge-graph promotion, gRPC + REST API, multi-tenant scoping, and optional auth (disabled by default in this release)

</div>
