# Routing residency and prepared rollback

Date: 2026-09-08. Scope: changes based on `ca332c2d11fed3680a88ad7e697835d798841823`,
using the retained Newport, buffered Oregon and complete Oregon–Washington–Idaho
SQLite candidates and `routing-prepared-le64-v1` publications. No source data,
public lookup IDs, browser, cost model or active deployment was changed. This
record follows the [historical prepared-loading milestone](0022-directly-loadable-routing-snapshots.md).

## Decision and trust boundary

Prepared rollback now verifies the selected source SHA-256 and streams the entire
artifact against its separately trusted publication receipt. It does not create a
query mapping, decode ancillary routing data or reconstruct topology. The receipt
reader is shared with runtime loading and checks source binding, prepared version,
source summary and graph/profile/cost metadata. A missing, foreign or corrupt
publication fails; there is no legacy fallback.

This is verification of an existing canonical publication, not an operation to
approve an arbitrary artifact. A self-checksummed artifact cannot replace the
trusted digest. Complete source/lookup/topology validation still belongs to the
trusted offline publisher. Full mapped structural checks still run before runtime
handler publication, including dimensions, references, CSR, probing, spatial and
recursive structures, restrictions and address evidence. Selection verification
no longer independently repeats those scans. Operator-owned receipt storage and
source/artifact immutability remain required; these are not signed publications.

Selection still uses the existing exclusive writer lock and atomic synced state
replacement. A canceled or failed verification leaves selection unchanged. Failed
runtime loading preserves the prior handler. Routing read leases and HTTP leases
through encoding are unchanged; validation neither borrows nor closes the serving
mapping. Lookup-only rollback retains full importer validation.

No prepared format changed. A graph representation migration would affect a much
larger correctness boundary and is deferred to a measured follow-up. The cost model,
incoming-edge identity, restriction history, destination phases, directional and
partial-edge costs, endpoint policy and source reconstruction are unchanged.

## Facilities, controls and predeclared budgets

The host is macOS 26.4.1 / Darwin 25.4, ARM64, with 16 GiB RAM and 16 KiB VM pages;
Go is 1.26.1. Initial swap usage was about 6.8 GiB. Available read-only facilities
were inspected before experiments: `vmmap`, `footprint`, `vm_stat`, `sysctl`,
`memory_pressure -Q`, `ps`, `getrusage` and `proc_pid_rusage` v2. The latter's layout
was checked against the installed SDK `sys/resource.h`.

`scripts/routing-residency.py` records nominal 100 ms process samples, command,
relevant environment and before/after host counters in a new `data/` directory.
`TestPreparedResidency` records Go heap, cumulative allocations, Go `Sys`,
minor/major faults and block-read counters at named stages, and takes VM snapshots.
The lifetime harness also records allocation/time stages. All heavy workloads were
serialized. Snapshot probes have overhead: separate lifecycle runs with periodic
`vmmap`/`footprint` are diagnostic and are not the latency comparison.

There is no supported kernel-enforced process RSS limit or controlled cold-cache
setup in this environment. No caches were purged, unrelated applications closed,
host settings changed, pressure generator started, infrastructure provisioned or
data acquired. Previously accessed files can still cause storage reads and faults
under ambient pressure. These are desktop observations, not controlled cold or
physical-limit results. `GOMEMLIMIT=3GiB GOGC=50` is the ordinary evaluation setting;
it controls Go runtime behavior, not mapped physical memory. Host compression and
swap counters include unrelated processes.

After inspecting retained evidence and the alias baseline, `data/residency-scale/budgets.json`
set the following goals before candidate evaluation:

- No additional query mapping during publication verification; 1 MiB stream buffer,
  at most 16 MiB additional heap and 15 seconds for multistate verification.
- Complete lifecycle sampled RSS ≤6.5 GiB and charged footprint ≤100 MiB.
  Clean mapped-file residency goal ≤6.1 GiB for the distinct Oregon/multistate
  union. This is a separate resource, not an RSS or total-system-memory bound.
- Multistate one-worker HTTP p95 <1 second, four-worker throughput ≥12 requests/s,
  and 564,380 Seattle–Boise labels. Newport/Oregon regression goal ≤25%.
- Startup goals: Newport ≤2 s, Oregon ≤12 s, multistate ≤25 s.

## What the alias baseline established

A diagnostic first loads the complete multistate publication, opens it again while
retaining the serving store, closes the alias, runs both retained route passes and
retires the original. It never modifies the file or purges its cache.

| Point in multistate diagnostic | `ps` RSS GiB | `footprint` clean mapped-file GiB |
| --- | ---: | ---: |
| Original mapping validated | 4.292 | 4.261 |
| Both mappings validated | 8.080 | 4.086 |
| Alias retired | 3.936 | 3.897 |

