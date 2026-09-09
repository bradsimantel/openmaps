# Dense prepared routing edges

Date: 2026-09-08. Scope: representation work based on `5e74860`, using retained
Newport, buffered Oregon and complete Oregon–Washington–Idaho snapshots. This
follows the [historical residency investigation](0023-routing-residency-and-rollback.md).
Inputs, retained publications, public IDs, source provenance, the active deployment,
the browser and `estimated-driving-v1` remain unchanged. Nationwide readiness is
not established.

## Experiment and decision

Prepared v2 retains the hierarchy and algorithm and replaces only directed-edge
records: 32 bytes instead of 48. Each edge stores two uint32 coordinate/CSR node
ordinals, a 31-bit segment ordinal plus a reverse bit, an int32 cell entrance,
and the original float64 distance and directional seconds. Incoming-edge labels,
restriction history, destination phases, partial endpoints and recursive paths
retain their original identities and behavior. No cost quantization is introduced.

The investigation traced actual uses before selecting this encoding. Legacy edges
hold two int64 source node IDs, an int-sized segment reference, reverse/padding,
a cell entrance and two float64 costs. Search repeatedly used those source IDs to
find coordinates, adjacency and junction ordinals. A dense edge can address all
three arrays directly; reconstruction can obtain exact geometry the same way.
The prototype changed those accesses before evaluating HTTP and diagnostic pages.

Source segments still contain their source way/node IDs and stable geometry
references. Their 48-byte records, the 512 MiB multistate node hash table,
coordinates, adjacency, segment directions, zones, continuations, cell transfers,
recursive reconstruction data, source strings and spatial indexes are unchanged.
Snapping still queries segment geometry through source IDs; address/access flags,
public-node membership and bounded source-connected endpoint walks retain the
lookup. Those walks reconstruct the original directed endpoints from the source
segment. Address evidence and public responses never expose node ordinals. Removing
remaining snapping lookups or shrinking other arrays is a possible later experiment.

This is a useful verified increment despite the resource-target misses
below, including the lifecycle clean-residency goal. The large measured reduction in logical query record pages and the smaller
underlying graph file justify retaining it. It does not claim that every declared
budget passed or that physical memory is enforceably bounded.

## Budgets, facilities and comparison method

`data/dense-scale/budgets.json` was written before prototype evaluation. Goals:

- At least 9% smaller mapped artifacts; at least 99% fewer Seattle–Boise
  source-node lookup pages; at least 10% fewer total query record pages.
- An 8% reduction in sampled clean mapped-file residency, considered separately
  from RSS, charged footprint, Go heap and file virtual size.
- Exactly 564,380 Seattle–Boise labels; multistate one-worker HTTP p95 below
  one second and four-worker throughput at least 12 requests/s.
- At most 25% matched Newport/Oregon regression; startup at most 2/12/25 seconds
  for Newport/Oregon/multistate.
- Four Portland–Ashland full-geometry requests during replacement: sampled RSS
  at most 6.5 GiB, charged footprint at most 1 GiB and publication at most 90 s.

Heavy jobs were serialized. Baseline HTTP/lifecycle binaries were compiled from
unchanged `5e74860` sources before edits. Ordinary runs use Go 1.26.1,
`GOMEMLIMIT=3GiB GOGC=50`, identical SQLite snapshots and retained workloads.
Diagnostic overlays use `.go.txt` files under ignored `data/dense-scale/`; they
are separate from normal timing binaries. Expanded record-page tracing compares
v1 and v2 publications with the same diagnostic reader. The original baseline
node-page trace remains retained under `data/residency-scale/`.

The host is Darwin 25.4/macOS on ARM64 with 16 GiB RAM and 16 KiB VM pages.
Read-only controls and tools were inspected and recorded in `controls.txt`:
`vmmap`, `footprint`, `vm_stat`, `sysctl`, `memory_pressure -Q`, process resource
sampling and Go statistics. There is no configured process RSS enforcement or
controlled cold-cache facility. No application was closed, system cache purged,
host setting changed, artificial physical pressure generated or infrastructure
provisioned. Integrity validation reads all artifact pages before first requests.
These are reproducible desktop observations on previously accessed files, with
ambient cache/pressure variability. Go soft limits do not limit mapped RSS.

