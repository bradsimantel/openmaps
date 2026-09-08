# Directly loadable routing snapshots

Date: 2026-09-08. Scope: the retained Newport, buffered Oregon and full
Oregon–Washington–Idaho candidates. Baseline revision:
`74b4de20af1d9261b9fc90ef593650a84383aa11`.

This advances the [historical recursive-cell milestone](0021-recursive-junction-cell-overlay.md).
The maintained [prepared snapshot design](../routing-prepared.md) describes current
commands, field inventory, validation boundaries and limitations. The API, source
snapshots, public lookup IDs, cost model, browser and active deployment are unchanged.
No national or supplemental address data was acquired. No commit or push was made.
Evidence and artifacts use new names under ignored `data/prepared-scale/`.

## Decision and validation boundary

Persist the complete routing query representation, retaining `junction-cells-v1`,
graph/profile versions and `estimated-driving-v1`. A prepared artifact contains
numeric topology/hierarchy arrays, fixed segment and guard records, string pools,
source-node hash lookup with public/restricted flags, all four spatial indexes,
and serialized restriction/address/driveway structures. Runtime reconstructs no
adjacency, tree, forced chain, cell, destination component or association index.

`routing-prepared-le64-v1` fixes the numeric `routing-hot-le64-v2` prefix and explicit
little-endian field layouts. Source IDs, incoming-edge identities, directional
costs, restriction failure transitions, destination phases and recursive original
paths are retained. Endpoint selection still precedes and is independent of route
success/cost. Returned source strings own their memory after a mapping closes.
No lossy coordinate or cost representation was introduced.

Preparation is an explicit offline command. It validates the complete SQLite
snapshot through the importer, constructs canonical query structures from the
source graph, and binds the source fingerprint before/after validation. It writes
and syncs an immutable artifact, then publishes a separate trusted receipt last.
Independent receipt digests cover complete artifact headers and payloads. Runtime
verifies the source and artifact against that receipt, then performs bounded-scratch
structural scans. It does not infer shortcut validity from an artifact's own hash.

The receipt directory is operator-controlled publication authority, not a
cryptographic authentication mechanism. Receiving a receipt together with an
untrusted artifact does not establish trust. Semantic validation and canonical
path construction remain preparation obligations. Runtime checks dimensions,
extents, references, recursive ordering, numeric validity, node probing and
restriction/spatial structure without regenerating the graph. Unknown versions,
foreign data, damage and missing artifacts reject loading; there is no implicit
legacy fallback. Explicit `-routing-legacy-load` retains legacy readability.

Only bounded ancillary decoding remains resident for routing: metadata, restriction
maps, address evidence and driveway adjacency. The ancillary input cap is 128 MiB;
decoded Go overhead can exceed that size. Measured ancillary bytes are 7,445,728
(Newport), 1,016,361 (Oregon), and 2,498,503 (multistate). The two larger graphs have
no address coverage. National address loading will need a further representation;
this cap must not be mistaken for an unlimited national address implementation.

## Baseline, budgets and measurements

The baseline reuses the retained `data/recursive-scale/*-candidate.sqlite` and
`cache-final` artifacts. Heavy processes run serially. Measurement observers are
read-only; the baseline algorithm and numeric layout are unchanged. Source
semantic checks and construction are intertwined in the existing constructor;
that phase is explicitly reported together rather than assigned misleading
independent timings. Compressed-source checksum checks remain within decoding.
The older canonical numeric verification/mapping stage is also a combined baseline
measurement. The candidate separates mapping, integrity, ancillary decoding and
structural checking.

`budgets.json` was written before candidate evaluation: multistate startup ≤25 s,
retained Go heap ≤500 MiB, startup allocation reduction ≥70%, replacement peak
macOS footprint ≤5 GiB; Oregon startup ≤12 s; Newport startup ≤2 s. Preserve
564,380 Seattle–Boise labels, one-worker HTTP p95 below 1 s, four-worker throughput
≥12 requests/s, and no more than 25% regression in Newport/Oregon HTTP metrics.
These are measured workload targets, not guarantees for arbitrary national data.