These snapshots are sequential and cannot be subtracted as simultaneous physical
measurements. Nevertheless, `vmmap` explicitly marks the mappings `SM=ALI`, and
`footprint` reports about one artifact's clean pages while RSS approaches two.
The original artifact is exactly 4,575,059,223 bytes (4.261 GiB); two aliases do
not double its distinct file offsets. In this run the clean mapped-file count
actually fell during alias validation while RSS increased by about 3.8 GiB.
Thus most of the reproduced rollback RSS spike is duplicate accounting, not an
additional 4.3 GiB of unique file residency. No exact retrospective physical-page
count can be recovered for the previous milestone's 8.55 GiB peak.

The loaded `ps` virtual size was about 427 GiB, while the artifact itself is
4.261 GiB. Go/runtime virtual reservations are not resident allocations. The
loaded diagnostic Go heap was about 14.2 MiB and Go `Sys` about 26.3 MiB. macOS
charged footprint includes private/dirty, compressed and page-table costs and
excludes much clean file memory. The two-mapping snapshot had roughly 35 MiB
charged footprint, including about 1.9 MiB swapped/compressed, alongside its
4.1 GiB clean mapped-file pages. None of these quantities is interchangeable.

Both baseline and streaming diagnostics read about 5.85 GiB according to process
storage counters over their complete load/verify/query runs. The unchanged full
integrity scans still require source and artifact reads. Removing aliases does
not remove the global file-cache working set. Major/minor faults and storage
counters are retained independently; `getrusage` block reads reported zero on
this host and must not be interpreted as proof of zero disk I/O.

## Lifecycle comparison

The ordinary lifecycle run uses four real loopback HTTP workers and temporary
selection, including Oregon → multistate replacement, failed selection, rollback
to the still-serving multistate publication, restoration of Oregon and retirement.
No active selection file is used.

| Resource | Baseline | Streaming verifier |
| --- | ---: | ---: |
| Sampled peak process RSS, GiB | 8.417 | 5.434 |
| Sampled peak charged footprint, MiB | 44.54 | 40.19 |
| Replacement publication, seconds | 11.179 | 11.385 |
| Complete test, seconds | 26.14 | 22.85 |
| Process storage reads, GiB | 10.750 | 11.967 |

RSS falls by about 35.4%; these runs meet the predeclared lifecycle goals. Storage
reads vary with ambient cache/pressure and do not show an improvement. Replacement
still overlaps two *different* artifacts: 6,523,951,904 bytes of mapped virtual
size. That necessary ownership overlap was preserved. Retired mappings report zero
bytes and reject subsequent queries; all returned snapshots remain consistent.

Periodic VM diagnostics separately sampled peak clean mapped-file residency of
5.227 GiB (baseline) and 5.041 GiB (candidate), versus sampled RSS of 8.356 and
5.106 GiB. These distinguish clean residency from alias-inflated RSS, but are not
exact lifetime maxima or total unique system physical memory. Probes increased
candidate replacement to 20.508 seconds, illustrating why those latency numbers
are kept separate. Old/new file residency, file-cache competition and query
scratch remain; eliminating an alias is not a general memory cap.

In the ordinary candidate, rollback to the still-serving publication took about
2.010 seconds and allocated 2.15 MiB cumulatively between lifecycle markers.
The serving mapping remained unchanged. Full baseline restoration, which actually
loads a different graph, took another 4.964 seconds. Runtime structural validation
remains part of that different-graph load.

A stronger subsequent lifecycle run selected the retained `Portland to Ashland
I-5 corridor` case for all four workers and returned full geometry during
replacement. Before either long-route lifecycle run, its additional goals were
recorded as RSS ≤6.5 GiB, charged footprint ≤1 GiB and publication ≤90 seconds,
based on the ordinary HTTP scratch observations. The 100 MiB goal above applies
to the original zero-distance lifecycle workload.

| Four long routes plus lifecycle | Baseline | Streaming verifier |
| --- | ---: | ---: |
| Sampled peak RSS, GiB | 8.040 | 5.320 |
| Sampled peak charged footprint, MiB | 186.69 | 198.01 |
| Replacement publication, seconds | 18.183 | 27.578 |
| Complete test, seconds | 36.55 | 39.20 |

Both runs passed response consistency, failure, rollback and retirement checks.
The candidate meets these additional resource/90-second goals but does not
improve charged footprint or publication latency in this stronger workload.
RSS still benefits from removing aliases. Search/encoding plus loading accumulated
about 9.57 GB of Go allocations in the candidate despite a much smaller retained
heap. Ambient memory/cache conditions were uncontrolled, so the slower publication
cannot be assigned uniquely to a particular cause. National replacement under
controlled pressure remains unverified.