## Artifact and logical query pages

| Publication | v1 bytes | v2 bytes | Saved MiB | Reduction |
| --- | ---: | ---: | ---: | ---: |
| Newport | 231,644,816 | 211,915,296 | 18.82 | 8.52% |
| Oregon | 1,948,892,681 | 1,764,750,905 | 175.61 | 9.45% |
| Multistate | 4,575,059,223 | 4,132,749,063 | 421.82 | 9.67% |

The multistate directed-edge section shrinks from 1,265.46 to 843.64 MiB.
Newport misses the 9% artifact target because other sections account for more
of its file; its edge section still shrinks by one third. No retained v1 file is
replaced. Section inventories, exact file lengths and independent hashes are
retained in `sections.json` and `reproducibility.json`.

The overlay records VM pages intersecting each mapped record or range read by
search, snapping and reconstruction, using actual addresses and OS page size.
It covers coordinates, edge records, CSR offsets/adjacency, directions, node
lookup, segment records/strings, guards, zones, continuations, components,
junctions, spatial nodes/IDs and cell data. It records full accessed records and
returned ranges, not individual CPU load instructions; ranges can conservatively
include records returned to a caller before it stops iterating. Ancillary Go maps,
heap labels, hardware prefetch and kernel reads are outside this measurement.
Section alignment can change individual page counts despite identical records.
These are logical record-page unions, not faults, simultaneous residency or total
system page-cache occupancy.

| Multistate page trace (16 KiB pages) | v1 | v2 | Reduction |
| --- | ---: | ---: | ---: |
| Seattle–Boise node lookup | 32,769 | 334 | 98.98% |
| Seattle–Boise edges | 47,784 | 35,320 | 26.08% |
| Seattle–Boise all traced records | 148,691 | 103,725 | 30.24% |
| Complete workload all traced records | 180,393 | 133,098 | 26.22% |

The 99% node-lookup goal is narrowly missed: the remaining 334 pages (5.22 MiB
of page addresses) come from source-boundary endpoint work. The complete workload
node-lookup union is 3,275 pages versus 32,769. It is not removed from the artifact.
All 31 Newport, 31 Oregon and 29 multistate diagnostic cases retain identical label
counts and outcomes between versions. The source suites separately verify
correctness; diagnostic agreement is not a substitute for Dijkstra or source checks.

## Normal HTTP and query scratch

Each retained fixture runs twice per worker with complete response encoding,
followed by the existing cancellation and four-response-held admission checks.
No diagnostic page instrumentation is present in these timing binaries.

| Region / workers | Baseline requests/s | v2 requests/s | Baseline p95 ms | v2 p95 ms |
| --- | ---: | ---: | ---: | ---: |
| Newport / 1 | 3002.17 | 2905.13 | 0.568 | 0.699 |
| Newport / 4 | 8538.70 | 8592.66 | 1.086 | 1.036 |
| Oregon / 1 | 45.83 | 49.10 | 31.422 | 27.467 |
| Oregon / 4 | 156.66 | 162.10 | 186.238 | 181.535 |
| Multistate / 1 | 10.67 | 12.08 | 441.087 | 434.500 |
| Multistate / 4 | 38.49 | 36.12 | 556.805 | 601.010 |

Normal latency/throughput gates pass. Newport one-worker p95 increases 23.1%,
within the 25% band. Multistate four-worker maximum is 980.057 ms versus
875.655 ms baseline. These observations support preserved performance, not a
uniform throughput gain. The final reader's second HTTP comparison gives v1/v2
one-worker p95 446.027/412.187 ms and four-worker throughput 37.59/38.40 requests/s.
V2 four-worker maximum is 898.892 ms in that pass. Thus normal v2 HTTP p95 ranges
412–435 ms and four-worker throughput 36.12–38.40 requests/s across the two runs.
A separate v2 domain run gives one-worker p95 470.304 ms
and four-worker 35.12 requests/s, maximum 1,054.509 ms. Historical baseline runs
also varied materially; cache and desktop pressure were not controlled.

