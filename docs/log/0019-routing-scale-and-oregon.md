# Routing scaling and Oregon evaluation

Date: 2026-09-08. Historical implementation and verification record.
Baseline revision: `bcf6dc71437c5ef49f64e6477ebfa65a1236865d`.
Scope: the uncommitted routing/storage/import/deployment changes accompanying
this record; no active deployment selection, retained snapshot, UI, commit or
remote branch was changed. This extends the
[historical estimated-time evaluation](0018-newport-estimated-driving-time.md).
Current behavior and reproduction commands belong in
[routing scale](../routing-scale.md), [routing](../routing.md) and
[refresh](../refresh.md).

## Outcome and evaluation limits

The router now loads versioned, bounded binary chunks from SQLite, retains
compact coordinate/adjacency arrays, indexes endpoint and access searches, and
uses geometric A* with a conservative forced-chain preprocessing level. Ordinary
stateful Dijkstra remains the internal correctness reference. Deployment HTTP
requests can run concurrently while a replacement loads. A separate Oregon
coordinate-only snapshot was built and evaluated without acquiring address data.

This is **not nationwide-ready routing**. It implements useful preprocessing
below the junction graph, not a complete turn-aware contraction hierarchy. Long
regional requests still visit a substantial core, query state grows with search,
and the regional runtime still keeps query data in RAM. Oregon establishes
regional feasibility and exposes costs; it does not establish national data
size, cross-country latency, production capacity or empirical travel-time accuracy.

## Baseline, machine and targets

The machine was an Apple M5 with 10 logical CPUs, 16 GiB RAM, Go 1.26.1
darwin/arm64, approximately 213 GiB available disk and 4.7 GB retained project
data at inspection. The existing Newport data, importer, graph semantics,
address orchestration and deployment code were inspected before changes. The
31-case coordinate and 22-case address-routing suites passed against the
retained v4 JSON candidate before implementation (`baseline-tests.txt`).
The original source was archived under ignored `data/scaling/baseline-src` to
measure its importer and loading separately from the changed implementation.

These explicit local acceptance budgets were recorded during development in
`data/scaling/targets.json`; they are engineering targets, not preregistered
statistical hypotheses:

| Measure | Newport target | Oregon target | Observed result |
| --- | ---: | ---: | --- |
| Persisted size | routing addition <60 MiB | snapshot <500 MiB | pass |
| Build wall time / peak RSS | measured comparison | <120 s / <8 GiB | pass |
| Startup wall time | <4 s | <25 s | pass |
| Startup peak RSS | <800 MiB | <6 GiB | pass |
| Retained Go heap | <240 MiB | <2 GiB | pass |
| Endpoint-pair selection p95 | <2 ms | <5 ms | pass |
| One-worker route p95 | <10 ms | <1 s | pass |
| One-worker route maximum | measured comparison | <3 s | pass |
| Replacement peak RSS | consistent responses required | <10 GiB | pass |

All routed cases must agree with the reference optimum within
`max(1e-6 seconds, abs(reference seconds) * 1e-10)`, with identical selected
endpoints, profile and restriction state. Existing contract/error cases must
remain valid. National claims require a subsequent larger contiguous evaluation
and a bounded query-data backend.

## Storage and memory decision

The old Newport file was 235.65 MiB, including a 202.87 MiB JSON graph. JSON
sections occupied approximately 74.40 MiB segments, 56.07 MiB provenance,
36.64 MiB nodes, 16.53 MiB cost records, 12.34 MiB guards and 6.76 MiB access
evidence. Loading temporarily held the raw payload, decoded record arrays and
source strings/maps alongside the constructed graph. A retained heap profile
attributed about 205 MB to graph construction; total retained Go heap was
231.19 MiB. Thus provenance/JSON were major transient costs, but eliminating them
alone could not eliminate necessary query topology.