## Prepared working-set investigation

| Multistate section | MiB | Share of artifact |
| --- | ---: | ---: |
| Directed edges | 1265.46 | 29.0% |
| Source segments | 650.88 | 14.9% |
| Source-node hash lookup | 512.00 | 11.7% |
| Recursive source paths | 275.50 | 6.3% |
| Segment directions | 216.96 | 5.0% |
| Adjacency | 210.91 | 4.8% |
| Coordinates | 209.06 | 4.8% |
| Source strings | 177.33 | 4.1% |

The complete section inventory for every retained publication is in
`section-inventory.json`. The edge representation is the largest section; source
lookup is a concrete cost but not the majority of the artifact. It is used by
point/adjacency access, endpoint checks, component and junction lookup, search and
source reconstruction. Edges/continuations/cell transfers serve search; coordinates,
segments and strings also serve snapping and exact geometry output. Ancillary
Newport address/restriction data has different heap behavior from routing-only
Oregon/multistate data.

A diagnostic Go overlay under ignored `data/residency-scale/` counts node-lookup
VM pages actually read by serialized retained queries **after full validation**.
It instruments only the two hash-slot lookup loops, does not alter production
source, and supplies no performance timings. The corrected v2 trace uses actual
slot addresses and OS page size so unaligned section boundaries are included;
the initial trace counted section-relative pages and is retained as superseded
diagnostic evidence. This measures logical page accesses, not faults, page cache
coldness or simultaneous physical residency. It does not trace every graph array.

Seattle–Boise reads all **32,769** VM pages intersecting the 512 MiB lookup;
the full multistate workload has the same union. The short Portland downtown
trip reads 1,763 of those pages (about 27.5 MiB of intersected page addresses).
The Oregon workload reads all 16,385 lookup pages; Newport reads 1,823 of 2,049.
The extra page reflects section alignment, not an extra hash slot.
Long routes therefore read essentially the entire 512 MiB lookup. A compact immutable source
lookup or dense graph identity could reduce this cost, but would require a new
prepared version, migration/readability checks and measured query tradeoffs.
No alternative layout was selected or claimed faster in this increment. The
remaining representation and per-query scratch costs are quantified rather than
hidden behind the low retained Go heap.


## Ordinary startup and query observations

Current directly prepared startup took 0.254 / 1.572 / 6.163 seconds for Newport /
Oregon / multistate. Retained Go heaps were 11.20 / 5.18 / 9.38 MiB. These meet
the startup goals but are cache-state observations; the runtime load path is
essentially unchanged. A separately timed multistate streaming verifier took
2.054 seconds, allocated 2,120,488 bytes cumulatively, and kept its original
4,575,059,223-byte mapping. Its heap fell across the observation because of GC.
The stream-buffer and validation-allocation goals pass.

First HTTP requests after full integrity validation took 1.274 / 2.439 / 6.805 ms
for Newport / Oregon / multistate, including loopback and encoding. They are the
first short fixture in each retained workload, not cold-storage or longest-trip
measurements. First/repeated query stage faults and VM snapshots remain in the
residency diagnostic logs.

The first ordinary HTTP evaluation retained the original complete workloads,
encoding, bounded admission and cancellation checks. Northwest one-worker p95 was
487.902 ms and four-worker throughput 34.31 requests/s. Seattle–Boise kept exactly
564,380 labels, 558,507 expansions and 4,366,369 cell transfers; detailed search
took 790.708 ms and allocated 172,566,232 bytes. The unchanged cost model and
source geometry remain the product result.

Newport's first one-worker p95 was 0.847 ms versus the historical 0.599 ms, outside
the 25% comparison band. That historical run used different Go settings. A
subsequent serialized baseline/candidate comparison used the same toolchain,
`GOMEMLIMIT=3GiB GOGC=50`, retained files and HTTP fixtures. The baseline binary
was compiled from the exact `ca332c2` production sources using a diagnostic overlay.

| Region / workers | Baseline requests/s | Candidate requests/s | Baseline p95 ms | Candidate p95 ms |
| --- | ---: | ---: | ---: | ---: |
| Newport / 1 | 2964.56 | 2744.61 | 0.613 | 0.687 |
| Newport / 4 | 8665.27 | 8473.37 | 1.019 | 1.175 |
| Oregon / 1 | 42.53 | 42.68 | 31.274 | 33.197 |
| Oregon / 4 | 137.78 | 132.65 | 226.956 | 219.721 |
| Multistate / 1 | 10.89 | 11.13 | 496.197 | 456.401 |
| Multistate / 4 | 33.27 | 24.17 | 677.979 | 823.150 |

