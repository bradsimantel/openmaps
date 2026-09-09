# Experimental Scout coordinate routing

The separate Scout backend reads pinned OSM Scout Server Valhalla **3.4.0 tiles**
as graph records. Go owns route search, costing, snapping, restrictions, geometry,
HTTP translation and snapshot lifetime. It does not execute Valhalla's routing
engine. The existing SQLite routing engine and active deployment are unchanged.

The national candidate uses nine directed landmark pairs for long routes. Its
frozen qualification includes every state/DC, rural roads, international road
legs, Alaska–lower-48 travel, Hawaii, disconnected islands and eastern-hemisphere
Aleutian roads. See the [historical final qualification report](log/0034-national-scout-qualification.md)
for exact inputs, outcomes, HTTP checks and resource measurements. The acquired
extras include known source defects and missing references outside the target;
no global audit pass or exhaustive upstream OSM coverage is claimed.
The [historical national closure and reverse-edge milestone](log/0032-national-closure-and-reverse-edge-identity.md)
records the expanded graph, all-state checks and corrected v2 reverse enumeration.
The [historical regional milestone](log/0030-persistent-scout-regional-candidate.md)
records measurements and reproducible inputs. The current PBF pipeline's separate
[national capacity preflight](routing-national.md) does not measure this backend.

## Experimental profile

`osm-scout-public-auto-v1`, cost model `scout-edge-speed-v1`, uses provider integer
metres and directional km/h: sum(edge metres × traversed fraction × 3.6 / speed).
No traffic, turn delay or calibrated travel-time claim is made. Ferries, tracks,
destination-only roads, unsupported uses, zero speeds and speeds above 140 km/h
are excluded. Encoded auto access and node access are enforced; the documented
experimental fixed car dimensions are checked against retained numeric rules.
Encoded conditional auto access closes travel; timed complex turns are prohibited
at all times. Immediate reversals are prohibited, including at dead ends.

This profile cannot reproduce `driving-time-v4`: normalized tiles lose raw access
specificity, malformed/unknown tags, excluded-road guards, source node/relation
identity, some conditions, speed provenance and property entrance evidence.
Provider normalization can admit a gate or bollard the existing profile excludes.
No destination-zone qualification, address association or address routing is
implemented. Neither a tile generation timestamp nor a dataset ID establishes
an independently verified OSM snapshot cutoff or production build configuration.

Coordinates use WGS84 longitude/latitude; GeoJSON uses `[longitude, latitude]`.
Snapping independently selects the nearest eligible stored shape within 100 m,
excluding motorway/trunk, bridges and tunnels. Off-road gaps are unverified and
excluded from travel distance/time. Full stored shapes are returned; source OSM
vertices and surveyed entrances cannot be recreated. Geometry joining currently
rejects gaps over 0.5 m. Distinct graph nodes never join merely by coordinates.
Graph references are snapshot-qualified, not stable public `om_` identities.

The experimental endpoint envelope is the retained tile data, **not a US boundary
mask or a certification of coverage**. Dateline projection/clipping and wrapped
spatial bins are implemented; frozen Adak, Attu and Shemya cases exercise actual Aleutian road geometry on
both sides of the dateline. They do not establish a road across the dateline.
Unacquired tiles remain `incomplete_data`, including where absence has not been
established by a complete provider inventory. Missing data never becomes
`unreachable` or an invented connection.

## Acquisition and preparation

`scripts/scout-acquire.py` uses the complete provider digest and directory inventory.
Identical repeated digest entries are accepted; conflicting entries are rejected.
Catalog selections only choose initial packages. The national selection includes
50 states/DC, Canada, Mexico, all 34 packages absent from the regional-list
union, 23 Central American packages required by the observed southern closure, and four
Kamchatka-selected packages containing eastern-hemisphere Aleutian tiles. Missing
reference closure cannot detect an entirely absent disconnected island network.
Positive source-tile and road probes supplement dependency auditing. The pinned
selection has 647 packages; it is not the complete 2,930-package planet.

Metadata, packages and per-package SHA-256 receipts are retained. Provider MD5
checksums are transport consistency evidence, not authenticated provenance.
Acquisition checks the complete digest and catalog before and after each run,
checks size sidecars before downloading, streams one package at a time, and
refuses changed bytes or receipts. Failed transfers remain partial; rerunning
reuses verified packages and retries only the incomplete package.

