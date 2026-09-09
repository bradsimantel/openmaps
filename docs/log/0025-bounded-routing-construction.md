# Bounded routing identity construction

Date: 2026-09-08. Scope: offline construction based on `9228d14`, using the pinned
Newport, buffered Oregon and complete Oregon–Washington–Idaho inputs. This follows
the [historical dense-edge milestone](0024-dense-routing-edges.md). The API,
`estimated-driving-v1`, source identities and active deployment are unchanged.
This is an increment toward nationwide construction, not nationwide readiness.

## Decision and boundaries

Replace the offline segment/guard identity map with a bounded sorted ordinal
index. The multistate baseline's map accounts for approximately 870 MiB in the
heap profile at segment construction. A batch contains at most 4,194,304 eight-byte
ordinals (32 MiB), default 1,048,576 (8 MiB). The comparator borrows source strings
from the already resident segment and guard arrays. Small graphs remain in memory;
large graphs write private sorted runs and merge at most 16 at a time, with 64 KiB
per stream. No graph-sized run list, copied key pool, database index, mapping or
validation set replaces the removed map. The final index resolves restriction
references by binary search and is closed before spatial/hierarchy construction.

A sorted run must contain valid ordinals and strictly increasing, nonempty source
IDs. The final file must contain exactly as many records as the source arrays.
Together, ordering, uniqueness, bounds and count checks establish a complete
permutation of authoritative records. Duplicate segment IDs, duplicate guard IDs,
collisions between the two, and malformed/truncated intermediate data fail.
Restriction references must resolve to an included segment in the requested
direction and still form a connected source path. Internal ordinals never become
public or source identities. Scratch has no persistent compatibility contract and
is never accepted as a publication or a future build's input.

Two smaller lifetime/encoding changes accompany the index. Returning metadata no
longer keeps decoded node, cost and ban arrays alive through hierarchy construction.
The importer joins its source-ordered included segment subsequence against sorted
motor-way vertices using one cursor, replacing another segment-membership map.
The join checks that every included segment is encountered, preserving barrier
gaps, excluded roads, original vertex ordinals and guard order. This is a bounded
membership operation over resident input/output arrays, not a bounded PBF importer.

The first prototype missed the whole-process footprint goal. Publication still
retained an accumulated string pool and encoded the entire 512 MiB node table
into a second allocation. The follow-up streams strings from source records,
node-table records and spatial ordinals through the existing 1 MiB writer. This
bounds the changed serialization buffers. Source-ID sorting, the node hash-slot
array, ancillary JSON and the complete source-validated graph remain separate
resident costs. No persistent encoding or search/hierarchy semantics change.

## Ownership and remaining construction peaks

| Phase | Retained data and temporary costs |
| --- | --- |
| PBF ways/relations | Parsed road and motor-way objects, node-need membership, raw source JSON and restriction relations coexist. Two decoder workers also retain library input/output queues. |
| PBF nodes | Needed coordinates enter a source-ID map; tagged node provenance, blocked nodes and point speed tags coexist with ways. The coordinate map alone accounts for about 1.55 GiB in the multistate profile. |
| Segments and guards | Source objects/coordinates remain while segment strings, directional costs, used-node membership and guard output grow. The new guard membership cursor removes only the duplicate membership set. |
| Restrictions and final node output | Restricted from/via adjacency retains all relevant departures, including only-turn alternatives. Path enumeration and source/provenance sorting remain in memory. Final nodes and raw parsing state can overlap. |
| SQLite chunk writing | Complete importer Data remains during provenance checks and 4,096-record gob/DEFLATE chunks. After writing, importer Data is released and collected before validating the exact unpublished transaction. |
| Routing decoding/provenance | All query records are decoded; provenance streams one checked chunk at a time. Required/seen provenance sets and access-evidence maps remain graph-sized. Speed note text is released; source records remain authoritative in SQLite. |
| Canonical graph construction | Source node lookup, points, original 48-byte edges, directions, cost lookup and public/restricted memberships coexist with source segments/guards. The new identity index bounds only segment/guard uniqueness and restriction-reference lookup scratch. CSR has a temporary cursor array. |
| Spatial indexing | Full graph plus feature bounds, sorted feature ordinals, tree arrays and growth copies. None of these arrays has been moved to external memory. |
| Hierarchy | Continuation/color arrays, junction components/ends/neighbors, cell membership/arcs/bounds/entrances and growing transfer/path arrays. Per-cell searches remain local, but global cell scratch/output is not bounded. |
| Publication | Canonical graph plus sorted source IDs and the complete hash-slot table. Strings and numeric encoding now stream. Ancillary JSON still allocates before its 128 MiB encoded-size check; that cap is not a heap bound. |

