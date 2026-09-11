# Junction hierarchy and mapped query data

Date: 2026-09-08. Historical implementation and verification record.
Baseline revision: `c4e2a76401be2efa8a93bdf45ea10230f09ee395`.
Scope: this worktree's routing, import, numeric storage and API deployment changes.
No commit, push, active deployment selection, retained database or browser change.
This extends the [historical Oregon experiment](0019-routing-scale-and-oregon.md).
Current behavior belongs in [routing scale](../routing-scale.md),
[routing](../routing.md) and [refresh](../refresh.md).

## Outcome and limits

This delivers a verified regional scaling increment: a conservative junction
elimination level, smaller per-query state, negative component filtering,
read-only mapped numeric arrays, bounded HTTP admission, and a larger contiguous
Oregon–Washington–Idaho candidate. **Nationwide readiness remains unfinished.**
The hierarchy still leaves a substantial junction core. The mapped backend bounds
Go heap use for its numeric sections, but still reconstructs the complete graph
at startup and retains other query structures. It does not bound process RSS.

The multistate correctness suite passed all 29 cases against ordinary Dijkstra
and independently checked source paths. Its one-worker p95 exceeds the 1-second
experiment target. No conterminous US candidate was acquired or built. National
measurements are absent; the resource scenarios below are explicitly extrapolations.
Alaska and Hawaii remain separate subsequent coverage scopes.

## Machine, inputs and targets

The host has 16 GiB RAM, an Apple M5, 10 logical CPUs, Go 1.26.1 darwin/arm64,
Osmium 1.19.1 and initially about 208 GiB available disk. It also runs desktop
applications. macOS RSS and physical footprint differ; neither equals Go retained
heap. `data/junction-scale/` retains full outputs, candidates, profiles, source
inspection and the pre-change instrumented test binary. Heavy measurement runs
were serialized. An early run overlapping the multistate build is retained as
`oregon-junction.txt` and excluded from latency conclusions.

The existing Oregon, Washington and Idaho PBFs were rehashed against the retained
lock. All have pinned release **2026-09-07T20:21:20Z**. No newer source or address
acquisition was needed. The retired experiment's Northwest source record captured
the three upstream SHA-256 values and the full merged derivative:

- `northwest-260907.osm.pbf`: 744,258,582 bytes, SHA-256
  `3af61891279d9a393d4630800e3253d698a59348fc71f4a2dc7c5cfeae6ebf2c`.
- Osmium merge of the complete three extracts; 98,544,271 OSM nodes,
  9,530,610 ways and 82,565 relations. `check-refs` found zero missing way nodes.
- Endpoint bounds `[-125,41.98,-110.9,49.01]`, longitude/latitude. Included routing
  geometry bounds `[-124.7228033,41.5401763,-110.9600424,49.08177]`.
  Complete ways may cross extract borders; rectangles do not certify continuous
  road coverage. California, Nevada, Utah, Montana, Wyoming and Canada beyond
  incidental extract buffers are not acquired. No searches are partitioned by state.
