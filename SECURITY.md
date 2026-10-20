# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**. Do not open a public issue or pull request.

Include what an attacker can do, the affected version or commit, and the steps to reproduce. We will acknowledge the report, keep you informed while we work on a fix, and credit you in the published advisory unless you prefer otherwise.

## Supported versions

Security fixes are released for the latest minor version. Published advisories are listed under the repository's [Security advisories](https://github.com/Prescott-Data/athena/security/advisories).

## Deployment guidance

Athena stores conversations and the knowledge derived from them, so treat it as a data store: keep it on a private network, enable authentication (Athena refuses to start without it unless explicitly told otherwise), and give each caller the narrowest key it needs. See [Security Model](docs/concepts/security-model.md).