The pinned PBF library allocates a 32 MiB input blob buffer and uses two workers,
with five queued inputs and five queued outputs per worker plus its serializer.
Decoded object expansion and retained source objects must also be counted; the
32 MiB blob limit is not a 32 MiB parsing-memory bound. The SQLite library's default
page-cache target is 2,000 KiB and default mmap size is zero. These defaults are
not an end-to-end memory cap. The new identity index adds no database cache.

Run payload disk usage is at most `16 × (segments + guards)` bytes while merge
generations overlap, plus filesystem allocation rounding and metadata. File count
depends on batch size; very small batches incur substantial directory/block
metadata overhead. The absolute regional disk budget applies to the measured
normal batches, not arbitrary one-record runs. Library cleanup enumerates directory
entries in bounded groups. OS file-cache residency remains outside the Go buffer
bound, and publication itself writes the unchanged multi-GiB artifact.

## Budgets and measurement method

The predeclared budgets are retained in `data/construction-scale/budgets.json`.
The importer join and publication follow-up have separately dated pre-evaluation
records in that directory. Normal settings are Go 1.26.1, `GOMEMLIMIT=3GiB`,
`GOGC=50`, serialized heavy work and new ignored output names. Targets:

- Identity-sort buffers at most 34 MiB including stream buffers and fixed metadata;
  default batch 8 MiB. Multistate run payload at most 384 MiB; publication plus
  run payload at most 4.25 GiB.
- At least 15% less live heap at the segment-construction probe, at most 3.75 GiB.
  Whole-construction live-heap goal: 10% reduction and at most 5 GiB.
- Whole-process sampled charged-footprint goal: 10% reduction and at most 5.5 GiB;
  RSS at most 5.5 GiB. Normal preparation at most 1.25 times baseline and 210 s.
- Import elapsed/footprint regression at most 5%; an additional 10% import
  footprint reduction goal, with an 8 GiB absolute ceiling, for the guard join.
- Multistate normal HTTP p95 below one second, four-worker throughput at least
  12 requests/s, exactly 564,380 Seattle–Boise labels, and at most 25% regional
  regression for Newport/Oregon.

The host is macOS/Darwin ARM64 with 16 GiB RAM and 16 KiB VM pages. Read-only
controls, shell limits and available accounting tools were inspected. No process
RSS enforcement or controlled cold-cache facility is configured. No applications
were closed, caches purged, host settings changed, pressure generated or
infrastructure provisioned. No national/supplemental address data was acquired.

Normal process samples use `proc_pid_rusage` every approximately 100 ms, reporting
RSS, charged footprint, pageins and storage reads/writes. Separate Go sampling
records HeapAlloc, cumulative allocations, the last GC's live heap, runtime
reservations and process faults. Sample maxima can miss short peaks; last-GC live
heap is not instantaneous liveness. Forced-GC heap profiles at named boundaries
identify retained allocations and are separate from normal elapsed-time results.
Diagnostic overlays use `.go.txt` filenames. The additional heap-sampled
baseline/candidate runs also capture `vmmap`/`footprint` during hierarchy
construction; those probes perturb timing. The two VM snapshots are illustrative
points, not synchronized phase peaks. RSS, Go reservations,
charged/private/compressed memory and file-backed
residency are distinct quantities, not interchangeable estimates of unique RAM.
Host swap/pressure observations include unrelated processes. In `footprint` output,
parenthesized swapped bytes are a subset of dirty memory and must not be added
again. Construction opens no query mapping; scratch file-cache pages can remain
outside process RSS/charged-footprint accounting.

