# OSM Scout planet-derived tiles: bounded Go compatibility experiment

Date: 2026-09-09. Baseline: `2d4972f` (the previous investigation).
The initial working tree was clean. This is a historical experiment, not an
adopted production architecture or a national performance qualification.
It follows [0027](0027-prebuilt-valhalla-feasibility.md) and
[0028](0028-newer-world-valhalla-builds.md).

## Recommendation

**Prefer OSM Scout for the next experimental acquisition and Go routing work.**
Real 3.4.0 compatibility, spatial package crossings, hierarchy transitions,
ordinary Go search, road snapping, geometry and representative restrictions now
work on a pinned adjoining sample. Its public, planet-derived distribution is
more useful for coherent boundary experiments than independently built regional
archives. Keep Librescoot as the independent 3.6.3 regression fixture.

This recommendation does **not** approve changing `driving-time-v4`, replacing
the current engine, or migrating a deployment. The tiles alone cannot reproduce
our present source, access, address, snapping or speed contract. Exact source
freshness/build provenance and bounded large-area search remain blockers.
No Valhalla routing engine, server, binding or routing HTTP service was used.
Our Go code owns all search, costing, snapping and reconstruction; the existing
Go HTTP API and active deployment remain untouched.

## Acquisition and pins

The [retained source config](0029-bremen-scout.json) records URLs,
SHA-256 and sizes for the catalog, directory listing, provider digest, all three
packages, all 35 decompressed tiles, and the unselected range probe. It also
records attribution and the observed upstream/provider revisions. Data and raw
measurements are retained under ignored `data/valhalla-scout/`; the previous
`data/valhalla-world-search/` evidence was preserved.