Newport and Oregon remain within the matched 25% band. Multistate meets its
absolute gates in both candidate runs, but four-worker throughput varies from
24.17 to 34.31 requests/s and the matched maximum latency reaches 1.458 seconds.
This is not evidence of steady latency under controlled host pressure. The
ordinary first-pass HTTP process peaked at 4.733 GiB RSS and 472.33 MiB charged
footprint, far above its retained routing heap because requests allocate labels,
queues and output. That memory remains a separate cost from validation.

## Separate Go soft-limit experiment

With `GOMEMLIMIT=128MiB GOGC=50`, the multistate HTTP suite passed, including
cancellation and bounded admission. One-worker p95 was 562.488 ms; four-worker
throughput was 15.90 requests/s, p95 1,305.407 ms and maximum 2,451.196 ms. There
were no HTTP timeouts. Sampled RSS still reached **4.610 GiB** and charged footprint
**347.55 MiB**, both far above 128 MiB. Process counters recorded 48,487 pageins
and 1.411 GiB storage reads. This exercises GC pressure from a smaller Go soft
limit; it is neither controlled physical pressure nor a hard memory cap. Its
latency tails are reported separately from the ordinary workload. Further physical
pressure/cold-cache evaluation requires an environment with genuine controls.

## Verification and remaining gates

The retained source suites use ordinary Dijkstra and the unchanged tolerance
`max(1e-6 seconds, abs(reference seconds) × 1e-10)`. Their explicit offline timeout
is **120 seconds**, with `GOMEMLIMIT=3GiB GOGC=50`; production timeouts are unchanged.
The complete 29-case Northwest suite passed in 108.14 seconds, including both
Oregon-to-Oregon Idaho optima, source adjacency/directions/restrictions, geometry
and cost sums. Seattle–Boise retained exact source-path equality; its reference
took 14.065 seconds in this run. This is not a controlled pressure-performance
comparison with the earlier 41.367-second reference.

The 31-case Oregon suite, Newport's 31 coordinate and 22 address cases (including
mixed routing), both sensitivity suites, Oregon boundary coverage, lookup-only
cycles and legacy-v3 → prepared-v4 → v3 cycles also pass. The source checks retain
all original assertions and do not use the diagnostic route trace as a substitute
for correctness.

Deterministic tests now cover publication validation with a serving mapping,
missing/foreign/corrupt publications, receipt/graph/cost versions, absent summaries,
partial schemas, cancellation before and between verification stages, unchanged
mapping ownership and lookup rollback selection atomicity. Existing prepared
reader tests still reject malformed dimensions/CSR/references, corruption and
self-checksummed changes, unsupported versions and cyclic structures, and exercise
partial endpoints, output ownership, concurrent close and canonical reproducibility.

`gofmt`, `go test ./...`, `go vet ./...`, routing/dataset/API/importer race tests and
the prepared Newport real-HTTP race workload pass. The first full Go check found
that diagnostic `.go` copies under `data/` formed an unintended package; these
copies were renamed `.go.txt` and the full check passed. Both logs are retained.
The prepared legacy-v3 → v4 lifecycle race run passed in 8.32 seconds.
That added check also exposed two
assumptions in the old regional harness: always requesting v4 duration and treating
every Newport endpoint as outside the candidate region. The harness now requests
duration only when the starting snapshot supports it and derives expected candidate
coverage from its prepared receipt. Both failed diagnostic runs are retained; they
were test expectations, not relaxed API or routing assertions.
An isolated CLI process rejects routing startup without preparation, reports
routing and duration available when prepared, and serves a mixed address/coordinate
request. It was stopped after verification.

Preparation encoding was unaffected: the deterministic independent-preparation
test passes, and complete hashes of all three retained independent prepared
artifacts were checked again against their canonical publications and receipts.
No regional importer or offline preparation was rerun; their existing construction
limits remain separate work. Retained legacy loading was exercised by the cycles.

Evidence is retained under `data/residency-scale/`: `evaluate.sh`, `verify.sh`,
`extras.sh`, `long-lifecycle.sh`, `final-checks.sh`, command/environment records, process samples, stage VM snapshots,
full HTTP and source logs, route reports, section inventory, diagnostic overlays,
and `measurements.json` with its summarizer. Existing inputs and publications were
preserved. Offline bounded-memory import/preprocessing and national address
loading are still independent readiness gates. This change reduces redundant
runtime validation memory; it cannot bound the offline graph construction peak
or remove the existing 128 MiB ancillary representation limit. No national data
or supplemental address data was loaded.