Multistate legacy phases took 10.495 s decoding, 7.437 s provenance validation,
14.088 s semantic validation/construction, 25.094 s spatial indexing, 33.782 s
hierarchy preprocessing and 6.523 s canonical artifact verification/mapping.
This rerun took 97.421 s, compared with the prior milestone's 87.512 s observation.
It allocated 39,641,148,800 bytes cumulatively and sampled a 6,183,838,016-byte
peak Go heap. The sample interval is 10 ms; `/usr/bin/time -l` independently
records process peaks and fault counters.

| Graph | Legacy load s | Prepared load s | Legacy / prepared retained heap MiB | Legacy / prepared allocations GiB | Prepared mapped GiB | Prepared peak RSS GiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Newport | 3.344 | 0.385 | 105.87 / 11.20 | 1.947 / 0.028 | 0.216 | 0.251 |
| Oregon | 34.200 | 3.721 | 803.26 / 5.18 | 15.296 / 0.008 | 1.815 | 1.841 |
| Multistate | 97.421 | 13.547 | 1854.06 / 9.38 | 36.919 / 0.020 | 4.261 | 4.212 |

Prepared multistate phases took 0.269 s source hashing, 0.000066 s mapping,
7.659 s artifact hashing, 0.026 s ancillary decoding and 5.591 s structural scans.
Total allocations were 21,427,552 bytes, a **99.95% reduction**. Sampled peak Go
heap was 18,222,320 bytes and retained heap 9,832,368 bytes. Startup fell **86.1%**
against the measured baseline. The later performance-process load took 9.731 s;
Newport and Oregon later loads took 0.223 and 1.523 s respectively.

The prepared load process's peak macOS footprint was 26,575,664 / 17,254,440 /
28,299,128 bytes for Newport/Oregon/multistate. Those values exclude much of the
reclaimable file-backed residency represented by RSS. They do not mean a 4.26 GiB
artifact occupies only 27 MiB physically. Mapped virtual size, RSS, compressed/private
footprint and Go heap are distinct measurements; none is a hard mmap memory bound.

The input files were previously read/generated on this desktop, but cache state
and competing application residency were not controlled. Report these as
previously accessed desktop-file observations, not certified cache-cold measurements.
Integrity scans touch the entire artifact before any request. The load processes
recorded 14,261 / 119,072 / 368,593 page faults and 2,434 / 2,084 / 5,328 page
reclaims respectively. OS fault counters alone do not establish disk reads or a
cold system cache. No controlled cold-cache experiment was performed.

## Query preservation and pressure findings

The same ordinary workload uses real loopback HTTP, complete masked geometry,
actual encoding, admission exhaustion and request cancellation. No browser is
involved. The retained prior HTTP measurements provide the regression comparison.

| Graph | Workers | Prior / prepared requests/s | Prior / prepared p95 ms |
| --- | ---: | ---: | ---: |
| Newport | 1 | 3338.73 / 2997.95 | 0.528 / 0.599 |
| Newport | 4 | 12873.98 / 10653.21 | 0.668 / 0.833 |
| Oregon | 1 | 48.78 / 48.62 | 26.957 / 26.617 |
| Oregon | 4 | 150.21 / 164.31 | 199.272 / 184.743 |
| Multistate | 1 | 12.04 / 11.56 | 424.199 / 434.147 |
| Multistate | 4 | 34.07 / 35.30 | 638.500 / 563.179 |

Seattle–Boise retains exactly 564,380 labels, 558,507 expansions and 4,366,369 cell
transfers. Its measured detailed search took 810.125 ms and allocated 172,566,120
bytes, including copied output source strings. The numeric/source path and cost
model remain unchanged. The requested normal-workload search gates pass. Newport's
four-worker throughput decreases 17.2%, with p95 increasing 24.7%; it remains
inside the predeclared 25% band but deserves attention in future storage work.

