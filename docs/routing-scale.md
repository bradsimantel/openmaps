# Routing storage, search and regional evaluation

Routing remains one Go service with immutable SQLite snapshots. The current
implementation supports a separate Oregon coordinate-routing evaluation, in
addition to the Newport coordinate/address candidate. **It is not nationwide
ready.** Spatial lookup and geometry-chain preprocessing address measured regional
bottlenecks; a hierarchy above the remaining junction graph and a bounded national
query-data backend remain unfinished.

The [historical scaling evaluation](log/0019-routing-scale-and-oregon.md) records
inputs, targets, measurements, comparisons and limitations. The API contract and
car profile remain in [routing](routing.md). The browser is an example client and
was not changed for this work.

## Snapshot representation

New builds retain graph semantics **4**, profile `driving-time-v4` and cost model
`estimated-driving-v1`. Storage layout **`routing-chunks-v1`** and preprocessing
semantics **`forced-chain-v1`** are versioned independently in the manifest and
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

Runtime coordinates and adjacency use contiguous arrays with a source-ID-to-dense
index map and compressed sparse row offsets. Segment/source identities remain
unchanged. Directed edges keep distance and elapsed cost; segment geometry,
one-way/access classifications, destination zones and the restriction automaton
remain query data. The loader transfers ownership of decoded segments to avoid
an extra copy, and constructs adjacency without per-node temporary slices.
Provenance, cost notes, validation maps, decoding buffers and the original node
record array are transient. Source strings and some maps still consume meaningful
memory; this is not a fully memory-mappable national layout.

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
This is useful geometry-node elimination, not full contraction hierarchies.

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
ALT and full turn-aware CH/CCH were considered; neither is implemented here.
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
and rolls back. Neither retained database nor the active deployment is changed.