Seattle–Boise retains exactly 564,380 labels, 558,507 expansions and 4,366,369 cell
transfers, with 9,196 geometry vertices. Its detailed query allocates 172,566,136
bytes cumulatively; the label map and queue representation are unchanged. Sampled
ordinary HTTP peaks are 4.352/4.339 GiB RSS and 441.43/459.93 MiB charged footprint
for baseline/v2. A 9.67% smaller artifact therefore does not imply the same drop
in a whole-process HTTP peak, and private query allocations remain significant.

## Startup, residency and lifecycle

Direct v2 startup in the stage diagnostic takes 0.259/3.892/10.025 seconds for
Newport/Oregon/multistate, within the 2/12/25-second goals. These are previously
accessed files after full integrity validation, not controlled cold reads.
A final load-only pass records v1/v2 startup 0.374/0.396 seconds for Newport,
2.352/4.054 seconds for Oregon and 9.057/10.093 seconds for multistate. The
absolute startup goals pass, but Oregon startup regresses in this matched pass;
dense runtime validation also checks source-segment endpoint ordinals. There is
no claim of a startup speedup. Post-GC retained v2 heaps are 11.20/5.18/9.38 MiB.
First short HTTP requests after validation take 1.242/4.225/5.580 ms for v2 versus
1.254/4.127/5.387 ms baseline; these include loopback and encoding, not cold storage.

Multistate publication verification takes 1.842 seconds and allocates 2,121,512
bytes cumulatively, leaving its 4,132,749,063-byte query mapping unchanged.

| Multistate stage | v1 RSS GiB | v2 RSS GiB | v1 clean mapped GiB | v2 clean mapped GiB |
| --- | ---: | ---: | ---: | ---: |
| Loaded | 4.290 | 3.881 | 4.260 | 3.849 |
| First short request | 3.937 | 3.602 | 3.908 | 3.570 |
| Two complete query passes | 3.964 | 3.796 | 3.816 | 3.657 |
| Retired | 0.107 | 0.139 | 0 | 0 |

The loaded clean category falls by 9.66%, exceeding the 8% goal at that stage;
after two passes it falls by only 4.19%. Full integrity scans, ambient eviction
and query allocations affect these separate snapshots. Loaded v2 Go heap is
10,943,344 bytes and Go `Sys` 32,069,912 bytes, versus 426.96 GiB process virtual
reservation and 3.849 GiB explicit query mapping. Two query passes accumulate
about 1.22 GB of Go allocations while heap is about 17.1 MiB at the final stage.
Go heap, virtual reservations, mapped file size, RSS and charged/private footprint
must not be substituted for each other. VM and footprint files retain clean,
dirty and swapped/compressed categories; host swap belongs to all applications.

| Four Portland–Ashland responses plus lifecycle | Baseline v1 | v2 |
| --- | ---: | ---: |
| Sampled peak RSS, GiB | 5.212 | 4.987 |
| Sampled peak charged footprint, MiB | 178.36 | 166.94 |
| Replacement publication, seconds | 17.464 | 15.432 |
| Complete test, seconds | 28.96 | 27.37 |
| Query-artifact mapping overlap, bytes | 6,523,951,904 | 5,897,499,968 |
| Process pageins | 525,956 | 435,868 |
| Process storage reads, GiB | 12.559 | 10.025 |