- OSM attribution and ODbL provenance remain in each lock and SQLite graph.
  See the primary [Geofabrik catalog](https://download.geofabrik.de/north-america/us.html)
  and [OSM copyright/license](https://www.openstreetmap.org/copyright), checked
  2026-09-08. The derivative's header lacks a replication timestamp; the release
  records come from its verified, same-release upstream extracts.

Before implementation, `targets.json` recorded these local engineering gates:

| Measure | Target |
| --- | --- |
| Correctness | Identical snaps/outcomes; cost tolerance `max(1e-6, abs(reference)*1e-10)` |
| Oregon long-route states / allocations | At most 80% of baseline |
| Oregon long-route latency | At most 90% of baseline |
| Mapped retained heap | At most 70% of resident-array backend |
| Mapped warm throughput | At least 80% of resident-array backend |
| Oregon / multistate startup | At most 35 / 75 seconds |
| Multistate build peak RSS / retained heap | At most 10 / 3 GiB |
| Multistate one-worker p95 / maximum | At most 1 / 3 seconds |
| Multistate four-worker throughput | At least 12 requests/second |
| Replacement peak RSS / latency | At most 12 GiB / 90 seconds |

The performance gates are bounded desktop experiments, not production SLOs or
preregistered statistical estimates. Correctness gates are independent of modeled
duration accuracy.

## Measurements before choosing the increment

The rerun on retained Oregon showed the dominant remaining work clearly:
Portland–Ashland took 1,034.357 ms in search, 0.159 ms snapping and 0.587 ms
materializing geometry. It expanded 955,333 states, retained 958,196 labels,
pushed 961,143 queue entries, traversed 5,007,880 forced-chain edges and allocated
511,822,440 bytes in the request. Queue peak was only 9,152 entries with capacity
11,468 (40 bytes per entry). State maps, repeated traversal and allocation dominate;
the live queue alone is not the main memory cost.

The former unreachable fixture started in a tiny disconnected component. Its
reverse starts in the large network, preserving exactly the same two independently
selected endpoints. On the new multistate graph it took ordinary Dijkstra
15.941 seconds, versus 0.139 ms for accelerated negative connectivity rejection.
The filter only compares weak components after both endpoints are selected; it
never makes a same-component request reachable or changes a snap.

## Algorithm decision and semantics

Current primary research was consulted before choosing the increment:
[Geisberger et al., Contraction Hierarchies](https://ae.iti.kit.edu/download/contract.pdf),
[Geisberger and Vetter, turn-aware routing](https://publikationen.bibliothek.kit.edu/1000097647),
and [Dibbelt et al., Customizable Contraction Hierarchies](https://arxiv.org/abs/1402.0402v5).
CH motivates explicit shortcut reconstruction and conservative partial contraction.
Turn-aware contraction reinforces retaining approach identity. CCH's separation of
ordering and metric customization is relevant to future changing metrics, but
this snapshot has a fixed cost model and much richer via-way/access state than a
plain node-weighted graph. We did not implement or claim full CH/CCH.

`independent-junction-v1` picks an independent set of ordinary three/four-departure
junctions on the forced-chain core. Nodes of every prohibited sequence and nodes
incident to destination access remain explicit. Incoming/outgoing shortcut pairs
are stored in factorized form using the selected-node array and original CSR
adjacency. Reversal of the same segment remains forbidden. Both original geometry
sequences are reconstructed; every traversed edge still advances turn history and
destination phase, and adds its original directional cost in traversal order.
Target-segment nodes stay explicit for partial arrivals. A single label map and
a typed priority queue reduce duplicate keys and interface boxing allocations.

This leaves contraction across restricted junctions, recursive shortcut levels,
witness searches and a complete hierarchy over turn-history/destination states
unfinished. It does not approximate optimality, relax restrictions, substitute
node-only state, alter the speed model or use endpoint-region graph loading.
Ordinary Dijkstra remains available under identical endpoints and costs. Ties may
choose different equal-cost paths under the documented tolerance; tested regional
paths matched their references.

## Storage and import implementation

New manifests retain `routing-chunks-v1` with the new preprocessing identifier;
legacy `forced-chain-v1` manifests and JSON graph formats 1–4 remain readable.
All runtime preprocessing is regenerated from validated source topology. The
optional `routing-hot-le64-v1` cache stores explicit little-endian numeric sections
with exact float64 costs, checked offsets/counts, zero padding and SHA-256.
The reader verifies ABI offsets before pointer-free read-only typed views.
An existing file must equal canonical bytes independently reconstructed from the
validated SQLite graph; it cannot inject different shortcuts or costs simply by
recomputing its own checksum. New cache files are synced and atomically published
without overwriting existing artifacts. Corruption and unsupported versions fail.

Coordinates, directed edges, CSR arrays, directions, chain continuations, zones,
components and junction selections move out of the Go heap. Segment strings and
records, spatial indexes, source-ID maps, restriction state and address evidence
remain resident; provenance stays in SQLite. There is no SQL per visited edge.
This is integrated runtime access, not the earlier coordinate-only microbenchmark.
It still has full reconstruction/validation startup memory. OS page residency and
query-label memory remain separate unbounded costs.

Two full-three-state build attempts exposed construction limits. The first was
stopped after 498.99 seconds: peak RSS 6.53 GiB, peak physical footprint 9.40 GiB,
with sampling dominated by GC while building restriction adjacency. The importer
then limited adjacency to from/via-way nodes while retaining every departure
needed to enforce only-turn alternatives. A second stopped attempt reached graph
validation but held a second query graph alongside raw import records. The final
pipeline writes chunks in the unpublished transaction, releases importer data,
then loads/validates those exact chunks before commit. Invalid graphs still roll
back. Source decisions and restrictions are checked for reproducibility separately.

The completed three-state build took **285.50 seconds**, with peak RSS
**6.36 GiB** and peak physical footprint **8.03 GiB**, under `GOMEMLIMIT=7GiB`.
SQLite is **690.12 MiB** and contains **13,700,712 nodes, 14,218,671 segments,
1,460,581 included ways, 2,651,284 guards and 26,188 prohibited paths** from
23,231 enforced relations. There are 55,467 destination segments and 570
conservative malformed-restriction closures. These counts describe interpreted
source data, not complete real-world road restrictions. Lookup tables are empty.

## Query and process measurements

Times below are wall-clock samples from compiled integration binaries, with
warm input/cache files and `GOMEMLIMIT=6GiB`. They are not statistically independent
trials. Preprocessing is included in load time. Process peaks include startup and
all requests; the separate startup-only runs below distinguish that scope.

| Graph / backend | Load s | Preprocess s | Selected junctions | Retained heap MiB | Mapped bytes | Whole-run peak RSS GiB / footprint GiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Oregon previous chains | 21.034 | — | 0 | 1,706.20 | 0 | 4.48 / 4.66 |
| Oregon junctions, heap | 23.940 | 2.480 | 249,440 | 1,733.40 | 0 | 3.52 / 3.48 |
| Oregon junctions, mapped | 23.599 | 2.489 | 249,440 | 803.26 | 975,292,145 | 4.03 / 3.11 |
| Newport junctions, mapped | 2.358 | 0.180 | 34,314 | 105.88 | 105,321,068 | 0.50 / 0.39 |
| Northwest junctions, mapped | 68.397 | 7.655 | 586,364 | 1,854.33 | 2,342,433,112 | 5.93 / 6.01 |

The Newport baseline retained 203.36 MiB. Oregon mapped heap is 46.34% of the new
heap backend; mapping cuts retained Go memory without implying a similar RSS cut.
The hierarchy's component/junction arrays add some heap to the unmapped backend.
Mapping leaves 1.81 GiB of Northwest query structures on the Go heap.

Portland–Ashland search fell from 1,034.357 to **837.863 ms** on the heap backend,
with labels falling from 958,196 to **643,753** and request allocation from
511,822,440 to **170,596,024 bytes**. Expansions fell to 640,694; chain-edge visits
rose to 6,583,714 because eliminating an intermediate queue label can repeat its
outgoing traversal for different incoming histories. There were 1,274,047 junction
shortcut visits. Queue peak/capacity were 10,291/11,468, still much smaller than the
label set. A label entry's key/value payload is 72 bytes before hash-table overhead;
a queue entry is 40 bytes. Reported allocation is total allocated bytes, not peak
live query memory. Both state and allocation reduction targets passed; latency
was 81.0% of baseline, passing its 90% target.

Northwest Seattle–Spokane needed 860,896 labels and 174,910,936 allocated bytes;
search took 1,131.511 ms. Seattle–Boise needed **1,639,595 labels**, 1,631,335
expansions, 17,171,002 chain-edge visits and **339,671,352 allocated bytes**;
search took **2,237.886 ms**, compared with 0.191 ms snapping and 1.415 ms geometry.
Those remaining labels and repeated edge work motivate a deeper hierarchy.

The bounded workload executes each case twice per worker. Oregon has 31 cases,
Northwest 29; workers use the same cyclic order. Times include snapping and
geometry, and failures are included. Percentiles are empirical request samples.

| Backend | Workers | Requests | Requests/s | p50 ms | p95 ms | Max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Oregon previous chains | 1 | 62 | 13.42 | 10.701 | 122.949 | 1,051.884 |
| Oregon previous chains | 2 | 124 | 26.26 | 10.669 | 560.558 | 1,077.169 |
| Oregon previous chains | 4 | 248 | 44.67 | 12.547 | 668.905 | 1,272.483 |
| Oregon junctions, heap | 1 | 62 | 16.45 | 9.266 | 98.828 | 838.313 |
| Oregon junctions, heap | 2 | 124 | 31.68 | 9.528 | 465.068 | 864.819 |
| Oregon junctions, heap | 4 | 248 | 55.14 | 10.498 | 530.755 | 997.053 |
| Oregon junctions, mapped | 1 | 62 | 16.39 | 9.403 | 100.068 | 840.203 |
| Oregon junctions, mapped | 2 | 124 | 31.84 | 9.807 | 461.444 | 855.227 |
| Oregon junctions, mapped | 4 | 248 | 55.02 | 10.732 | 542.457 | 1,007.962 |
| Newport junctions, mapped | 1 | 62 | 2,754.35 | 0.177 | 0.917 | 3.316 |
| Newport junctions, mapped | 2 | 124 | 5,523.75 | 0.154 | 1.033 | 3.345 |
| Newport junctions, mapped | 4 | 248 | 11,268.57 | 0.162 | 1.012 | 3.155 |
| Northwest junctions, mapped | 1 | 58 | 3.71 | 46.232 | 1,433.363 | 2,558.624 |
| Northwest junctions, mapped | 2 | 116 | 7.30 | 48.309 | 1,502.226 | 2,436.237 |
| Northwest junctions, mapped | 4 | 232 | 10.79 | 61.405 | 1,964.255 | 3,229.172 |

Mapped Oregon throughput is effectively unchanged in this sample, passing the
80% retention gate. Northwest passes one-worker maximum latency, startup, build
RSS and retained heap gates, but **fails one-worker p95 and four-worker throughput**.
The experiment therefore does not authorize progression to a national build.

### Access and file-cache limits

`TestMappedRoutingAccess` runs the actual Oregon router after `MADV_DONTNEED`,
then repeats the same source-backed workload. Startup was 22.792 s. After Go's
`FreeOSMemory`, retained heap was 842,276,600 bytes and `ps` RSS was 4,251,984 KiB;
virtual size was 451,094,560 KiB, mostly address reservations, not committed RAM.
The advice did not materially reduce RSS. Pass 1 took 1.922 s with 27,735 minor
faults and zero major faults; pass 2 took 1.920 s with 36,320 minor faults and zero
major faults. Counts include request allocations and other process activity.

**This is not a verified cold-file-cache measurement.** Startup validation reads
all mapped bytes, and macOS advice did not establish file-cache eviction. Rebooted
or controlled Linux cache experiments and storage-I/O instrumentation remain
necessary before claiming cold national latency. No system-wide cache purge was
performed on this shared desktop. Mapping's cold behavior remains a stated limit.

## Correctness, coverage and source investigation

All 29 Northwest and 31 Oregon source-backed cases passed reference Dijkstra,
including exact snapped evidence, same success/failure classification, source
adjacency, direction, prohibited sequences, destination access and original cost
sums. All those regional accelerated/reference source paths were identical.
Newport's 31 coordinate and 22 address/mixed cases also passed with mapped arrays.
The deterministic suite covers shortcut use, partial arrivals/departures, ties,
via-way history, access phases, disconnected components and cancellation. Flat
artifact tests cover truncation, byte corruption, header padding, unsupported
format/preprocessing versions, cancellation, mapping reuse and lifetime.

The new endpoints were chosen from source road midpoints near ten predeclared
city centers, independently of route success or cost. Coeur d'Alene's selected
road midpoint was 2.216 km from its center; the initial 2 km seed-selection run
failed before any routing. The declared selection radius became 3 km, and the
source-only streaming generator succeeded. The failed selection report is retained.
Sixteen new bidirectional cases join thirteen retained Oregon/error/boundary cases.
They cover Seattle, Spokane, Bellingham, Wenatchee, Yakima, Coeur d'Alene, Lewiston,
McCall, Boise and Idaho Falls, including urban roads, river/bridge crossings,
mountain corridors and long interstate trips. Jordan Valley–Ontario in both
directions has Oregon endpoints but is required to enter the Idaho check box
`[-117,43.2,-116.2,43.7]`; both optimum source paths do so. There is no artificial
state-boundary search cutoff.

Route plausibility was checked against original OSM decisions and the primary
[WSDOT regional milepost maps](https://wsdot.wa.gov/mapsdata/products/maps_pdf/MilepostMap_AllRegions.pdf),
[WSDOT I-90 corridor description](https://apps.wsdot.wa.gov/construction-planning/major-projects/i-90-snoqualmie-pass-east-project),
and [Idaho Transportation Department maps](https://itd.idaho.gov/gis-maps/).
These sources identify corridors; they do not validate modeled travel times.

The suspicious Seattle detours expose existing cost-model limitations:

| Trip | Fastest modeled distance km | Fastest modeled duration h | Shortest-distance path km |
| --- | ---: | ---: | ---: |
| Seattle–Spokane | 500.496 | 7.70 | 448.599 |
| Seattle–Boise | 915.105 | 12.91 | 772.504 |
| Boise–Seattle | 963.516 | 12.75 | 771.347 |

Seattle–Spokane uses US 2/Stevens Pass and SR 28/281 before I-90; Seattle–Boise
also heads north before US 97/I-82/I-84. The reverse chooses I-84 via Portland and
I-5. A source-backed distance-objective comparison found 36.535 km of the shorter
Seattle–Spokane I-90 path costed at 5 km/h with `maxspeed:variable=yes`, plus
0.217 km with `maxspeed:variable=no`. For example, OSM ways 1012095795 and
1525395343 retain explicit 65/70 mph limits, but the unchanged conservative profile
treats their unsupported speed metadata as 5 km/h. OSM's
[`maxspeed:variable` documentation](https://wiki.openstreetmap.org/wiki/Key:maxspeed:variable)
distinguishes variable limits from a fixed limit; treating even `no` as an unknown
slow limit is a concrete model defect for a later versioned interpretation change.

The I-90 source scan inspected 1,356 included ways and 539 tagged referenced nodes.
It also found way 969544659 conservatively closed because `maxweight:hazmat=10000 lbs`
is treated as an unsupported dimension restriction. This is a goods-specific
qualifier (see [OSM hazmat](https://wiki.openstreetmap.org/wiki/Key:hazmat)); its
contribution to these particular detours was **not established**. The shorter
Seattle–Boise mountain alternatives use SR 410 and forest/service roads with
slower explicit model defaults. Original source tags and the sensitivity paths
are retained for review. No speed or access interpretation changed in this work.
Exact optimality under this model is separate from empirical ETA accuracy, which
was not measured. The API continues to expose estimated elapsed time without live
traffic, documented rounding, uncertainty and excluded off-road connectors.

## Real HTTP and isolated deployment

The loopback HTTP harness uses full Compute Routes responses and the same fixture
workload. It measures original request snap/search/geometry phases; residual time
also includes HTTP transport and encoding. Separately, repeated JSON encoding of
the decoded actual envelope measures pure re-encoding cost, not the original
encoder's exact contribution. Portland–Ashland's 110,203-byte envelope averaged
0.417 ms to re-encode; Seattle–Boise's 237,819 bytes averaged 0.939 ms. Search still
dominates. This preserves the existing Google Routes v2 supported contract;
current primary references are [Compute Routes](https://developers.google.com/maps/documentation/routes/reference/rest/v2/TopLevel/computeRoutes)
and [Google error conventions](https://google.aip.dev/193).

| Graph | HTTP workers | Requests/s | p50 ms | p95 ms | Max ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Oregon | 1 | 15.59 | 9.410 | 103.487 | 894.805 |
| Oregon | 2 | 30.52 | 10.139 | 474.119 | 892.556 |
| Oregon | 4 | 54.01 | 11.052 | 547.646 | 1,013.132 |
| Northwest | 1 | 3.38 | 46.301 | 1,509.243 | 2,332.282 |
| Northwest | 2 | 6.70 | 50.153 | 1,713.127 | 2,654.425 |
| Northwest | 4 | 10.66 | 64.843 | 2,020.759 | 3,316.830 |

A long HTTP request canceled after 10 ms released its handler within 0.316 ms on
Oregon and 0.429 ms on Northwest; a subsequent request succeeded. The server's
new default four-request budget is shared across snapshot replacement and held
through response encoding. Excess POST Compute Routes requests receive HTTP 429,
`RESOURCE_EXHAUSTED` and `Retry-After: 1`, with no pending queue. Other endpoints
are unaffected. Deterministic tests cover overload, cancellation and release.
Four workers bound simultaneous searches but do not impose a hard memory bound
on any one label map; unlimited caller-selected concurrency remains inappropriate.

The isolated Oregon→Northwest test retained a live old graph while new loading
and real HTTP traffic ran, then exercised a corrupt selection, continued old
service, rollback and restoration of the actual baseline. It passed in 234.86 s.
Publication took **80.097 s**, sampled peak Go heap was **7,347.54 MiB**, and whole
process peak RSS/physical footprint were **5.47/8.03 GiB**. Post-GC heap while the
test deliberately retained the old Store was 2,656.71 MiB. Old/new mappings were
975,292,145 and 2,342,433,112 bytes, a **3,317,725,257-byte virtual overlap**;
retired mapped bytes became zero after outstanding HTTP leases completed.
That sum is mapping size, not a claim that all overlap pages were resident.
Replacement passed the predeclared 90-second/12-GiB-RSS gates with limited timing
margin. All selections used temporary deployment files; the active deployment
was never changed.

## National gate and next experiment

There are **no measured national build or routing results**. The pinned Geofabrik
US catalog entry for `us-260907.osm.pbf` was 12,129,480,135 bytes, about 16.30 times
the merged three-state PBF. That is a whole-US input including Alaska/Hawaii,
not a conterminous-US census. Compressed bytes do not predict road, restriction,
geometry or query-state density reliably. Nevertheless, applying that ratio to
this build illustrates why a national attempt is not justified on 16 GiB RAM:

| Quantity | Three-state measurement | Input-byte proportional scenario, not measured |
| --- | ---: | ---: |
| Numeric mapping | 2.18 GiB | 35.55 GiB |
| Retained query heap | 1.81 GiB | 29.51 GiB |
| Build peak physical footprint | 8.03 GiB | 130.87 GiB |
| SQLite snapshot | 0.674 GiB | 10.98 GiB |
| Build wall time | 285.50 s | 77.55 min |

The scenario's mapped arrays alone exceed physical RAM; current startup also
constructs them on heap. Disk capacity was about 201 GiB after retained builds,
so memory and failing multistate query targets are the immediate limits. A larger
machine alone would not establish national latency or improve the current speed
model. These extrapolations are not a procurement specification or a guaranteed
minimum machine size.

The smallest concrete next experiment is **the same pinned 13.7-million-node
Northwest graph**, with no new acquisition: add a second/recursive contraction
level that preserves edge/trie/access state, and require Seattle–Boise to reduce
its 1.64 million labels by at least half while every existing reference case and
source-path check still passes. Retain the original 1-second p95 and 12-request/s
four-worker workload gates. In parallel as a separate implementation experiment,
move geometry strings/indexes and topology construction into independently
validated flat sections with a streaming loader; target startup without retaining
a second complete graph and measure both cold and warm residency on a controlled
host. The existing desktop can run each of these regional experiments serially.
Only after those gates pass should a larger contiguous Western candidate and
then conterminous-US resource census/build be attempted. Alaska and Hawaii need
separate coverage and disconnected-network evaluation afterward.

A later cost-model revision should explicitly interpret fixed/variable speed
qualifiers and goods-specific limits, with versioned source fixtures and new
plausibility checks. That should not be mixed into hierarchy correctness claims.
The present partial hierarchy, resident cold structures, full startup validation,
unbounded per-request label map, unverified cold cache, uncalibrated duration and
incomplete national coverage are all remaining product limitations.

## Rebuild verification and retained evidence

Independent builds used the identical pinned Go toolchain, bundle/PBF inputs and
new output names. The narrowed restriction-adjacency importer reproduced **every
compressed graph chunk byte** in retained Newport and Oregon, including source
records, prohibited paths and costs. Their only logical table difference was
`routing_graph`: exactly its preprocessing identifier and consequent SHA changed.
Every lookup/entity/source/provenance/relationship/FTS table and refresh identity
history matched. SQLite integrity and foreign-key checks passed for all six files.

The first Newport comparison used plain `cmd/import` and correctly found missing
refresh-only metadata (`identity_history`, `replacements`, `input_bundle_sha256`).
The original candidate had been built through `cmd/refresh build`. Repeating that
same workflow into `newport-refresh.sqlite` reproduced all metadata and lookup
identities. The plain-import candidate and initial failed comparison remain
retained; no metadata was patched into a file to manufacture agreement.

The independent Northwest rebuild matched **every logical table, graph manifest,
compressed chunk and whole SQLite file byte**. Its build took 205.77 s, peak RSS
6.61 GiB and physical footprint 7.99 GiB. Different timings from the first
285.50-second build reflect cache/GC/desktop variability; logical equivalence does
not imply a fixed resource cost. Newport refresh rebuilding took 8.96 s; Oregon
rebuilding took 49.21 s. Full resource outputs remain in their build logs.

Candidate SHA-256 fingerprints:

| Candidate under `data/junction-scale/` | Bytes | Whole-file SHA-256 |
| --- | ---: | --- |
| `newport-refresh.sqlite` | 68,358,144 | `556aeb0d066c75794e734de30b6ce2f167807ad454f3d66c3673ac561978597c` |
| `oregon-final.sqlite` | 301,228,032 | `f7d27213fc85041cb5dbfed277213d99a2ef715683df3f36be253a1c32418621` |
| `northwest-final.sqlite` and `northwest-rebuild.sqlite` | 723,644,416 | `711aa9744e01b333c1edc15a1137fffe41ea456a331951bb38ec7524986e0ad3` |

Northwest graph-manifest SHA-256 is
`26a00a6488b919cca99af4a00e4c25c57f41da36768e35de17950f84e2c49bbc`.
`reproducibility.json` retains row counts, ordered type/length-delimited table
hashes, all baseline/candidate fingerprints and exact comparison exclusions.
`verify-rebuilds.py` reproduces the comparison; no exclusion applies to Northwest.

Retained local evidence is grouped under ignored `data/junction-scale/`:

- `targets.json`, `input-checksums.txt`, `fileinfo.json`, `bundle.json`,
  `bundle.sha256`, source PBF, source lock and candidate databases.
- `build*.txt`, `newport-build.txt`, `newport-refresh-build.txt`, `oregon-build.txt`,
  `northwest-rebuild.txt`, failed-attempt profiles and their intermediate files.
- `newport-baseline.txt`, `oregon-baseline-isolated.txt`, `oregon-heap.txt`,
  `newport-mapped.txt`, `oregon-mapped.txt`, `northwest-performance.txt`,
  `oregon-access.txt`, and the startup-only outputs.
- `northwest-seeds*.json`, seed-selection outputs, `northwest-routes.jsonl`,
  `oregon-routes.jsonl`, regional verification outputs, `i90-sources.json`,
  `northwest-sensitivity.jsonl` and `detour-analysis.json`.
- `northwest-http.txt`, `oregon-http.txt`, `lifetime.txt`, final check outputs,
  `reproducibility.json`, and successful/failed comparison outputs.
- `measure-regions.sh`, `verify-regions.sh`, `investigate-http.sh`, `rebuild.sh`,
  `final-checks.sh`, `verify-rebuilds.py`, compiled baseline/current binaries and
  the verified numeric cache files. Maintained invocation examples are in
  [routing scale](../routing-scale.md#northwest-intermediate-evaluation).

Final startup-only runs, with no query workload and warm input/cache files, were:

| Mapped graph | Load s | Retained heap MiB | Peak RSS GiB | Peak physical footprint GiB |
| --- | ---: | ---: | ---: | ---: |
| Newport | 2.265 | 105.87 | 0.49 | 0.38 |
| Oregon | 23.738 | 803.27 | 4.12 | 3.21 |
| Northwest | 67.359 | 1,853.93 | 4.87 | 6.01 |

These isolate startup process peaks from the earlier query runs. Oregon's new
manifest produced a separately named numeric cache file; Northwest reused its
verified file. Startup remains a full graph validation/reconstruction operation.

Final verification completed:

- Changed Go files formatted with `gofmt`; `go test ./...` and `go vet ./...` passed.
- Race checks passed for routing, dataset, API and importer. The randomized,
  deterministic-seed via-way/access comparison now alternates heap/mapped stores
  and asserts that junction shortcuts actually execute; no reference differences.
  A further corruption test verifies rejection even when changed mapped payload
  bytes carry a recomputed, self-consistent checksum.
- Newport's 31 coordinate and 22 address/mixed cases passed against the independently
  rebuilt refresh candidate. The 26-case geocoding suite passed on baseline and
  candidate, including ambiguity, source evidence, bounds and public IDs.
- Isolated lookup-only→new routing→baseline and lookup-only→retained v3→baseline
  snapshot cycles passed, preserving Places IDs and eight ambiguous Bellevue
  addresses. Routine version tests cover retained JSON semantics 1–4; mapped
  corruption/version tests and chunked old/new preprocessing loading passed.
- Oregon and Northwest source-backed reference suites, 1/2/4-worker performance,
  actual HTTP cancellation/concurrency, mapped access and the multistate
  replacement/failure/rollback experiment passed their correctness checks.
  Performance gates that failed are reported above, not relabeled as successes.
- Documentation content, local links, fenced blocks and `git diff --check` reviewed.
  The final active `data/deployment.json` SHA-256 remains
  `d7070c4c86ff7221ea2691aae07fe37436b5d37fc878e12c7b2c5aa1c2e90aff`, identical
  to the initial inspection. No retained source/snapshot, browser, commit or remote
  deployment was changed.