## Validation and publication guarantees

Preparation continues to hash the authoritative SQLite file before full importer
validation, reconstruct the canonical source topology/hierarchy, check source
stability, and publish immutable artifacts with the trusted receipt last. Sorting
scratch cannot bless arbitrary topology or shortcuts. The complete source binding,
source/provenance checks, directional costs, incoming-edge identity, via-way history,
destination phases, partial endpoints and exact source geometry remain unchanged.
The ordinary Dijkstra reference and documented numerical tolerance are unchanged.

Scratch is scoped to a private build directory and removed before successful graph
return or on ordinary errors/cancellation. Uncaught process termination can
leave private scratch; retained inputs and selected publications are never used as
scratch. Intermediate failures cannot publish a receipt. Prepared version checks,
corruption rejection, immutable publication, HTTP response leases, replacement and
streaming rollback verification retain their existing boundaries. Production
startup still requires an existing trusted preparation and cannot silently rebuild.

## Preparation results

The first prototype removes the validation map and shortens decoded-data lifetimes.
Its normal multistate preparation takes 196.68 s versus 163.28 s baseline, but
sampled charged footprint decreases only 1.92%. That misses the declared 10%
whole-process reduction. The publication follow-up addresses the demonstrated
remaining encoding allocations without changing file bytes.

| Multistate primary preparation | Baseline | Final |
| --- | ---: | ---: |
| Elapsed seconds | 163.28 | 197.80 |
| Sampled charged footprint, GiB | 6.01 | 5.22 |
| Sampled RSS, GiB | 4.22 | 4.60 |
| Process pageins | 94 | 94 |
| Process storage reads, GiB | 8.51 | 8.49 |
| Process storage writes, GiB | 3.85 | 4.22 |

Charged footprint decreases **13.19%**, passing the relative and 5.5 GiB goals.
Elapsed time increases 21.14%, within the 25% relative and 210 s primary-run
budgets. RSS **increases** about 9%, although it stays below 5.5 GiB. A lower
charged footprint is not a claim that all physical-memory metrics improved.
The unchanged output is 4,132,749,063 bytes. Sampled temporary run payload remains
below 258 MiB, against 384 MiB; artifact plus run payload is below 4.25 GiB.
The new run writes explain approximately 386 MiB of extra logical write traffic.
Disk counters are observed storage traffic, not identical to logical bytes written.

| Additional heap-sampled repeat | Baseline | Final |
| --- | ---: | ---: |
| Elapsed seconds | 205.79 | 228.55 |
| Maximum sampled last-GC live heap, GiB | 5.69 | 5.11 |
| Maximum sampled HeapAlloc, GiB | 5.69 | 5.11 |
| Cumulative allocations, GiB | 39.92 | 37.64 |
| Go runtime Sys, GiB | 6.84 | 6.01 |
| Sampled charged footprint, GiB | 5.92 | 5.22 |
| Sampled RSS, GiB | 4.17 | 4.38 |
| Process minor / major faults | 2,891,661 / 147 | 1,924,621 / 93 |

Live heap decreases **10.15%** and charged footprint **11.80%** in this repeat.
The **5 GiB absolute live-heap goal is missed**, by about 117 MiB. The repeat's
**210 s elapsed ceiling is also missed**. Its 11.06% relative time increase remains
within the relative budget. These runs add Go statistics and one VM snapshot pair;
file cache, compression, other processes and sustained-host variability are not
controlled. The large baseline spread is retained, not dismissed or replaced with
a selected best run. Consistent 210 s construction latency is not established.
The heap repeat's scratch counter watched the wrong parent directory; it is
unavailable, not zero. The disk conclusions above use the separate explicit-path
primary runs. This limitation is recorded beside the raw measurements.

