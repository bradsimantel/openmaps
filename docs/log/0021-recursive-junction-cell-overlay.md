# Recursive junction-cell routing overlay

Date: 2026-09-08. Scope: the retained pinned Oregon–Washington–Idaho graph,
with Newport and buffered Oregon regression workloads. Baseline revision:
`1032581a4db5bb5e0a4bb1c276ddaf15c3b31c68`.

This record advances the [historical single-junction experiment](0020-junction-hierarchy-and-mapped-query-data.md).
The API remains the product. No browser, active deployment, retained input,
lookup ID, address policy, cost model, commit or remote was changed. All candidates
and local evidence use new names under ignored `data/recursive-scale/`.

## Controlled baseline and scope

The compiled baseline uses the original implementation and the retained
`data/junction-scale/{newport-refresh,oregon-final,northwest-final}.sqlite` files.
Fixtures are the unchanged maintained Newport, Oregon and Northwest JSON suites.
`baseline.sh` serializes all regional routing and real loopback HTTP processes;
workers within each workload deliberately run concurrently. Each worker executes
the full fixed cyclic workload twice. Errors remain in the latency samples.
`/usr/bin/time -l` captures process peaks; routing diagnostics separate snap,
search and geometry, expansions, labels, pushes, queue peak/capacity and total
request allocations. HTTP retains full masked responses and actual error envelopes.

These are warm-file-cache desktop observations, not independent statistical
trials or production SLOs. Baseline reruns already differ from the historical
1.51-second HTTP p95: current Northwest p95 is 1.740 seconds and four-worker
throughput is 8.29 requests/second. Seattle–Boise still has exactly 1,639,595 labels
and 1,631,335 expansions, confirming the same graph and algorithmic workload.
Its search takes 2.655 seconds, snapping 0.193 ms and geometry 1.570 ms; total
request allocation is 339,868,024 bytes. Targets stay fixed at ≤820,000 labels,
≤1-second one-worker HTTP p95 and ≥12 requests/second at four workers.

## Algorithm decision and correctness argument

Primary research consulted:

- [Geisberger et al., Contraction Hierarchies](https://ae.iti.kit.edu/download/contract.pdf)
  motivates conservative partial contraction and recursive source-path unpacking.
- [Dibbelt et al., Customizable Contraction Hierarchies](https://arxiv.org/pdf/1402.0402)
  separates ordering from metric work; full CCH customization is beyond this fixed-model experiment.
- [Geisberger and Vetter, turn-aware routing](https://publikationen.bibliothek.kit.edu/1000097647)
  reinforces approach-sensitive state.
- [Delling et al., Customizable Route Planning in Road Networks](https://www.microsoft.com/en-us/research/wp-content/uploads/2013/01/crp_web_130724.pdf),
  sections 3–5, motivates cell boundary transfers, source/target cell opening and
  turn-aware entry/exit representations.

The chosen increment is a bounded junction-cell overlay above the existing forced
chains, retaining the independent-junction fallback near endpoints. It is neither
full CH nor a complete national multilevel partition. Cells group at most 32
ordinary junctions using deterministic source-ordered bounded unions. Eligible
nodes have two to four departures and at least one non-forced incoming approach;
ordinary degree-two geometry vertices do not become cells. One-way forks can qualify.
Every prohibited-path node and every node incident to a destination-zone segment
stays outside cells. No witness search, node-only or otherwise, removes transfers.

For each cell entrance, local Dijkstra uses **incoming directed-edge identity**
as its label. It forbids immediate reversal and computes the cheapest legal
transfer to each distinct final incoming edge outside the cell. Cycles and parallel
approaches are handled as edge states. Distinct exits are never merged into a node.
Only cell entrances receive transfer tables; interior origin states use ordinary
search until they leave. Recursive predecessor records share identical prefixes
within a cell, and leaves expand the original forced-chain segments.

The restriction/access simplification is deliberately narrow. Every transfer
edge has an unprotected source node and is absent from the entire prohibited-path
alphabet. Consuming its first edge therefore resets **any** incoming trie history
to zero, without accepting a prohibited continuation. All its edges are public;
phases 0/1 become phase 1, while phase 2 cannot take the transfer. Restricted
junctions continue through the original full trie/phase transitions. This is a
proved state reset on these particular arcs, not a general node-based turn model.

Cell bounds include every interior and exit-chain vertex. If either destination
segment endpoint lies in those bounds, the query opens the cell and uses the
existing detailed search there. Conservative overlap only increases work. Forced
walks still stop at destination-segment nodes. Thus interior partial destinations
cannot disappear, and partial origins retain their original heading and fractional
cost. A terminal approach with only the prohibited immediate reversal cannot be
an intermediate route state; its overlay exit is omitted, with target cells opened
and destination-segment nodes retained. Fresh origin headings remain supported.

Every legal complete route can be divided into detailed endpoint/boundary pieces
and cell transfers with the same boundary incoming-edge/phase/history state.
Replacing a cell piece by its cheapest legal transfer cannot raise the optimum;
each stored transfer also unpacks to a legal source path, so it cannot introduce a
cheaper nonexistent route. Ordinary Dijkstra remains the full-graph reference.
Search sums precomputed directional costs in groups; output reconstructs every
source edge and sums source geometry/cost in traversal order. Floating-point
verification retains `max(1e-6 seconds, abs(reference seconds)*1e-10)`, independently
of route plausibility or empirical duration accuracy. Equal optima may use different
source paths; ordering and repeated queries must remain deterministic.

## Artifacts and loading

New manifests use preprocessing `junction-cells-v1`; source graph semantics 4,
`driving-time-v4`, `estimated-driving-v1` and `routing-chunks-v1` are unchanged.
Legacy `forced-chain-v1`, `independent-junction-v1` and JSON graphs 1–4 remain
readable and regenerate the current runtime overlay from validated topology.

The optional numeric cache advances to `routing-hot-le64-v2`. New sections hold
cell bounds, entrances, transfers and recursive paths. A 32-bit entrance index
occupies former padding in each 48-byte directed-edge record; no per-geometry-node
lookup array is added. All fields have explicit little-endian encoding and checked
ABI offsets. Canonical reconstruction still validates every array against SQLite;
self-consistent foreign payloads cannot authorize changed paths or costs. Old cache
files remain untouched; current code builds separately named v2 cache files.
Unknown versions, damaged sections and malformed manifests fail rather than repair
in place. Recursive indices have checked signed-32-bit capacity. Cell construction
observes cancellation as well as the existing mapping validation/search checks.

Full startup reconstruction and resident geometry, strings, source-ID maps, spatial
indexes, restriction state and address evidence remain. Mapping does not bound
physical residency. This experiment does not solve bounded national loading or
establish cold-file-cache behavior.

## Experiments and verification

All heavy build, query, HTTP and replacement workloads are serialized.
Worker concurrency inside each measured workload is deliberate.

### Intermediate choices

The first 16-junction version retained transfers for every incoming approach,
including approaches already inside a cell. On Oregon it stored 18,420,991
transfers and 38,586,206 recursive path records. It improved Portland–Ashland
labels to 334,844, but loaded in 56.093 seconds. Its multistate trial was stopped
after starting preprocessing; it is not a completed measurement. No retained
artifact was overwritten or removed.

Keeping only cell entrances reduced Oregon to 6,629,710 transfers and 14,308,244
path records. This 16-junction version gave Northwest 988,834 Seattle–Boise
labels and 1.342-second search. Increasing the bound to 32, including eligible
one-way forks, using a typed local queue and interning recursive prefixes gave
915,336 labels. That still missed the label target and stored 22,502,514
multistate transfers. These intermediate results motivated terminal-approach
pruning under the already-established no-immediate-reversal contract.

The selected version retains 10,688,283 multistate transfers and 24,073,552 path
records across 110,892 cells and 1,224,250 entrances. Its first complete run gives
**564,380 Seattle–Boise labels**, **65.58% fewer** than baseline. Expansions are
558,507, pushes 788,032, forced-chain visits 2,141,751 and evaluated cell transfers
4,366,369. Queue peak/capacity are 11,824/14,540. Request allocation is
172,413,272 bytes, about half the baseline; hash-map growth thresholds mean
allocation does not scale smoothly with label count. Label key/value payloads
remain 72 bytes and queue entries 40 bytes; allocations are total allocated bytes,
not a measurement of peak live label memory.

This is a useful bounded choice, not a claim of optimal partition size. In
particular, 16 versus 32 cells was not isolated again after terminal pruning.
Better partitions, reduced transfer scans and smaller construction scratch space
remain possible improvements. The final source-path/reference suites on this
implementation pass for all retained multistate/Oregon cases with identical
selected endpoints and source paths.

### Cost model and additional retained-source findings

No import rule, source tag, directional cost, speed note, vehicle/access model or
`estimated-driving-v1` interpretation changed. Algorithmic optimality, geographic
plausibility and observed travel duration remain separate questions. Seattle's
modeled detours remain; there are no empirical ETA measurements in this experiment.

A new census of the retained targeted I-90 source scan, saved as
`source-model-census.json`, identifies **101 included ways with
`maxspeed:variable=yes` and two with `maxspeed:variable=no`**. These are counts
within that scan, not a statewide census or measurements of their contribution
to a particular route. The retained closed way **969544659** carries both
`maxweight:hazmat=10000 lbs` and `maxweight:hgv=26000 lbs`. The same scan includes
583 ways with HGV-specific speed limits (524 at 60 mph, 59 at 65 mph).
Future versioned source-model fixtures should distinguish goods-specific
qualifiers from general passenger-car limits and fixed speed ceilings from
variable-limit metadata, without treating every qualified key alike. This adds
source examples/counts to the historical findings; it does not repair the model
or establish that the hazmat closure caused a specific detour.

### Resource scope and remaining gates

The final numeric mapping grows from 2,342,433,112 to **2,825,464,808 bytes** on
Northwest: **483,031,696 additional bytes (20.62%)**. Oregon grows from 975,292,145
to 1,181,254,388 bytes (21.12%); Newport from 105,321,068 to 132,576,132 bytes
(25.88%). Retained mapped Go heap remains approximately 1,854.2, 803.3 and
105.9 MiB respectively because the new arrays are mapped too.

The query gain trades additional immutable numeric storage and construction work
for fewer transient labels and edge visits. It does not remove deployment memory
costs. In the first complete Northwest query run, whole-process peak physical
footprint was 6,475,306,984 bytes versus baseline 6,454,564,576 bytes; Oregon's
whole-run peak rose from 3,303,689,168 to 4,782,182,384 bytes. Those runs include
startup and queries, so they cannot by themselves isolate overlap or startup RSS.
Dedicated startup and replacement measurements below provide that scope.

Warm startup in that first run rose from 72.190 to 94.138 seconds on Northwest.
The first post-GC Portland–Ashland detail sample on the larger graph took 1.550
seconds, versus 1.325 seconds in baseline, despite much better subsequent warmed
workload latency and the standalone Oregon improvement. This outlier is retained;
it is not evidence of verified cold-cache performance. Startup reads/checks the
whole mapping, and OS residency/compression may still affect first requests.

The first full multistate rebuild took **416.61 seconds**, peak RSS
6,389,252,096 bytes and peak physical footprint 8,798,576,368 bytes under
`GOMEMLIMIT=7GiB`. A one-second sample near its end found cell-table construction,
local queue/maps and recursive-path interning active. It was not a bounded streaming
loader. These resource costs are material even though the build completed on the
existing 16-GiB host; improved query performance alone does not justify national
acquisition or a national build.

## Final candidate measurements

The final binaries and newly built candidates repeat the same fixed workloads.
All three requested search/HTTP gates pass: Seattle–Boise has **564,380 labels**,
one-worker multistate HTTP p95 is **424.199 ms**, and four-worker HTTP throughput
is **34.07 requests/second**. This is a regional result under the unchanged model.
The host is an Apple M5 with 16 GiB RAM and 10 logical CPUs, running Go 1.26.1
on darwin/arm64. Full environment and `/usr/bin/time -l` outputs are retained.

| Graph | Workers | Baseline HTTP rps | Final HTTP rps | Baseline p95 ms | Final p95 ms | Final p50 / max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| newport | 1 | 2546.24 | 3338.73 | 0.828 | 0.528 | 0.245 / 1.495 |
| newport | 2 | 4784.42 | 6540.69 | 1.023 | 0.634 | 0.240 / 1.546 |
| newport | 4 | 8922.15 | 12873.98 | 1.085 | 0.668 | 0.236 / 2.113 |
| oregon | 1 | 16.34 | 48.78 | 100.565 | 26.957 | 3.695 / 268.744 |
| oregon | 2 | 30.91 | 96.34 | 466.036 | 158.336 | 3.617 / 270.709 |
| oregon | 4 | 56.44 | 150.21 | 529.375 | 199.272 | 4.312 / 353.425 |
| northwest | 1 | 3.14 | 12.04 | 1740.089 | 424.199 | 13.745 / 706.855 |
| northwest | 2 | 6.10 | 22.79 | 1909.269 | 451.332 | 11.864 / 715.902 |
| northwest | 4 | 8.29 | 34.07 | 2505.282 | 638.500 | 18.334 / 996.799 |

| Graph / version | Startup s | Retained heap MiB | Peak RSS GiB | Peak footprint GiB |
| --- | ---: | ---: | ---: | ---: |
| newport / baseline | 2.399 | 105.88 | 0.49 | 0.38 |
| newport / final | 3.047 | 105.87 | 0.66 | 0.53 |
| oregon / baseline | 26.330 | 803.26 | 4.00 | 3.09 |
| oregon / final | 30.671 | 803.26 | 4.55 | 4.45 |
| northwest / baseline | 66.963 | 1854.90 | 5.02 | 6.01 |
| northwest / final | 87.512 | 1853.82 | 5.19 | 6.08 |

| Graph / build | Wall s | Peak RSS GiB | Peak footprint GiB |
| --- | ---: | ---: | ---: |
| newport / candidate | 10.54 | 0.80 | 0.78 |
| newport / rebuild | 11.70 | 0.82 | 0.67 |
| oregon / candidate | 56.74 | 5.11 | 4.84 |
| oregon / rebuild | 59.13 | 4.96 | 5.33 |
| northwest / candidate | 416.61 | 5.95 | 8.19 |
| northwest / rebuild | 580.08 | 5.75 | 8.03 |

Startup rows are fresh-process load-only runs with warm authoritative/cache files;
they exclude request workloads. Northwest's added startup time is **20.549 seconds
(30.69%)**. Oregon's startup peak physical footprint increases by about **1.36 GiB**
and Newport's by **0.15 GiB**; these are material construction costs, despite the
unchanged retained mapped Go heap. Northwest's measured startup footprint rises
by about **0.065 GiB**, with `GOMEMLIMIT=6GiB` limiting Go allocation growth rather
than imposing a hard RSS bound. The increment stays within the existing host's
measured resources, with faster queries and lower query allocation providing the
quantified reason to accept extra immutable storage and preprocessing. It does
not lower deployment resource requirements generally.

The independent multistate rebuild takes **580.08 seconds**, with a large system-time
component. A retained sample near its end shows existing spatial-index sorting
inside full graph reconstruction, whereas the first build's late sample shows
cell construction. Neither sample establishes a complete causal performance
profile. These variations reinforce the remaining construction/residency gate;
a successful rebuild is not a bounded-load guarantee.

In the final detail samples, Seattle–Boise search is **819.862 ms**, snapping
0.232 ms and geometry 1.655 ms, with the same deterministic counts as the earlier
terminal run. Portland–Ashland on Oregon uses **229,498 labels** (baseline
643,753), 227,752 expansions, 316,422 pushes and 862,149 chain visits. Its search
is **283.092 ms** (baseline 894.637 ms), with 65,955,032 allocated bytes (baseline
170,596,024). Queue peak/capacity is 11,794/14,540. The slightly larger queue does
not offset the label reduction; the label maps and millions of transfer visits
remain the main long-route search costs.

The HTTP harness times the **original JSON encoder including its writes**, through
an internal test observer. That observer adds no public fields or protocol changes.
Separate re-encoding samples remain in the raw evidence for comparison with the
historical harness, but are not substituted for original encoding time. One-worker
Northwest phase p50/p95/max in milliseconds:

| Phase | p50 | p95 | Max |
| --- | ---: | ---: | ---: |
| Endpoint snapping | 0.147 | 0.528 | 1.163 |
| Search | 11.546 | 421.885 | 704.255 |
| Geometry | 0.330 | 1.310 | 1.644 |
| Original encoding/writes | 0.154 | 0.568 | 0.673 |

Phase percentiles are computed independently and do not add to HTTP percentiles.
Transport, request parsing, translation and other handler work remain in the
measured residual. The detailed Seattle–Boise HTTP sample has 958.283 ms search,
2.677 ms snapping, 2.694 ms geometry, 0.822 ms encoding and 0.416 ms residual.
This separate sample is not a claimed p95 for that trip.

Both long HTTP cancellation probes pass: Portland–Ashland releases in 2.673 ms
and Seattle–Boise in 0.425 ms after cancellation in the final multistate run.
Subsequent requests succeed. Four completed-route responses deliberately held
inside encoding occupy all four permits; a fifth real HTTP request receives 429,
`RESOURCE_EXHAUSTED` and `Retry-After: 1`. A non-routing probe remains available,
and releasing the writers restores routing admission. Real deployment health,
failed loading and rollback are tested separately below.

## Reproducibility and preservation

Independent candidates preserve every logical lookup, source, provenance,
relationship, identity-history and graph-chunk row. Comparing each candidate to
its retained `data/junction-scale/` predecessor changes **only `routing_graph`**:
its preprocessing identifier and consequent manifest checksum. Graph/profile,
cost model, every compressed graph chunk and all public lookup IDs remain equal.
Independent candidate/rebuild comparisons have no table exclusions and reproduce
all logical rows and graph manifests. SQLite integrity and foreign-key checks
pass on all baseline, candidate and rebuild files.

Oregon and Northwest also reproduce the entire SQLite file byte-for-byte.
Newport's logical rows and graph bytes match but its file layout differs; whole
SQLite file identity was not a requirement. `reproducibility.json` records ordered,
type/length-delimited table hashes, row counts and all fingerprints. The retained
`verify-rebuilds.py` reproduces those comparisons.

| Candidate under `data/recursive-scale/` | Bytes | Whole-file SHA-256 |
| --- | ---: | --- |
| `newport-candidate.sqlite` | 68,354,048 | `9294c127707ddea4531739a984e0f4841ae02e94af40760b1f73c26f6b729c91` |
| `oregon-candidate.sqlite` | 301,228,032 | `75fdf957da9d77c8f388e44f3b974d8fa50506d3b4c9236fb1802e22727322a1` |
| `northwest-candidate.sqlite` | 723,644,416 | `ae60908ead29671766ddb15a65e69187dfd9c0f0175d2af8d56f49228afa321b` |
| `newport-rebuild.sqlite` | 68,358,144 | `c78922b4e3635a56651e295650f60b59d5ab637b2116ea27092a3fce848f6d7a` |

Northwest graph-manifest SHA-256 is
`b05de775c10178f5b5fe408b2a71fc716d986f7ad0206e8ef7cd7971f451c2ac`.
All source PBF checksums were rechecked against retained pins; no source was
acquired. Original candidates, research evidence and the active deployment remain
untouched. The final independent numeric-cache comparison and integration results
are recorded below.

## Final correctness and integration

The fresh candidates pass all **29 multistate**, **31 Oregon** and **31 Newport**
coordinate cases, including the retained source-path checks. Both Oregon-to-Oregon
routes through Idaho remain covered. Selected snaps and source paths match the
reference on the retained multistate/Oregon suite; optimal costs meet the existing
tolerance. No endpoint is reselected based on reachability or cost.

The **22 Newport address/mixed cases** pass, as do lookup-only → routing →
lookup-only and legacy v3 → current → v3 snapshot cycles. The retained Oregon and
Northwest time-versus-distance sensitivity suites and buffered/unbuffered Oregon
boundary comparisons pass. These comparisons retain their original input scope:
different geographic inputs can yield different optima, independently of the
hierarchy. Fresh source-seed and I-90 investigations use the retained PBF only.

Deterministic small fixtures exercise recursive paths at depth three or greater,
via-way histories, every incoming automaton state at transfer edges, destination
boundaries, directional speeds, partial endpoints, stable ties, terminal teeth,
legacy distance models and cancellation after actual cell use. The existing
randomized mapped/heap restriction/access comparisons remain enabled. Validation
checks every unpacked transfer's adjacency, reversal policy, public access, source
cost and endpoint bounds. Canonical cache tests additionally reject altered cell
paths even when the foreign payload carries a recomputed checksum; existing
version, padding, truncation and checksum rejection remain covered.

The multistate mapped-access probe loads in **82.122 seconds**, then completes the
fixed route passes in **2.312** and **2.195 seconds** after the advisory
`MADV_DONTNEED` call. They add 57,301 and 38,987 minor faults and **zero major
faults**. Maximum process RSS is 5.69 GiB and physical footprint 6.01 GiB. The
advice does not evict the filesystem cache or prove cold storage access; startup
has already read and validated every numeric byte. These observations cannot
close the cold-cache readiness gate.

Isolated Oregon → Northwest publication completes in **89.541 seconds** under
four concurrent real HTTP callers and the shared four-permit admission limit.
It narrowly meets the historical 90-second publication gate in this observation;
the harness used a 180-second deadline to permit the remaining failure/rollback
checks even if that gate was missed. Sampled peak Go heap during replacement is
**7,818.77 MiB**, and retained two-graph heap after GC is **2,656.31 MiB**. Combined
old/new numeric mappings are **4,006,719,196 bytes** of virtual space. The retired
mapping reaches zero bytes after leases finish and rejects new queries.

Selecting a temporary invalid SQLite file returns degraded 503 health while the
previous snapshot continues routing. Rollback restores healthy routing, then
restores the Oregon baseline and its address domain. The entire test passes in
315.31 seconds; process peak RSS is **6.25 GiB** and physical footprint **8.39 GiB**
with `GOMEMLIMIT=8GiB`. This is an isolated test, not an active deployment switch
or a guarantee about replacement under production load. A late retained stack
sample shows full graph reconstruction during the rollback sequence, consistent
with the separate loading limitation.

The Newport real-HTTP workload and held-encoding admission probe also pass under
the race detector. Its instrumented timings are not performance comparisons.
Geocoding passes 26 cases and 25 raw-source identity/coordinate checks on both
baseline and candidate; **all 8,545 address entities are identical**, with none
changed, absent or added. The active deployment's before/after SHA-256 remains
`d7070c4c86ff7221ea2691aae07fe37436b5d37fc878e12c7b2c5aa1c2e90aff`.

Independent loads of all three rebuilt SQLite files into a new cache directory
reproduce the **entire canonical numeric files byte-for-byte**, including all
recursive paths. `flat-reproducibility.json` retains headers, section dimensions,
payload checksums and full-file checksums:

| Graph | Numeric bytes | Whole-file SHA-256 |
| --- | ---: | --- |
| Newport | 132,576,132 | `1696c1283d199df749ce5647e2333d9c8ca3ef077a6320ecd3ef63a632666d76` |
| Oregon | 1,181,254,388 | `ddbde078661d25b2b978d644a6aafc023bc1f36d58f01c18ef2332a014578dcc` |
| Northwest | 2,825,464,808 | `ce053e4f70fed2a1d8fbbc672c9e439018b17ec6c7280580e33bb333aa29afdd` |

Fresh source seeds and the complete I-90 source report are also structurally
identical to their retained predecessors.

Final checks pass: `gofmt` on all changed Go files, `go test ./...`, `go vet ./...`,
and `go test -race ./internal/routing ./internal/dataset ./internal/api
./internal/importer`. The opt-in source/reference, sensitivity, mapped-access,
snapshot-lifetime, geocoding and real-HTTP suites above also pass, including the
separate race-instrumented Newport HTTP run. `verified-go-{test,vet,race}.txt`
retains the final command output. Documentation links/fences and `git diff --check`
pass. No national-data, cold-file-cache or empirical ETA check was run.

## Evidence and remaining work

All local evidence is under ignored `data/recursive-scale/`. The final measurement
set is `*-final-performance.txt`, `*-final-http.txt`, `*-final-startup.txt` and
`*-baseline-startup.txt`. `final-measurements.json` and `final-tables.md` summarize
the recorded values; `summarize.py` retains the extraction logic. Original reruns
are `*-baseline.txt` and `*-baseline-http.txt`. Intermediate `first-cells`,
`entrance-cells`, `cells32-a` and `terminal` logs retain the explored tradeoffs.

`baseline.sh`, `build-candidates.sh`, `final-validation.sh` and
`final-cache-checks.sh` retain exact build/test environment and command sequences.
The `*-candidate-build.txt` / `*-rebuild-build.txt` files record independent build
resources. `reproducibility.json`, `verify-rebuilds.py` and
`flat-reproducibility.json` record preservation and rebuild checks. Final route
reports are `*-final-routes.jsonl` / `*-final-verification.txt`; additional source,
sensitivity, boundary, address-cycle, geocoding, mapped-access, HTTP race and
lifetime logs are named by their suite. `environment.txt`, `input-checksums.txt`
and `deployment-{before,after}.txt` identify the host, retained inputs and unchanged
deployment.

All three requested long-route query targets are met on this fixed workload.
Search still evaluates 4.37 million cell transfers and retains 564,380 labels for
Seattle–Boise; transfer scans, partition quality and label storage remain useful
optimization targets. The overlay does not contract protected restriction/access
boundaries or provide a complete geographic hierarchy.

Full graph reconstruction, construction scratch space, resident geometry/index
structures and old/new snapshot overlap remain separate deployment gates. The
87.512-second warm startup still exceeds the historical 75-second load target;
one narrowly passing publication observation is not robust latency headroom.
Cold-file-cache behavior and national-sized loading remain unverified. Variable
speed and hazmat interpretation require a later explicit cost-model version,
separate route-plausibility checks and independent empirical duration validation.
