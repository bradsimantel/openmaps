# Newport data refreshes

Refreshes are explicit, offline-reviewed snapshot changes. `cmd/places-geocoding-prepare` acquires
and normalizes pinned sources; `cmd/places-geocoding-refresh build` constructs a new SQLite file
with identity history; `compare` validates it and produces the review artifact.
`review` records the reviewer and findings against that artifact's SHA-256.
`activate` selects it for the server; `rollback` restores the previous selection.
There is no release discovery, scheduler, background downloader or automatic
identity matching.

The default Places/geocoding config and demo select **Overture 2026-08-19.0**
for all four source inputs. Generated databases from the former direct street
import are not compatible refresh baselines and should be rebuilt without
identity migration.
The historical candidate config selects **Overture 2026-07-22.0**, with the same
region and Overture Transportation release as its other Overture inputs. This is
an intentionally older historical rehearsal: August was already the latest
Overture release when it was prepared. The original rehearsal used the now-retired
Geofabrik street input; its logs remain historical, while the retained candidate
config now rebuilds all lookup domains from Overture 2026-07-22.0.
See the [historical investigation](log/0004-newport-refresh-investigation.md) and
[historical verification](log/0005-newport-refresh-verification.md).

## Build and validate

Run from the repository root. Retain `data/openmaps.sqlite`, its original inputs,
and `config/places-geocoding.json`. Do not overwrite them to prepare a refresh.
Basemap pins and tiles are independent and do not change with this data refresh.

```sh
go run ./cmd/places-geocoding-prepare -fetch \
  -config docs/log/0004-newport-refresh-candidate.json \
  -data data/newport-2026-07-22

go run ./cmd/places-geocoding-refresh build \
  -baseline data/openmaps.sqlite \
  -bundle data/newport-2026-07-22/newport.json \
  -config docs/log/0004-newport-refresh-candidate.json \
  -replacements docs/log/0004-newport-refresh-replacements.json \
  -candidate data/newport-2026-07-22/openmaps-reviewed.sqlite

go run ./cmd/places-geocoding-refresh compare \
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
suggestion. The deterministic representative expectations are defined by
`importer.NewportPlacesQueryChecks`; pass `-queries PATH` to compare against a
different JSON query set. Review all result positions as well as the first
result: a passing check is a smoke test, not a coverage or accuracy
certification. Raw source records and winning attribute provenance remain in
each SQLite snapshot.

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
identity mapping. Keep replacement files with the source config; a replacement
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
go run ./cmd/places-geocoding-refresh init -baseline data/openmaps.sqlite
```

Stop the fixed-database demo before reusing port 8080, then start:

```sh
go run ./cmd/server -deployment data/deployment.json
```

`-deployment` supersedes `-db`; without it, the server continues to use a fixed
SQLite file as before. `places-geocoding-refresh status` shows the selected files, while
`/healthz` reports the database actually loaded by this process.

Inspect the full comparison and provenance. Record specific findings, especially
unresolved matches, before recording a review. For this historical rehearsal:

```sh
go run ./cmd/places-geocoding-refresh review \
  -report data/newport-2026-07-22/reviewed-report.json \
  -review data/newport-2026-07-22/review.json \
  -reviewer 'Your name' \
  -reason 'Reviewed historical July changes and search results; Pearl replacement has source evidence; seven uncertain pairs remain distinct; restore August after the rehearsal.'

go run ./cmd/places-geocoding-refresh activate \
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

On the next lookup API or health request, the server detects a changed selection,
validates and opens Places and geocoding before replacing their handler. Requests
hold shared lookup leases; one request loads a changed selection while others
use the previous snapshot, and publication waits for old leases before closing
SQLite. A failed reload retains the last working handler and makes health return
HTTP 503 with an error. Scout routing has a separate immutable selection and
admission pool; lookup replacement never retires its readers. Successful API
responses include `X-OpenMaps-Lookup-Snapshot`. Check health after every switch.
Autocomplete and details are separate requests; an ID removed between
those requests can correctly return `NOT_FOUND`.

## Roll back

```sh
go run ./cmd/places-geocoding-refresh rollback
curl -fsS http://127.0.0.1:8080/healthz
go run ./cmd/places-geocoding-refresh status
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
fresh `cmd/places-geocoding-import` database as a substitute for a refresh built with history.

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
IDs, details, coordinates, and a consistent lookup snapshot identity across
requests:

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
OPENMAPS_CONFIG="$PWD/docs/log/0004-newport-refresh-candidate.json" \
OPENMAPS_REPLACEMENTS="$PWD/docs/log/0004-newport-refresh-replacements.json" \
  go test -tags=integration ./internal/importer -run TestRefreshRebuild -count=1 -v
```

For a new release, copy the Places/geocoding config to a new named file,
explicitly select releases and unchanged or deliberately reviewed bounds, and obtain trusted
catalog and regional GeoParquet-export hashes. Use the existing maintainer
`places-geocoding-prepare -accept-reviewed-source-update` operation
only to establish the new export and bundle hashes; independently fetch again
without that flag. Build, inspect churn, establish replacements, rebuild to a
new candidate path, compare and review before selecting it. Never use
`-accept-reviewed-source-update` to bypass an unexpected mismatch in an established pin.

## Independent routing snapshots

`cmd/server -deployment STATE -routing-snapshot DIRECTORY` serves lookup data and
Scout routing together. Refresh builds and compares lookup SQLite only; it does
not import or validate retired SQLite routing payloads. Existing nonempty lookup
databases can still serve their places and geocoding records. Routing-only legacy
databases are not supported lookup snapshots. No database bytes are rewritten.

`-routing-selection` watches a separate `{"directory":"prepared-directory"}` file.
The new graph and indexes are fully verified before publication. Routing leases
cover HTTP encoding; global admission spans both generations during retirement.
Failures retain the previous graph and appear in health `reload_error`.
See [Scout preparation and replacement](routing-scout.md).

The earlier SQLite graph refresh, address association, mapped overlays and
prepared rollback workflows are historical; see records
[0015](log/0015-newport-driving-routing.md) through
[0025](log/0025-bounded-routing-construction.md) in `docs/log/` for their original
investigations. They are not current setup instructions.
