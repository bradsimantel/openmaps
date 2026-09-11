# National Scout coordinate-routing qualification

Date: 2026-09-09. Scope: separate candidate built from `d24c604` plus the
uncommitted implementation accompanying this report. No commit, push, active
baseline replacement, paid infrastructure or host-wide setting change occurred.
Historical verification record; current commands and limits are maintained in
[Scout routing](../routing-scout.md).

## Result and scope

The isolated Go candidate serves `POST /directions/v2:computeRoutes` on
`http://127.0.0.1:8097`, with candidate metadata at `/healthz`. It owns decoding,
snapping, cost, turn restrictions, search, geometry, API translation, admission
and immutable snapshot lifetime. No Valhalla routing engine is invoked.

The frozen **83 cases** include all 50 states/DC, seven rural cases, state
boundaries, Alaska–lower-48 travel, Canadian and Mexican road legs, high latitude,
Hawaii, Aleutian roads on both sides of the dateline, unsnappable points and
exhaustively disconnected networks. **75 routed paths pass independent
source-path validation; 66 also exactly match ordinary Dijkstra costs.** Two
cases remain unreachable and six remain unsnappable. The suite has no unexpected
outcome. This is a working experimental national candidate, not exhaustive proof
that every US source road is present or every possible route meets its budget.

The endpoint envelope is retained tiles, not a jurisdiction mask. The provider
normalization cannot reproduce the existing `driving-time-v4` profile or discarded
source-access/entrance evidence. The separately named `osm-scout-public-auto-v1`
profile and `scout-edge-speed-v1` cost model remain visible in responses.
Ferries and national address routing are excluded. Exact upstream OSM cutoff and
production build configuration remain unverified. Provider byte consistency and
reproducibility are not independently attested provenance.

## Inputs and construction

The routing config `config/routing.json` pins **647 packages,
9,192,382,849 compressed bytes**, catalog/digest/directory metadata, source
attribution and the observed `2026-06-20_07:12` generation. Actual tile layout is
3.4.0, dataset ID 183131145. The full manifest has 2,930 packages; 34 are absent
from the regional catalog union and all 34 were explicitly acquired. Observed
southern dependencies required 23 additional Central American packages. Positive
island checks required four Kamchatka-selected packages. That last discovery and
its bounded extension are documented in [0033](0033-eastern-aleutian-source-extension.md).

| Final artifact | Value |
| --- | --- |
| Prepared directory | `data/scout-national-20260909/national-aleutian-prepared` |
| Tiles / nodes / directed edges | 37,362 / 122,732,001 / 285,220,167 |
| Permitted ordinary directed edges | 154,858,520 |
| Hierarchy transitions | 28,369,117 |
| Edges with simple prohibition masks | 436,091 |
| Complex restriction records / timed turns | 78,297 / 14,835 |
| Cross-package edges / transitions | 329,358 / 7,429,640 |
| Tile spool | 24,522,784,768 bytes |
| Forward / reverse turn payloads | 6,684,672 / 6,553,600 bytes |
| Nine directed landmark pairs | 8,836,743,168 bytes |
| Graph receipt SHA-256 | `4cb6be091e5a7751d77d45d5ae719849e29971af6d5677f63e35e71abafda2a1` |
| Landmark manifest SHA-256 | `e41c8a76e103ee0e586d4d7944001ef246b3d6a829d489b2d398bfcc723df8cf` |

All raw packages, acquisition receipts, intermediate artifacts, resource reports,
failed experiments and full verification outputs remain in the ignored
`data/scout-national-20260909/` directory. The earlier 643-package build was
independently decompressed from all compressed inputs; all 32,933 retained initial
tile payloads were identical across rebuilds. The final four-package extension
reuses a verified copy-on-write base, explicitly distinct from another complete
independent decompression. Its landmark reindex proof checks actual retained tile
blocks, not merely equal claimed hashes, and rejects new finite-component
connections. An adversarial fixture compares reindexed vectors byte-for-byte with
full recomputation, tests a shifted dense node order, inconsistent payload pins,
new finite connections, immutable resume and corrupt proof rejection.

Regional construction and query experiments preceded national scaling; see
[0030](0030-persistent-scout-regional-candidate.md). The incorrect assumption that
opposing edge pointers are bijective was discovered nationally, corrected, and
old v1 landmark vectors quarantined. [0032](0032-national-closure-and-reverse-edge-identity.md)
records the exact parallel service-road identities and corrected reverse view.
No provider shortcuts are traversed. Lower-bound construction relaxes turns and
node access only for admissible directed distances; actual routes preserve access,
full turn history, partial endpoints, reopening and every source step.

