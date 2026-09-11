# Naming and organization cleanup

Date: 2026-09-10

Status: historical implementation record. Current paths and behavior are
documented in the [configuration guide](../../config/README.md),
[refresh guide](../refresh.md), [routing guide](../routing-scout.md), and
[deployment guide](../deployment.md).

Scope: repository naming and maintained-input ownership after the audit of the
Places, geocoding, routing, and API domains.

The Places/geocoding snapshot selector moved from the broad `internal/dataset`
package to `internal/placesgeocoding/snapshots`. Its public identity is now
`lookup_snapshot` and `X-OpenMaps-Lookup-Snapshot`; the former `dataset` health
field and `X-OpenMaps-Dataset` header remain compatibility aliases during
migration. Routing selection remains independent.

The three lookup maintenance commands now state their domain:
`places-geocoding-prepare`, `places-geocoding-import`, and
`places-geocoding-refresh`. The source-acceptance operation is named
`-accept-reviewed-source-update` and requires `-fetch`. Server routing flags now
use the consistent `routing-*` prefix. The old `-routing-scout`,
`-scout-selection`, and `-scout-cache-mib` spellings remain deprecated aliases
for existing deployments.

Routing inputs now live under `config/routing/`. The national acquisition plan
contains only acquisition fields and is named
`scout-national-acquisition.json`; its decoder rejects unknown and trailing
fields. Landmark seeds and maintained route cases are operational inputs in the
same directory. Historical snap probes moved beside the qualification report.
The opt-in Bremen integration test uses a stable fixture under
`internal/routing/testdata/scout-bremen/` rather than consuming a historical log
file directly.

The deployment guide was reduced to a reusable runbook. Machine-specific
September ports, launchd labels, binaries, and rollback evidence remain in the
historical Scout cutover log. The unrelated duplicate `0039` log number was
resolved by assigning the earlier domain-layout record number `0040`; this
cleanup is record `0041`.

Verification covered `go test ./...`, `go vet ./...`, JSON parsing of all moved
inputs, stale maintained-path searches, and `git diff --check`.
