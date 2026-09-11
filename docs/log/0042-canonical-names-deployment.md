# Canonical names deployment

Date: 2026-09-10

Status: historical implementation and deployment record. Current commands and
operations remain documented in the [deployment guide](../deployment.md),
[refresh guide](../refresh.md), and [routing guide](../routing-scout.md).

Scope: removal of the naming-migration aliases introduced in
[0041](0041-naming-and-organization-cleanup.md), continuous integration, and
local service verification. Source baseline: `18297f0`.

## Compatibility removal and CI

The server no longer accepts `-routing-scout`, `-scout-selection`, or
`-scout-cache-mib`. Their canonical replacements are `-routing-snapshot`,
`-routing-selection`, and `-routing-cache-mib`. Lookup responses no longer emit
`X-OpenMaps-Dataset`, and health no longer contains `dataset`; clients must use
`X-OpenMaps-Lookup-Snapshot` and `lookup_snapshot`.

`.github/workflows/ci.yml` now checks Go formatting, repository whitespace,
maintained JSON parsing, `go test ./...`, and `go vet ./...` on pushes and pull
requests. The workflow was parsed and every command was run locally before
deployment.

## Local service migration

The installed `com.openmaps.regional` and `com.openmaps.national` launchd jobs
were confirmed active with the old routing flags. Their original property lists
were retained as `*.plist.pre-canonical-20260910` in
`~/Library/LaunchAgents/`. Both jobs now use the separately built
`data/scout-cutover-20260909/bin/server-canonical-20260910`, whose SHA-256 is
`26dcf1a44330a5410270c49c2c60e9fa1e05c0e2947374d42ed7ff6dfaf58e5d`,
and only canonical routing flags.

The regional service on port 8096 was stopped gracefully, unloaded, loaded, and
started before the national service on port 8097 received the same rolling
restart. Both returned healthy with their original routing snapshot identities,
the original lookup fingerprint, `lookup_snapshot`, and no `dataset` field.

## Replacement and rollback verification

An isolated server on port 8106 changed from the retained regional prepared
snapshot `a95fc11c45adee7793394ef9e76d388dd7fea7464cc602098d1e5a9da1a2a7dd`
to the independently indexed snapshot
`31ff34afa3db8e754632d9dbb4ffd632be7b47c7a237c8b9da54511a713b26d3`
and back. A Newport route retained the same selected distance, duration, and
geometry hash
`a86ad00da91f3944af4df6ca10f194bbf19058c88995d7477b15278d255ea36e`
before, during, and after replacement. The isolated selection and process were
then removed.

The retained July database was considered for lookup replacement but correctly
failed current identity-history validation and was not activated. A fresh
candidate built from the current pinned bundle had zero entity, relationship,
query, or identity-history changes and a distinct database SHA-256 of
`2ef1a511dca2d1c446ba5d9e5cd66336a431ab495fb480d096e14d9149b2d94a`.
Both live services selected it, passed the Places integration suite and all 26
geocoding cases, then rolled back to the original lookup fingerprint
`5cce6cf3a97b65ead56fc1da5cf810470a9f1f0197746503ab8faf7d2b5242b8`.
The original selection document was restored byte-for-byte and temporary
candidate/review files were removed.

Routine `go test ./...`, `go vet ./...`, formatting, whitespace, maintained JSON,
workflow syntax, live health, installed launchd arguments, and removed-interface
checks all passed.