## Coverage, snapping and source failures

`national-complete-state-coverage.json` uses the pinned official Census 2025
1:500,000 state polygons. Every state/DC has verified snapped endpoints; no
retained missing-reference tile box or recorded geometry defect intersects these
coarse state footprints. This is not a legal boundary mask, a source-road census,
or a guarantee that an entirely absent disconnected network was discovered.
The Attu/Shemya positive checks demonstrate why dependency closure alone was
insufficient. The actual case coordinates are retained in
`internal/routing/qualification/testdata/national-cases.json`.

The full final source audit scanned all tiles in **270.110 seconds**, sampled
peak RSS **371,539,968 bytes**, child-reported maximum **429,522,944 bytes**, with
no resource abort. It deliberately exited **1** after retaining all results:
**19 endpoint geometry mismatches, 11 on permitted edges**, and **706 distinct
missing references** in acquired extras. The prior Fiji and western-Pacific
defects remain; the four added packages include further Japanese boundary
geometry defects. All 19 examples are retained, below the 128-example cap.
No defect was silently repaired or counted as a global pass.

Twelve frozen snap probes were compared against a full scan of eligible stored
shapes. Ten agree, including four positive-longitude Attu/Shemya endpoints and
one honest Adak unsnappable point. The `+180,65` and `-180,65` Bering Sea probes
both report missing tile `2/892799/0`; the retained full scan finds no shape.
Those two are **not nearest-match passes or certified empty coverage**. Synthetic
wrapped-bin and dateline geometry tests cover the projection mechanics.

Point Roberts→Blaine travels 40.373 km and reaches latitude 49.093962, verifying a
US→Canada→US road detour. Anchorage→Seattle verifies Alaska road connectivity
through Canada. San Diego→Tijuana and Tijuana→Yuma form a deliberate paired
US→Mexico→US journey, with 30.413 km and 293.063 km legs. This uses two coordinate
requests through an explicit Mexican waypoint; it is not evidence that an
unconstrained US→US cheapest route automatically chooses Mexico. Laredo→Brownsville
stays in Texas and is classified accordingly. No unsupported border-crossing
rights, current opening hours or traffic claims are inferred.

Honolulu→Hilo and Utqiagvik→Anchorage exhaust the supported disconnected networks
and remain unreachable. Adak, Attu and Shemya local paths do not create island
connections. Original off-road endpoint failures remain frozen alongside the
source-selected successful cases rather than being erased from the evidence.

The source-path verifier independently replays raw simple/complex restrictions,
access and dimension rules, node transitions, immediate reversals, partial costs,
and geometry continuity; it does not trust the search labels or compiled turn
state. It shares the low-level tile decoder and clipping primitives. It therefore
cannot independently certify decoder fidelity to discarded upstream OSM records.
Adversarial small fixtures and downloaded regional source tests cover those
separate responsibilities, including partial endpoints and many-to-one reverse
edge identities.

## Acceleration failures and measured limits

Four and eight landmark pairs still exhausted the 2,000,000-label HTTP budget on
Chicago→San Francisco. Optional unbalanced bidirectional ALT, a 4,000,000-label
offline experiment, and larger 32 MiB vector caches did not resolve that case
within their trial deadlines. Reports retain these failures. They were not used
to increase the serving limit. Page-buffer reuse is restricted to copied fixed
records and restored before borrowed shape data; cross-page corruption tests
cover the earlier ownership failure. A bounded per-query heuristic cache avoids
repeated vector reads.

A ninth San Francisco seed improved the difficult route. That case is now named
**former holdout**, because landmark placement was tuned to it. Reno→Cleveland
and Dallas→Washington DC are the separately frozen held-out routes; both pass.
The final service uses one-sided directed ALT, 2,000,000 labels and a 30-second
calculation deadline. State budgets include obsolete labels. Ordinary Dijkstra
remains the correctness reference, not the national serving strategy.

The eight-pair corrected build used 2,891.085 seconds of unique vector computation
across checkpoint/resume runs, then 261.584 seconds added the ninth pair. Largest
sampled landmark-construction RSS was approximately 2.43 GB, below the 4 GiB
supervisor threshold. The final disconnected reindex took 83.342 seconds at
273,088,512 sampled RSS bytes, writing 8.84 GB of vectors. Potential/turn preparation
and the copy-on-write extension are separately measured in their resource JSONs.

