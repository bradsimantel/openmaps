# Routing storage, search and regional evaluation

Routing remains one Go service with immutable SQLite snapshots. The current
implementation supports separate Oregon and Oregon–Washington–Idaho coordinate-routing
evaluations, in addition to the Newport coordinate/address candidate. **It is not nationwide
ready.** Spatial lookup, geometry chains and a recursive junction-cell overlay address
measured regional search bottlenecks. A complete restriction-aware hierarchy
and a backend with bounded national construction memory remain unfinished.
Direct prepared loading and streaming prepared rollback verification are implemented; see [representation and trust boundaries](routing-prepared.md).

The [historical Oregon evaluation](log/0019-routing-scale-and-oregon.md),
[historical junction/mapping experiment](log/0020-junction-hierarchy-and-mapped-query-data.md), and
[historical recursive-cell experiment](log/0021-recursive-junction-cell-overlay.md)
record inputs, targets, measurements, comparisons and limitations. The API contract and
car profile remain in [routing](routing.md). The browser is an example client and
was not changed for this work.

## Snapshot representation

For production startup, [prepare the complete query representation offline](routing-prepared.md).
That loader bypasses source decoding and all graph/index/hierarchy construction.
The construction path described below remains the explicit offline/legacy path.


New builds retain graph semantics **4**, profile `driving-time-v4` and cost model
`estimated-driving-v1`. Storage layout **`routing-chunks-v1`** and preprocessing
semantics **`junction-cells-v1`** are versioned independently in the manifest and
snapshot comparison summary. Changing any of these semantics requires explicit
version handling; an unknown version fails loading.

`routing_graph` holds one small JSON manifest and its SHA-256. The manifest names
every ordered chunk, its record count and compressed-byte SHA-256.
`routing_chunks(kind, ordinal, data)` stores up to 4,096 records per chunk, as Go
gob slices of the explicitly named domain structs, compressed with DEFLATE. The
version fixes those record meanings, encoding and compression convention. No
native pointer layout or unsafe struct casts are persisted. Floating-point
coordinates and costs are retained without lossy quantization. Compressed and
decoded chunks are bounded at 32 MiB; counts, checksums, versions and trailing data
are checked. The manifest digest commits to query data **and** provenance.

Separate chunk kinds contain nodes, segments, costs, prohibited paths, snap
guards, access ways/areas/entrances, and source records. Original source records,
source IDs, object versions, decisions, cost assumptions and attribution stay in
SQLite. Runtime validation streams provenance one chunk at a time, checking
references and address evidence against source tags. Validated speed-assumption
text is released before building query data; numeric directional costs remain.
`routing.ReadData` is an explicit offline inspection interface that retains full
source records. It is not the runtime loading path.

Legacy construction uses contiguous coordinate and adjacency arrays with a source-ID-to-dense
index map and compressed sparse row offsets. Segment/source identities remain
unchanged. Directed edges keep distance and elapsed cost; segment geometry,
one-way/access classifications, destination zones and the restriction automaton
remain query data. The loader transfers ownership of decoded segments to avoid
an extra copy, and constructs adjacency without per-node temporary slices.
Provenance, cost notes, validation maps, decoding buffers and the original node
record array are transient. Source strings and some maps still consume meaningful
memory; this is not a fully memory-mappable national layout.

Prepared v2 uses 32-byte directed edges with dense endpoint ordinals; v1's
48-byte source-endpoint edges remain readable. Search and reconstruction index
coordinates and CSR directly, while source segments, snapping, address/access
relationships and public identities keep their source references. No hierarchy or
cost model changes accompany this encoding. See the
[maintained prepared layout](routing-prepared.md) and
[historical representation experiment](log/0024-dense-routing-edges.md).

Routing `way:ordinal` segment references reproduce for an identical pinned source
build. They are source geometry references, not permanent public entity IDs
across OSM geometry edits. Existing `om_` lookup identities remain unchanged.

Retained JSON graph formats 1–4 remain readable, with checksum and semantic
validation. Versions 1–3 keep their original distance objective and duration
availability; address routing still requires their existing v3 evidence. No
retained file is migrated or overwritten. A missing routing table is supported
only if there is no partial chunk table. A corrupt, mixed-layout or unsupported
snapshot fails loading instead of becoming silently unavailable.

