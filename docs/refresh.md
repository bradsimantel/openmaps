# Newport data refreshes

Refreshes are explicit, offline-reviewed snapshot changes. `cmd/prepare` acquires
and normalizes pinned sources; `cmd/refresh build` constructs a new SQLite file
with identity history; `compare` validates it and produces the review artifact.
`review` records the reviewer and findings against that artifact's SHA-256.
`activate` selects it for the server; `rollback` restores the previous selection.
There is no release discovery, scheduler, background downloader or automatic
identity matching.

The original August snapshot remains the default demo and retained baseline.
The checked-in second source lock selects **Overture 2026-07-22.0**, with the same
Geofabrik 2026-08-01 streets and region. This is an intentionally older historical
rehearsal: August was already the latest Overture release when it was prepared.
See the [historical investigation](log/0004-newport-refresh-investigation.md) and
[historical verification](log/0005-newport-refresh-verification.md).

## Build and validate

Run from the repository root. Retain `data/openmaps.sqlite` and its original
inputs, `imports/newport.lock.json`, `imports/newport.bundle.sha256` and
`imports/identities.json`. Do not overwrite them to prepare a refresh. Basemap
pins and tiles are independent and do not change with this lookup refresh.

```sh
go run ./cmd/prepare -fetch \
  -manifest imports/newport-2026-07-22.lock.json \
  -data data/newport-2026-07-22 \
  -checksum imports/newport-2026-07-22.bundle.sha256

go run ./cmd/refresh build \
  -baseline data/openmaps.sqlite \
  -bundle data/newport-2026-07-22/newport.json \
  -checksum imports/newport-2026-07-22.bundle.sha256 \
  -replacements imports/newport-2026-07-22.replacements.json \
  -candidate data/newport-2026-07-22/openmaps-reviewed.sqlite

go run ./cmd/refresh compare \
  -baseline data/openmaps.sqlite \
  -candidate data/newport-2026-07-22/openmaps-reviewed.sqlite \
  -report data/newport-2026-07-22/reviewed-report.json
```

Preparation without `-fetch` verifies and uses archived local sources. All
normal commands enforce checksums; none fall back to latest. Builds refuse an
existing candidate path. Choose a new filename for another build; logical rows
are reproducible, SQLite file bytes are not guaranteed to be identical. Keep
large source files, databases, reports and review receipts in ignored `data/`.

The comparison contains:

- Baseline and candidate manifests and whole-database SHA-256 fingerprints.
- Optional routing metadata and graph payload SHA-256 on each side, making graph
  additions, changes and removal visible.
- Counts by entity kind; every added, absent and changed entity, including full
  before/after public attributes and changed-field names.
- Added/removed source keys and changes in release, raw JSON, normalized
  attributes, attribute paths, source priority and public ID.
- Added/removed relationships including their evidence; an evidence change
  appears as removal plus addition.
- Possible identity matches for review, and ordered search results on both sides.
- Validation violations. Any violation prevents review and activation.

Validation checks SQLite integrity and foreign keys, source-derived public IDs,
identity history, complete normalized FTS content and row coverage, expected
first results or empty results, and details for every returned candidate
suggestion. `imports/newport.queries.json` holds the deterministic representative
expectations. Review all result positions as well as the first result: a passing
check is a smoke test, not a coverage or accuracy certification. Raw source
records and winning attribute provenance remain in each SQLite snapshot.

## Identity and disappearance rules

Continuing source-qualified keys retain their anchor and public ID. Name,
address, coordinates and releases cannot change that ID. Continuing kinds must
also agree. Builds inherit all previous explicit mappings and append identity
history for every source key, including keys absent from the new data. A
returning historical key must use its original anchor and kind.

A record absent from the regional snapshot is unavailable: its details return
`NOT_FOUND` if no other contributing source or reviewed replacement preserves
the entity. It is removed from autocomplete and its former relationships are
absent. This does **not** assert a real-world closure or provider deletion: it
could reflect boundary movement, filtering or source coverage. Explicitly closed
businesses remain available through details and are excluded from autocomplete,
as before. Retained snapshots and identity history preserve the distinction.

A replacement file is an array of `old`, `new`, `reviewer`, and `evidence` source
key decisions. Only reviewed **one-to-one** replacement is supported:

- The old key must exist in the baseline and be absent from the candidate.
- The new key must exist in the candidate and have no prior identity history.
- Kinds must match. No source of the old entity may remain in the candidate.
- An old entity or new key cannot be used twice. Chains, remapping an existing
  identity, and unreviewed new aliases fail.

The new key gets the old entity's permanent anchor, even if that anchor's source
record is no longer present. Evidence and the cumulative history are stored in
candidate metadata; relationships are rebuilt against resolved public IDs.
The input bundle checksum remains recorded separately from this effective
identity mapping. Keep replacement files with the source locks; a replacement
is an assertion for the compared snapshots, not a reusable fuzzy rule.