The live [catalog](https://data.modrana.org/osm_scout_server/countries_provided.json)
still has 439 regional Valhalla selections, timestamp `2026-06-20_07:12`, schema
`2`, selecting distribution namespace `valhalla-34`. Its SHA-256 is
`770e6c599cfb42d4f35cbe8b7b5be0295dae45d2d25ebe93ce3ccc9c23eca362`, unchanged
from 0028 and from a second fetch after graph acquisition.

| Package | Selection / actual content | Compressed bytes | Tile bytes |
| --- | --- | ---: | ---: |
| 144 | Bremen selection; level 0 tile `0/3197`, nominal [8,50]–[12,54] | 14,663,220 | 36,976,088 |
| 1985 | Bremen selection; level 1 `1/51668` and 16 local tiles, nominal [8,53]–[9,54] | 28,831,448 | 76,919,256 |
| 1983 | Adjoining southern package; level 1 `1/51308` and 16 local tiles, nominal [8,52]–[9,53] | 39,034,262 | 100,940,968 |

Bounds are longitude/latitude tile extents, not continuous road coverage or
endpoint promises. Edges and hierarchy references can extend outside them.

Package SHA-256 values, in that order:

```text
10998c12a8f0e956f3baebd2aa0ccdbd030879cad66e6f8327eb4ae0a12e4f4b
bb4e2d47f1ab40e2f26660b959319f27ee21a2bf2b49785d3db32cfd0e80388e
1947a7ddf2e83601c10ce8e2ad9892496d97834fecb5f0c465e05c7988ae8a3c
```

Inspected HTTP HEAD sizes before the initial downloads. After a longer route
needed `1/51308`, inspected published size sidecars and 1 MiB HTTP ranges of
1983 and 1984. Incremental bzip2 decoding exposed their first tile paths:
`1/51308` and `1/51309`. Resumed 1983 from its downloaded prefix; did not download
1984 beyond its prefix. Thus **new graph network payload was 83,577,506 bytes**:
82,528,930 bytes of complete packages plus 1,048,576 unselected probe bytes.
No further graph downloads were made. This is below 100,000,000 bytes, including
probes. Metadata and upstream source inspection are separate from graph payload.
No country/planet archive, paid data, account, purchase or infrastructure was used.

### A complete public package manifest exists

The root [digest.md5.bz2](https://data.modrana.org/osm_scout_server/digest.md5.bz2)
is 890,414 bytes compressed / 6,259,510 expanded. Each line supplies MD5,
mtime-like text, and a relative filename. Its graph package IDs exactly match
all **2,930** files in the
[package directory](https://data.modrana.org/osm_scout_server/valhalla-34/valhalla/packages/).
It includes the **34 packages omitted** from the 2,896-package union of regional
selections. Its MD5 entries match all three downloaded packages and the catalog;
our lock separately supplies SHA-256 pins. MD5 is provider transport evidence,
not an authentication scheme or a substitute for our pins.

Omitted IDs (also recorded machine-readably in the lock):

```text
1 2 67 244 247 248 249 250 251 252 253 345 397 398 425 444 523
560 621 623 624 626 853 854 855 1033 1047 1230 1558 1559 1560
1657 1666 2579
```

A future complete acquisition should:

1. Obtain/retain a frozen provider generation, its catalog, full digest and
   directory inventory. Prefer a provider-attested immutable manifest tying the
   packages to an OSM replication timestamp/checksum and generator/configuration.
2. Enumerate **all graph package paths from the full digest**, cross-check them
   against the directory, and reject duplicate, missing or unexpected paths.
   Regional selections are convenience lists, not the planet manifest.
3. Acquire into a new immutable generation, validate provider digests, establish
   SHA-256/size pins, and index every member's tile ID, package, level and hash.
   Validate package metadata, tile versions and dataset agreement. Reject tile
   collisions with conflicting bytes; never merge different builds by filename.
4. Resolve node endpoints, opposing edges, transitions, bins and complete turn
   paths across the **whole** generation, including files outside regional lists.
   Transit/unknown sections require their own reader support or an explicitly
   validated road-only extraction; this prototype rejects transit tiles.
5. Recheck the source manifests after acquisition and retain the verified bytes.
   A change rejects the attempted generation. Unchanged manifests plus matching
   headers/digests are useful consistency evidence, but cannot independently
   prove one coherent OSM input during a mutable publication. An attested frozen
   generation remains the stronger requirement before coverage certification.

No complete acquisition was performed here. Public URLs are mutable, retention
is not guaranteed, and the exact OSM cutoff is still unknown. A separately named
candidate, immutable receipts, leases, replacement and rollback would still be
needed before integration with our service.

## Actual 3.4.0 format and build evidence

Inspected upstream tag 3.4.0, commit
`cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e`, against the previous 3.6.3 source.
All 35 real headers report **3.4.0**, dataset ID **183131145** and zero in the
reserved byte-88 word. Internal timestamps agree across packages.

Local tile creation fields are **4539**, which decodes to **2026-06-06**;
upper-level fields are zero. The upstream
[graph builder](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/src/mjolnir/graphbuilder.cc)
uses its wall date in `America/New_York`, measured from January 1, 2014.
This is a generator date, **not an OSM replication cutoff**. It also shows why
the June 20 packaging timestamp cannot be treated as the graph's build date.
Dataset ID is not a date or a complete source checksum.

| Record | Verified 3.4.0 interpretation / difference from 3.6.3 |
| --- | --- |
| [Header](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/valhalla/baldr/graphtileheader.h) | Same 272-byte layout and used section offsets. `base_ll()` derives the origin from GraphId; the Go 3.4.0 branch does likewise. Byte 88 is reserved, not the later source checksum. |
| [Nodes](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/valhalla/baldr/nodeinfo.h), [edges](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/valhalla/baldr/directededge.h) | Used 32/48-byte topology, coordinate, access, speed, local-turn-index and shortcut fields have matching offsets. Later node elevation/time-zone extensions and HGV destination flag must not be inferred from old spare bits. |
| [Edge info](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/valhalla/baldr/edgeinfo.h) | Same core 12-byte header, OSM way-ID extension and encoded shape. Shapes use zigzag/7-bit varints at 1e-6 degree precision; nodes retain split seventh decimals. Later encoded elevation, conditional-speed and OSM-node-ID features are absent from this audited layout. |
| [Access records](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/valhalla/baldr/accessrestriction.h) | Same 16-byte edge/type/mode/value fields. Bit 40 was spare: the reader exposes destination exemption only for 3.6.3. |
| [Complex turns](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/valhalla/baldr/complexrestriction.h) | Same forward from/to record plus reverse-ordered via list; packed type, modes and timed flag. The builder stores prohibited alternatives even when the retained type is `only_*`. |
| Transitions/bins | Same road levels 0/1/2 and 4°/1°/0.25° grids. Equivalent nodes connect levels at zero cost. Local 5×5 bins reference edges on other levels and in other tiles. |

The provider's latest import-directory commit before the package timestamp was
`4ec7940c448777a125354bc8323e27adba1d1d4e`. Its
[environment template](https://github.com/rinigus/osmscout-server/blob/4ec7940c448777a125354bc8323e27adba1d1d4e/scripts/import/.env.template)
pins `VALHALLA_VERSION=3.4.0` and
`ghcr.io/gis-ops/docker-valhalla/valhalla:${VALHALLA_VERSION}`. Its
[compose file](https://github.com/rinigus/osmscout-server/blob/4ec7940c448777a125354bc8323e27adba1d1d4e/scripts/import/docker-compose.yaml)
requests admin/time-zone/transit building, disables serving/tar creation, and
leaves explicit elevation building commented out. This is observed source,
**not an attestation** of the June production image digest, options, Lua, speed
inputs or PBF. The repository's client `data/valhalla.json-3.4.0` is not proof of
the production builder's configuration.

The June
[packaging code](https://github.com/rinigus/osmscout-server/blob/4ec7940c448777a125354bc8323e27adba1d1d4e/scripts/import/valhalla/make_packs.py)
uses one planet tile tree, packages upper-level tiles separately from lower-level
ones, targets roughly 30 MiB of gzip members where subdivision is available,
and writes the current packaging time. Leaf packages can exceed that target,
as 1983 does. The newer source inspected in 0028 has reorganized this code;
neither observed revision alone certifies the production build.

## Implemented access path and bounds

Changes stay in `internal/routing/valhallatiles`, its offline command, pins and
tests. `Open` still requires the pinned 3.6.3 tar. `OpenScout` accepts only the
explicit 3.4.0 Scout lock. Merely recognizing a version string does not approve
arbitrary future Valhalla layouts.

`OpenScout` hashes each input before reading it, then streams bzip2 → tar →
single gzip member into a private seekable spool. It checks exact expanded sizes,
SHA-256, gzip and bzip2 CRCs, package list/timestamp, member types, canonical tile
paths, tile/header identity, dataset agreement and section extents. Duplicate,
unlisted, truncated, oversized, mixed or unknown input fails; archive paths are
never extracted as filesystem paths. Copying/decompression observes cancellation.
A failed open removes its spool, and `Close` removes successful spools. A killed
process can leave an orphan `openmaps-scout-*.tiles` file; it is never reused as
trusted input. Keep input packages immutable while opening/using the reader.

The spool contains provider tile bytes, **not** our complete in-memory graph or
a converted Open Maps topology. Fixed record/shape reads go through 64 KiB LRU
pages. Tile metadata and offsets stay in a capped map; no node/edge arrays or new
spatial index are constructed. The existing whole-tile backend remains available
for the matched Librescoot comparison (`-page-cache` switches that tar to pages).

| Explicit experiment limit | Value |
| --- | --- |
| Complete compressed package inputs | Strictly below 100,000,000 bytes; at most 8 packages |
| Expanded payload / individual tile | 256 MiB total / 64 MiB each |
| Index / tar metadata | 128 road tiles, 512 members per package, 64 KiB per metadata member |
| Outer tar expansion | 128 MiB per package |
| Page payload cache | 64 KiB–64 MiB in library; CLI 1–64 MiB |
| Complex turns / automaton prefixes | 4,096 / 65,536 |
| Snap candidates / names | 100,000 candidate IDs / 64 KiB per name |
| Query labels | 1–200,000, including obsolete improved labels |

The page limit bounds retained **payload**, not process RSS. Maps, page list
entries, copied records, up to one bounded shape/name read, restriction state,
query labels/queue/geometry, transient evicted buffers, Go reservations and OS
file cache are separate. No buffer is reused while a decoder may still hold a
view. Readers remain single-owner; sharing one concurrently is unsupported.
Concurrency/admission and national disk-backed indexes remain future work.

Complex-turn preprocessing retains all records from the selected tiles, including
paths containing absent edge IDs; it validates present references and does not
silently delete incomplete restrictions. Forward records live with the final edge,
so a completed path inside loaded data still has its prohibition. Traversal,
snapping or hierarchy access requiring an absent tile returns typed
`MissingTileError`, never a fabricated connection or `unreachable`. A failed
bounded search makes no optimality claim. `Audit()` remains strict;
`AuditSample(true)` explicitly inventories external references and validates
available records without claiming global completeness.

## Verified routes and sample audit

The final 35-tile audit found 1,087,421 nodes, 2,549,065 directed edges including
69,915 shortcuts, and 247,949 hierarchy transitions. There are 9,063 cross-tile
edge endpoints, **532 resolved cross-package edges** and **19,892 resolved
cross-package transitions**. **260 distinct external tile dependencies remain**;
these are expected for the broad level-0 tile and are not claimed complete.
Counts are records across levels, not deduplicated OSM entities.

Available endpoints/opposing edges and reciprocal transitions were checked,
including coordinate agreement. Every ordinary stored shape was decoded and
compared with available topological endpoints. Maximum discrepancy was
**0.571435 m**, at `0/3197/76995`, below the audit's explicit 1 m tolerance.
That exceeds simple sixth/seventh-decimal rounding alone; its preprocessing
cause was not established. It is not proof of original OSM vertex fidelity.
Returned route joins separately require at most 0.5 m discontinuity.

| Frozen case | Verified result |
| --- | --- |
| Ahrensstraße [8.745062,53.083827] → Stuhrer Straße [8.765082,53.085226] | 2,013.217 m / 173.644 s; 52 steps, crosses local tiles `2/824434`, `2/824435` and level 1. Snaps have 0.0644/0.0265 m gaps. |
| Adelheider Straße, [8.61671082563697,52.999802063019516] ↔ [8.615838,53.000541999999996] | 101 m / 5.194 s each direction; coordinate snapping and geometry across latitude 53° and spatial packages **1983/1985**. Forward path uses edge records in both packages. Reverse edge storage can remain in one package while opposing/node references require the other. |
| Ahrensstraße → Beim Industriehafen [8.7154925,53.135412] | **13,956.590 m / 896.213 s**, 178 steps, 452 positions, all three road levels and packages 144/1985; 46,752 settled states, 47,551 labels, queue peak 867. Endpoint separation is much shorter than road travel. |
| Farther attempt to [8.625,53.18] | Original two-package set fails on `1/51308`; adding 1983 resolves that dependency but search then fails on eastern local tile `2/824436`. No route or national implication is claimed. |

Source-selected restriction midpoints isolate maneuvers from endpoint ranking:

| Prohibited record/path | Enforced result | Disposable comparator with restriction removed |
| --- | --- | --- |
| Simple: `0/3197/141765` → `2/825873/5`, B 212 approach | 631.5 m / 41.176 s, 12 steps; crosses packages | 234.5 m / 21.402 s, traverses forbidden turn |
| Complex: `0/3197/145125`, `1/51668/11327`, `0/3197/143593`, Beim Industriehafen | 2,378 m / 171.216 s, 55 steps; rejects full cross-package sequence | 24 m / 1.728 s, traverses the prohibited three-edge maneuver |

Comparators change only a disposable local spool or test automaton, never input
packages. An independent output walk checks adjacency through transitions,
permitted edges, immediate reversals, simple masks, complete forbidden sequences,
cost sums and snapped geometry endpoints. Measured route steps, cost and geometry
are identical across tested cache limits. These tests establish encoded graph
semantics, not surveyed legality or exact equivalence to the current router.

## Contract information audit

The output identifies `osm-scout-3.4.0-go-feasibility-v1`. It keeps the narrower
public-auto experimental policy from 0027: provider integer metres and km/h,
no turn penalties/traffic, no destination-only traversal, supported road uses,
no motorway/trunk/bridge/tunnel snapping, nearest eligible shape within 100 m.
The 3.6.3 output retains its existing profile identifier.

| Information | Verified retention and behavior | Unsupported or unknown |
| --- | --- | --- |
| Access/directions | Forward/reverse auto masks, node access, destination flag, numeric access records decoded. 1,798,865 ordinary auto-access edges; 1,220,356 eligible under this experiment; 396,957 destination-only edges. Fixed car dimensions are checked against encoded limits. | Raw tag specificity, unknown/malformed values, customer/private distinctions and our exact conservative parsing cannot be recovered. No destination-zone or endpoint qualification model. |
| Conditions/turns | 15,343 simple masks; 687 auto complex records, **4 timed**. Tests confirm timed sequences are banned at all times. Access types 6/7 occur 2,785/1,957 times and auto-applicable encoded rules close travel. | No calendar, conditional one-way or complete raw expression interpretation. Upstream can omit unsupported conditions/overlong via chains. Completeness against original OSM relations is unknown; relation IDs are absent. |
| Speeds | Provider directional speed byte, posted limit byte, road class/use/surface available. Go computes additive elapsed cost. | 3.4.0 lacks the later conditional-speed payload feature. No raw directional/advisory/conditional values, assumption notes or attested custom speed inputs. `estimated-driving-v1` cannot be reconstructed exactly. |
| Barriers | Node types/access/private/tagged flags decoded and inventoried: 16,932 gate, 8,471 bollard, 21 sump-buster records, 9,266 auto-blocked nodes overall. Search checks encoded node auto access. | Node type alone is not our gate/lock/unknown-barrier policy. Prototype does not reproduce private-node, physical-bollard or incident-segment guard rules; access normalization may admit a gate/bollard our current policy closes. Raw locks/access exceptions are unavailable. |
| Classification | All eight road-class values, 24 use values and seven surface values observed. Shortcut flags, ordinary superseded edges, bridge/tunnel and roundabout fields identified. | Reclassified links, inferred turn channels/internal intersections and merged buckets do not retain original OSM tags. Provider production options remain unattested. |
| Geometry | Full **stored** shape, orientation and partial clipping verified; selected names and way IDs decoded. Tagged records: 24,435 layer, 2,486 level, 102 tunnel-name entries (unique per-tile edge infos). | Original vertices, segment ordinals, node IDs, raw excluded roads and build-time simplification history are absent. Original precision and identities cannot be recreated. |
| Source identity | Package/tile SHA-256, dataset/version, way IDs and source attribution retained; GraphIds identify records inside this snapshot. | No original node/relation IDs, source object versions or exact PBF/replication checksum. GraphIds must be snapshot-qualified, never substituted for stable public `om_` identities. |
| Snapping/address/API | Go owns projection, candidate selection and errors. Current Go HTTP layer is intact. | Lost excluded-road guards, buildings, parking rings, source-connected driveway/entrance evidence prevent present address/snap policy. No HTTP integration or API compatibility claim for this backend. |

The upstream [Lua rules](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/lua/graph.lua),
[graph enhancer](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/src/mjolnir/graphenhancer.cc)
and [restriction builder](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/src/mjolnir/restrictionbuilder.cc)
show inherited choices that Go cannot undo: permissive normalized barrier/access
handling, country overrides, speed/road normalization and warnings that can omit
overlong turn paths. The provider client config and upstream defaults are clues,
not proof that the production builder used every default unchanged.

## Resource measurements and cache thrashing

Apple M5 / Mac17,3, 16 GiB RAM, macOS 26.4.1 arm64, Go 1.26.1. Built the command
before timing; serialized processes under `/usr/bin/time -l`, without race
instrumentation, concurrent benchmark work, cache purges or memory pressure.
Each Scout process reconstructs its temporary spool from pinned compressed inputs.
Startup hashing/decompression/complex preprocessing takes **5.33–5.47 s**.

`-cold-cache` clears the **application** cache after preprocessing. First routes
are application-cache cold; repeated routes reuse it. The OS cache is uncontrolled
and startup has just read/written the data. **Physical cold-disk latency was not
measured.** Routing time includes snaps/search/geometry; JSON encoding of the last
result occurs after timing. These are neither HTTP nor concurrency benchmarks.

| Workload / page cache | Repetitions | Cold ms | Warm p50 / p95 ms | Peak RSS / footprint MiB | Post-GC heap MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| 2.01 km / 1 MiB | 30 | 50.11 | 56.62 / 58.36 | 21.53 / 16.53 | 1.27 |
| 2.01 km / 8 MiB | 30 | 14.50 | 13.59 / 14.02 | 27.00 / 24.70 | 8.26 |
| 2.01 km / 16 MiB | 30 | 12.71 | 11.12 / 11.28 | 41.75 / 39.50 | 16.27 |
| 2.01 km / 32 MiB | 30 | 12.20 | 8.57 / 8.78 | 63.58 / 61.36 | 27.41 |
| 13.96 km / 8 MiB | 10 | 287.79 | 284.36 / 321.26 | 42.39 / 39.83 | 8.28 |
| 13.96 km / 32 MiB | 10 | 51.84 | 46.13 / 46.98 | 81.33 / 78.08 | 30.12 |
| 13.96 km / 64 MiB | 10 | 51.70 | 46.34 / 46.95 | 81.92 / 79.02 | 30.12 |

Warm p95 uses nearest-rank over repetitions excluding the first. Ten repetitions
provide only nine warm observations, so their p95 is the maximum, not a robust
tail estimate. Lifetime RSS/footprint include startup and encoding; heap/cache
numbers describe different resources and are not hard physical-memory limits.

| Workload / cache | Cumulative allocation MiB | Logical file reads MiB | Evictions | Peak retained page payload MiB |
| --- | ---: | ---: | ---: | ---: |
| Short / 1 | 14,305.1 | 14,210.6 | 227,354 | 1.00 |
| Short / 8 | 1,899.0 | 1,820.3 | 28,997 | 8.00 |
| Short / 16 | 924.3 | 846.9 | 13,294 | 16.00 |
| Short / 32 | 103.9 | 27.1 | 0 | 27.13 |
| Long / 8 | 33,919.2 | 33,413.4 | 534,487 | 8.00 |
| Long / 32 | 494.7 | 29.8 | 0 | 29.81 |
| Long / 64 | 494.7 | 29.8 | 0 | 29.81 |

The short query settles only 554 states; much of its time is snapping (last warm
32 MiB run: 7.94 ms snapping, 0.46 ms search). Bins can reference broad tile
records, shapes and access tables, so geographic proximity is not page locality.
For the longer 8 MiB run, search dominates (last route: 279.83 ms search versus
5.71 ms snapping); 32 MiB reduces search to 42.29 ms. A 64 MiB budget adds no
benefit on this workload because fewer than 30 MiB of pages are touched.

A matched **Librescoot** rerun uses the same original archive, 8 MiB budget,
30 repetitions, endpoints, Go traversal and restrictions:

| Backend | Cold / warm p50 / warm p95 ms | Cumulative allocation MiB | Logical read MiB | Peak RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| Whole tiles | 77.31 / 74.91 / 77.68 | 41,779.95 | 41,688.60 | 34.52 |
| 64 KiB pages | 6.61 / 5.94 / 6.24 | 65.19 | 5.13 | 18.56 |

Both return exactly the original 47-step, 2,012.217 m / 296.077 s route.
Whole tiles reload 11,850 times; pages load 82 times with no eviction. This
reproduces 0027's roughly 40 GiB allocation traffic and identifies a concrete
improvement: cache the requested records' pages instead of multi-MiB whole tiles.
It does **not** eliminate thrashing when the page working set exceeds its budget,
as the Scout long/8 MiB result demonstrates. The next locality work should target
bounded record caches, access-table lookup and snapping batches, with exact
reference comparisons and accounting for all retained/transient buffers.

Logical reads are bytes passed through `ReadAt`, **not physical storage traffic**;
cumulative allocations are not live memory. Retained inputs occupy 82,528,930
bytes, or 83,577,506 including the unselected prefix. The temporary padded spool
is **214,892,544 bytes** (204.94 MiB), containing 214,836,312 tile bytes.
Peak graph disk including the probe and spool is **298,470,050 bytes** (284.64 MiB),
plus filesystem metadata, command binary, metadata/source inspection and reports.
Normal close removes the spool. No additional complete graph copy is retained.

## Hierarchy and next blockers

Ordinary Dijkstra across all road levels remains the correctness baseline.
Zero-cost node transitions retain incoming edge and complex-turn state. All
shortcuts are excluded and all ordinary superseded edges stay available. No
hierarchy distance threshold, transition-count pruning or custom-cost assumption
was introduced to make these cases fast.

Exploiting the provider's shortcuts needs Go chain recovery from shortcut and
superseded identities, adjacency/restriction validation, and repricing of every
ordinary edge under one explicit supported cost. A transfer must preserve entry,
exit and any relevant turn/destination/history state and partial endpoints.
Aggregated length/speed is not generally an exact transfer under changed costs.
The [3.4.0 shortcut builder](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/src/mjolnir/shortcutbuilder.cc)
and [graph reader recovery](https://github.com/valhalla/valhalla/blob/cbabe7cfbad2225090e4c006c778f1f0a7b3ec4e/src/baldr/graphreader.cc)
are concrete format references, not algorithms executed in this prototype.

Upstream level/distance pruning is not a proof of optimality for arbitrary Go
costs. Keep costs nonnegative and prove an admissible lower bound or construct
cost-valid transfers/overlays. Compare every proposed optimization to ordinary
search, including cheap local detours, cross-level restrictions, endpoint partials,
only-turn alternatives and missing/disconnected coverage. Shortcut recovery and
accelerated search remain unimplemented.

Before a broader **regional** evaluation: secure a complete consistent tile set
for the intended search extent; obtain producer/source metadata; decide a reviewed
experimental contract or concrete source supplements; resolve the observed
geometry discrepancy; bound restriction indexing/query scratch/cancellation across
all phases; test independent concurrent readers, malformed-data fuzzing, leases
and admission. Current caps deliberately reject larger datasets; do not remove
them merely to make acquisition succeed.

Before **national** evaluation: replace capped in-memory indexes/global restriction
preprocessing with a scalable validated representation; establish acceleration
correctness; measure long/disconnected/border/dateline/high-latitude workloads,
physical cold storage, sustained concurrency, full HTTP encoding and old/new
snapshot overlap under real resource budgets. The current snapping implementation
still rejects polar/dateline requests. No country performance or footprint is
extrapolated from these measurements. The current engine's separate national
capacity failure in 0026 remains accurately documented.

## Reproduction and verification

Use new output directories. The following downloads only the three pinned sample
packages (82.53 MB). It inspects all sizes before downloading and refuses altered
bytes; never update the lock to bypass a mismatch. Retain verified inputs because
upstream URLs can change. Metadata URLs/checksums are in the same lock.

```sh
python3 - <<'PY'
import hashlib, json, pathlib, urllib.request
lock = json.loads(pathlib.Path('docs/log/0029-bremen-scout.json').read_text())
packages = lock['packages']
assert sum(p['bytes'] for p in packages) < 100_000_000
for p in packages:
    with urllib.request.urlopen(urllib.request.Request(p['url'], method='HEAD'), timeout=60) as r:
        assert int(r.headers['Content-Length']) == p['bytes'], p['id']
root = pathlib.Path('data/valhalla-scout-recheck')
root.mkdir()  # refuses an existing directory
for p in packages:
    target = root / (p['id'] + '.tar.bz2')
    partial = target.with_suffix('.partial')
    digest, total = hashlib.sha256(), 0
    with urllib.request.urlopen(p['url'], timeout=60) as r, partial.open('xb') as out:
        while chunk := r.read(min(1 << 20, p['bytes'] - total + 1)):
            total += len(chunk)
            if total > p['bytes']:
                raise RuntimeError('download exceeds pin')
            digest.update(chunk)
            out.write(chunk)
    assert total == p['bytes'] and digest.hexdigest() == p['sha256'], p['id']
    partial.rename(target)
PY

go build -o data/valhalla-scout-recheck/routing-valhalla ./cmd/routing-valhalla
data/valhalla-scout-recheck/routing-valhalla \
  -scout-packages data/valhalla-scout-recheck \
  -lock docs/log/0029-bremen-scout.json \
  -scratch data/valhalla-scout-recheck -cache-mib 32 -audit \
  > data/valhalla-scout-recheck/audit.json

/usr/bin/time -l data/valhalla-scout-recheck/routing-valhalla \
  -scout-packages data/valhalla-scout-recheck \
  -lock docs/log/0029-bremen-scout.json \
  -scratch data/valhalla-scout-recheck -cache-mib 32 -cold-cache \
  -max-labels 200000 -repeat 30 \
  > data/valhalla-scout-recheck/short-32.json \
  2> data/valhalla-scout-recheck/short-32.time
# Repeat with caches 1, 8, 16 and new filenames for the short matrix.
# Long matrix: caches 8, 32, 64; -repeat 10 -to 8.7154925,53.135412.
# Spatial crossing: -from 8.61671082563697,52.999802063019516 \
#                   -to 8.615838,53.000541999999996

# Matched original sample: run once without and once with -page-cache.
/usr/bin/time -l data/valhalla-scout-recheck/routing-valhalla \
  -tiles data/valhalla-feasibility/bremen.tar \
  -lock imports/valhalla-bremen.lock.json -cache-mib 8 -cold-cache \
  -repeat 30 -page-cache > data/valhalla-scout-recheck/librescoot-pages.json \
  2> data/valhalla-scout-recheck/librescoot-pages.time

go test ./...
go vet ./...
go test -race ./internal/routing/valhallatiles
OPENMAPS_SCOUT_DIR="$PWD/data/valhalla-scout-recheck" \
OPENMAPS_VALHALLA_TAR="$PWD/data/valhalla-feasibility/bremen.tar" \
  go test -tags=integration ./internal/routing/valhallatiles \
  -run 'TestScout|TestProvider' -count=1 -v
OPENMAPS_SCOUT_DIR="$PWD/data/valhalla-scout-recheck" \
  go test -race -tags=integration ./internal/routing/valhallatiles \
  -run '^TestScoutBoundary$' -count=1 -v
```

The two original Bremen packages can be inspected alone by making a disposable
copy of the lock with only package entries 144/1985, keeping their byte pins
unchanged. This narrows declared inputs; it does not authorize altered bytes.
The final benchmark uses all three entries. Seed discovery is separate and opt-in:
set `OPENMAPS_SCOUT_DISCOVER=1` and run `TestScoutDiscover` or
`TestScoutSpatialPackageSeeds`. It reads only local pinned inputs.

Deterministic tests include the 1,032-byte synthetic nested packages, comparison
with the hand-built ordinary graph, version-specific origins, unknown/mixed
versions/datasets, duplicate/path/pin rejection, truncated/corrupt/multiple gzip
streams, expansion caps, outer bzip CRC, cleanup/cancellation, page-crossing reads,
eviction with retained views, I/O errors and missing dependencies. Existing
simple/complex/one-way/access/geometry tests still pass. Downloaded-data tests
separately validate both provider versions, actual inventory, timed prohibitions,
coordinate/partial routes, spatial and hierarchical package boundaries, path
identity under eviction, and missing-area errors. A skipped provider suite is not
provider verification; only optional discovery tests were skipped in assertions.

Completed checks: `gofmt`, `go test ./...`, `go vet ./...`, isolated race tests,
downloaded-data integration suite, downloaded Scout boundary under race, and
`git diff --check`. The race-instrumented real boundary test took about 59 s;
its timings are not in the performance tables. Evidence includes
`audit-final.json`, `check-*.txt`, source/metadata files and
`measurements/{name}.json`, `.time`, `.command.json`, `summary.json`.

No commit, push, deployment, service replacement or production migration was
performed. Existing engine and active data were preserved.
