# Persistent Scout regional candidate and national acquisition

Date: 2026-09-09. Baseline: `d24c604`. Scope: implementation of a separate
coordinate-routing backend following historical [0029](0029-osm-scout-go-compatibility.md).
This is a regional milestone record, not national completion or a production
migration. Work continues toward the authorized national candidate.

## Inputs and acquisition

The working tree was clean. The user authorized a separate experimental profile,
US 50-state/DC coverage with Canadian/Mexican detours, incremental downloads,
local services and implementation; no commit, push or active deployment change.

Rechecked provider metadata and retained it in `data/scout-national-20260909/`:

| File | Bytes | SHA-256 |
| --- | ---: | --- |
| `catalog.json` | 1,117,239 | `770e6c599cfb42d4f35cbe8b7b5be0295dae45d2d25ebe93ce3ccc9c23eca362` |
| `digest.md5.bz2` | 890,414 | `9019d68065ea30241a71112c51b2c2c11b5d2ce8d30a4fe6fdb3743e859a2e6e` |
| `packages.html` | 3,127,794 | `326a60201d6a62eee4c829c23db3976c0d4b3af3fba729f7cf6c5a197342d63c` |

The full digest has 1,112 repeated identical path/checksum/timestamp records and
no conflicts. Its 2,930 package paths match the directory. The acquisition tool
accepts identical repetitions but rejects conflicts. SHA-256-pinned metadata
and full-package checksums establish byte consistency, not an attested OSM cutoff.

The RI/MA/CT regional selection has **14 packages, 432,888,021 compressed bytes**.
Its first prepared graph has **178 tiles and 1,157,890,048 padded bytes**.
The US/Canada/Mexico selection has **586 packages, 8,850,152,713 bytes**; adding
all **34 catalog-omitted packages** produces **620 packages, 8,921,997,783 bytes**.
Catalog selection does not establish dependency closure or country coverage.
The full metadata, selection plans, per-package receipts and compressed inputs
are retained; national acquisition was still running at this milestone.

Initial host observation: 16 GiB RAM, 115,484,647,424 available filesystem bytes
(approximately 107.6 GiB; free capacity changes with unrelated host activity).
Acquisition reserves ≥32 GiB free disk, with a 12 GiB compressed-input budget.
The proposed candidate spool budget is 48 GiB; graph-sized source parsing is not
performed. Heavy construction jobs use sampled RSS supervision, with a 4 GiB
abort threshold. Sampling and Go soft limits are not hard RSS guarantees.

## Implemented and checked

The maintained [Scout contract](../routing-scout.md) describes persistent streaming
preparation, flat forward/reverse restriction tables, fixed page/record caches,
explicit budgets, coordinate HTTP integration, immutable candidate leases and
replacement. The original prototype caps remain in `OpenScout`; the new prepared
backend has its own admission contract rather than removing the sample limits.

Preparation hashes and validates nested inputs, publishes atomically and retains
failed builds as unpublished evidence. Runtime verifies prepared bytes without
package decompression. Its turn lookup matches the reference on all **8,281**
regional complex rules, including **1,612** timed rules. Timed rules remain banned
at all times under the explicit experimental profile.

Geographic A* preparation audited **6,322,420 permitted retained edges**, recording
minimum **0.02162139971285482 seconds/metre**, with a downward numerical margin.
There were **1,514 references to missing edge-end tiles** in that pass; this is
neither the distinct missing-tile count nor a full topology audit. The potential
collapses reciprocal hierarchy-equivalent nodes only for its lower bound.
Ordinary traversal still preserves every road level and explicit missing errors.

Bidirectional search additionally retains backward turn history and validates
joining maneuvers. It passed **1,200 deterministic partial-endpoint combinations**
with and without simple/complex restrictions, and independent source-path walks
on the real regional cases. No upstream shortcuts or hierarchy pruning are used.