To reproduce the pinned candidate from its retained acquisition directory, copy
the repository lock into that directory under a new plan name, then prepare into
a new output directory. The root must retain the three pinned metadata files,
`packages/` and `receipts/`:

```sh
cp imports/valhalla-scout-national.lock.json data/scout-national-20260909/locked-acquisition.json
go build -o data/scout-prepare ./cmd/scout-prepare
python3 scripts/scout-run-bounded.py --root data/scout-national-20260909 \
  --report data/scout-national-20260909/rebuild.resources.json \
  data/scout-prepare -root data/scout-national-20260909 \
  -plan locked-acquisition.json -out data/scout-rebuilt

data/scout-prepare -root data/scout-national-20260909 -out data/scout-rebuilt -turns-only
data/scout-prepare -root data/scout-national-20260909 -out data/scout-rebuilt -reverse-turns-only
data/scout-prepare -root data/scout-national-20260909 -out data/scout-rebuilt -potential-only
data/scout-prepare -root data/scout-national-20260909 -out data/scout-rebuilt -reverse-support-only
```

Apply the same supervisor to each construction phase. Rebuilds need additional
space for the new graph and landmarks **plus** the disk reserve. To reacquire
missing packages from this generation, `scout-acquire.py fetch --root ... --plan
locked-acquisition.json` checks the complete pinned provider metadata and rejects
changes. If the provider has rotated the generation, use retained verified inputs;
do not change the lock to bypass rejection.

For a new generation, from the repository root:

```sh
python3 scripts/scout-acquire.py snapshot --root data/scout-next
python3 scripts/scout-acquire.py plan --root data/scout-next \
  --regions north-america/us,north-america/canada,north-america/mexico \
  --extra 1,2,67,244,247,248,249,250,251,252,253,345,397,398,425,444,523,560,621,623,624,626,853,854,855,1033,1047,1230,1558,1559,1560,1657,1666,2579
python3 scripts/scout-acquire.py fetch --root data/scout-next

go run ./cmd/scout-prepare -root data/scout-next -out data/scout-prepared-next
go run ./cmd/scout-prepare -root data/scout-next -out data/scout-prepared-next -turns-only
go run ./cmd/scout-prepare -root data/scout-next -out data/scout-prepared-next -reverse-turns-only
go run ./cmd/scout-prepare -root data/scout-next -out data/scout-prepared-next -potential-only
```

The explicit extra IDs describe the observed generation; inspect the new plan's
`catalog_omitted_packages` before relying on that list for a later generation.
These commands capture newly observed pins, not an instruction to replace an
existing lock to bypass a mismatch. Preserve all verified metadata and receipts.

Preparation streams nested bzip2/tar/gzip into one immutable seekable tile file.
It checks package SHA-256/available provider MD5, gzip/bzip2 CRCs, tile identity,
version/dataset agreement, timestamps, member lists, sizes and section extents.
Conflicting duplicate tiles fail; identical payload duplicates are deduplicated.
Publication receipts are linked atomically after successful writes and syncing.
Failed preparation directories are retained as unpublished evidence and never
loaded without complete receipts. Retry into a new directory.

An extension can preserve verified tile bytes without decompressing old packages:

```sh
data/scout-prepare -root data/scout-national-20260909 \
  -plan national-aleutian-acquisition.json \
  -extend data/scout-national-20260909/national-closure-prepared \
  -clone-base -out data/scout-extended-new
```

`-clone-base` uses macOS copy-on-write cloning into a distinct inode; it never
appends to a hardlink of serving data. Without it, a bounded ordinary copy needs
space for the full graph plus reserve. The extension verifies the old graph,
requires every old package pin unchanged, imports the new packages, and pins its
base receipt. Prepare the derived turn/potential indexes for the new graph.

For disconnected additions only, existing landmarks can be reindexed:

```sh
data/scout-landmarks -reindex-from data/scout-old \
  -built data/scout-old/landmarks -prepared data/scout-extended-new \
  -out data/scout-extended-new/landmarks
```

