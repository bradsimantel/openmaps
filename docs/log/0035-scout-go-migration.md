# Sole Scout backend and Go workflow migration

Date: 2026-09-09 (America/Los_Angeles). Scope: migration from `dc20ef2`,
subsequently committed and pushed as `8d4a688`. Historical implementation and verification record; current commands
are maintained in [Scout routing](../routing-scout.md). Supersedes the parallel
service and Python workflow described in historical [0034](0034-national-scout-qualification.md).
Verification preceded the commit and push. No dataset deletion or deployment
replacement occurred during that milestone. The later deployment cutover and
remaining code cleanup are recorded in [0036](0036-scout-deployment-cutover.md).

## Architecture and removed components

Scout is the sole routing backend in `cmd/server`. The same service serves
Places, geocoding, basemap ranges and static files. `-db` or `-deployment` selects
lookup SQLite; `-routing-scout` supplies routing pages. `-db ''` permits a service
without lookup, whose lookup API returns unavailable. `-scout-selection` changes
only routing. Lookup refresh and routing replacement have independent ownership;
route leases cover HTTP encoding and admission remains global across generations.

Removed:

- All top-level `internal/routing/*.go` legacy graph code: PBF-derived/SQLite graph
  versions, destination/address associations, guard snapping, junction/cell and
  recursive overlays, dense/mapped caches, chunked/prepared formats and loaders.
- Routing-only PBF importers, access/speed/vehicle interpretation and exclusive
  tests; routing additions/comparisons/rollback paths in import and refresh.
  Shared OSM street parsing and its PBF test fixtures remain for lookup imports.
- `cmd/routing-prepare`, the parallel `serveScout` HTTP server, and flags
  `-routing-pbf`, `-routing-prepared`, `-routing-cache`, `-routing-legacy-load`.
- The original Valhalla 3.6.3 tar reader and whole-tile cache, transient `OpenScout`
  spool backend, page-vs-tile experiment, obsolete provider-specific tests,
  Oregon/Northwest routing locks, Librescoot Bremen lock and legacy route fixtures.
  `cmd/routing-valhalla` is replaced by prepared-only `cmd/scout-audit`.
- All nine tracked Python files: `routing-capacity.py`, `routing-residency.py`,
  `test_routing_capacity.py`, `scout-acquire.py`, `scout-coverage.py`,
  `scout-http-verify.py`, `scout-run-bounded.py`, `test_scout_acquire.py`, and
  `test_scout_coverage.py`.

The hand-encoded Go graph reference now uses Scout 3.4.0 records directly.
Ordinary Dijkstra, independent raw-source path replay, turn/access adversaries,
dateline projection, reverse-edge identity, page ownership, corruption, snapshot
leases and graph/landmark extension tests remain. Optional bidirectional search
is an offline Scout search experiment, not another backend or serving strategy.
National serving still uses one-sided directed landmark A*.

No Go dependency could be removed after `go mod tidy`: retained dependencies
serve places, geocoding, GeoParquet, SQLite or shared PBF street fixtures. The
removed Python programs used the standard library; there was no Python dependency
manifest. The UI now offers selected coordinates and map points, and states that
direct address routing is unavailable.

## Go acquisition, preparation and verification

`cmd/scout-acquire` and `internal/importer/scout` implement metadata snapshot,
regional/explicit package planning, resumable fetch and offline verification.
The complete digest and directory inventory must agree; identical repeated digest
entries are accepted and conflicts rejected. Plans retain catalog omissions,
regions, explicit dependency packages, source generation, attribution, provenance,
size, MD5 and available SHA-256 pins. Fetch checks the complete catalog/digest
before and after the run. Every retained package is rehashed; receipts must agree
with the pinned generation. Interrupted transfers remain partial and only that
package is retried. Publications use synced private files and non-replacing links.
Download ceilings and disk reserve checks apply before and during transfer.

The existing Go preparation pipeline now also validates acquisition plans against
the complete local manifest. Explicit per-tile pins, when supplied, must match
actual members and bytes. It retains nested compression checks, tile/generation
validation, identical duplicate deduplication, conflicting duplicate rejection,
immutable receipts, bounded writes and safe graph/landmark extension proofs.

`cmd/scout-coverage` reads the pinned Census ZIP/Polygon/DBF formats in Go and
checks holes, polygon/rectangle crossings, missing references, source geometry and
state route endpoints. `cmd/scout-http-verify` compares full HTTP geometry and
rounded costs against independently verified offline JSONL, retains first bodies
and request measurements, and supports concurrent stress. It rejects unqualified
or conflicting offline cases and caps retained input at 128 MiB.
`cmd/scout-run-bounded` observes the owned process and filesystem, terminates its
process group on sampled RSS/free-disk crossings, handles cancellation, and records
exit status and resource observations. Sampling is not a hard RSS bound.

These commands do not invoke Python or Valhalla's routing engine. The supervisor
uses the host `ps` utility; optional macOS copy-on-write preparation uses `cp -c`.
Go implements acquisition, decompression, graph preparation, indexes, landmarks,
route search and verification. No new third-party Go module was needed.

## Verification evidence

Artifacts are isolated in ignored `data/scout-go-migration-20260909/`. The pinned
83-case file and national lock are unchanged. The full original historical
qualification and its retained outputs remain intact.

- `go test ./...`, `go vet ./...`, gofmt and `git diff --check` pass. Race checks
  pass for `cmd/server`, API, lookup deployment, acquisition, routing/qualification,
  Scout and resource supervision. Default acquisition fixtures cover resume,
  bad retained/downloaded checksums, mixed generations, complete inventory,
  conflicting duplicates, changed provider metadata and preflight/stream budgets.
