# National coordinate-routing capacity preflight

Date: 2026-09-09. Inspected revision:
`62069a6d8f4987f6253d53f35367988d29f9b7eb`. The working tree was clean before
this work; the [historical construction increment](0025-bounded-routing-construction.md)
was already committed. Scope: national routing source metadata and capacity
assessment, a proposed release/evaluation contract, and fresh retained-regional
verification. No national snapshot was built.

## Outcome and next decision

**National construction is blocked on the available 16 GiB Mac.** The current
pipeline does not safely fit either the US-only or proposed North America
scope within the declared 8 GiB process and 32 GiB free-disk-reserve policy.
This is a failed capacity admission estimate, not a measured national OOM or a
claim that national routing is impossible on smaller hardware with different
construction/storage code.

The [maintained national plan](../routing-national.md) proposes coverage of all
50 states and DC, distinguishes geographic inclusion from connectivity, excludes
ferries and new address data, and predeclares build/query/lifecycle budgets.
It identifies the whole construction path and actual remaining resident costs.
It is a proposal, not a replacement for the implemented regional contract.

The missing scope decision is whether to include Canadian/Mexican detours between
US endpoints. Including them is proposed so Alaska and border destinations are
not disconnected merely by country clipping. The independent host-capacity
assessment covers both choices; neither fits the current host. Before a national
run, supply an existing suitable build host. The concrete conservative plan is
512 GiB RAM, at least 16 CPU cores and 1 TiB free local NVMe, with a 320 GiB
construction budget. A 384 GiB host leaves the specified 64 GiB minimum headroom;
512 GiB is preferred. No infrastructure was provisioned and no charges incurred.

The alternative is a substantial external-memory importer **and** graph/index/
hierarchy/publication path plus additional disk, not another isolated regional
allocation reduction. That alternative is described as unimplemented. The
current source/PBF ratio is an imperfect predictor, so a larger host authorizes
only an experiment, not a promise that all national gates will pass.

## Evidence inspection

Read the maintained routing, scale, prepared and refresh documents and inspected
records 0024/0025. Inspected retained preparation, query, representation and
residency evidence under `data/recursive-scale/`, `data/prepared-scale/`,
`data/residency-scale/`, `data/dense-scale/` and `data/construction-scale/`.
The distinctions in earlier experiments remain material: recursive cells improved
regional search, prepared loading removed startup reconstruction, streaming
rollback removed a second verification mapping, and dense edges reduced artifact
bytes. None established a physical-memory bound or national construction.

Recomputed the principal retained construction/process maxima from the raw
`samples.jsonl` files and matched them against `measurements.json`; inspected
raw HTTP/label logs, heap statistics and the three logical rebuild reports.

| Retained measurement | Confirmed result |
| --- | ---: |
| Multistate preparation, baseline / final | 163.276 / 197.798 seconds |
| Sampled charged footprint, baseline / final | 6,457,989,024 / 5,606,069,240 bytes (6.01 / 5.22 GiB) |
| Final preparation sampled RSS | 4,942,282,752 bytes (4.60 GiB) |
| Instrumented final maximum last-GC live heap | 5,491,519,624 bytes (5.11 GiB) |
| Instrumented import, elapsed / charged footprint | 498.785 seconds / 7,219,058,712 bytes (6.72 GiB) |
| Normal multistate HTTP, one-worker p95 / four-worker rate | 424.145 ms / 39.47 requests/s |
| Seattle–Boise labels | 564,380 |
| Prepared v2 artifact | 4,132,749,063 bytes |

The original live-heap and repeat elapsed targets were missed; this preflight
does not reinterpret those as successes. All three recorded independent rebuilds
agree on logical tables, graph/source identities and prepared v2 bytes. Newport
SQLite physical file bytes differ across its recorded rebuild; logical records
agree. That distinction is retained.