Forced-GC phase profiles isolate the allocation changes. At the segment probe,
live heap falls from 4,706,243,504 to approximately 3,811,140,000 bytes: **19.02%**,
meeting the 15% and 3.75 GiB goals. Spatial/hierarchy boundary live heap each loses
about **544 MiB** of no-longer-needed decoded data. Hierarchy boundary live heap
falls from about 5.04 to 4.51 GiB. Node-table publication still holds the complete
source graph, 512 MiB hash slots and, while inserting, approximately 105 MiB of
sorted source IDs. This is a concrete remaining peak, not bounded publication.

VM snapshots show substantial private anonymous and swapped/compressed memory,
with no query-artifact mapping during construction. Their private dirty totals
include swapped bytes. They were captured at different hierarchy instants and
must not be presented as synchronized residency peaks. No duplicate query mapping
accounts for the saved allocations. OS cache for SQLite, scratch and output files
and host-wide swap remain outside any enforced bound. Fault/pagein differences
are not evidence of cold-disk performance.
For scale, `vmmap` reports total virtual reservations of approximately 8.4 / 7.3 GiB
and resident totals of 2.5 / 3.0 GiB at those baseline/final snapshot instants.
Those virtual reservations are neither live Go heap nor physical-memory use.
Before/after the primary baseline, host swap remains 6,226.69 MiB and the
read-only memory-pressure free estimate moves from 68% to 51%; the final run
records 6,194.69 MiB swap and 56% to 51%. Host compressor occupancy also rises.
These are host-wide endpoint observations, not per-process peaks or controlled
pressure experiments, and cannot be attributed solely to this workload.

## Source, HTTP and lifecycle verification

The retained 29-case multistate suite passes in 85.73 s, including both
Oregon-to-Oregon optima through Idaho, independent source adjacency/direction/
restriction checks and ordinary Dijkstra. Seattle–Boise has exact source-path
agreement with its reference. Oregon's 31 routes, Newport's 31 coordinate and
22 address cases (including mixed requests), both sensitivity suites and Oregon
boundary coverage pass. Lookup-only and retained graph-v3 snapshot cycles pass.
The offline reference timeout is explicitly **120 seconds**, with
`GOMEMLIMIT=3GiB GOGC=50`; production timeouts and the tolerance
`max(1e-6 seconds, abs(reference seconds) × 1e-10)` are unchanged.

| Normal HTTP workload | Baseline one-worker p95, ms | Final one-worker p95, ms | Baseline four-worker requests/s | Final four-worker requests/s |
| --- | ---: | ---: | ---: | ---: |
| Newport | 0.576 | 0.672 | 8,272.11 | 8,768.12 |
| Oregon | 27.790 | 27.057 | 146.52 | 155.48 |
| Multistate | 411.783 | 424.145 | 39.44 | 39.47 |

The final multistate four-worker p95 is 545.585 ms. Normal API targets pass, with
no material Newport/Oregon regression against the declared 25% budget. The
baseline uses retained v2 publications; the candidate uses newly generated v2
files with identical contents. Both use the same runtime code and settings,
appropriate because search/loading behavior is unchanged. Seattle–Boise retains
**exactly 564,380 labels**. HTTP checks hold four complete encodings, reject a
fifth request with 429, preserve health/subsequent routing, and exercise request
cancellation, real loopback transport and complete GeoJSON response bodies.