Logical records and graph/chunk hashes must reproduce from pinned inputs and the
pinned toolchain. SQLite file-byte equality is not required. Whole-file snapshot
fingerprints still cover all tables, including identity/provenance history.

## Endpoint indexing and search

Packed bounding-volume trees index road segments, excluded guards, access areas
and driveway geometries. Each feature appears once; long segments are indexed by
bounds, not just by their midpoint. Sorted candidate ordinals feed the existing
projection, distance, containment, crossing and source-junction predicates. The
index is a conservative candidate filter, not a replacement for those predicates.
Driveway adjacency is built once. Endpoint selection remains independent of the
other endpoint and of route success or cost. Coordinate bounding rejection also
widens with latitude where needed to avoid losing roads within the 100 m limit.

The default time search combines A* with a conservative bottom level of topology
contraction. Preprocessing identifies a unique non-reversing directed continuation
away from **every node in a prohibited path** and away from destination zones.
Search follows such forced chains without adding each geometry vertex to the
queue. It stops at either endpoint of the destination segment, preserving partial
arrivals, and at branches and relevant restriction/access boundaries. Functional
graph cycles have a deterministic break point. Every traversed edge still applies
the existing restriction automaton and destination phase, and costs accumulate
in source traversal order. Reconstruction expands every source segment and vertex.
This remains the bottom level of the partial hierarchy described below.

A* uses spherical distance to the selected road destination divided by the
maximum effective speed in the loaded graph (distance alone for a distance
comparison). Ignoring turns and destination constraints can only lower this
bound. A downward floating-point margin protects pruning. Reopening states is
allowed; no epsilon discards a genuine cost improvement. The reference
`RouteReferenceEndpoints` uses ordinary Dijkstra under identical endpoints,
costs, turn history and destination phases.

Queue ties are deterministic: priority, accumulated cost, directed-edge ordinal,
restriction state and destination phase. Equal relaxations retain the first
predecessor; an equal-cost direct segment wins. Different search orders can choose
different paths with exactly equal modeled costs, but must preserve the optimum
within `max(1e-6 seconds, abs(reference seconds) × 1e-10)`. This numerical tolerance
is for verification, not seconds of travel-time accuracy. Evaluated Newport and
Oregon paths matched their reference paths. No live traffic or calibrated duration
claim is added.