Splits and merges are handled conservatively. Surviving keys keep their own IDs;
new records get separate IDs; absent ones become unavailable. Multiple old IDs
cannot redirect to a survivor, and multiple split children cannot inherit one
old ID. A reviewer may establish one continuing entity through a one-to-one
replacement when the above conditions hold. All other identities stay distinct.
There is no automatic redirect, fuzzy merge or cross-kind conflation.

The review report flags same-kind pairs within **50 metres** with an equal
normalized name, or businesses with an equal nonempty website. At least one
side must be added or absent. Comparing against surviving counterparts exposes
possible splits and merges, not just replacement pairs. These are deliberately
limited review leads, not a complete churn detector; every addition and absence
is still listed even without a lead. Shared names, sites or coordinates alone do
not authorize a merge. Resolve uncertain cases with more evidence or explicitly
retain them as distinct in the review findings.

## Review and select a snapshot

Initialize the deployment **once**, pointing at the retained baseline:

```sh
go run ./cmd/refresh init -baseline data/openmaps.sqlite
```

Stop the fixed-database demo before reusing port 8080, then start:

```sh
go run ./cmd/server -deployment data/deployment.json
```

`-deployment` supersedes `-db`; without it, the server continues to use a fixed
SQLite file as before. `refresh status` shows the selected files, while
`/healthz` reports the database actually loaded by this process.

Inspect the full comparison and provenance. Record specific findings, especially
unresolved matches, before recording a review. For this historical rehearsal:

```sh
go run ./cmd/refresh review \
  -report data/newport-2026-07-22/reviewed-report.json \
  -review data/newport-2026-07-22/review.json \
  -reviewer 'Your name' \
  -reason 'Reviewed historical July changes and search results; Pearl replacement has source evidence; seven uncertain pairs remain distinct; restore August after the rehearsal.'

go run ./cmd/refresh activate \
  -candidate data/newport-2026-07-22/openmaps-reviewed.sqlite \
  -report data/newport-2026-07-22/reviewed-report.json \
  -review data/newport-2026-07-22/review.json

curl -fsS http://127.0.0.1:8080/healthz
```

Activation checks the review fingerprint, requires the report's baseline to be
the current selection, and recomputes the entire comparison. Changing the
candidate, baseline, report or expectations invalidates the review. This is a
local operator workflow, not a cryptographic signature or authentication system.

Deployment state is a single atomically replaced, synced JSON document with
baseline, current and previous file references and checksums. An exclusive
sibling `.lock` serializes writers. A crash may leave this lock: confirm no
refresh writer is running before removing the stale lock. Never edit a selected
SQLite file in place. Archive it along with its lock, bundle and identity evidence.

On the next API or health request, the server detects a changed selection,
validates and opens Places, the geocoding address index and any optional routing
graph before replacing its handler. It holds a lock through each
lookup request; requests are serialized for this small demo. A failed reload
retains the last working handler and makes health return HTTP 503 with an error.
Successful API responses include `X-OpenMaps-Dataset`. Check health after every
switch. Autocomplete and details are separate requests; an ID removed between
those requests can correctly return `NOT_FOUND`.

## Roll back

```sh
go run ./cmd/refresh rollback
curl -fsS http://127.0.0.1:8080/healthz
go run ./cmd/refresh status
```

Rollback verifies the previous file before changing selection. It exchanges
current and previous, so a second rollback exchanges them again. It restores the
exact prior snapshot, including that snapshot's identity history. The original
baseline reference is retained across activations. Validation failures during activation or rollback
do not alter the state. If a directory sync fails after publication, the command
explicitly reports that state was published; check status and health. The command changes desired state; health confirms
whether a running server has loaded it.

Retain the candidate and its replacement decisions after rollback. For a later
refresh, compare against the currently deployed snapshot and carry forward all
applicable reviewed mappings. Historical candidate decisions must be revisited
if the same keys reappear with different coexistence or kind evidence; a rollback
does not approve a new merge. Do not discard the prior snapshot chain or treat a
fresh `cmd/import` database as a substitute for a refresh built with history.

## Verification