The experimental service ran on **127.0.0.1:8096**, independent of the active
service. Health explicitly reports `national_coverage_verified: false`.
Small tests cover persisted reopen/close, corruption, failed publication,
shared admission during replacement, old-lease retirement, failed selection,
partial endpoints and synthetic dateline geometry. Race checks for the modified
routing/API packages passed at this stage. Full repository checks and broader
national/source suites remain required before completion.

## Measurements and failures

Measurements use the shared Apple M5 / 16 GiB macOS host. OS cache and unrelated
host activity were uncontrolled; acquisition can coexist with these early trials.
They are diagnostic runs, not a finalized isolated performance qualification.
Physical cold-disk behavior was not measured.

| Preparation | Elapsed seconds | Peak RSS bytes | Peak charged footprint bytes |
| --- | ---: | ---: | ---: |
| Regional streaming graph | 28.99 | 25,526,272 | 20,185,592 |
| Forward turn table | 0.88 | 84,688,896 | 82,559,552 |
| Geographic lower-bound scan | 3.86 | 153,944,064 | 151,732,800 |

The forward table has **21,860 states**, 21,859 transitions and 720,896 padded
bytes. Runtime page caches retain different resources from process RSS.

| Coordinate case | Metres | Estimated seconds | Source steps |
| --- | ---: | ---: | ---: |
| Newport: `[-71.31373108,41.49138952]` → `[-71.30830418,41.48654393]` | 1,253.6596 | 89.7354 | 47 |
| Boston–Cambridge: `[-71.0601,42.3551]` → `[-71.1190,42.3736]` | 8,700.8842 | 470.6431 | 103 |
| Newport–Boston: first origin above → Boston point above | 114,050.9150 | 4,646.5703 | 550 |

The longer route exhausted the original 200,000-label cap. With the separately
bounded prepared query limit, ordinary Dijkstra settled **954,605 states**;
one-sided A* settled **571,440**, with identical steps/costs. One A* trial took
1,228.77 ms and allocated **1,996,074,528 cumulative bytes**; process peak RSS
was **396,967,936 bytes**. The 64 MiB graph cache loaded 20,979 pages and evicted
19,955, demonstrating remaining cache thrashing despite bounded retained memory.

Bidirectional search settled **436,685 states**. Its first 64 MiB trial was slower
(about 2.53 s search), so a lower state count was not called a latency win.
After removing a redundant source-node lookup and using a declared 128 MiB cache,
three route times were **1,207.536 / 1,165.936 / 1,168.599 ms**; cumulative
allocation was **1,817,590,712 bytes across all three**, and peak RSS
**451,215,360 bytes**. These three observations do not establish robust tail
latency, national performance or real-world time accuracy.

## Reproduction and remaining work

See maintained commands in [Scout routing](../routing-scout.md). For the retained
regional inputs use `-plan regional-acquisition.json` for acquisition/preparation.
Use new output directories. Optional downloaded-data verification:

```sh
OPENMAPS_SCOUT_PREPARED="$PWD/data/scout-national-20260909/regional-prepared" \
  go test -tags=integration ./internal/routing/valhallatiles \
  -run '^TestPreparedScoutRegional$' -count=1 -v
OPENMAPS_SCOUT_PREPARED="$PWD/data/scout-national-20260909/regional-prepared" \
  go test -tags=integration ./internal/api -run '^TestScoutHTTP$' -count=1 -v
```

Raw evidence includes `regional-prepare.time`, `regional-turns.time`,
`regional-potential.time`, `regional-integration*.log`, `newport-boston-*.json`,
`.time`, and acquisition logs. Tests independently walk returned edges and scan
encoded restrictions; agreement with a reference does not certify upstream OSM
completeness, access legality or a surveyed route.

Outstanding: finish national acquisition and dependency closure, audit graph
coverage, qualify useful long-distance acceleration, run all-state/border/island
and geography cases, bound and measure full responses/concurrency/replacement,
resolve source-backed failures, complete repository/race checks and publish an
honest national report. The running regional candidate is not the requested
national endpoint.