Implementation inspection confirms that `internal/importer/routing.go` retains
ways, raw source JSON, restrictions, needed-node and coordinate maps while
building segments/guards. `routing.New` still constructs full edge/node/CSR
arrays and membership maps; spatial/cell construction adds global scratch.
`prepared.go` still allocates source-ordered IDs and a power-of-two node hash
table. The current constructor caps segments at `MaxInt32/2`; prepared arrays
and recursive paths also have checked integer bounds. The ancillary JSON cap
is 128 MiB, with allocation before the writer's size rejection. These limits
must be checked against actual national counts, not inferred from file hashes.

## Host and current source metadata

The host is Darwin ARM64, model `Mac17,3`, Go 1.26.1, ten physical/logical CPUs,
16 GiB RAM, 16 KiB VM pages, an internal Apple Fabric SSD and APFS. Initial
`df -k` reported 113,473,924 KiB available (108.22 GiB). Initial host swap used
5,818.69 MiB; the compressor occupied 236,886 pages (3.61 GiB). These are whole-host
observations, not this workload's private allocations or free allocatable RAM.
Read-only memory-pressure, VM, swap, storage and shell-limit controls were inspected.
No process RSS enforcement or controlled cold-cache facility is configured.

Official references checked on September 9:
[Geofabrik US catalog](https://download.geofabrik.de/north-america/us.html),
[North America catalog](https://download.geofabrik.de/north-america.html),
[extract technical details](https://download.geofabrik.de/technical.html),
[US extract polygon](https://download.geofabrik.de/north-america/us.poly) and
[North America extract polygon](https://download.geofabrik.de/north-america.poly).
Geofabrik's polygons describe extract buffers, not legal country boundaries.
Extraction preserves crossing ways/multipolygons; restriction-reference completeness
still requires explicit validation. The upstream data attribution is
[OpenStreetMap contributors](https://www.openstreetmap.org/copyright).

Only catalogs, polygons, upstream MD5 sidecars and **65,536 bytes of each PBF's
header range** were downloaded. Servers returned HTTP 206 and full-size
Content-Range totals. No full national PBF, places, addresses or supplemental lookup
data was acquired. `osmium fileinfo -j -F pbf` reads these retained header ranges;
it does not establish whole-file validity or source object counts.

| Candidate source | Observed body bytes | Upstream MD5 | Header bounds [west,south,east,north] |
| --- | ---: | --- | --- |
| `us-260908.osm.pbf` | 12,131,406,419 | `59fb766c1715b254145ae898d78fcaed` | [-180,15.92097,180,72.98845] |
| `north-america-260908.osm.pbf` | 19,349,498,972 | `36aa404b4d3428390e245a24dbef7678` | [-180,5.57228,180,85.04177] |

Both headers report replication timestamp **2026-09-08T20:21:01Z** and
`Type_then_ID` sorting. Observed upstream MD5 values are transfer metadata, not
full local SHA-256 pins or topology authority. `capacity-model.json` explicitly
records `full_sha256: null` and `full_body_acquired: false`. No import lock with
an invented source checksum was created. A later acquisition must verify the
complete file, compute its SHA-256, check header/reference consistency and retain
its provenance before establishing a usable lock. If this dated release expires,
choose and document another coherent release explicitly; never fall back silently.

## Capacity calculation and budgets

The retained merged Northwest source is 744,258,582 bytes and its SQLite file
723,644,416 bytes. Its graph has 13,700,712 nodes, 14,218,671 segments, 2,651,284
guards, 27,644,385 directed edges and 24,073,552 recursive path records. The v2
ancillary section is 2,498,503 bytes. National compressed-source multipliers are
16.300× for the US and 25.998× for North America. These are source-size ratios,
not measured graph growth factors.

`scripts/routing-capacity.py` computes each resource independently, applies 1.5×
contingency, counts first publication and two-generation lifecycle disk, and
rejects insufficient host/process budgets or projected format dimensions. It
reads current RAM/free disk, writes a new report exclusively, and never starts
acquisition or changes a publication. Zero/negative/nonfinite values, missing
measurements/evidence, duplicate named inputs, unsupported schema and overflow
fail. A report that fits an estimate is explicitly not a capacity qualification.

The current-host declaration is 8 GiB process construction, 32 GiB free-disk
reserve. With contingency, US / North America estimates are:

- Construction charged footprint: **164.38 / 262.19 GiB**.
- First publication workspace: **128.03 / 204.20 GiB** before reserve.
- Two database/artifact generations plus source and scratch:
  **238.61 / 380.59 GiB** before reserve.
- Prepared artifact: **94.11 / 150.10 GiB** each; old/new mapping size is double
  those bytes and is not a physical-residency estimate.
- Import: **3.39 / 5.40 hours**; preparation: **1.34 / 2.14 hours**.

The model includes current parsing in process memory and no on-disk staging,
because that importer has no staging representation. The sort payload allowance
uses the implementation's `16 × (segments + guards)` bound, not a zero inferred
from a missing sampler. Publication counts its temporary/final hard-linked file
once. A single coherent source needs no overlapping state extracts or merged
derivative. An external-memory redesign needs a new staging allowance and cannot
reuse this zero-staging estimate.

As a storage sensitivity check, retained buffered Oregon has 330,005,692 source
bytes, 301,228,032 SQLite bytes and 1,764,750,905 prepared bytes. Its prepared/source
ratio is 5.348 versus Northwest's 5.553 (about 4% lower). It does not validate
national scaling. The model's upper national node/segment/edge/path and ancillary
projections stay below current format ceilings; **actual national dimensions
remain unknown**. Neither a new encoding nor lifting the ancillary limit is
justified by these estimates alone.

The maintained plan predeclares the larger-host budgets before any national
experiment: 320 GiB construction, 640 GiB workspace, 8/4-hour import/preparation,
600-second startup/lifecycle, normal sub-second p95 and at least 12 requests/s at
four workers, separately reported difficult cases, allocation and cancellation
targets. It also records the proposed 64 GiB serving RSS experiment and separate
controlled physical-memory gate. The current request-triggered reload's two-minute
server write timeout, import CLI signal handling, endpoint rectangle and dateline
projection assumptions need explicit treatment before a national operational claim.

## Fresh regional verification

Heavy workloads were serialized with `GOMEMLIMIT=3GiB GOGC=50`; the offline
reference timeout override was **120 seconds**, with no production timeout or
cost-model changes. Built fresh integration binaries from the inspected revision.
The full commands are retained in `data/national-scale-20260909/verify.sh` and
`verify-final.sh`; they only use temporary deployment state and new evidence paths.

The 29-case Northwest source suite passes in 95.62 seconds; Oregon's 31-case suite
passes in 19.48 seconds. Newport's 31 coordinate and 22 address cases, mixed
requests, lookup-only and retained v3 cycles pass. Both sensitivity suites and
Oregon boundary coverage pass, including the retained Oregon-to-Oregon Idaho
detours. Source adjacency/direction/restriction/geometry checks and the ordinary
Dijkstra tolerance remain unchanged.

| Fresh full HTTP workload | One-worker p95, ms | Four-worker p95, ms | Four-worker requests/s |
| --- | ---: | ---: | ---: |
| Newport | 0.620 | 1.105 | 8,604.00 |
| Oregon | 26.831 | 178.573 | 166.22 |
| Northwest | 406.452 | 493.491 | 43.39 |

All regional normal targets remain satisfied. These are shared-desktop
observations, not controlled performance comparisons on a new national graph.
Seattle–Boise still records **564,380 labels**, 558,507 expansions, 4,366,369 cell
transfers and 9,196 geometry vertices. The separate detailed query takes about
1.123 seconds of search and cumulatively allocates 172,566,232 bytes; its timing
is not substituted for the normal HTTP distributions. Endpoint, search, geometry,
encoder and complete response byte observations remain in the raw logs.

Four full Portland–Ashland workers pass replacement, failed loading, rollback
and retirement with all-v2 publications and both v1/v2 arrangements. Publication
takes 21.277 / 22.210 / 16.163 seconds; every cycle ends with zero retired mapping
bytes. Sampled RSS peaks are 4.632 / 4.669 / 4.751 GiB and charged footprints
181.58 / 197.71 / 188.35 MiB. The multistate HTTP run peaks at 4.041 GiB sampled
RSS and 484.58 MiB charged footprint. These figures must not be interpreted as
unique clean file-cache residency or national physical-memory requirements.

`go test ./...`, `go vet ./...`, routing/dataset/API/importer race suites, fresh
real-HTTP admission/cancellation under race, and a v3/v4 lifecycle under race pass.
`gofmt -l cmd internal` is empty. The new capacity tool's six deterministic unit
tests pass, including two-generation disk accounting, exact admission boundaries,
independent memory/disk failures, order-independent worst ratios, invalid data,
duplicates and representation/arithmetic overflow. No Go production code or
routing data representation changed, so a new routing source rebuild was not
performed in this preflight.

## Preservation, limitations and retained evidence

Seventeen streaming SHA-256 checks reconfirm the retained source PBFs/bundle, three SQLite
snapshots and both retained/newer prepared v2 publications against their pins.
The inspected prior independent rebuild comparisons remain valid evidence;
this work does not claim a newly executed rebuild. The active deployment hash
is unchanged. No retained input/publication was overwritten, browser modified,
commit made or push performed. All generated binaries, metadata and evidence
are under the new ignored `data/national-scale-20260909/` directory. No diagnostic
Go overlay was needed.

Useful evidence entry points:

- `capacity-model.json`, `capacity-report.json`, `capacity-decision.txt`:
  observed regional inputs, explicit contingency, host admission and both scopes.
- `source-metadata.json`, `source-*.html`, `source-*.poly`, sidecars, partial PBF
  headers and `*-header.json`: request headers, observed byte counts, checksums
  of metadata and coherent replication headers; not whole national source pins.
- `retained-evidence-audit.json`, `preservation-checks-final.json`: raw retained sample
  agreement, historical logical rebuild reports and fresh immutable-file checks.
- `host-before.txt`, `host-after.txt`, per-run `before-host.txt`, `after-host.txt`,
  `run.json`, `samples.jsonl`, `exit.json`: platform, settings and process accounting.
  The standalone host inventory was recorded during the serialized verification
  window; initial free-disk/swap observations above came from the first preflight.
- `verified-*.txt`, `*-routes.jsonl`, `query-labels.txt`, `regional-verification-summary.json`,
  `http-*/workload.txt`, `lifecycle-*/workload.txt`: fresh regional source/HTTP/lifetime
  checks, complete encoding, cancellation, failures and retirement.
- `go-test.txt`, `go-vet.txt`, `go-race.txt`, `http-race.txt`, `lifecycle-race.txt`,
  `gofmt-check.txt`, `capacity-tests-reviewed.txt`, `capacity-exclusive-output.txt`:
  current check results and refusal to overwrite an existing capacity report.

The approximately 100 ms process sampler can miss peaks. Charged footprint,
Go heap, cumulative allocations, virtual mappings, RSS, compressed/private pages,
system file cache, faults and storage traffic remain different quantities.
No fresh forced-GC construction profile or `vmmap`/`footprint` residency experiment
was run; the retained construction/VM evidence remains identified as historical.
Host swap/cache activity includes other applications. No application was closed,
cache purged or host-wide control changed.

National geographic coverage/build success, national route correctness and
performance, and production physical-memory/operational qualification are all
**unachieved**. The national workload is a geographically diverse design awaiting
source-selected coordinates and independent evidence, not a passing benchmark.
There is no national artifact to reproduce or serve and therefore no fabricated
build/serve command. National address routing remains separate future work.