This verifies every old tile payload and scans all ordinary edges and hierarchy
transitions. It checks retained tile hashes against actual blocks in both owned files and
rejects any new connection touching a finite old landmark
component, in either direction. Otherwise the finite components are unchanged:
old distance values are copied into the new dense node order, and added nodes
receive infinity. A checksummed proof binds both graphs and the old landmarks;
new vectors and the final manifest are published immutably. An extension that
fails this conservative proof requires full landmark recomputation. This is a
specific extension mechanism, not a general incremental shortest-path algorithm.

The page index contains tile offsets and hashes; it does not expand nodes, roads
or shapes into the existing in-memory graph. Forward and reversed prohibition
tries are prepared into flat state/transition files. Serving uses sorted binary
lookup and a 4 MiB page cache per turn file, rather than reconstructing global
restriction maps. Offline trie construction still uses bounded resident maps.

## Search and resource limits

Ordinary Dijkstra is retained as the reference. One-sided A* uses a separately
prepared spherical-distance lower bound, computed over every permitted retained
ordinary edge. Hierarchy-equivalent nodes share a coordinate for the potential;
reciprocal transitions are checked. The minimum actual cost/geometric-distance
ratio accounts for provider integer-length rounding. The bound does not assume
that upstream hierarchy pruning is correct for this cost model.

The optional bidirectional search uses balanced potentials and independently
prepared forward/reversed prohibition automata. Meeting paths retain incoming
and outgoing identities, validate the simple joining turn, and replay up to 33
suffix edges through forward history. All ordinary road levels remain available;
no provider shortcut or hierarchy-level pruning is used. The optional bidirectional landmark experiment did not solve the difficult
national case within the HTTP budget; the serving strategy uses one-sided A*.

| Resource | Explicit limit or current default |
| --- | --- |
| Acquisition | 12 GiB compressed plan default, 256 MiB per package; ≥32 GiB free disk |
| Tile preparation | 48 GiB spool default (maximum 64 GiB), 256 MiB per tile, 512 MiB outer tar per package |
| Tile index | 100,000 tiles, 64 MiB serialized metadata |
| Offline turns | 250,000 rules, 2,000,000 trie states; 4,096 decoded rules per tile |
| Runtime cache | CLI prepared pages up to 128 MiB; service default 128 MiB per independent reader (`-scout-cache-mib`), plus turn caches and fixed record caches |
| Query | HTTP: 2,000,000 labels; offline admission: up to 4,000,000, including both frontiers and obsolete labels; 1,000,000 output positions; 30-second HTTP calculation timeout |
| Service | 1–4 readers; non-waiting shared admission across snapshot replacement and through HTTP encoding |

These are resource admission and payload/record limits, **not hard RSS bounds**.
Go heap overhead, slice/map growth, output encoding, transient page buffers and OS
file cache are separate. `scripts/scout-run-bounded.py` samples process RSS and
free disk, terminates the job on a crossing, and records its observations. It can
miss short peaks; `GOMEMLIMIT` is not a physical-memory limit. Use a compiled
binary as its command so the sampled PID is the job itself.

## Isolated service and lifetime

The qualified local instance uses `127.0.0.1:8097` and
`data/scout-national-20260909/national-aleutian-prepared`. The historical
qualification report pins its binary and inputs; `/healthz` reports the current
snapshot. The example below is for another prepared directory and unused port.

```sh
go run ./cmd/server -routing-scout data/scout-prepared-next \
  -routing-concurrency 2 -listen 127.0.0.1:8096
```

Use an unused loopback port. Scout mode cannot be combined with `-deployment`,
legacy routing or the SQLite prepared backend. It needs no lookup database.
It implements the same explicit coordinate request subset and response masks at
`POST /directions/v2:computeRoutes`; Google translation remains in `internal/api`.
The current service uses one-sided A*. Bidirectional search is available in the
offline harness with `-bidirectional`; `-accelerated` selects one-sided A* and
omitting both selects ordinary Dijkstra.

For landmark acceleration, prepare `landmarks/` inside the graph directory before
selecting that immutable candidate:

```sh
go build -o data/scout-landmarks ./cmd/scout-landmarks
python3 scripts/scout-run-bounded.py --root data/scout-next \
  --report data/scout-next/landmarks.resources.json \
  data/scout-landmarks -prepared data/scout-prepared-next \
  -out data/scout-prepared-next/landmarks \
  -seeds imports/scout-national-landmarks-extended.json
```

A completed subset can be published without changing the resumable build:

```sh
data/scout-landmarks -prepared data/scout-prepared-next \
  -built data/scout-prepared-next/landmarks \
  -out data/scout-completed-prefix -publish-prefix 4
```

This requires both directions of the first four pairs, checks their source binding,
headers and hashes, and hardlinks immutable payloads into a new directory. It
refuses to overwrite a publication. A serving snapshot needs its own complete
`landmarks/` directory; never select the actively written build before its final
manifest exists.

The extended seed file records nine chosen coordinates. Each resulting receipt pins the
selected source node. Preparation uses ordinary permitted directed edges and
explicit zero-cost hierarchy transitions, relaxing turns and node access only
for the lower bound. It computes distances to/from each landmark. Serving uses
conservative directed triangle inequalities with downward-quantized float32
intervals; unreachable vector entries contribute no bound. Query traversal keeps
the full access and turn constraints and can reopen states. With landmarks loaded, the offline bidirectional search uses two independent
admissible endpoint bounds and reopens improved states. It stops when the maximum
of the two minimum complete-path bounds reaches a verified joining cost; it does
not add unbalanced landmark bounds. The service currently uses one-sided A*.
Uniquely forced chains of at most 64 ordinary edges retain every source edge and
the complete turn state; they stop at branches, hierarchy transitions and targets.

Landmark preparation admits at most 128 million nodes and sixteen seeds. Dense
distance/heap-position scratch costs 12 bytes per source node plus an indexed
heap; it does not construct an expanded topology. Each vector costs four bytes
per node on disk. One vector is built at a time, with checksummed resumable
receipts bound to graph/profile/node order/seeds. The CLI's 2 GiB Go memory target
is soft; the external sampled RSS and free-disk supervisor is still required.
Serving defaults to 8 MiB per vector (144 MiB for nine pairs; 256 MiB
at the sixteen-pair ceiling),
in addition to graph, turn and fixed record caches. A query-local, 262,144-entry direct-mapped heuristic cache avoids repeated
landmark lookups for the same node and fixed target. Offline experiments may
select 1–32 MiB per vector; 32 MiB at sixteen pairs is a 1 GiB payload ceiling.
Query and construction phases recycle page buffers only while decoding copied
fixed records; shape decoding runs with ordinary buffer ownership restored.
Offline construction uses a 256 MiB graph cache and an optional source-bound
`reverse-support.bin` certificate. Prepare it with `scout-prepare -reverse-support-only` before building landmarks. Only certified nodes use the
fast opposing-index reverse view; exceptional nodes enumerate every actual
incoming ordinary edge, preserving many-to-one opposing identities. `CacheReport` includes these
separate caches and their total retained payload limit.

Candidate loading detects the optional `landmarks/` directory, requires its final
manifest, checks vector bytes and source bindings, and includes the landmark
manifest in snapshot identity. Responses expose the selected search strategy.
The offline harness accepts an external directory with `-accelerated -landmarks`.
`cmd/scout-verify` runs frozen JSON coordinate cases, compares ordinary traversal
when requested, and checks paths independently of search labels and automata.

The response identifies the experimental profile, cost model, snapshot,
source limitations, attribution, both selected snaps and unverified gaps.
Address requests return 503 `address_routing_unavailable`; missing tiles return
503 `incomplete_data` with the dependency; a query limit returns 503
`query_budget_exhausted`. Exhaustive no-route remains HTTP 200 with empty routes
and `unreachable`. Invalid requests/masks retain their existing 400 errors;
excess concurrent work returns 429 with `Retry-After: 1`.

`-scout-selection path.json` optionally watches a separate file containing
`{"directory":"prepared-directory"}`. Relative directories resolve beside the
selection file. A new candidate is fully loaded before publication; old response
leases finish before retirement. Replacement shares the existing admission
budget and serializes old/new overlap. A failed load preserves the old snapshot.
No existing deployment selection is read or written. `/healthz` includes candidate
identity, load errors and an explicit statement that exhaustive upstream source
coverage is unverified. Frozen route qualification is a separate measured report.

Preparation/startup hashing warms filesystem caches. Application-cache-cold
measurements are distinct from physical cold-disk behavior, which remains
unmeasured on the shared desktop. The qualification report records full HTTP bodies, sustained concurrency,
independent national path checks, startup and replacement measurements. It also
retains the failed tuning experiments and limits the conclusions to measured cases.