Four Portland–Ashland full-geometry workers also pass replacement, failed loading,
rollback and retirement. The all-v2 cycle publishes in 13.957 s, with 5.45 GiB
sampled RSS and 177.47 MiB charged footprint. Oregon-v1 → multistate-v2 → Oregon-v1
and the reverse prepared-version arrangement publish in 21.609 / 14.247 s, with
5.40 / 5.81 GiB sampled RSS. Every cycle reports zero retired mapping bytes.
The larger lifecycle RSS includes overlapping query mappings and must not be
confused with offline construction RSS. Failed selection leaves the serving
publication usable. Rollback retains streaming verification without an additional
query mapping. Tests use temporary deployment state and immutable linked
publications; they never select the real deployment.

`gofmt`, `go test ./...`, `go vet ./...` and routing/dataset/API/importer race suites
pass. Real HTTP admission/cancellation and the retained v3/v4 lifecycle also pass
under the race detector. New deterministic tests cover batch/merge boundaries,
multiple merge generations, stable source ordinals, empty/duplicate IDs across
segments and guards, missing nodes/restriction references/directions, overflowing
batch and intermediate ordinals, malformed/truncated/missing runs, cancellation,
failed destination creation, cleanup and reproducibility across supported batch
sizes and graph versions. PBF fixtures cover reordered ways, numeric-versus-lexical
way/vertex ordering, barriers, excluded ways and duplicate vertices. Publication
tests cover buffer-crossing multibyte/zero-containing source strings and cancellation
at each changed output-section boundary, preserving v1/v2 encoding support.

## Import results

The independent multistate rebuild uses matching baseline/candidate phase probes
and GC settings. It takes **498.79 s versus 494.52 s** (+0.86%). Sampled charged
footprint falls from **6.97 to 6.72 GiB** (3.51%), with RSS essentially unchanged
at 5.69 GiB. The 5% regression and 8 GiB ceiling budgets pass; the additional
**10% import footprint reduction goal is missed**. Earlier segment construction
still dominates. Import runs include SQLite writing and validation of the exact
unpublished transaction, including the new bounded identity index.

| Import boundary | Baseline live heap, GiB | Final live heap, GiB |
| --- | ---: | ---: |
| Parsed ways/relations | 3.14 | 3.14 |
| Parsed needed nodes | 4.82 | 4.82 |
| Included segments | 5.81 | 5.81 |
| Guards | 5.75 | 5.75 |
| Resolved restrictions | 4.56 | 4.56 |
| Final importer Data | 3.20 | 3.20 |
| SQLite chunks written, importer Data released | 0.004 | 0.004 |
| Subsequent validated graph segments | 4.38 | 3.55 |

The guard join removes a temporary membership map, so the forced-GC boundary
heaps barely change. Cumulative allocation by final importer Data falls by
approximately **990 MiB**. The guard interval's sampled charged peak falls from
6.97 to 6.45 GiB; the candidate's preceding segment interval still reaches
6.72 GiB. Approximate interval time falls from 64.55 to 16.45 s, while earlier
node/segment intervals are slower on this shared host. These intervals use heap
profile modification times and include probe overhead, not isolated benchmarks.
End-to-end time is the regression comparison, not the fastest individual phase.

Process pageins are 96 / 206, storage reads 1.27 / 1.39 GiB, and storage writes
6.35 / 392.48 MiB for baseline/final. These are sampled storage-accounting
counters, not logical SQLite output sizes. The candidate additionally writes
external-sort runs during final validation. Cache and compression conditions
are uncontrolled. Newport's complete lookup/address plus routing rebuild takes
11.24 s; Oregon's routing rebuild takes 55.92 s. No source dataset changes.

## Independent rebuilding and cleanup

All three regions are rebuilt from their pinned PBF/bundle inputs into new
SQLite files. Newport uses the retained lookup baseline and the normal refresh
builder, preserving addresses, access relationships and public identity history.
The comparison streams every table in primary-key order, including provenance,
lookup/FTS records and exact routing chunk blobs. Schemas, logical records,
graph summaries, source identities and graph SHA-256 digests match throughout.
Oregon and multistate SQLite files also reproduce byte-for-byte. Newport's
SQLite physical layout differs, including internal metadata row order, while
every keyed metadata value and all logical records match; this is recorded
separately and is not a public
identity change. Its new receipt correctly binds its own SQLite checksum.