Exhaustive Dijkstra produces a different memory workload from the HTTP benchmark.
The first multistate reference run with `GOMEMLIMIT=6GiB` exceeded the shared
30-second case deadline on the full-component disconnected search and
Seattle–Boise. A `2GiB` repeat also exceeded that deadline. Both failed runs were
retained and interrupted after the failures were established. Completed comparisons
agreed. Desktop compressed-memory occupancy and page churn were high; no other
application was closed or modified to alter that environment.

An explicit offline-only timeout override now permits 30 s through five minutes,
with the default unchanged. Final source verification uses 120 s,
`GOMEMLIMIT=3GiB` and `GOGC=50`. This changes neither route assertions nor cost
tolerance and does not relax production timeouts. It separates correctness
verification from a claim about reference-search or request latency under memory
pressure. Ordinary prepared HTTP performance does not certify pressure-resistant
national latency. Physical residency and exhaustive-query scratch remain open gates.

## Independent preparation and offline build gate

Fresh preparations from the retained independent SQLite rebuilds reproduce the
complete artifact SHA-256 for all three regions:

| Graph | Artifact SHA-256 |
| --- | --- |
| Newport | `3331c7d58c3660c1700c1c9ac89d1e387ee61fae5c0afbc04af19b2236f2aacb` |
| Oregon | `db553664b078e9eb1f67d9e4213145b20fab1632e38078ab1ef1562e58737cbe` |
| Multistate | `e24b8ca0be7862cb984179265305381fb71bcc9ac0e270ab317dc772111ebe48` |

The Newport source database files have different whole-file hashes while producing
identical prepared artifacts. Oregon and multistate retained independent builds
also have identical SQLite file hashes. Source graph digests and source snapshot
fingerprints are retained in the receipts and `measurements.json`.

Offline preparation still constructs the full graph. Independent Newport/Oregon/
multistate preparation took 3.68 / 38.70 / 107.77 seconds and peaked at 0.640 /
4.938 / 6.288 GiB macOS footprint. The source PBF importer was not rerun or rewritten;
its prior construction limits remain. These offline peaks are separate from direct
runtime loading. A national external-memory build, smaller artifact/node lookup
representation, bounded national address evidence and controlled residency/cold-cache
validation remain unfinished.

## Lifecycle, first requests and verification

The isolated real-HTTP Oregon → multistate replacement published in **8.501 s**
(previous observation 89.541 s). It sampled **18.78 MiB** peak Go heap during
replacement, compared with 7,818.77 MiB previously. Old/new routing structures
retained 13.12 MiB after GC in the harness. The old mapping reported zero bytes
after retirement and rejected new route calls. Concurrent responses retained the
correct snapshot fingerprint; failed selection preserved the working handler and
reported degraded health. Prepared rollback and restoration of the actual baseline
also passed, with admission shared across the switch.

The complete replacement/failure/rollback process took 21.00 s and peaked at
**48,829,696 bytes (46.57 MiB) macOS footprint**, below the declared 5 GiB target
and far below the earlier 8.39 GiB footprint. However, peak RSS **increased from
6.25 to 8.55 GiB**. The prepared old/new artifact sizes sum to 6,523,951,904 bytes
(6.076 GiB), versus the old numeric-only sum of 4,006,719,196 bytes. The in-process
rollback verifier also opens a mapping while the same snapshot is still serving.
This larger mapping footprint and possible duplicate aliases must remain visible;
private/charged footprint improvements are not evidence of bounded physical
residency. Avoiding repeated validation mappings and reducing the expanded disk
representation are useful follow-ups.