New `routing-chunks-v1` stores up to 4,096 domain records per checksummed,
DEFLATE-compressed gob chunk. The small manifest hashes all chunks, including
provenance. Original source tags, IDs, versions, import decisions, speed notes
and attribution remain inspectable offline. Runtime validation streams source
chunks and releases bulky strings and validation records. Coordinates and CSR
adjacency are arrays; the loader owns its segment array and avoids duplicate
segment and per-node adjacency construction. Query-critical geometry, directional
costs, edge history, destination zones, snap guards and spatial bounds remain
resident. Stable lookup IDs and original source node/way/relation identities are
preserved. Source `way:ordinal` segment references are reproducible within a
pinned geometry build, not a new permanent public identity scheme.

The new Newport snapshot is 65.19 MiB, adding about 34 MiB routing data to lookup
data. Compressed routing chunks include 9.68 MiB sources, 9.37 MiB nodes,
7.63 MiB segments, 2.71 MiB guards, 1.35 MiB access areas, 0.53 MiB costs,
0.52 MiB access ways and 0.02 MiB bans. Provenance is retained rather than
discarded to obtain the reduction.

A separate opt-in coordinate access prototype compared resident arrays, a
read-only mmap, and an eight-page, 512 KiB bounded `ReadAt` cache. It used the
5,701,921 Oregon coordinates (91,230,736 bytes), 100,000 sequential or scattered
accesses, and two passes with equal accumulated checksums:

| Access pattern | Arrays, first / repeat ns | mmap, first / repeat ns | Bounded cache, first / repeat ns |
| --- | ---: | ---: | ---: |
| Sequential | 0.6 / 0.6 | 1.7 / 0.6 | 1.7 / 1.6 |
| Scattered | 6.0 / 2.3 | 41.0 / 1.9 | 2,176.9 / 2,169.7 |

The cache missed 99,443 times per scattered pass and 25 times per sequential
pass. This is a warm-filesystem microbenchmark, not a full router comparison;
the first mmap pass includes mapping faults, not a guaranteed cold-disk read.
It supports resident regional arrays on this machine, and identifies mmap as a
credible next backend. It does not justify SQL per visited edge or prove that
bounded loading with better locality is impossible. Compressed gob chunks are
portable versioned storage, not directly mapped native arrays. A flat indexed
geometry/topology section, lifecycle management and actual fault/load testing
remain necessary before mmap becomes a runtime option.

## Search increment and correctness

Packed bounding-volume trees conservatively filter roads, guards, access areas
and driveways; existing exact predicates and original candidate ordering still
select endpoints. No route cost or route-success feedback changes selection.
Long-segment bounds, latitude-dependent radius, access containment tolerance,
source junctions and excluded-road crossings remain relevant to filtering.

The implemented preprocessing marks unique non-reversing continuations away
from every prohibited-path node and destination zone, and breaks functional
cycles deterministically. Search can traverse those chains without queuing every
geometry vertex. Each edge still advances turn history and access state, and
partial destination endpoints stop chain traversal. Full source geometry is
reconstructed. The A* lower bound uses spherical distance divided by the graph's
maximum effective speed, with a downward floating-point margin. Relaxation does
not drop cost improvements using an epsilon.