Both ordinary long-route runs meet the RSS/footprint/publication budgets. All four
workers receive full geometry. Failed selection leaves responses usable; rollback
to the still-serving publication does not add a query mapping. Restoration of the
original graph validates and loads its own publication, then retires the prior
mapping. Retired bytes are zero. Cumulative allocations reach 8.42 GB for v2 versus 9.44 GB baseline over the
lifecycle. Workers loop during loading, so these are different request counts;
compact files do not shrink the label/encoding workload.
Storage/pagein differences are observations, not controlled cache experiments.

The final ordinary (zero-distance) four-worker lifecycle also passes: publication
12.407 seconds, sampled peak RSS 5.120 GiB and charged footprint 38.13 MiB. That
fixture has substantially less per-request work than the full-geometry long route.

Mixed v1 Oregon → v2 multistate → v1 Oregon and v2 Oregon → v1 multistate → v2
Oregon cycles both pass, with four complete long-route workers, publication
19.210/18.747 seconds and sampled peak RSS 4.938/5.229 GiB respectively. These use
new directories linking immutable retained publications and temporary deployment
state. Existing publications and the active deployment remain unchanged.

Periodic `vmmap`/`footprint` sampling runs separately from latency measurements.
Their peak clean mapped-file category is **4.611 GiB baseline versus 4.596 GiB v2**,
only a 0.33% decrease: the 8% lifecycle clean-residency goal is **not met**.
Sampled RSS is 5.082/4.833 GiB and publication 28.978/27.298 seconds in those
instrumented runs. Probes perturb timing and can miss lifetime maxima. This
experiment demonstrates smaller files and fewer logical query pages, plus lower
startup clean residency; it does not establish a comparable sustained lifecycle
physical-memory reduction. Clean mapped-file accounting also excludes unrelated
system cache and is not total unique system physical memory.

## Separate Go soft-limit observation

With `GOMEMLIMIT=128MiB GOGC=50`, the v2 HTTP workload passes cancellation and
bounded admission with no timeouts. One-worker p95 is 472.338 ms; four-worker
throughput is 20.92 requests/s, p95 967.282 ms and maximum 2,020.319 ms. Sampled
RSS still reaches 4.199 GiB and charged footprint 345.99 MiB, well beyond 128 MiB.
The process records 156,321 pageins and 3.058 GiB storage reads. This is pressure
on Go's GC budget, not kernel-enforced physical pressure or a hard memory limit.
A separately serialized baseline run with the same soft-limit settings has
one-worker p95 462.172 ms, four-worker 27.94 requests/s, p95 755.867 ms and maximum
1,564.431 ms, with no timeouts. Baseline sampled RSS is 4.614 GiB and charged
footprint 346.97 MiB, with 279,946 pageins and 4.695 GiB storage reads. The candidate
therefore has worse throughput/tails in this pressure observation despite lower
RSS; charged footprint is essentially unchanged. Files/cache and physical pressure
remain uncontrolled, so neither run establishes a physical-memory guarantee.
Latency tails and these resource counters remain separate from the normal table.

## Validation and publication boundaries

The trusted offline publisher still validates the entire SQLite snapshot, including
lookup identity and source provenance, constructs canonical topology/hierarchy,
checks source stability and publishes the receipt last. It does not approve a
caller-supplied compact artifact. Dense records are streamed directly from that
source-validated graph with checked ordinal/segment capacity. No runtime
translation map, graph-sized buffer or graph reconstruction replaces the mapping.

Prepared v1 and v2 have independent artifact names and explicit width checks.
Receipts and headers must agree on the supported version. Complete source and
artifact hashes, source metadata/summary binding, overflow-safe dimensions and
canonical padding remain mandatory. Runtime validation checks dense endpoint
bounds before CSR use and checks each endpoint against the source segment's node
ordinal and direction. Existing direction, CSR, continuation, recursive path,
spatial, restriction and access checks remain. A self-checksummed topology change
cannot change the independently trusted artifact digest.

Rollback continues streaming source/artifact verification with a 1 MiB buffer
and no additional query mapping. Failed validation leaves selection and serving
mapping ownership unchanged. Queries hold read leases through reconstruction;
HTTP leases cover complete response encoding. Returned geometry and strings own
their storage after retirement. A final cancellation check now also rejects
cancellation delivered at completion of structural validation, before mapping
ownership is published.