Repreparation uses **262,144 records per batch**, versus the default 1,048,576
used for the primary preparation, crossing different run/merge boundaries.
Every region's complete prepared v2 artifact matches both its retained baseline
and the new original-snapshot publication byte-for-byte. Newport/Oregon/multistate
repreparation takes 3.76 / 33.66 / 179.02 s. These are additional reproducibility
runs with a different batch and host state, not replacements for primary timings.
The multistate graph digest remains
`b05de775c10178f5b5fe408b2a71fc716d986f7ad0206e8ef7cd7971f451c2ac`,
and its prepared artifact SHA-256 remains
`3878cb0e201a21edb531d2fdde8fb8bbdbacaf5a4f001521840495f77b4a77bc`.

A separate real command is interrupted after two external runs exist, about
20.00 s into multistate preparation. It exits with cancellation, removes its
private scratch in 0.042 s and creates no publication. Deterministic tests cover
later merge/publication failures and cleanup. Final checksum checks confirm
that retained source snapshots and `data/deployment.json` are unchanged; the
retained publications remain usable throughout source and lifecycle verification.

## Remaining readiness gates

This is a verified bounded identity-index increment and a reduction in several
temporary publication/import allocations. It does not bound the complete import,
validation, graph, spatial-index, hierarchy or publication pipeline. The parser's
source-object and coordinate maps, original 48-byte edge array, source-node map,
provenance/restriction validation sets, global hierarchy arrays and publication
hash slots remain concrete obstacles to larger graphs. Whole-preparation live
heap still exceeds 5 GiB; import still reaches approximately 6.7 GiB charged
footprint. Reaching a nationwide size by extrapolating these regional numbers is
not established. Future construction work should measure those remaining peaks
before choosing the next external-memory stage.

Controlled runtime physical-memory and cold-cache tests remain separate gates;
neither `GOMEMLIMIT` nor bounded sort buffers enforce process RSS or system cache.
National source/artifact size, storage throughput and runtime residency under
pressure remain unverified. National address loading is also a separate gate.
No national or supplemental address data, browser change, service boundary,
dependency or cost-model change is included.

## Retained evidence

All generated evidence and artifacts use new names below ignored
`data/construction-scale/`; diagnostic Go overlays retain `.go.txt` suffixes.
Useful entry points relative to the repository root:

- `budgets.json`, `import-increment-budgets.json`,
  `publication-followup-budgets.json`, `controls.txt`, `measurement-notes.json`:
  pre-evaluation criteria, host controls and measurement limitations.
- `measurements.json`, `phase-heaps.json`, `heap-summary.json`,
  `import-phases.json`: process/heap results; each sampled run also retains its
  command, environment, exit status, samples, workload and host observations.
- `baseline-heap-vmmap.txt`, `final-heap-vmmap.txt`,
  `baseline-heap-footprint.txt`, `final-heap-footprint.txt`: illustrative VM
  accounting; phase profiles and overlay sources are retained alongside them.
- `verified-*.txt`, `verified-*-routes.jsonl`, `http-summary.json`,
  `lifecycle-summary.json`, `query-labels.txt`, `go-test.txt`, `go-vet.txt`,
  `go-race.txt`, `http-race.txt`, `lifetime-race.txt`: correctness and runtime
  verification, including complete responses and legacy cycles.
- `rebuild.sh`, `compare-rebuilds.py`, `reproducibility.json`, `cancellation.json`,
  `retained-inputs.json`, `deployment-before.txt`, `deployment-after.txt`:
  independent source rebuilds, equality checks, cleanup and preservation.

The prepared runtime, source/API identities and active deployment are preserved.