This choice follows the distinction between admissible bounds and hierarchical
preprocessing in [Goldberg and Harrelson's A* research](https://www.microsoft.com/en-us/research/publication/computing-the-shortest-path-a-search-meets-graph-theory/),
the [original contraction hierarchies paper](https://ae.iti.kit.edu/download/contract.pdf),
and [turn-aware contraction research](https://publikationen.bibliothek.kit.edu/1000097647).
Landmarks and a full CH/CCH core were considered but not implemented. Node-only
shortcuts cannot safely stand in for the existing via-way automaton and
destination phase. The implemented bottom level is deliberately conservative
around these states; further contraction needs an explicit state-aware proof
and equivalence tests.

Queue order is deterministic by priority, actual cost, directed edge, turn state
and destination phase. Equal relaxations keep the first predecessor and a tied
direct partial segment wins. Different search ordering could choose another
exactly tied optimum; this is documented rather than promising identical paths
for every possible tied graph. Evaluated Newport and Oregon paths matched the
reference. Durations still represent uncalibrated estimated elapsed road time,
with the same rounding, distance, static-duration equality, absence of traffic
and exclusion of uncertain off-road connectors. No supported response field,
lookup public ID or address-resolution behavior was changed.

## Oregon acquisition and graph scope

The provider was Geofabrik's published OpenStreetMap regional extract service,
using its [Oregon](https://download.geofabrik.de/north-america/us/oregon.html),
[Washington](https://download.geofabrik.de/north-america/us/washington.html) and
[Idaho](https://download.geofabrik.de/north-america/us/idaho.html) releases dated
2026-09-07. All three PBF headers identify `2026-09-07T20:21:20Z`. Published MD5s
were downloaded and matched; SHA-256 pins are in
[the maintained lock](../../imports/oregon-routing.lock.json). The original PBFs,
provider polygons, header reports, checksum responses and acquisition record
are retained in ignored `data/scaling/`. Original sizes were 253,399,721 bytes
Oregon, 362,812,088 Washington and 128,536,486 Idaho.

Data is under [OpenStreetMap's ODbL 1.0 attribution and licensing terms](https://www.openstreetmap.org/copyright).
Provider coverage was checked against the published polygons and
[Geofabrik's technical documentation](https://download.geofabrik.de/technical.html);
an extract boundary is not asserted to be a legal state line.

Homebrew `osmium-tool` 1.19.1_1 (osmium 1.19.1) was installed after resource/tool
inspection. Following the official [extract](https://docs.osmcode.org/osmium/latest/osmium-extract.html)
and [merge](https://docs.osmcode.org/osmium/latest/osmium-merge.html) documentation,
`complete_ways` selected southern Washington
`[-124.8,45.4,-116.4,46.6]` and western Idaho
`[-117.3,41.98,-116.5,44.3]`. These were merged with the complete Oregon extract;
no graph partition or endpoint-region-only routing was introduced. Exact commands
are in the maintained scale document and `data/scaling/acquisition.json`.

The 330,005,692-byte derivative SHA-256 is
`8b02d8f64a2e88c10a50cc68624b3763e6a2c12921751c97431878d36f7bab63`.
It contains 42,875,457 OSM nodes, 4,173,834 ways and 30,987 relations. Osmium's
way-node reference check found zero missing references. Import retained
5,701,921 graph nodes, 5,925,348 segments, 623,156 included ways, 967,582 guards,
31,528 blocked nodes, 36,360 destination segments and 10,882 prohibited paths
from 9,356 enforced relations. There were 225 conservative malformed-restriction
closure decisions. Source decision records preserve exclusions and limitations;
this is not a claim of complete real-world restriction data.

The endpoint rectangle is `[-124.8,41.98,-116.45,46.3]` in longitude/latitude.
Actual graph geometry extends to approximately
`[-124.563238,41.5401763,-116.2185697,46.7012]`, since complete ways extend beyond
extract boxes. Neither rectangle certifies continuous road coverage. California,
Nevada and Idaho outside the acquired corridor remain absent. Southern/eastern
trips whose valid optimum leaves this coverage remain uncertified. No Oregon
address source was acquired; the routing-only snapshot has empty lookup tables.

## Source-backed route findings

The checked-in 31 cases in `internal/routing/testdata/oregon.json` select
endpoints from adjacent pinned source nodes by road name/class and geography,
record way/node IDs and tags, and use broad geographic distance guards. They
cover Portland downtown/bridges/one-ways, parking and residential streets,
Beaverton–Hillsboro, Salem–Eugene, Bend, Medford/Ashland, coastal and mountain
trips, long interstate travel, desert roads, dead ends, Columbia alternatives,
Idaho detours, zero travel, disconnected topology and invalid/unavailable points.
The unreachable case was established independently with undirected source
component connectivity. Expectations were not copied from accelerated routes.

For every successful case, verification checks source-node adjacency, legal
directions, source tags, banned edge sequences, destination-only shortcuts,
independent edge-cost sums, unchanged snaps, and reference optimality. Bridge
cases require source bridge evidence. The
[ODOT state numbered route map](https://www.oregon.gov/ODOT/Data/Documents/Num_Route_Map.pdf)
and [WSDOT bridge inventory](https://www.wsdot.wa.gov/publications/manuals/fulltext/m23-09/Bridgelist.pdf)
provided independent corridor/crossing context, not elapsed-time calibration.
All 31 expected outcomes passed: 27 successful cases including zero travel, and
four expected invalid, unavailable or unreachable outcomes.

Boundary evidence changed the acquired scope. In the initial Oregon/Washington
graph, Jordan Valley–Ontario was reachable only through a much slower modeled
alternative. The ODOT map shows US 95 leaving Oregon; adding western Idaho
permits US 95/ID 19/I-84 paths with the same endpoints and cost model:

| Direction | Before: metres / seconds | With Idaho: metres / seconds |
| --- | ---: | ---: |
| Jordan Valley → Ontario | 138,916.939 / 20,353.131 | 134,570.993 / 7,532.267 |
| Ontario → Jordan Valley | 138,929.506 / 20,355.652 | 134,041.611 / 7,471.416 |

Both before and after results matched reference search. The final paths pass
through an independently specified Idaho interior box. Portland–Astoria used
Oregon US 30 in both directions under this model; those samples did not prove
Washington coverage necessary, though it retains legitimate competing corridors.

Two large detours were investigated separately with a distance objective:

| Trip | Time optimum: km / modeled seconds | Distance optimum: km / modeled seconds |
| --- | ---: | ---: |
| Bend → Medford | 341.452 / 20,293.793 | 275.691 / 25,370.904 |
| Klamath Falls → Ashland | 118.795 / 9,133.592 | 101.769 / 11,066.898 |

The shorter Bend path uses OR 230/OR 62 at assumed 40 km/h and about 13.6 km of
unnamed service roads at 10 km/h; the time objective prefers longer US 97/OR 140
travel. The shorter Klamath path includes about 67.8 km of OR 66 at 35 km/h plus
slower local roads; the time objective prefers OR 140/Dead Indian travel. Raw
tags and cost decisions are retained in `oregon-sensitivity.jsonl`. Reference
agreement establishes model optimization, not that drivers should take these
detours or that the durations are calibrated. Independent travel observations
are the missing evidence for adjusting this model.

## Build, loading and latency measurements

All sizes use binary MiB/GiB. `/usr/bin/time -l` reports macOS peak process RSS
in bytes. Retained heap is Go `HeapAlloc` after GC; it is not process RSS.
Startup-only runs end after loading and GC, avoiding contamination by route
workloads. Filesystem caches were warm; these are bounded desktop measurements,
not cold-start guarantees. Build measurements use a precompiled importer;
Oregon builds use `GOMEMLIMIT=8GiB`, route workloads `6GiB`.

| Metric | Original Newport JSON | New Newport | Final Oregon + buffers |
| --- | ---: | ---: | ---: |
| SQLite file MiB | 235.65 | 65.19 | 287.27 |
| Build wall seconds | 7.53 | 8.49 | 49.27 |
| Build peak RSS GiB | 1.78 | 0.946 | 4.89 |
| Startup-only seconds | 2.163 | 2.007 | 20.128 |
| Startup peak RSS MiB | 899.59 | 388.33 | 3,026.14 |
| Retained Go heap MiB | 231.19 | 203.37 | 1,706.20 |
| Endpoint pair p50 / p95 ms | 23.678 / 26.963 | 0.067 / 0.127 | 0.078 / 0.173 |

Separate complete-workload load observations were 2.168, 2.121 and 20.660 s;
pre-GC heaps were 570.65, 362.81 and 2,972.27 MiB. The original loader's
`HeapSys` reached 939 MiB, versus 371.34 MiB Newport and 2,987.34 MiB Oregon.
Oregon build peak physical footprint was 7.90 GiB, higher than its 4.89 GiB RSS;
the distinction matters on macOS. Storage compression did not materially speed
Newport loading; the important changes are size, peak allocation and query work.

Route workload: two passes of the 31 coordinate cases per worker, including
expected failures, concurrent workers with sequential requests each. This is an
in-process route/snap benchmark, not HTTP encoding or an open-loop load test.
Reference and source verification run separately. Sample counts are deliberately
bounded; p95 is an empirical order statistic, not a production tail estimate.

| Graph | Workers | Requests | Throughput requests/s | p50 ms | p95 ms | Maximum ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Original Newport | 1 | 62 | 40.76 | 23.887 | 30.305 | 63.201 |
| Original Newport | 4 | 248 | 134.94 | 28.676 | 42.835 | 69.603 |
| Original Newport | 8 | 496 | 167.34 | 47.292 | 62.552 | 131.700 |
| New Newport | 1 | 62 | 2,918.60 | 0.139 | 0.996 | 3.225 |
| New Newport | 4 | 248 | 10,470.03 | 0.145 | 1.180 | 3.656 |
| New Newport | 8 | 496 | 14,055.14 | 0.183 | 1.805 | 7.150 |
| Final Oregon | 1 | 62 | 13.77 | 10.340 | 121.327 | 1,057.998 |
| Final Oregon | 2 | 124 | 26.54 | 10.556 | 557.668 | 1,062.557 |
| Final Oregon | 4 | 248 | 44.75 | 12.374 | 656.206 | 1,271.954 |

Oregon's complete workload peaked at 4.50 GiB RSS. The longest Portland–Ashland
case still takes roughly a second; regional speedups do not remove national
core-search concerns. Persisted size is not a proxy for resident query memory.

## Snapshot integrity, replacement and verification

Graph semantics 4, profile `driving-time-v4`, cost model `estimated-driving-v1`,
layout `routing-chunks-v1` and preprocessing `forced-chain-v1` are explicit.
Chunk/manifest corruption, missing or orphan chunks, unsupported versions,
invalid source references and mixed legacy/chunk layouts fail validation.
Retained JSON versions 1–4 remain readable with their original profile behavior.
The importer adds routing to an unpublished transaction and refuses existing
output files. Routing-only manifests explicitly reject fabricated lookup records
and are not valid published snapshots until a routing graph exists.

Independent Newport and Oregon rebuilds had equal logical rows in every SQLite
table, including provenance, FTS, manifest and compressed chunks. Newport file
bytes differed while all logical tables matched; no byte-identity claim is made
for SQLite generally. Both Oregon files were byte-identical. Final Oregon file
SHA-256 is `bee0cfa49430771b3cb7528a259596347124eee2808802bf79888c7719734ea2`;
manifest SHA-256 is
`9dfe7900b8fe21210b8bda1c12a674182c0c43895b900d16791ea319266b370f`.
The Newport comparison preserved 11,602 public lookup IDs with zero entity,
source or relationship changes and zero violations.

Live requests hold shared snapshot leases through response completion. One
request validates/loads a candidate outside that lease while others use the old
snapshot. Publication waits for old responses, then closes their SQLite store.
The already validated graph is reused instead of loaded twice. The initiating
request waits for the load; the server write timeout is now two minutes. This
does not implement background reload orchestration or admission control.

Four concurrent real HTTP workers were exercised during isolated replacement,
failure and rollback. Dataset headers and route outcomes had to identify the
same snapshot. A blocked-response unit test verified another response can finish
and that Close waits for the old response lease. Measurements retained both
routers explicitly through GC:

| Temporary deployment | Replacement s | Sampled peak Go heap MiB | Retained overlap MiB | Process peak RSS GiB |
| --- | ---: | ---: | ---: | ---: |
| Oregon → independent Oregon rebuild | 22.089 | 5,210.54 | 3,411.12 | 4.67 |
| Newport → Oregon | 20.739 | 3,653.33 | 1,908.20 | 5.09 |

The two-Oregon run's peak physical footprint was 7.985 GiB. These are different
memory measures sampled over the full switch/failure/rollback experiment, not
simultaneous RSS and heap readings. Failed selections degraded health to 503
while leaving the previous handler usable. Separate Newport lookup-only and
retained v3 cycles preserved IDs, ambiguity and profile behavior. All state files
were temporary; `data/deployment.json` retained SHA-256
`d7070c4c86ff7221ea2691aae07fe37436b5d37fc878e12c7b2c5aa1c2e90aff`.

Verification completed:

- `gofmt`, `git diff --check`, `go test ./...`, `go vet ./...`, and race tests for
  routing, dataset, API and importer; integration-tag compilation.
- Small deterministic corruption/version tests; randomized spatial/full-scan
  coordinate and address equivalence; 540 seeded accelerated/reference grid
  cases with direction costs, one-ways, via-way bans and destination state;
  forced chains/rings, partial endpoints, ties, pre-cancelled and in-progress
  cancellation, and snapshot lifetime checks.
- Existing 31 Newport coordinate and 22 address cases, reference comparisons,
  26 geocoding cases on baseline and candidate, retained-profile snapshot cycles.
- All 31 source-backed Oregon cases, explicit Idaho before/after tests and
  southern route sensitivity checks. All 31 also passed against a separately
  running real HTTP server.
- Six representative Newport real HTTP requests covering coordinate, address,
  both mixed directions, ambiguous resolution and unsupported-unit behavior.
  Oregon address resolution returned `no_match` with no candidates, as expected
  for its empty lookup tables. Both servers were stopped after verification.

## Retained evidence and next experiment

Candidate files are `data/scaling/newport.sqlite` and
`data/scaling/oregon-final.sqlite`; independent rebuilds and the initial
Oregon/Washington-only graph remain alongside them. All downloads, generated
databases, temporary deployments, profiles and full benchmark outputs are ignored
under `data/`, outside routine tests and version control. Key evidence files
under `data/scaling/` are:

- `baseline-tests.txt`, `baseline-original-build.txt`, `baseline-performance.txt`,
  `baseline-load-only.txt`, `newport-rebuild.txt`, `newport-measurement.txt`,
  `newport-load-only.txt`.
- `acquisition.json`, `targets.json`, `oregon-final-build.txt`,
  `oregon-load-only.txt`, `oregon-performance.txt`, `storage-strategies-final.txt`.
- `oregon-final-integration.txt`, `oregon-final-routes.jsonl`,
  `oregon-boundary-final.txt`, `oregon-sensitivity.jsonl`, `oregon-http.txt`,
  `oregon-no-address.json`, `newport-http.jsonl`, `geocoding-final.txt`.
- `reproducibility.json`, `newport-report.json`, `oregon-lifetime.txt`,
  `cross-region-lifetime.txt`, `newport-final-verification.txt`,
  `newport-legacy-cycle.txt`, `final-test.txt`, `final-vet.txt`, `final-race.txt`.

The next experiment should use a larger contiguous multistate graph, including
interior endpoints whose best routes leave their immediate regions. Compare a
flat read-only mapped/bounded backend and a turn-history/destination-aware
hierarchy above the forced-chain core against this reference. Measure cold and
warm faults, worst-case disconnected searches, long routes, query-state memory,
startup and concurrent replacement, with bounded request admission. Import
construction also still has large transient maps and source material.

For scale intuition only, 100 million similarly represented nodes would imply
about 29 GiB retained query heap by linear extrapolation from this Oregon graph.
That is a scenario, not a measured US node count or an adequate national sizing
model. Restriction density, source geometry, access data, long-query frontiers,
neighbor detours and reload overlap can change the result. A national dataset,
full hierarchy, bounded runtime backend and independent duration observations
remain unfinished work.
