# Prebuilt Valhalla tiles as a Go routing graph

Date: 2026-09-09. Scope: bounded, offline feasibility investigation on Bremen.
Repository baseline inspected: `a87aa40cb8292514d5e6fc83dd272817dfac75bf`.
This is a historical investigation, not an adopted architecture or release contract.

## Finding and recommendation

**A tile-based Go router is feasible. Compatibility with a real downloadable
provider archive is demonstrated. Replacing the current routing contract from
tiles alone is not. National performance remains untested.**

The prototype downloads no data at runtime, calls no Valhalla routing engine or
service, and uses no C/C++ binding. Our Go code owns snapping, ordinary Dijkstra,
costs, restrictions and geometry reconstruction. It reads records directly from
an immutable tar through a bounded tile cache. It never expands the roads into
the existing complete in-memory graph. The production engine, HTTP API,
SQLite snapshots and active deployment are untouched.

Proceed with a **separate experimental Go backend**, conditional on resolving
the profile/provenance questions below before integration. Do not start a
production migration on this evidence. If preserving `driving-time-v4` and
`estimated-driving-v1` exactly is mandatory, do **not** adopt the downloaded
tiles as the sole source: their lossy normalization does not retain enough
information. Supplemental source data or a reviewed new profile is required;
neither is implemented or presumed approved here.

The earlier [historical national preflight](0026-national-routing-preflight.md)
still describes our existing construction pipeline accurately. This experiment
investigates bypassing that pipeline; it does not supersede its national
capacity result with a new national measurement.

## Source access, freshness and pinning

Sources checked on the investigation date:

| Source | Coverage and cadence | Access and format evidence |
| --- | --- | --- |
| [Interline Tilepacks](https://www.interline.io/valhalla/tilepacks/) | Advertises a daily global build and regional extracts. | Subscription required. Advertises v3 tiles for Valhalla 3.0.0+. No anonymous small sample was available through the documented acquisition path. No purchase, subscription, credential use or tile download attempted. Specific generator revision and current build configuration remain unverified. |
| [Librescoot tiles](https://github.com/librescoot/valhalla-tiles) | Monthly workflow; German states, Benelux, Île-de-France and northwest Italy. Regions built independently. | Public release assets, `.tar` and `.tar.zst`; generator 3.6.3. Downloaded only Bremen's 12,902,400-byte tar. Seven `.gph` files report 3.6.3. This is the actual prototype input. |
| [Historical community planet archive announcement](https://github.com/valhalla/valhalla/discussions/3879) | Announces a December 2022 planet archive; no dependable update cadence established. | Not acquired or probed as a sample. Historical availability is not a maintained supply plan. |

Interline's [PlanetUtils acquisition documentation](https://github.com/interline-io/planetutils)
requires a subscription/API token for tilepacks. Its advertised v3 compatibility
is not evidence that every optional field matches our pinned 3.6.3 reader.
Interline provider compatibility and its build-policy suitability remain blocked
on access to a small, metadata-complete sample. The public Librescoot sample
removes the general sample-access blocker; it does not prove Interline compatibility.

[The source lock](../../imports/valhalla-bremen.lock.json) records the tar and
individual tile SHA-256 values, byte counts, release asset ID, observation of the
provider source revision and upstream code pin. Archive SHA-256:

```text
92e3c58cb1f69b9392b96dc98477b295b491b77617b25f6b5ae5b70c1d3a0b91
```

GitHub asset **539136227** was updated **2026-09-01T06:55:30Z**. The mutable
`latest` release's creation date is April 24; that is not the asset's data date.
Level-2 headers contain creation-day 4626, corresponding to September 1, 2026
using the upstream January 1, 2014 pivot. Upper-level creation-day fields are zero.
All headers share dataset ID `14136987431` and source-checksum field
`13679109215180415287`. These are not an OSM replication timestamp or a full
PBF SHA-256. The archive supplies neither of those authoritative source pins.
The provider workflow uses mutable Geofabrik and community-speed inputs, so
archive bytes are reproducibly readable, but independently rebuilding identical
provider bytes from the available metadata is not established.

The inspected [provider workflow](https://github.com/librescoot/valhalla-tiles/blob/b82a4222688a405e36ebf125de35ebdf69e4e967/.github/workflows/action.yml)
pins Valhalla 3.6.3, removes bicycle/pedestrian-only content, assigns community
default speeds, builds country/admin and single-zone timezone overlays, and
modifies surface classification. This observed configuration is not a signed
build attestation tying that revision to the asset. The data retains OpenStreetMap
attribution under ODbL; see [the attribution reference](https://www.openstreetmap.org/copyright).

Separately built state/region archives must not be combined by copying colliding
tile filenames. Graph IDs and restriction references are local to a coherent
build. A national supplier should supply extracts from **one** consistent build
with all levels and sufficient boundary/detour coverage, plus manifests and
retained release assets. A current URL plus a matching checksum proves byte
identity, not that the provider's topology implements our policy.

## Upstream format and the implemented reader

Inspected Valhalla tag **3.6.3**, commit
`e2f017b16080f49203de245a211b09efab09cf72`. No upstream routing binary was built
or run. The reader uses explicit little-endian loads rather than Go struct casts.
It accepts only this version/layout; unknown versions, oversized inputs, corrupt
extents and missing referenced tiles fail. An engine version string is not a
complete schema description, so new releases need a layout and semantic audit.

Primary format/code references:

| Item | Layout / interpretation and prototype status |
| --- | --- |
| [GraphId](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/graphid.h) | Low 3 bits level, next 22 tile index, next 21 record index. Same numeric namespace represents nodes or edges according to context. IDs are snapshot-local, not persistent source/public identities. Implemented. |
| [Tile header](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/graphtileheader.h) and [section initialization](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/baldr/graphtile.cc) | 272-byte header. Counts describe fixed-width sections; forward/reverse complex restrictions, edge info, text and optional sections use absolute offsets. Header byte offsets include version 16, dataset 32, node/edge counts 40, transitions 48, access/admin counts 72, source checksum 88, complex-forward/reverse 96/100, edge info/text 104/108, bins 116–215, tile size 224. Do not confuse count bitfields at 64 and 72. Implemented road sections; transit explicitly rejected. |
| [Nodes](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/nodeinfo.h) | 32 bytes: tile-relative coordinates, access, contiguous departure range, transition range; also admin/timezone, barrier/node type, traffic control and local headings. Coordinates have split microdegree and seventh-decimal offsets. Core coordinates/access/ranges implemented; full node-type costing is not. |
| [Directed edges](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/directededge.h) | 48 bytes, optionally followed by a separate 8-byte-per-edge extension section. Includes end node, opposing index, shape direction, simple turn mask, access/complex flags, destination-only, speed, class/use/surface, directional access, grade/control attributes, integer metre length, local indices and shortcut masks. Core traversal/road attributes implemented; ancillary fields identified rather than all decoded. |
| [Geometry and names](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/edgeinfo.h) and [decoder](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/midgard/encoded.h) | Relative edge-info offset reaches a 12-byte fixed header, 4-byte name entries, encoded shape and optional extra way-ID bytes/elevation. Shapes use 7-bit varints plus zigzag latitude/longitude deltas at **1e-6 degree precision** by default: “7” means varint packing, not seven coordinate decimals. Implemented OSM way ID, ordinary names, posted-speed byte, full shape and reversal. Tagged payload types counted; their payloads are not interpreted. |
| [Access restrictions](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/accessrestriction.h) and [constants](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/graphconstants.h) | Sorted 16-byte records: edge index 22 bits, type 6, modes 12, destination exemption, 64-bit value. Auto access bit is 1. Dimensions/weight encode hundredths of metres/metric tonnes; conditional records encode a time domain. Reader and narrow conservative filtering implemented; calendar/exemption evaluation is not. |
| [Simple turn evaluation](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/sif/autocost.cc) | Incoming edge's 8-bit mask indexes the departing edge's **local-level index**, even after hierarchy transitions. It does not index the current tile's edge array. Opposing local index identifies immediate reversal. Both enforced in Go. |
| [Complex restrictions](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/complexrestriction.h) and [builder](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/mjolnir/restrictionbuilder.cc) | 24-byte fixed record followed by up to 31 via GraphIds; from/to IDs, type/mode and packed date/time fields. Forward records are stored with the destination tile; their via list is in **reverse traversal order**. Builder expands `only_*` alternatives into forbidden sequences, including intermediate departures. The final sequence is forbidden even when its retained type says “only.” Go reverses vias and builds a capped prefix/failure automaton. Reverse records are extent-checked but unused by forward Dijkstra. |
| [Spatial bins](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/baldr/graphtile.cc) | Level-2 tiles carry a 5×5 grid with cumulative offsets into GraphId lists. IDs may refer to roads on higher levels or other tiles; a bin is a candidate filter, not a nearest-node index. Go queries intersecting bins, deduplicates IDs, projects onto full shapes and considers both permitted directions. No new spatial index is built. |
| [Transitions](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/valhalla/baldr/nodetransition.h) and [hierarchy construction](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/mjolnir/hierarchybuilder.cc) | 8-byte transition contains a corresponding node GraphId and up/down flag. Road levels use 4°, 1°, 0.25° tiles. Ordinary roads are distributed across levels; level 2 alone is not a complete base-road graph. Go follows equivalent nodes at all levels without charging distance/time or resetting incoming edge/history. |
| [Shortcuts](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/mjolnir/shortcutbuilder.cc) and [recovery](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/baldr/graphreader.cc) | Shortcut, superseded and local opposing masks describe aggregated chains on upper levels. Underlying non-shortcut edges remain available. Go skips shortcuts and retains superseded ordinary edges. Unpacking/acceleration is assessed below, not implemented. |

Implementation: [reader](../../internal/routing/valhallatiles/reader.go),
[search and snapping](../../internal/routing/valhallatiles/route.go),
[audit](../../internal/routing/valhallatiles/audit.go),
[offline command](../../cmd/routing-valhalla/main.go).

The tar's `index.bin` is not required: the reader scans tar headers into a small
GraphId-to-file-offset map and uses `ReadAt`. It hashes the full archive once
before use and checks dataset/checksum header agreement. This is an experimental
provider trust boundary, not the existing canonical SQLite publication authority.
Keep the archive immutable throughout the reader's lifetime.

Explicit prototype caps are **64 MiB archive, 128 tiles, 16 MiB per tile,
64 MiB maximum configured cache, 4,096 car complex records and 65,536 automaton
prefixes**. A tile larger than the configured cache is rejected. Candidate IDs
are capped at 100,000; request labels, including obsolete improvements, at
1–200,000 (CLI default 100,000). Limits fail explicitly, rather than omitting
topology or restrictions. The reader is single-owner, not concurrent. These
caps bound this investigation, not a future national interface.

The only all-dataset preprocessing is tar indexing, integrity/header checks and
the small complex-restriction automaton. Node/edge arrays, shapes and bins remain
in provider tiles. Methods copy records; cached byte slices are not exposed.
The LRU bounds retained tile payload, not all live heap, Go reservations, transient
eviction allocations, kernel file cache or process RSS. Audit uses additional
per-tile scratch to count unique edge-info tags.

## Prototype semantics and contract assessment

The output explicitly names `valhalla-3.6.3-go-feasibility-v1` and describes its
limitations. It is **not** `driving-time-v4` or Valhalla's full auto costing.
Its cost is provider integer edge length × traversed geometry fraction ×
3.6 / provider speed in km/h. It has no turn penalty or traffic component.
Full geometry fractions use spherical segment lengths and local projection.
This experiment keeps source-prepared road lengths, not the existing router's
adjacent-OSM-vertex distance sums.

Eligible edges require auto access, a positive length and speed in 1–140 km/h,
supported ordinary road uses, and no destination-only flag. Tracks, emergency
roads, ferries and unsupported uses remain excluded. Snapping excludes
motorway/trunk, bridge and tunnel edges and selects the nearest eligible retained
road within 100 m, independently of route success. Origins/destinations may be
inside an edge; reversal and turn history still apply at subsequent junctions.
Exact endpoint nodes supply a fresh initial heading. Immediate U-turns are
excluded, including at dead ends. Missing tiles fail as incomplete data, rather
than becoming “unreachable.”

Node auto-access is checked at traversed junctions. Encoded dimensional limits
use our fixed 1.9/2.0/5.0 m, 1.8/1.1 t assumptions; this experimental conservative
check also applies encoded truck-mode dimensional records. Auto-applicable
conditional/destination/unknown access records are closed. The prototype cannot
infer restrictions which the provider did not encode. Encoded complex timed
turns, if present, are treated as always active; probable/unknown turn types
reject loading. Calendar behavior is not implemented.

| Current Open Maps obligation | Can these tiles alone reproduce it? |
| --- | --- |
| Road connectivity, direction, full stored geometry and ordinary turn restrictions | Substantially available and demonstrated. Inherit segmentation, filtering, road reclassification and quantization. Exact original OSM vertex geometry/ordinals are not available. |
| HTTP request/response fields, error envelope, admission and lifecycle | These remain ours to implement/adapt; no HTTP integration attempted. Existing API code is unchanged. Profile/source metadata must truthfully identify a future backend. |
| Fixed car limits and strict raw-tag parsing | Numeric normalized restrictions exist; original units, malformed values, specificity and unsupported keys can be lost. Cannot recreate current conservative closure rules from absent tags. |
| Conditional handling | Some access, turn and speed conditions survive in different packed formats; unsupported expressions can be discarded upstream. The prototype closes encoded auto access conditions and enforces encoded timed turns at all times. It identifies but **does not evaluate conditional speed payloads**. These are explicitly outside its declared cost model. |
| Destination-only zones, qualified endpoints, private/customer/permit exclusion | A destination flag and exemption records do not preserve every raw access distinction or our source-evidence policy. Prototype closes all destination-only edges. It has no destination-zone prefix/suffix state. A follow-up needs semantic evidence, not just a flag-to-flag translation. |
| Closest excluded-road guards and safe service-to-street selection | Missing. Removed ways/approaches cannot act as guards. Prototype nearest-eligible snapping can choose a road the current guarded policy would reject. No current-contract snapping claim. |
| Address routing, buildings/parking rings, shared entrance nodes, driveway association | Not supplied by this archive. Existing places/geocoding can remain in Go/SQLite, but their coordinates alone do not replace source access evidence. |
| `estimated-driving-v1` speed assumptions | Not reproducible exactly. Provider class/surface buckets and assigned speeds lose distinctions, original tags and assumption notes. Repricing retained edges in Go is possible under a separately defined model; arbitrary cost changes do not validate hierarchy pruning. |
| Source provenance and stable references | OSM way IDs and selected names remain. No OSM node-ID tagged records were found; upstream node retention is optional and disabled by default. Raw ways/relations, object versions, source-node ordinals and original restriction relation IDs are absent. GraphIds must be snapshot-qualified; they cannot replace stable `om_` identities. |
| Nationwide coverage, border detours and geographic edge cases | Unproved. Bremen includes disconnected coverage; no international or national workload run. Dateline and latitude beyond ±80° snapping are explicitly unsupported in the prototype. |

Concrete inherited normalization differences matter. Upstream
[graph filtering](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/mjolnir/graphfilter.cc)
removes excluded modes and can aggregate nodes. Its
[Lua interpretation](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/lua/graph.lua)
does not implement our access precedence/unknown-value policy verbatim. Its
[enhancer](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/mjolnir/graphenhancer.cc)
converts US/MM/LR weight restrictions from short tons to metric, whereas our
current profile treats unitless mass as metric even in US data. The restriction
builder can warn and omit an overlong via chain; our importer instead fails
enumeration overflow. These choices occur before Go reads the tile and cannot
be undone by changing a search algorithm.

## Demonstrated behavior on the provider archive

The complete small-sample audit passed:

| Quantity | Observed |
| --- | ---: |
| Tiles / tile payload bytes | 7 / 12,887,360 |
| Nodes / directed edges including shortcuts | 70,224 / 151,586 |
| Shortcuts skipped | 3,270 |
| Hierarchy transitions | 12,660 |
| Directed edges ending in a different tile | 212 |
| Non-shortcut edges with simple restriction masks | 1,026 |
| Auto-relevant forward complex records / timed records | 140 / 0 |
| Non-shortcut auto-access edges / experimental eligible edges | 133,431 / 77,977 |
| Non-shortcut destination-only edges | 49,946 |
| Maximum node-to-shape-end discrepancy | 0.064886 m |
| Unique per-tile non-shortcut edge-info tagged values | 1,124 layer; 576 conditional-speed; 111 levels; **0 OSM-node-ID** |

Audit resolves every enumerated edge endpoint/opposing endpoint and transition,
checks reciprocal transitions, and compares every ordinary edge's shape endpoints
with its topological nodes. It does not check raw OSM fidelity or reconstruct
shortcut chains. Restrictions were checked by following output edge subsequences
independently of the search automaton. Geometry tolerance accounts for the
different node/shape precision; it is not a surveyed positional-error estimate.

The coordinate boundary case is Ahrensstraße **[8.745062, 53.083827]** to
Stuhrer Straße **[8.765082, 53.085226]**. Seeds came from named source-tile nodes
on opposite sides of longitude 8.75°, not from optimizing benchmark latency.
The route is **2,012.217 m / 296.077 s**, 47 directed steps and 96 shape positions.
It visits local tiles `2/824434`, `2/824435` and arterial tile `1/51668`.
Snaps are about 0.0644 m and 0.0265 m from the requested coordinates, giving tiny
partial edges rather than pretending node and shape quantization agree exactly.
Search settles 537 states and creates 622 labels; it rejects 16 simple and
2 complex-restricted expansions. Both cache configurations return identical
steps and cost.

Frozen restriction cases use **mid-edge snaps** to isolate maneuvers from
geographic endpoint ranking; they are not address-policy tests:

| Provider record(s) | Go result with restrictions | Disposable comparator with restrictions removed |
| --- | --- | --- |
| Simple mask: `0/3197/206` → `2/824434/9141`, Beim Industriehafen → Windhukstraße | 262 m, 32.506 s | 49 m, 6.189 s, traverses exactly the forbidden cross-level turn |
| Complex no-U-turn: `0/3197/306`, `1/51668/1188`, `0/3197/273`, Beim Industriehafen | 6,351 m, 315.015 s | 24 m, 2.891 s, traverses the prohibited three-edge chain |

Only the in-memory test comparator is altered. On-disk archive bytes stay pinned
and unchanged. The legal route walks independently check connectivity through
transitions, permitted directions/access, simple turns, forbidden full sequences,
partial geometry endpoints and accumulated cost. The simple case clears its
incoming mask; the complex comparator disables the complex automaton and asserts
that the frozen prohibited sequence appears. Neither comparator is a public mode.
These checks prove the sampled tile semantics, not surveyed road legality.

The [offline synthetic tests](../../internal/routing/valhallatiles/reader_test.go)
separately exercise a hand-encoded two-tile graph, alternative arrival histories,
simple/complex detours, one-way partial travel, zero routes, numeric access-limit
equality/rejection, conditional access closure, missing/corrupt input, malformed
geometry, label limits, cancellation and eviction. They require no download and
are explicitly **not** substituted for provider-format compatibility evidence.
No timed car-turn record was present in this provider sample; calendar semantics
are not validated by this result.

## Resource measurements

Host: Apple M5, `Mac17,3`, 16 GiB RAM, macOS arm64; Go 1.26.1. Built the standalone
command before timing. Ran one process at a time, 30 repetitions of the frozen
boundary route, after code checks completed. The desktop and filesystem cache
were uncontrolled; no cache purge, memory pressure or host setting changes.
Integrity hashing/preprocessing touches all tiles before the first query, so
**“first” is not cold disk**. Timed requests include both snaps, search and geometry;
the command JSON-encodes only the last result after timing. These are not HTTP,
concurrent, long-distance, representative-distribution or national benchmarks.

| Measurement | 16 MiB cache | 8 MiB cache |
| --- | ---: | ---: |
| Startup hash/index/complex preprocessing, ms | 10.027 | 9.017 |
| First route, ms | 6.479 | 80.872 |
| Route p50 / p95 / max, ms | 4.676 / 6.479 / 6.653 | 77.582 / 85.416 / 86.752 |
| Peak retained tile payload, MiB | 12.290 | 7.808 |
| Tile loads / evictions, including startup | 7 / 0 | 11,870 / 11,868 |
| Query allocation, MiB per route (30-run mean) | 1.913 | 1392.490 |
| Post-GC heap, MiB | 12.517 | 7.823 |
| Go runtime reserved Sys, MiB | 37.596 | 37.752 |
| Peak process RSS / footprint, MiB | 32.453 / 30.422 | 31.109 / 27.969 |

The 8 MiB cache cannot hold both large local tiles simultaneously. Interleaved
bin/edge lookups repeatedly evict them: over 40 GiB cumulative allocation across
30 short routes despite a small retained heap. This is allocation traffic, not
40 GiB live memory or evidence of equivalent physical disk reads. Warm OS cache
hides physical I/O; the byte-copy/GC cost remains. A production design should
measure bounded page/record caching, reusable buffers and local lookup batching
before increasing a cache budget by guesswork. The 16 MiB result fits the whole
sample and must not be extrapolated to a country.

Tar disk use is **12,902,400 bytes (12.305 MiB)**; `.gph` payload is **12.291 MiB**.
No extracted or converted graph copy is needed. A roughly 215 MiB local source
checkout was used for inspection and is not a serving dependency. The command
binary and diagnostic JSON/text are separate experiment outputs. `/usr/bin/time -l`
RSS/footprint are process lifetime peaks; Go post-GC heap and LRU payload are
different measures. None constitutes a hard physical-memory guarantee.

## What construction disappears, and what remains

Buying/acquiring suitable prepared data transfers these expensive construction
steps to its producer: OSM PBF parsing, node-coordinate joins, junction/edge
assembly, access/tag normalization, barrier and turn compilation, road hierarchy
and shortcut construction, bin generation, speed assignment, admin/timezone and
optional elevation association. Our current source-graph SQLite import, global
Go graph arrays, spatial tree build, recursive-cell hierarchy and prepared-format
publication need not run for a tile-native backend. No conversion back to the
current storage format is necessary.

Remaining work includes coherent acquisition, archive decompression if compressed,
hashing and version/configuration validation, a disk-backed archive/directory
index when the current small map cap is exceeded, bounded cache management,
restriction-state indexing, query scratch and output encoding. The prototype's
whole-region complex automaton must become bounded/on-demand or a separately
validated index at national scale. It is a real remaining preprocessing step,
though much smaller here than constructing road topology.

Preserving current access/snap/address semantics also requires a concrete source
supplement plan; lost geometry/tags cannot be inferred. Release-qualified IDs,
attribution, cancellation throughout snapping/loading, concurrency/admission,
snapshot leases, trusted publication, rollback and retirement still need to be
integrated and tested in our service. Hashing supplied bytes does not provide the
existing publisher's proof that topology follows our authoritative source rules.

## Hierarchy: correctness before acceleration

The working search uses **all ordinary edges across all levels** and zero-cost
node transitions. It is not level-2-only routing. Labels distinguish incoming
edge and restriction automaton state, so arrivals with different relevant
histories are not incorrectly merged. The directed graph is read lazily, but
ordinary Dijkstra still expands an area that can grow very large for long routes.
Bounded graph storage does not itself bound a query's work below our label limit.

Upstream shortcuts contract selected chains, with assumptions about matching
access/class/use/surface, intermediate branches, restrictions and aggregated
attributes. This differs from treating every shortcut as a universally valid
shortest path under any custom cost. Upstream recovery walks/reconstructs the
underlying edges using shortcut/superseded identities; it is not just a universal
two-child CH representation. The reader recognizes those flags but never uses
shortcut length/speed as an exact custom-cost transfer.

A next acceleration experiment should first implement chain recovery in Go,
validate every recovered adjacency and terminal turn state, and recompute additive
costs from base edges for one pinned profile. Keep endpoint/interior and turn/
access boundaries open. Preserve any newly introduced destination/history state;
turn-sensitive costs cannot be safely represented by length and averaged speed
alone. Reprice or discard transfers whenever the supported cost changes.

The upstream [bidirectional search](https://github.com/valhalla/valhalla/blob/e2f017b16080f49203de245a211b09efab09cf72/src/thor/bidirectional_astar.cc)
uses level-specific expansion limits, distance thresholds, transition counts and
additional pruning. These are not a proof of exactness for our arbitrary custom
costs. Do not copy “stop expanding local roads after N transitions” and assume
optimality. Compare any pruning scheme against the ordinary Go reference on
frozen cases, including adversarial cheap local-road detours, restrictions crossing
levels, partial endpoints and disconnected searches. An admissible A* bound or
an independently built cost-valid overlay may be preferable; neither was added.

## Concrete next gates

1. Obtain a small sample from the intended national supplier with generator
   revision, build options, source replication timestamp/checksums, custom Lua,
   speed inputs, update retention and attribution. Establish coherent cross-border
   coverage and complete extract references before acquiring any large dataset.
2. Decide the accepted routing profile. Inventory losses against the table above;
   either design bounded source supplements or explicitly version changed behavior.
   Do not relabel the current prototype as the existing profile.
3. Improve tile/page caching and spatial lookup locality. Validate bounded memory,
   malformed data rejection and cancellation; replace the small global restriction
   automaton/index caps with an explicit scalable representation.
4. Recover and validate shortcuts, then evaluate acceleration against ordinary
   search under the exact supported costs. Merely using the provider hierarchy
   is insufficient to claim fast or optimal country-scale routing.
5. Only after regional correctness/resource gates pass, adapt service snapshot
   ownership and APIs on a separate candidate, retaining all current source tests.
   A later national experiment needs measured artifact size, acquisition/startup,
   per-state/border/disconnected workloads, cold storage, long-route scratch,
   four-request concurrency and replacement/rollback. No national cost/latency
   estimate is inferred from this seven-tile result.

## Reproduction and checks

All commands run from the repository root. Use new output names if the example
paths already exist. The acquisition URL is mutable; **checksum failure is a
blocker, never permission to update the lock**. Retain the verified archive.
The GitHub asset-ID URL in the lock can identify the observed release asset;
its continued retention is not guaranteed. No national download is required.

```sh
mkdir -p data/valhalla-recheck
curl -fL --max-time 90 --max-filesize 20000000 \
  https://github.com/librescoot/valhalla-tiles/releases/download/latest/valhalla_tiles_bremen.tar \
  -o data/valhalla-recheck/bremen.tar
shasum -a 256 data/valhalla-recheck/bremen.tar

# Open verifies the full pinned checksum again before reading graph records.
go build -o data/valhalla-recheck/routing-valhalla ./cmd/routing-valhalla
data/valhalla-recheck/routing-valhalla \
  -tiles data/valhalla-recheck/bremen.tar -audit \
  > data/valhalla-recheck/audit.json

/usr/bin/time -l data/valhalla-recheck/routing-valhalla \
  -tiles data/valhalla-recheck/bremen.tar -cache-mib 16 -repeat 30 \
  -from 8.745062,53.083827 -to 8.765082,53.085226 \
  > data/valhalla-recheck/routes-16.json 2> data/valhalla-recheck/time-16.txt
/usr/bin/time -l data/valhalla-recheck/routing-valhalla \
  -tiles data/valhalla-recheck/bremen.tar -cache-mib 8 -repeat 30 \
  -from 8.745062,53.083827 -to 8.765082,53.085226 \
  > data/valhalla-recheck/routes-8.json 2> data/valhalla-recheck/time-8.txt

go test ./...
go vet ./...
go test -race ./internal/routing/valhallatiles
OPENMAPS_VALHALLA_TAR="$PWD/data/valhalla-recheck/bremen.tar" \
  go test -tags=integration ./internal/routing/valhallatiles -run TestProvider -v
```

For upstream inspection only, outside serving/runtime:

```sh
git clone --depth 1 --branch 3.6.3 https://github.com/valhalla/valhalla.git \
  data/valhalla-recheck/upstream
git -C data/valhalla-recheck/upstream rev-parse HEAD
# Expected: e2f017b16080f49203de245a211b09efab09cf72
```

Optional source-ordered restriction discovery is available with
`OPENMAPS_VALHALLA_DISCOVER=1` and `-run TestProviderRestrictionSeeds`; it reads
the already downloaded archive. Normal tests generate only their tiny synthetic
fixture in temporary directories. The provider suite skips without the explicit
archive environment variable; a skip is not provider verification.

Completed: `gofmt`, `go test ./...`, `go vet ./...`, isolated package race tests,
and the opt-in provider boundary/restriction suite. Existing engine tests passed
(unchanged packages were eligible for Go's test cache). The provider audit passed;
the seed-discovery test is intentionally skipped in the final assertion run.
Code and Markdown were checked with `git diff --check` plus content/link review.

Retained local evidence is under `data/valhalla-feasibility/`: source release and
workflow metadata, `bremen.tar`, `audit-final.json`, `serial-16.json/.time`,
`serial-8.json/.time`, and `check-{test,vet,race,provider}.txt`. These files are
ignored experiment outputs; source pins and assertions are tracked candidates.
Earlier exploratory timing files are not the table's final serialized runs.

Initial inspection found existing national-preflight edits in README/routing
documentation and new national-preflight scripts/log files. Their content was
preserved; by the final inspection those files were present in repository HEAD.
This work creates only the isolated command/package, source lock and this log.
No commit, push, service replacement, active deployment edit or production
migration was performed by this investigation.