- Separate downloaded-data suites pass: Scout source/package boundaries, real
  simple/complex restrictions, prepared regional routes and landmarks, API masks
  and errors, pinned Census coverage and all retained acquisition receipts.
  Explicit seed-discovery experiments remain opt-in and are skipped.
- Offline verification rehashes all **647 packages / 9,192,382,849 compressed
  bytes** against their full metadata generation and receipts: 15.716 s,
  40,599,552 sampled RSS bytes in the standalone Go command. A separate final
  downloaded-input test also passes.
- The final national offline run passes **83/83 cases**: **75** independently
  verified routes, **66** ordinary-reference cost matches, **2** unreachable and
  **6** unsnappable. It takes 36.155 s including startup, with 780,664,832 sampled
  RSS bytes. No routing outcome, cost or geometry expectation was weakened.
- Full national HTTP verification passes **83/83**, including every geometry
  coordinate, rounded metre/second costs, profile and snapshot identity.
  The integrated-service run takes 17.906 s and receives 5,198,292 bytes.
  A final-binary repeat (lookup explicitly disabled) also passes 83/83 in
  19.159 s; its disabled lookup endpoint returns HTTP 503 without a panic.
- The final two-snapshot service test checks two admitted/eight simultaneous
  clients (**2 HTTP 200, 6 HTTP 429**), `Retry-After`, cancellation capacity
  recovery, distance-only masks, broad-mask rejection, unavailable addresses,
  Bering Sea incomplete data, nonexistent selection and malformed JSON. Both
  specific selection errors are observed before restoring valid input.
  Concurrent round-trip traffic passes **2,013/2,013** full responses,
  **1,528,809,084 bytes**, with both snapshots observed and zero mismatches;
  the expanded national snapshot is restored. A preceding separate round trip
  passes 2,927 responses; its counts are not folded into the final run.
- The national service lifetime samples **1,745,731,584 RSS bytes**, below the
  4 GiB threshold; free disk remains above 32 GiB. It shuts down cleanly.
- A fresh **14-package regional graph** builds in Go in 29.804 s with
  40,845,312 sampled RSS bytes. Its tile spool hash, byte count, full tile index,
  version, dataset and timestamp exactly equal the retained regional graph.
  Fresh forward/reverse turns, potential and reverse-support indexes publish
  successfully. Two landmark pairs build in 17.179 s with 459,177,984 sampled
  RSS bytes. The rebuilt graph/landmarks pass separate routing and HTTP tests.
- A second service combines that newly built regional graph with an isolated
  copy of lookup deployment selection. The live Places and geocoding suites
  pass, and basemap byte ranges return the PMTiles header. Browser verification
  selects White Horse Tavern and Redwood Library, calculates a **1.25 km** route,
  and visibly renders the route, requested/snapped endpoints and gap markers.

The supervised graph, index, landmark, offline, HTTP, service and compiled live
API checks run with an executable search path containing only `ps` and `cp`, so
Python cannot be resolved by those jobs. Default/race checks use the installed Go
compiler. Construction and verification overlap on this shared desktop; timing
and sampled RSS are observations, not isolated benchmarks or physical-cold tests.

The full national source audit is rerun: **37,362 tiles**, **19 geometry endpoint
mismatches (11 permitted)** and **706 distinct missing references**, matching the
known failures. It intentionally exits **1**, retains all results, takes
284.014 s and samples 397,721,600 RSS bytes without a resource abort. The Go
coverage checker applied to this fresh audit finds no retained missing-reference
tile boxes or geometry examples inside the pinned coarse state/DC footprints;
all 51 state/DC route checks pass. The two Bering Sea snap probes still report
missing data, not certified empty coverage. This is not a global audit pass.

Initial verification exposed a relative lookup-path regression in the unified
server and a map-shaped missing-reference report in the coverage port. Both were
corrected and covered by regression/downloaded-input tests before final checks.
The initial failed artifacts remain alongside successful reports. An ignored
`data/go.mod` keeps retained scratch Go experiments outside `go test ./...`;
no archived source program or dataset was rewritten to make it compile.

## Preserved deployment and remaining limits

Only isolated ports **8106/8107** and migration-owned selection files are used.
The original **8096/8097** services remain healthy, and their binaries, selection,
packages and graphs are preserved. Verification services are stopped afterward.
The preexisting 8097 supervisor still runs Python, intentionally untouched to
preserve deployment; its eventual restart should use the documented Go commands.
**79 ignored Python files under `data/`** remain as downloaded provider references
or historical investigation/measurement artifacts. None is imported or invoked
by the supported workflow. No tracked Python or legacy routing implementation
remains, and no Valhalla routing-engine dependency remains.

The profile stays `osm-scout-public-auto-v1` / `scout-edge-speed-v1`, not
`driving-time-v4`. It provides no address association, ferry connectivity,
destination-only access, traffic, turn delay or reconstructed source tags.
Exact OSM cutoff and production provider configuration remain unverified.
Representative national qualification is not exhaustive road coverage. Source
bugs, missing dependencies, disconnected roads, unverified snap gaps, query
budgets and unmeasured physical-cold behavior remain explicit limitations.

The [machine-readable migration evidence](0035-scout-go-migration.json) records
removed paths, retained changed-file hashes, pinned inputs, binary/report hashes,
resource observations and outcome counts. A second full national graph/vector
rebuild was not attempted: it would need roughly 33.4 GB additional payload plus
the 32 GiB reserve. Fresh regional preparation and unchanged national input/graph
verification cover this migration without deleting retained evidence.