Sources for the algorithm choices:
[Goldberg and Harrelson, A* and landmark lower bounds](https://www.microsoft.com/en-us/research/publication/computing-the-shortest-path-a-search-meets-graph-theory/),
[Geisberger et al., contraction hierarchies](https://ae.iti.kit.edu/download/contract.pdf),
and [turn-aware contraction research](https://publikationen.bibliothek.kit.edu/1000097647).
Full turn-aware CH/CCH and ALT remain unimplemented.
The measured remaining core search motivates a next hierarchy experiment rather
than a claim that geometric A* alone solves nationwide queries.

## Oregon scope and reproducible build

[The source lock](../imports/oregon-routing.lock.json) pins Geofabrik Oregon,
Washington and Idaho extracts at **2026-09-07T20:21:20Z**, and the exact derived PBF.
The graph includes all Oregon extract roads, a southern Washington corridor
`[-124.8,45.4,-116.4,46.6]`, and a western Idaho corridor
`[-117.3,41.98,-116.5,44.3]`. Coordinates are longitude, latitude. The corridors
support boundary alternatives; they are not independent routing partitions.
Osmium `complete_ways` extraction followed by `merge` preserves complete ways
and source node identities. All route searches can traverse the whole merged
graph. Original PBF checksums and acquisition/derivation records are retained.

Supported endpoint bounds in this evaluation are the rectangle
`[-124.8,41.98,-116.45,46.3]`, **not Oregon's legal boundary or a claim of continuous
road coverage**. The Idaho detour extends beyond the native Oregon extract.
Other neighboring coverage is incomplete: California, Nevada and Idaho beyond
the declared corridor are not acquired. Southern-border and eastern-border
routes whose best paths leave that coverage are not certified by this benchmark.
No endpoint-region loading or arbitrary state partition is used.

The snapshot is explicitly `routing_only`: lookup tables are empty and there are
no Oregon address records, geocoding coverage or address associations. No Newport
records are copied into it. Coordinate routing uses the same HTTP contract.
Address requests cannot resolve a source address in this snapshot.

After acquiring and verifying the pinned PBFs, reproduce the derivative with
Osmium 1.19.1 (output files must not exist):

```sh
osmium extract -b -124.8,45.4,-116.4,46.6 -s complete_ways \
  data/scaling/washington-260907.osm.pbf \
  -o data/scaling/washington-south-260907.osm.pbf
osmium extract -b -117.3,41.98,-116.5,44.3 -s complete_ways \
  data/scaling/idaho-260907.osm.pbf \
  -o data/scaling/idaho-west-260907.osm.pbf
osmium merge data/scaling/oregon-260907.osm.pbf \
  data/scaling/washington-south-260907.osm.pbf \
  -o data/scaling/oregon-buffered-260907.osm.pbf
osmium merge data/scaling/oregon-buffered-260907.osm.pbf \
  data/scaling/idaho-west-260907.osm.pbf \
  -o data/scaling/oregon-detours-260907.osm.pbf
osmium check-refs data/scaling/oregon-detours-260907.osm.pbf
```

Create the empty normalized bundle from the maintained lock using Python 3:

```sh
python3 - <<'PY'
import hashlib, json, pathlib
root = pathlib.Path('data/scaling')
manifest = json.loads(pathlib.Path('imports/oregon-routing.lock.json').read_text())
bundle = dict(schema=1, manifest=manifest, identities={}, records=[], relationships=[])
raw = (json.dumps(bundle, indent=2) + '\n').encode()
(root / 'oregon-final-bundle.json').write_bytes(raw)
(root / 'oregon-final-bundle.sha256').write_text(hashlib.sha256(raw).hexdigest() + '\n')
PY
GOMEMLIMIT=8GiB go run ./cmd/import \
  -bundle data/scaling/oregon-final-bundle.json \
  -checksum data/scaling/oregon-final-bundle.sha256 \
  -routing-pbf data/scaling/oregon-detours-260907.osm.pbf \
  -db data/scaling/oregon-next.sqlite
```

This establishes a new local normalized-bundle checksum; it does not change or
bypass the PBF pin. Keep the verified archives: upstream dated downloads expire.
The importer refuses existing outputs and publishes only after graph validation.
Use another filename for a rebuild. Nothing here selects the active deployment.

## Verification and deployment

Set `OPENMAPS_PREPARED` to a trusted publication directory to exercise direct
loading in the performance, HTTP, source-backed and lifetime suites below. Leave
`OPENMAPS_ROUTING_CACHE` unset in that mode. Legacy loads remain the default for
these explicit offline tests. `TestLoadingPhases` records phase times, cumulative
allocations, sampled peak Go heap, retained heap and mapped bytes.


Small offline tests cover chunk corruption and unsupported versions, spatial
scan equivalence, randomized accelerated/reference comparisons, directional
costs, via-way history, destination access, partial endpoints, closed rings,
deterministic ties, cancellation and concurrent snapshot leases.

Regional files and network acquisition are excluded from routine tests:

```sh
go test ./...
go vet ./...
go test -race ./internal/routing ./internal/dataset ./internal/api ./internal/importer
OPENMAPS_OREGON_DB="$PWD/data/scaling/oregon-final.sqlite" GOMEMLIMIT=6GiB \
  go test -tags=integration ./internal/routing -run '^TestOregonRouting$' -count=1 -v
OPENMAPS_PERF_DB="$PWD/data/scaling/oregon-final.sqlite" \
  OPENMAPS_PERF_CASES=testdata/oregon.json OPENMAPS_PERF_CONCURRENCY=1,2,4 GOMEMLIMIT=6GiB \
  go test -tags=integration ./internal/routing -run '^TestRoutingPerformance$' -count=1 -v
```

The 31-case Oregon suite checks pinned source endpoints/tags, source-node
adjacency, directions, prohibited paths, destination shortcuts, independent
cost sums, broad geographic detour guards and reference optimality. The dedicated
`TestOregonSensitivity` compares distance and time objectives on suspicious
southern routes. `TestOregonBoundaryCoverage` additionally takes
`OPENMAPS_OREGON_BOUNDARY_BEFORE` for the retained Oregon/WA-only investigation.
`TestCoordinateStorageStrategies` takes `OPENMAPS_PERF_DB` and a new
`OPENMAPS_STORAGE_FILE` under ignored `data/`; it compares arrays, mmap and a
bounded page cache, including first and repeated accesses.

To verify a separately running server, set `OPENMAPS_OREGON_URL` and run
`TestLiveOregonRouting` with the integration tag. No browser checks are needed
for this API/storage work.

Deployment requests share a read lease through response completion. One request
loads a changed selection outside that lease; concurrent requests keep using the
previous immutable snapshot. Publication waits for old response leases before
closing its SQLite store. Validation returns its already loaded graph, avoiding
an additional graph load. Failed loads preserve the previous handler and report
a degraded health result. The initiating request can take a full regional load;
the server write timeout is two minutes to accommodate it. This remains
request-triggered reload, not asynchronous operator orchestration.

`TestRegionalSnapshotLifetime` uses only a temporary deployment state, with
`OPENMAPS_LIFETIME_BASELINE` and `OPENMAPS_LIFETIME_CANDIDATE`. It sends concurrent
real HTTP requests during replacement, checks snapshot-consistent responses,
retains old query data through the overlap measurement, forces a failed selection
and rolls back under the shared HTTP admission budget. Neither retained database
nor the active deployment is changed. `OPENMAPS_LIFETIME_TIMEOUT` can extend the
default 90-second harness deadline (up to ten minutes) for a measured slow-load
experiment; the report still identifies whether publication met the original
90-second gate. This does not change the production server timeout.

## Junction elimination and request memory

The runtime's **`junction-cells-v1`** adds bounded cells above forced chains.
Deterministic source-ordered unions group at most **32** ordinary junctions.
Candidates have two to four departures and a non-forced incoming approach;
one-way forks qualify, ordinary degree-two geometry does not. Every prohibited-path
node and every node incident to a destination-access segment stays explicit.

For each cell entrance, local Dijkstra labels **incoming directed edges** and
stores the cheapest legal transfer to each distinct outgoing boundary edge.
Immediate reversal remains forbidden. Cycles, directional costs and parallel
approaches retain their edge identities; no node-only witness search is used.
Recursive predecessor records share equal prefixes and unpack original forced
chains. Interior origin states search normally until leaving their cell. The
previous independent-junction bypass remains a fallback for detailed local work
and the internal distance-objective comparison on time-model snapshots.

Every transferred edge is absent from the entire restriction alphabet, because
its source node is outside every prohibited path. Its first edge therefore resets
any incoming trie history to zero. All transfer edges are public: phases 0/1 become
phase 1, and destination suffix phase 2 cannot enter. This narrow proven reset
avoids replaying each skipped edge during search. Restriction/access boundaries
still run the full original transitions; query labels retain incoming edge,
restriction history and phase throughout.

Cell bounds contain all interior and outgoing chain vertices. If either destination
segment endpoint falls inside a cell's bounds, that cell uses detailed search,
conservatively preserving partial destinations. Forced walks still stop at target
segment nodes; origins keep their original headings and partial costs. A terminal
approach whose only exit is the forbidden immediate reversal is omitted from the
overlay and queue unless needed at a destination-segment node. Target cell opening
retains interior dead-end destinations, and origins can still choose a fresh heading.

Search groups sums of unchanged directional costs. Reconstruction expands every
source segment and sums output distance/duration in source traversal order. Cost
verification uses the documented tolerance; equal-cost ties may choose different
source paths. This is a partial overlay with recursive paths, **not full CH/CCH**.
Restricted-junction contraction, a deeper geographic hierarchy and complete
turn-history/access-state contraction remain unfinished.

A weak-component array rejects endpoints in different components after both snaps
are selected. It never certifies reachability inside one component. Dijkstra skips
this filter and remains the ordinary full-graph correctness reference. A single
label map holds distance and predecessor data; a typed heap avoids per-operation
interface allocations. Queue tie ordering and strict cost improvements are
unchanged. Diagnostic `SearchMetrics` separates endpoint selection, search and
geometry, with counts for expansions, states, queue capacity, junction bypasses
and cell transfers. The opt-in HTTP harness also measures the original encoder
(including writes) separately and reports phase latency distributions.

New SQLite manifests name `junction-cells-v1`; retained `forced-chain-v1` and
`independent-junction-v1` manifests remain readable; offline preparation or explicit legacy loading
regenerates current preprocessing from their validated source topology. Unknown versions fail. No preprocessed source path is
trusted from a legacy file. The optional flat artifact below fixes and checks the
actual generated arrays, including components, selected junctions and recursive
cell transfers. Cell construction observes cancellation and checks index capacity.

The server admits at most **four concurrent Compute Routes HTTP requests** by
default, including encoding. `-routing-concurrency` accepts 1–64. The budget is
shared across snapshot replacements, with no waiting queue. Excess requests get
HTTP **429**, `error.status=RESOURCE_EXHAUSTED` and `Retry-After: 1`. Health and
lookup requests retain their existing behavior. This bounds concurrent query
work, not the memory of one arbitrarily large search. It is not authentication,
billing, or a throughput guarantee.

## Legacy mapped numeric query data

With explicit `-routing-legacy-load`, `-routing-cache data/routing-cache` enables
**`routing-hot-le64-v2`**. A fixed 4 KiB header identifies graph checksum,
preprocessing, section offsets/counts/widths and payload SHA-256. Fields use
explicit little-endian integers and IEEE-754 float64 values; padding is zero.
Sections contain coordinates, directed edges, CSR offsets/adjacency, segment
directions, forced continuations, destination zones, weak components and junction
selection, cell bounds, entrance ranges, directional transfers and recursive path
records. The 48-byte edge record uses four former padding bytes for its entrance
index. Source IDs and numeric costs are preserved without quantization. Retained
v1 cache files remain untouched; the current loader generates separately named
v2 caches from legacy SQLite graphs.
The reader checks native widths and every edge-field offset before creating
pointer-free typed views of a read-only mapping. Other ABIs fail explicitly.

SQLite remains authoritative. Loading first validates its complete graph and
provenance, reconstructs the canonical arrays, and hashes their explicit encoding.
An existing flat file must exactly match this independently reconstructed header
and payload hash. Wrong versions, foreign data, corrupt payloads, truncation and
nonzero header padding fail; a corrupt cache is never silently rebuilt. New files
are synced and atomically linked without replacing another file. Concurrent
publishers must validate the winning artifact. Keep cache files immutable while
mapped; remove obsolete files only after their readers are retired.

This legacy backend maps numeric arrays. The [prepared loader](routing-prepared.md)
also persists the remaining graph and spatial structures. Neither is a complete
national readiness claim. It releases their Go heap allocation and lets the OS manage residency;
it does not impose an RSS bound. Segment records/strings, source-ID lookup maps,
spatial indexes, the restriction automaton and address evidence remain resident.
Source provenance stays in SQLite. There is no SQL per visited edge. Canonical
reconstruction and checksumming still incur full construction memory and touch
the mapped pages at startup. First requests after validation are therefore not
cold-disk measurements. The integration harness distinguishes these from warmed
requests and reports process faults; a true system-cache-cold run needs a separate
controlled host.

A routing store holds a read lease through search and reconstruction. Explicit
`Close` waits for those calls and unmaps once; a cleanup is a fallback for abandoned
stores. Dataset replacement also keeps its existing HTTP response lease, so mapped
arrays survive until old responses finish encoding. Failed loads close unpublished
resources and leave the previous snapshot usable. Fixed-database servers close
the router on shutdown. Direct users of `OpenMapped` must close after use.

The importer now limits temporary restriction adjacency to from/via-way nodes,
retaining every legal departure there, including unrelated alternatives that an
only-turn restriction must prohibit. It writes chunks inside the unpublished
transaction, releases source construction data, then validates the exact written
graph before commit. This avoids constructing a second full graph alongside the
importer's raw sources; rollback still protects against invalid graph data.

The [historical junction and mapped-backend experiment](log/0020-junction-hierarchy-and-mapped-query-data.md)
records targets, measured regional results, failed attempts and remaining gates.

## Northwest intermediate evaluation

The [Northwest source lock](../imports/northwest-routing.lock.json) reuses the same
pinned complete Oregon, Washington and Idaho extracts, merged without clipping.
The routing-only candidate has 13.7 million routing nodes and no address coverage.
Its 29-case source-backed suite includes Seattle–Spokane, Seattle–Boise,
Bellingham, Yakima/Wenatchee, northern Idaho, McCall, Idaho Falls, Oregon bridges,
access streets, the two Oregon-to-Oregon optima through Idaho, and both directions
of a disconnected case. All searches can traverse the entire merged graph.

The historical single-junction runtime missed the one-worker p95 and four-worker
throughput targets. The [historical recursive-cell evaluation](log/0021-recursive-junction-cell-overlay.md)
records the cell overlay meeting those query targets on the retained workload,
with its comparisons and startup/storage tradeoffs. This
is an intermediate dataset, not a nationwide-ready deployment. Direct prepared
loading now removes full startup reconstruction and maps geometry/index data;
[physical residency, cold-file-cache behavior and national preparation](routing-prepared.md)
remain unverified.

Reproduce the merged input with Osmium 1.19.1 into a new output:

```sh
osmium merge data/scaling/oregon-260907.osm.pbf \
  data/scaling/washington-260907.osm.pbf data/scaling/idaho-260907.osm.pbf \
  -o data/junction-scale/northwest-260907.osm.pbf
osmium check-refs data/junction-scale/northwest-260907.osm.pbf
```

Create an empty routing-only normalized bundle from
`imports/northwest-routing.lock.json` using the same bundle construction shown
above for Oregon, and build it into a new database with `cmd/import`. Verify all
source checksums first. The historical report retains the precise local filenames.
Run the new suite and isolated performance/HTTP harnesses from the repository root:

```sh
OPENMAPS_NORTHWEST_DB="$PWD/data/junction-scale/northwest-final.sqlite" \
OPENMAPS_ROUTING_CACHE="$PWD/data/junction-scale/cache" GOMEMLIMIT=6GiB \
  go test -tags=integration ./internal/routing -run '^TestNorthwestRouting$' -count=1 -v

OPENMAPS_PERF_DB="$PWD/data/junction-scale/northwest-final.sqlite" \
OPENMAPS_PERF_CASES="$PWD/internal/routing/testdata/northwest.json" \
OPENMAPS_ROUTING_CACHE="$PWD/data/junction-scale/cache" \
OPENMAPS_PERF_DETAIL=1 OPENMAPS_PERF_CONCURRENCY=1,2,4 GOMEMLIMIT=6GiB \
  go test -tags=integration ./internal/routing -run '^TestRoutingPerformance$' -count=1 -v

OPENMAPS_PERF_DB="$PWD/data/junction-scale/northwest-final.sqlite" \
OPENMAPS_PERF_CASES="$PWD/internal/routing/testdata/northwest.json" \
OPENMAPS_ROUTING_CACHE="$PWD/data/junction-scale/cache" GOMEMLIMIT=6GiB \
  go test -tags=integration ./internal/api -run '^TestRoutingHTTPPerformance$' -count=1 -v
```

`TestMappedRoutingAccess` uses the same three path variables to report RSS/virtual
size, heap, minor/major faults and first/repeated access after a `MADV_DONTNEED`
hint. It does not purge the OS file cache. `TestNorthwestSensitivity` compares
distance/time objectives without altering costs. `TestNorthwestSourceSeeds`
streams only the source records needed to select independently named city/road
endpoints; it takes `OPENMAPS_NORTHWEST_SEEDS` as a new output path.
`TestNorthwestI90Sources` takes `OPENMAPS_I90_REPORT` as a new output path for
source decisions and tagged nodes on the investigated corridor. These opt-in
tests never acquire data or select a deployment. The lifetime harness defaults to its
retained zero-distance route to isolate graph loading. To overlap real search and
full response geometry with replacement, set `OPENMAPS_LIFETIME_CASE` to an exact
case name in `OPENMAPS_PERF_CASES`, choosing endpoints supported by both snapshots.
For the Oregon/multistate pair, `Portland to Ashland I-5 corridor` is such a case.
Keep these longer-query resource results separate from the default lifecycle test.

The [historical residency investigation](log/0023-routing-residency-and-rollback.md)
measures the former rollback alias, source-node lookup page use and the complete
HTTP lifecycle. Removing validation aliases improves process RSS accounting and
avoids redundant address-space overlap; it does not shrink the immutable graph
or impose a physical residency bound. The directed-edge/segment representation,
source-node lookup and per-query labels remain concrete working-set costs.