The first HTTP requests after integrity validation took 1.288 ms (Newport),
2.151 ms (Oregon) and 9.989 ms (multistate), including loopback and encoding.
They use the first retained short-trip fixture, not the longest route. The separate
mapped-access run loaded in 8.036 s, reporting 9,829,272 Go-heap bytes and
4,501,872 KiB RSS. `MADV_DONTNEED` barely changed RSS. Its first/second route passes
took 2.382 / 2.385 s, with 8,527 / 8,203 minor faults and zero additional major
faults. This is evidence of warmed accesses following validation, not a cold-cache
result. The much larger process virtual reservation also includes Go/runtime arenas;
use the explicit mapped-byte count to describe the artifact itself.

The complete source-backed Northwest (29 cases) and Oregon (31 cases) suites pass,
including both Oregon-to-Oregon Idaho optima, source adjacency/directions,
restrictions, geometry/cost sums and ordinary Dijkstra agreement. The final Northwest
suite took 269.06 s. Seattle–Boise's reference took 41.367 s under pressure, with
exact source-path equality. The disconnected full-component reference took 24.708 s
in that run. All completed source cases preserve the documented cost tolerance.
Newport's 31 coordinate and 22 address cases, mixed requests, both sensitivity
suites, Oregon boundary coverage, lookup-only cycles, v3 → prepared-v4 → v3
cycles, mapped access and the retained geocoding suite also pass.

Small deterministic tests exercise prepared routing against ordinary Dijkstra,
randomized directional/via-way/destination cases, all retained graph versions,
partial endpoints, output ownership after close, concurrent close, and independent
encoding. Reader tests reject truncated/corrupt files, source/receipt mismatches,
self-checksummed changes, unsupported prepared/preprocessing/graph/cost versions,
malformed dimensions/overflow, bad CSR and edge references, recursive cell/spatial
cycles, invalid hash-table dimensions and nonzero padding. Cancellation is tested
before loading and at mapping, hashing and ancillary-decoding boundaries; a failed
load does not poison a subsequent reader. Missing prepared data cannot invoke the
legacy loader. Source changes and existing output names prevent publication.

Evidence scripts are `baseline.sh`, `prepare.sh`, `evaluate.sh`,
`final-verification.sh` and `final-extras.sh`; explicit reruns retain separate logs.
`measurements.json`, `measurement-table-final.txt` and `summarize.py` retain the
resource and reproducibility calculations. The `*-baseline-load.txt`,
`*-prepared-load.txt`, `*-performance.txt`, `*-http.txt`, `*-prepare.txt` and
`*-independent-prepare.txt` files retain full measurements. `final-*` verification
logs and route reports retain source, boundary, sensitivity, geocoding, mapping,
legacy-cycle and lifetime outcomes. The first seed-reproduction invocation refused
a retained output filename without overwriting it; its rerun uses a new output
under this milestone's directory. Failed default-deadline reference runs remain
available as `NorthwestRouting-verification.txt` and `northwest-2g-verified.txt`.
The deployment fingerprint still matches the retained pre-milestone value.

The fresh city-seed output and 1,896-record I-90 source report reproduce their
retained counterparts exactly. The prepared Newport real-HTTP race run passes.
A separately compiled server exits with an explicit preparation-required error
when given a routing snapshot without `-routing-prepared`. With that option it
reports routing/duration available and serves a mixed address/coordinate request
(1,254 m, 138 s) on an isolated loopback port. That process was stopped after the
check; the active deployment was never selected or changed.

Final `gofmt`, `go test ./...`, `go vet ./...`, and race checks for routing,
dataset, API and importer pass. Documentation links/fences and `git diff --check`
also pass. Routine verification is offline; the retained regional suites remain
explicit integration workloads. CLI results are `cli-{missing-prepared,health,
mixed-route}` files; successful source/race reruns are `verified-source-seeds.txt`
and `verified-http-race.txt`. `complete-go-*` / `review-go-*` retain routine checks.

The runtime startup, ancillary heap and ordinary query budgets are met on these
retained workloads. Physical RSS did not improve in the full lifecycle test, and
the default 30-second exhaustive-reference budget did not hold under desktop
pressure. Those limitations, national build memory, national address evidence
and controlled cold/residency behavior remain explicit unfinished work.