Deterministic tests exercise both prepared versions, unsupported/mismatched
versions, malformed dimensions and overflow, invalid dense/source references,
corrupt/foreign/self-checksummed artifacts, cancellation at each load stage,
failed publication, sparse source IDs exceeding float64 integer precision,
directional source-edge identity, partial geometry, destination/access and via-way
restriction behavior, concurrent close, independent encoding, version cycles and
output validity after retirement. Initial prototype tests exposed and corrected a
source-node versus dense-node comparison at equal snapped endpoints; the failure
and successful rerun remain in ignored evidence.


## Source verification, reproducibility and remaining gates

The retained source suites pass: Northwest's 29 cases (91.76 seconds), Oregon's
31 cases, Newport's 31 coordinate and 22 address cases including mixed API
requests, both sensitivity suites and Oregon boundary coverage. The Northwest
suite includes both Oregon-to-Oregon optima through Idaho, independent source
adjacency/direction/restriction checks, geometry/cost sums and ordinary Dijkstra.
Seattle–Boise has exact source-path equality; its reference takes 10.276 seconds
in this run. All numerical assertions retain
`max(1e-6 seconds, abs(reference seconds) * 1e-10)`. The explicitly logged offline
reference timeout is **120 seconds**, with `GOMEMLIMIT=3GiB GOGC=50`; production
timeouts and the reference algorithm are unchanged.

Lookup-only and retained legacy-v3 → prepared-v2 graph-v4 → v3 snapshot cycles
pass. The latter intentionally distinguishes the prepared encoding version from
the source graph/profile version. Both real-HTTP admission/cancellation and the
v3/v4 lifecycle also pass under the race detector. `gofmt`, `go test ./...`,
`go vet ./...` and routing/dataset/API/importer race suites pass. The final
cancellation-stage regression is included in the Go and race checks.

Two independent source-backed preparations reproduce all three v2 artifacts
byte-for-byte. All **75 non-edge sections** (25 per publication) additionally
match their retained v1 section bytes exactly, checked by streaming hashes in
`unchanged-sections.json`; section counts and graph digests also match. Their
complete artifact SHA-256 values are recorded in
`data/dense-scale/reproducibility.json`, alongside immutable receipts. Preparation
still validates the authoritative SQLite inputs and generates the unchanged
hierarchy. No conversion command blesses an unvalidated artifact. Newport and
Oregon preparation take about 4–5 and 35–38 seconds; multistate preparation varies
from 163.77 to 202.37 seconds on this desktop. The first multistate preparation takes
163.77 seconds and reaches 6,045 MiB sampled charged footprint despite the 3 GiB
Go soft limit. Live construction still holds the full original 48-byte edge array
and source-node map. Streaming smaller output records therefore does not establish
bounded-memory offline import or preprocessing.

National import/preprocessing, national address loading, the existing 128 MiB
ancillary representation cap, enforceable physical-memory bounds and controlled
cold-cache/pressure behavior remain separate readiness gates. The source-node
lookup and 48-byte segments still consume substantial mapped space. Query label
scratch and complete response geometry remain independent costs. Variable-speed
and hazmat interpretation remain unchanged issues for a separate versioned
cost/profile milestone. No national or supplemental address data was acquired.

Evidence under ignored `data/dense-scale/` includes the predeclared budgets,
command/environment records, baseline/prototype/final binaries, source and HTTP
logs, diagnostic overlays and scripts, page traces, section inventories, VM
snapshots, resource samples, independent receipts and artifacts, and the resource
summarizer. `prototype-evaluate.sh`, `next-checks.sh`, `verify.sh` and `finish.sh`
record the serialized workflow. Their generated output paths must be changed to
new names for a rerun; existing publication and residency output paths refuse
replacement. No browser change, commit, push or active deployment change is part
of this work.