For geocoding, run the [retained-snapshot quality benchmark](geocoding.md#deterministic-quality-benchmark)
on baseline and candidate before activation. It checks the actual source evidence,
expected result IDs, precision, distances and coverage outcomes. After each switch,
verify forward lookup and map-click reverse lookup in the in-app browser, including
ambiguity and unsupported input. The benchmark is separate from the Places
comparison/review artifact; passing Places checks alone does not verify geocoding.

Routine checks need no network or regional files:

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
```

An explicit check against a running deployment validates autocomplete, returned
IDs, details, coordinates, and a consistent dataset fingerprint across requests:

```sh
OPENMAPS_URL=http://127.0.0.1:8080 \
  go test -tags=integration ./internal/api -run TestLiveDemo -count=1 -v
```

It does not verify browser rendering. Use the Codex desktop in-app browser plugin
for the real business and address autocomplete → details → marker flow. This
connection passed the [historical desktop verification](log/0006-desktop-browser-verification.md);
the external Chrome extension connection remains unresolved. Do not infer a
rendered map from successful HTTP responses.

To reproduce all logical candidate rows from archived inputs, use absolute paths:

```sh
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/newport-2026-07-22/openmaps-reviewed.sqlite" \
OPENMAPS_BUNDLE="$PWD/data/newport-2026-07-22/newport.json" \
OPENMAPS_CHECKSUM="$PWD/imports/newport-2026-07-22.bundle.sha256" \
OPENMAPS_REPLACEMENTS="$PWD/imports/newport-2026-07-22.replacements.json" \
  go test -tags=integration ./internal/importer -run TestRefreshRebuild -count=1 -v
```

For a new release, copy the source lock to a new named file, explicitly select
releases and unchanged or deliberately reviewed bounds, and obtain trusted
catalog/PBF hashes. Use the existing maintainer `prepare -write-lock` operation
only to establish the new export and bundle hashes; independently fetch again
without that flag. Build, inspect churn, establish replacements, rebuild to a
new candidate path, compare and review before selecting it. Never use
`-write-lock` to bypass an unexpected mismatch in an established pin.

## Optional driving graph

A new `build` can take `-routing-pbf data/rhode-island-260801.osm.pbf`. The PBF must
match the bundle manifest's existing pin. Graph construction occurs inside the
unpublished candidate; it never edits the baseline. Omitting the flag creates a
lookup-only snapshot, and comparison makes any loss of routing explicit. See
[the maintained driving profile, contract and build commands](routing.md).

Routing payloads have their own version and content checksum in `routing_graph`.
Snapshot validation checks graph integrity and connectivity references as well as
the existing lookup checks. A missing table is supported for retained snapshots;
a present but corrupt/unsupported graph fails validation and loading. Health reports
`routing_available` on both fixed-database and deployment servers. Routing requests
on retained lookup-only snapshots return 503 `UNAVAILABLE`.

Live selection loads Places, geocoding and the candidate's optional graph before
replacing any domain. Rollback restores routing availability alongside lookup data.
Existing reports for two lookup-only snapshots remain readable and reproducible;
new routing comparisons are included in the same reviewed report fingerprint.
The routing integration suite rehearses a real snapshot cycle using a temporary
state file, leaving `data/deployment.json` untouched. Building/testing a routing
candidate does not authorize activating the user's deployment.

Retained graph format 2 (`driving-distance-v2`) contains the ordinary-car limits, destination
zones and guarded snapping documented in [routing](routing.md). Format 1 remains
loadable with its retained profile; comparison shows each version and graph hash.
For current route-quality changes, use the v4 comparison suites below and a
snapshot cycle from both lookup-only and retained routing baselines. Historical
v1 → v2 → v1 profile responses were checked at the earlier milestone.
All such cycles use `t.TempDir()` deployment state. See the
[historical quality milestone](log/0016-newport-driving-quality.md) for the candidate,
rebuild/comparison checksums, source findings and evaluated differences.


Retained graph format **3** (`driving-distance-v3`) contains automatic address
endpoint evidence from the same pinned PBF. New v4 builds inherit that evidence
using the unchanged lookup bundle and existing `-routing-pbf` option. Existing Places IDs, source records
and coordinates remain unchanged. The independently checksummed SQLite payload
contains local access geometry and source references, while API code resolves
address labels at request time. Formats 1 and 2 remain readable for coordinate
routing; address requests explicitly require format 3 or later. No migration modifies a
retained snapshot. See [routing](routing.md#automatic-address-endpoints).

Run the address benchmark, coordinate/geocoding benchmarks, comparison and an
independent rebuild before review. The historical lookup-only → v3 → lookup-only
and v2 → v3 → v2 cycles used the integration snapshot cycle’s `t.TempDir()` state. Address availability, loaded profile and geocoding identities must return
to the prior state. Never exercise switching against the active deployment merely
to test a candidate. The [historical address milestone](log/0017-newport-address-routing.md)
records actual before/after evaluations and reproducibility checks.


Graph format **4** (`driving-time-v4`, cost model `estimated-driving-v1`) adds
explicit directional effective speeds, interpreted numeric ceilings and assumption
notes in the existing SQLite payload. Graph v1–v3 remains readable with its original
distance objective and no duration estimates; v3 retains address orchestration.
Unknown cost-model versions and missing/invalid costs fail loading. Comparison
reports the cost-model version as well as the graph/profile and payload checksum.

For time-model changes, run the **31-trip coordinate** and **22-case address**
suites, including the distance-versus-time comparison under one model. Build a
separate candidate and independent rebuild from the pinned PBF; verify identical
logical rows and graph payloads, stable IDs, source provenance and unchanged
restrictions. Run loading/performance checks and isolated lookup-only → v4 →
lookup-only and v3 → v4 → v3 cycles. The integration cycle uses `t.TempDir()` and
must never use the active deployment state. Health reports
`routing_duration_available` alongside routing availability. See the [historical
time-routing verification](log/0018-newport-estimated-driving-time.md) for the
source findings, route comparisons, candidate checksums and remaining uncertainty.