The final 83-case offline run took **49.197 seconds including 25.398-second
startup**, sampled peak RSS **782,942,208 bytes**, with no budget abort. Some
construction/verification jobs overlapped on this shared M5/16 GiB host; their
elapsed times are observations, not isolated machine benchmarks. Preparation and
startup hash files and warm the filesystem cache. Physical cold-disk latency and
complete OS-cache residency remain **unmeasured**. Logical page-read counters and
cumulative allocation totals are not physical disk traffic or peak resident heap.

## Reproduce and inspect

Run from the repository root. The retained acquisition directory is necessary to
reproduce this exact generation if the provider has since rotated its files.
The maintained [acquisition/preparation commands](../routing-scout.md#acquisition-and-preparation)
show pin validation, a fresh rebuild, all derived indexes and landmark construction
with `internal/routing/qualification/testdata/national-landmarks.json`. A fresh full graph plus
vectors needs roughly 33.4 GB additional logical payload **and** the 32 GiB
reserve; the host does not currently have room for another ordinary full copy.
The verified copy-on-write extension fits without deleting earlier evidence.

```sh
go build -o data/scout-national-20260909/scout-server-qualified ./cmd/server
go build -o data/scout-national-20260909/scout-verify-complete ./cmd/scout-verify

# Use an unused port if the qualified candidate is already running on 8097.
data/scout-national-20260909/scout-server-qualified \
  -routing-scout data/scout-national-20260909/national-aleutian-prepared \
  -routing-concurrency 2 -listen 127.0.0.1:8097

python3 scripts/scout-run-bounded.py --root data/scout-national-20260909 \
  --report data/scout-national-20260909/reverification.resources.json \
  data/scout-national-20260909/scout-verify-complete \
  -prepared data/scout-national-20260909/national-aleutian-prepared \
  -landmarks data/scout-national-20260909/national-aleutian-prepared/landmarks \
  -cases internal/routing/qualification/testdata/national-cases.json

python3 scripts/scout-http-verify.py \
  --offline data/scout-national-20260909/national-complete-all.jsonl \
  --out data/scout-national-20260909/http-reverification
```

The verifier writes JSONL to stdout; redirect it into a new report file when
retaining a new offline run. Output directories and supervisor reports must be
new. The actual service is run under the same 4 GiB sampled RSS / 32 GiB free-disk
supervisor as preparation, with a separate selection file. Its resource JSON is
finalized only on process exit; the completed qualification lifetime is retained
separately from the final running restart. No baseline configuration is consulted.

The source audit command is deliberately expected to return nonzero for the
recorded outside-target defects:

```sh
data/scout-national-20260909/routing-valhalla \
  -prepared data/scout-national-20260909/national-aleutian-prepared \
  -audit -audit-snaps internal/routing/qualification/testdata/national-snap-probes.json -cache-mib 128

python3 scripts/scout-coverage.py \
  --boundaries data/scout-national-20260909/cb_2025_us_state_500k.zip \
  --audit data/scout-national-20260909/national-aleutian-audit.json \
  --routes data/scout-national-20260909/national-complete-all.jsonl
```

Default checks are small and offline:

```sh
go test ./...
go vet ./...
go test -race ./internal/routing/valhallatiles ./internal/api
python3 -m unittest discover -s scripts -p 'test_scout_*.py'
```

Downloaded regional/source checks are separate:

```sh
OPENMAPS_SCOUT_DIR="$PWD/data/valhalla-scout" \
OPENMAPS_VALHALLA_TAR="$PWD/data/valhalla-feasibility/bremen.tar" \
OPENMAPS_SCOUT_PREPARED="$PWD/data/scout-national-20260909/regional-indexed" \
OPENMAPS_SCOUT_LANDMARKS="$PWD/data/scout-national-20260909/regional-landmarks-v2" \
go test -tags integration ./internal/routing/valhallatiles ./internal/api \
  -run 'TestScout|TestProvider|TestPreparedScoutRegional' -count=1
```

All these checks passed, including gofmt and diff formatting review. The downloaded
suite took 45.000 seconds for tile routing and 0.869 seconds for API integration.
An initial final-check attempt found two scratch `main` programs in the ignored
data directory; explicit `//go:build ignore` tags corrected their accidental
inclusion in `go test ./...`, then test and vet both passed. Scratch source is
retained and can still be run explicitly. A probe initially named a not-yet-built
binary; that failed invocation and its successful compiled retry are retained.

## Per-state frozen route evidence

Every row below passed source-path verification and ordinary-reference cost
comparison. These are representative queries, not exhaustive per-state road
inventories. Additional rural, international, island and long cases remain in
the frozen JSONL report.

| State/DC | Case | Road km | Application-cache-cold seconds |
| --- | --- | ---: | ---: |
| AL | AL Birmingham | 1.686 | 0.0540 |
| AK | AK Anchorage | 1.801 | 0.0386 |
| AZ | AZ Phoenix | 2.273 | 0.1204 |
| AR | AR Little Rock | 4.250 | 0.0900 |
| CA | CA Sacramento | 2.417 | 0.0988 |
| CO | CO Denver | 2.348 | 0.1612 |
| CT | CT Hartford | 2.619 | 0.0703 |
| DE | DE Dover | 1.834 | 0.0363 |
| DC | DC Washington | 2.056 | 0.1805 |
| FL | FL Orlando | 2.613 | 0.0966 |
| GA | GA Atlanta | 2.126 | 0.1682 |
| HI | HI Honolulu | 2.948 | 0.0600 |
| ID | ID Boise | 0.685 | 0.0556 |
| IL | IL Springfield | 2.348 | 0.0703 |
| IN | IN Indianapolis | 2.087 | 0.1378 |
| IA | IA Des Moines | 2.357 | 0.0751 |
| KS | KS Wichita | 2.195 | 0.0785 |
| KY | KY Lexington | 1.685 | 0.0574 |
| LA | LA Baton Rouge | 2.705 | 0.0523 |
| ME | ME Portland | 2.271 | 0.0422 |
| MD | MD Baltimore | 2.093 | 0.1252 |
| MA | MA Boston | 8.701 | 0.2038 |
| MI | MI Lansing | 2.145 | 0.0832 |
| MN | MN Minneapolis | 2.246 | 0.4044 |
| MS | MS Jackson | 2.388 | 0.0590 |
| MO | MO Columbia | 2.246 | 0.0569 |
| MT | MT Helena | 1.663 | 0.0360 |
| NE | NE Lincoln | 2.295 | 0.0942 |
| NV | NV Reno | 2.412 | 0.0757 |
| NH | NH Concord | 0.629 | 0.0368 |
| NJ | NJ Trenton | 2.079 | 0.0564 |
| NM | NM Albuquerque | 2.367 | 0.1765 |
| NY | NY Albany | 2.156 | 0.0822 |
| NC | NC Raleigh | 2.340 | 0.1611 |
| ND | ND Bismarck | 2.044 | 0.0437 |
| OH | OH Columbus | 2.767 | 0.1122 |
| OK | OK Oklahoma City | 1.990 | 0.0918 |
| OR | OR Salem | 2.090 | 0.0682 |
| PA | PA Harrisburg | 2.776 | 0.0641 |
| RI | RI Newport | 2.157 | 0.0643 |
| SC | SC Columbia | 2.454 | 0.1224 |
| SD | SD Pierre | 0.699 | 0.0175 |
| TN | TN Nashville | 1.826 | 0.1203 |
| TX | TX Austin | 2.299 | 0.1857 |
| UT | UT Salt Lake City | 2.513 | 0.1166 |
| VT | VT Montpelier | 2.577 | 0.0196 |
| VA | VA Richmond | 3.407 | 0.1290 |
| WA | WA Olympia | 2.008 | 0.0937 |
| WV | WV Charleston | 2.957 | 0.0489 |
| WI | WI Madison | 2.065 | 0.0945 |
| WY | WY Cheyenne | 2.506 | 0.0343 |

## Final cold/warm query measurements

Ten cases were run three consecutive times on the final graph. The first clears
application caches; the next two keep them. GC runs before the measured calculation,
and source-path verification occurs after it and can warm graph records before
the next repetition. The beginning overlapped the separate HTTP/admission checks;
physical disk cache was not cleared. Every returned benchmark path passed the
independent verifier. Allocation is cumulative per query, not live memory.

| Case | Cold s | Warm s | Warm s | Cold / last-warm allocated MiB | Labels |
| --- | ---: | ---: | ---: | ---: | ---: |
| CA Sacramento | 0.1115 | 0.0236 | 0.0237 | 58.5 / 13.1 | 126 |
| New York to Newark | 0.1925 | 0.0355 | 0.0345 | 110.0 / 16.1 | 3954 |
| Laredo to Brownsville | 1.8001 | 1.5504 | 1.5339 | 612.0 / 488.3 | 422177 |
| Anchorage to Seattle | 0.3445 | 0.0503 | 0.0505 | 216.2 / 25.0 | 9907 |
| Los Angeles to Boston | 0.7634 | 0.6776 | 0.6736 | 326.9 / 127.1 | 3533 |
| Seattle to Miami | 0.7037 | 0.6033 | 0.5966 | 322.0 / 105.7 | 4281 |
| Chicago to San Francisco (former holdout) | 0.7339 | 0.6513 | 0.6462 | 310.8 / 105.0 | 6064 |
| Tijuana to Yuma Mexican detour return leg | 0.7466 | 0.4329 | 0.4249 | 287.9 / 120.3 | 103184 |
| Reno to Cleveland held out | 3.2946 | 2.5127 | 2.1679 | 853.8 / 619.1 | 524230 |
| Dallas to Washington DC held out | 2.5344 | 2.1670 | 2.1081 | 1017.9 / 835.9 | 765110 |

The process took 46.375 seconds including startup and verification,
with 861,732,864 bytes sampled peak RSS and no resource abort.
The earlier 643-package nine-landmark benchmark remains in
`national-final-benchmark.jsonl`; the table above measures the final expanded graph.

## HTTP behavior and snapshot ownership

The final graph's **83/83 full HTTP responses** match status, rounded costs and
all geometry coordinates from the independently verified offline routes. The
serial run took 20.293 seconds, receiving 5,197,545 bytes, with p50 0.0735 seconds,
p95 0.8099 seconds and maximum 5.7840 seconds. `national-complete-http-all/` retains
the complete first body for every case and per-request timings/hashes.

An eight-client simultaneous Dallas→DC probe admitted two requests, both HTTP 200
in about 4.135 seconds, and rejected six with HTTP 429 and `Retry-After: 1` in
about 25 ms. After disconnecting another expensive request, both slots completed
new Anchorage→Seattle requests one second later (about 0.445 seconds each).
This demonstrates slot recovery, not a measured sub-250-ms cancellation bound.

Distance-only masks omit unrequested route fields. Broad unsupported masks return
400, addresses return 503 `address_routing_unavailable`, and both Bering Sea
endpoints preserve 503 `incomplete_data`. A nonexistent selection directory and
malformed selection JSON both preserve the selected snapshot with a visible
health error; restoring valid selection clears the error. Default/race tests
also cover lease ownership, corruption, close, cancellation and global admission
across replacement. Results are in `national-complete-errors-and-selection.json`
and the `http-admission-complete` / `http-cancellation-complete` reports.

A first five-minute final-binary traffic run completed **7,252/7,252** full
Anchorage→Seattle comparisons, 5.508 GB of loopback response bodies. The new graph
load took 88.937 seconds while graph scanning also ran. That traffic interval
ended just before publication, so its responses contain only the prior snapshot.
It demonstrates uninterrupted old-snapshot serving during load, not response
coverage on both sides of publication.

A subsequent round trip deliberately selects the prior nine-landmark graph and
then restores the final graph under two-client full-body traffic. Load plus
retirement took 33.636 and 35.654 seconds respectively. The service's global
admission remains two queries across both generations, and response leases keep
their graph alive through encoding. Earlier historical 4→8 and 8→9 landmark
replacements also completed 8,029 and 8,660 full-response comparisons respectively;
those remain separate evidence, not inflated final-graph counts.

The final four-minute round-trip run completed **6,164/6,164** full
responses, 4,681,305,276 bytes, with both snapshots observed, zero mismatches,
p50 0.0768 seconds, p95 0.0819 seconds and maximum
0.3571 seconds. It restored the final expanded graph before completion.
`national-complete-two-snapshot-stress/` retains the body, hashes and summary;
`national-complete-roundtrip-events.json` records both observed selection changes.

The completed qualification service lifetime lasted 880.387 seconds,
with **1,761,312,768 bytes sampled peak RSS**, child-reported
maximum 1,763,606,528 bytes, and no budget abort. Its prior
8→9-landmark service lifetime peaked at 1,761,935,360 sampled bytes. The final
binary was then started on the fully qualified expanded graph under a fresh
supervisor, leaving the candidate running on port 8097. The active port 8080
baseline and earlier candidate files remain intact.

The [machine-readable qualification](0034-national-scout-qualification.json)
pins the graph/landmarks, frozen cases, changed Go/Python source files, final binary,
key result files and resource observations. It distinguishes the successful
sampled qualification from the intentionally failed global source audit and
unmeasured physical-cold behavior.

The final running restart loaded in **30.768 seconds**. Its health reports final
snapshot `453d3ad01d124f750fb80220d237c00da3ec5b0e939c64545e7c78cf09f3cc27`.
A fresh three-case full-body smoke check covers Anchorage–Seattle, Attu and Shemya
after restart; the full 83-case HTTP suite is the qualification above.
