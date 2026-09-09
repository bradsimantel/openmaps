# National coordinate-routing candidate plan

**Status: the original full-PBF construction proposal fails this host's capacity
preflight. A separate [Scout tile candidate](routing-scout.md) implements national
coordinate routing under a distinct experimental profile. Its measured
[historical qualification](log/0034-national-scout-qualification.md) is separate
from this PBF construction proposal and does not establish the proposed
`driving-time-v4` national contract below.** The supported
regional implementation remains described in [routing](routing.md),
[scaling](routing-scale.md) and [prepared snapshots](routing-prepared.md).
The [historical national preflight](log/0026-national-routing-preflight.md)
records measured host capacity, source metadata, estimates and verification.

## Proposed release contract

The target is coordinate routing on supported source roads throughout the **50
states and District of Columbia**, including Alaska, Hawaii and the Aleutians
on both sides of longitude 180°. Geographic inclusion does not imply that every
pair of destinations connects. Do not certify national coverage from a single
bounding rectangle, total node count or a successful coast-to-coast route.
Require a per-state/DC report of retained nodes, directed segments, excluded
roads/guards, applicable restrictions, component sizes and tested endpoints.
Report the source extract polygons separately from administrative coverage.

Keep `driving-time-v4`, `estimated-driving-v1`, incoming-edge identity, via-way
history, destination-only access, partial endpoints, independent snap selection
and exact source geometry. This includes the existing conservative conditional,
vehicle-limit and unsupported-speed interpretations. This milestone neither
calibrates travel times nor introduces traffic or navigation instructions.
Retain the existing API masks, outcomes, IDs, source provenance and attribution.
An expanded graph may legitimately change the optimum by adding a detour.

The proposed graph source is a **single coherent North America extract** to
support Canadian and Mexican detours between US endpoints. This avoids a
country/state merge and preserves source node identities across borders. **Canadian and Mexican road detours were authorized on 2026-09-09 for the
separate Scout candidate, including Alaska-to-lower-48 connectivity where source
roads permit it.** A US-only alternative must
explicitly report missing international paths: Alaska-to-lower-48 road journeys,
Point Roberts and other border detours cannot be promised. Incidental foreign
roads in an extract's buffer do not establish complete foreign connectivity.
Even with Canada/Mexico present, border crossing rights, documents, opening hours,
delays and current closures are unsupported; the graph models only the existing
source profile, not permission to cross a border.

Ferries remain excluded. Hawaii's islands, ferry-only communities, isolated
Alaskan road networks and other disconnected roads remain distinct components.
Unsnappable endpoints retain `unsnappable`; two successfully selected endpoints
without a supported connection retain `unreachable`. No ferry, flight, straight
line or alternate-component resnap may manufacture connectivity. An unreachable
result is not proof that no real-world journey exists.

Territories and general Canadian/Mexican endpoint service are outside the target.
Existing routing metadata supports one inclusive rectangle, not a country mask
or dateline-wrapping union. Before publishing a national endpoint promise, define
and verify its accepted envelope explicitly. If an exact US endpoint mask is
required, introduce a versioned coverage representation and preserve legacy
rectangle readers. A whole-world-longitude rectangle must not be advertised as
a US boundary. Audit Aleutian/high-latitude snapping and spatial bounds against
full scans before claiming that coverage.

National snapshots are `routing_only`: no new places, address acquisition,
geocoding index, inferred address identity or nationwide address coverage.
Newport's existing address/mixed behavior remains a regression gate on its own
snapshot. Basemap tiles remain separate.

## Capacity gate and budgets

These are **pre-evaluation budgets**, not achieved national measurements. The
available Mac has 16 GiB unified RAM and about 108 GiB free on its internal APFS
SSD. It has no configured hard RSS limit or controlled cold-cache facility.
Reserve 8 GiB RAM for the shared host and at least 32 GiB free disk. Heavy work
is serialized. An 8 GiB process construction ceiling is an admission criterion;
Go's soft memory limit cannot enforce it.

The September 9 preflight rejects both source scopes. It uses measured full
Oregon–Washington–Idaho source size and construction peaks, then applies the
compressed-source ratio and a **1.5× contingency**. This is a planning model,
not a national graph census, a statistical confidence bound, or evidence of a
national out-of-memory failure. Compression, road density, source tags, hash-table
capacity steps and hierarchy transfer growth may violate linear scaling.

| Quantity | US-only central / planning estimate | North America central / planning estimate |
| --- | ---: | ---: |
| Published source size, GiB | 11.30 / 11.30 | 18.02 / 18.02 |
| Peak importer charged footprint, GiB | 109.59 / 164.38 | 174.79 / 262.19 |
| Peak preparation charged footprint, GiB | 85.10 / 127.65 | 135.74 / 203.61 |
| Preparation live heap, GiB | 83.36 / 125.05 | 132.97 / 199.45 |
| SQLite, GiB | 10.99 / 16.48 | 17.52 / 26.28 |
| Prepared artifact, GiB | 62.74 / 94.11 | 100.07 / 150.10 |
| Identity sort payload allowance, GiB | 4.10 / 6.15 | 6.54 / 9.80 |
| Import elapsed, hours | 2.26 / 3.39 | 3.60 / 5.40 |
| Preparation elapsed, hours | 0.90 / 1.34 | 1.43 / 2.14 |

Source sizes are observed HTTP Content-Range totals; all other national values
are estimates. Import time includes post-write graph validation, so it is not
just PBF parsing. The current single-source importer has no reusable staging
store or merged derivative: their disk cost is zero **for that path only**.
Identity sort is separate temporary storage. OS caches, library buffers, Go
reservations and compression are not made safe by reducing sort buffers.

Planning peak disk is **128.03 / 204.20 GiB** for the first US / North America
publication, including its source, database, prepared artifact and sort scratch.
With two database/artifact generations for independent rebuilding and replacement,
it is **238.61 / 380.59 GiB**, before the 32 GiB host reserve. This counts each
temporary prepared file once: linking its immutable final name does not duplicate
its content. It does not keep an extra merged PBF or needless third publication.
Full old/new virtual query mappings alone are estimated at **188.21 / 300.20 GiB**
with contingency. Virtual overlap is distinct from resident physical pages.

A concrete path to the first experiment with the current construction strategy
is an **existing 512 GiB RAM host, at least 16 CPU cores, and 1 TiB free local
NVMe storage**. No provisioning or expenditure is authorized. A 384 GiB host
could satisfy the declared 320 GiB process budget with 64 GiB reserved, but
512 GiB provides additional shared/cache headroom. US-only may fit an existing
256 GiB host with a 192 GiB construction budget; that does not solve its foreign
connectivity omissions. CPU/storage throughput must be measured on the actual
host; core count is not a speedup guarantee.

| Gate on the proposed larger host | Absolute budget | Relative / comparison gate |
| --- | --- | --- |
| Construction process | 320 GiB charged footprint/RSS and live heap; at least 64 GiB host headroom | ≤1.5× measured Northwest footprint per compressed source byte; reevaluate using actual graph counts |
| Build workspace | 640 GiB total incremental data; ≥128 GiB disk remains free | ≤1.5× projected database/artifact sizes; staging allowance must be counted explicitly |
| Acquisition | 6 hours, checksum required | Record bytes/s and retries; no fallback release |
| Import / preparation | 8 / 4 hours; 18 hours acquisition through first publication | Each phase ≤2× regional source-ratio elapsed estimate on recorded hardware |
| Startup | 600 seconds including full source/artifact integrity and structural validation | ≤2× byte-scaled regional startup on the same host |
| Serving experiment | 64 GiB process RSS, 8 GiB charged/private scratch target; four admitted requests | ≤25% regional p95 regression and ≥80% regional throughput in a matched run |
| Normal workload, complete HTTP encoding | p95 <1 second at one and four workers; ≥12 requests/s at four workers | Same frozen workload and hardware before/after search changes |
| Difficult workload | p95 ≤10 seconds, maximum ≤30 seconds, reported separately | No omission of failing long-distance or disconnected cases |
| Query memory / cancellation | ≤512 MiB cumulative allocation normal; ≤2 GiB difficult; cancellation returns within 250 ms | Include search, reconstruction and full encoding; measure retained live scratch separately |
| Replacement / rollback | 600 / 600 seconds, same four-request admission pool; retired mapping bytes zero | Old responses complete; failure preserves the selected/serving snapshot |

The 600-second operational target exceeds the current server's two-minute write
timeout. National reload must be evaluated and, if necessary, moved out of a
request's load path or given an explicit operator workflow before claiming this
operational target. No timeout or production setting was changed by preflight.
The 64 GiB serving target is an experiment, not a claim that two 150 GiB mappings
can meet latency under pressure. A separate controlled physical-memory gate must
measure the entire process and relevant page cache. No host-wide cache purge,
application closure, pressure generation or setting change is part of desktop
verification.

Recalculate the current-host admission estimate from retained evidence:

```sh
python3 scripts/routing-capacity.py \
  --model data/national-scale-20260909/capacity-model.json \
  --construction-gib 8 --disk-reserve-gib 32 \
  --out data/national-scale-20260909/capacity-recheck.json
```

The output must be new. Exit 2 with a report means the estimate is blocked; exit
0 means only that it fits the estimate. The tool does not download, launch a
build, enforce memory limits or authorize topology. Missing/invalid evidence
inputs fail. Its retained model identifies measurement files and source metadata;
it is not a portable source lock. Unit checks use
`PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_routing_capacity.py'`.

## Whole construction path

The first experiment should use the existing complete source-validated pipeline
on a host that passes preflight, without waiting for every allocation to become
constant-size. Instrument import phases before acquisition/build, support signal
cancellation in `cmd/import` (it currently supplies `context.Background()`), and
monitor process plus disk budgets with abort/cleanup reporting. A supervisor's
samples can miss peaks; they are not a hard RSS guarantee.

| Phase | Current resident costs and necessary next boundary |
| --- | --- |
| Acquisition | Pin one dated PBF, replication timestamp, upstream checksum, byte count, observed polygon and attribution. Download to a new partial file, verify, compute full SHA-256, then establish the new source lock. No `latest` fallback. |
| Parsing | Parsed motor ways, raw highway source JSON, restrictions, needed-node and blocked-node sets coexist with decoder queues. Record object/vertex counts and phase peaks; reject duplicate/conflicting source objects rather than silently choose a partition. |
| Node resolution | Needed coordinate map and tagged speed/barrier provenance coexist with ways. All retained way references must resolve; no clipping or coordinate-based identity join. |
| Segments / restrictions | Preserve source way and original vertex ordinals, guards, costs and every relevant departure at from/via nodes. Only-turn alternatives and via-way sequences cross partition boundaries. Enumeration overflow must fail, not omit restrictions. |
| SQLite / source validation | Chunk output is bounded but importer Data and required/seen provenance sets are not. Validate the exact unpublished transaction after releasing importer Data. Retain source/checksum/release bindings. |
| Graph / spatial indexes | Source-node map, original 48-byte edges, points, CSR, directions, public/restricted memberships and spatial bounds/trees remain graph-sized. Check counts before allocations, including the stricter constructor segment ceiling. |
| Hierarchy | Global junction/cell scratch and recursive transfers/paths coexist with the graph. Count actual entries, transfers and paths before predicting national capacity. Keep restriction/access boundaries explicit. |
| Trusted publication | Reconstruct canonical topology and canonical cell optima from authoritative SQLite; a self-checksum never authorizes topology or shortcuts. Count node hash slots, source-ID sorting, ancillary JSON and output overlap. Publish the trusted receipt last. |

Single-source acquisition avoids overlapping extracts. If acquisition must use
multiple extracts, require identical replication timestamps, preserve complete
ways and all referenced restriction members, deduplicate by source type/ID/version
with identical content, and reject conflicts. Check source references before
building; a same-date filename alone does not establish coherence.

If no suitable build host exists, the alternative is a substantial construction
implementation: spool source ways/provenance and node references, external-sort
and join source IDs with coordinates, emit deterministic segments and globally
resolved restrictions, then construct graph/spatial/hierarchy arrays through
sequential/file-backed passes. These stages need versioned, checksummed completed
intermediate receipts bound to source release, profile and toolchain. Retain them
only where they avoid an expensive repeat; never reuse incomplete outputs or
treat intermediate checksums as publication authority. Reserve up to 128 GiB
additional staging until actual sizes are measured, and re-run disk preflight.
This alternative still needs more disk than the present host budget; replacing
one coordinate map alone does not make the full national path fit.

## Workload design and acceptance

Select source segment midpoints by named road/class and a geographic seed before
running the router. Persist source way/node IDs, tags, source release and the
selection rule. The following are **planned cases, not source-validated fixtures**:

| Workload group | Cases to freeze after source acquisition |
| --- | --- |
| Per-state/DC coverage | At least one urban and one rural/access pair per state; two DC pairs. Include reversed directions. Report every state, including failed snaps and closures. |
| Urban / metro | Boston–Cambridge, Manhattan–Brooklyn, DC–Arlington, Chicago river crossing, Detroit suburbs, Minneapolis–St Paul, Atlanta, Dallas–Fort Worth, Houston, Denver, Phoenix, Los Angeles, San Francisco–Oakland, Seattle. |
| Rural / terrain | Maine inland/coast, West Virginia mountains, Mississippi delta, Kansas/Nebraska plains, Montana reservations/access roads, Nevada desert, northern Minnesota, Alaska local roads and Big Island Hawaii. |
| State boundaries | NJ–NY, PA–DE, DC–MD–VA, TN–AR, MO–KS, OR–WA, OR–ID retained detours, CA–NV. |
| Difficult long distance | Seattle–Miami, Los Angeles–New York, Boston–San Diego, Maine–Florida, Anchorage–Seattle when foreign detours are in scope. |
| Foreign detours | Point Roberts–Bellingham and Northwest Angle–mainland Minnesota; Detroit–Buffalo as a permitted-choice investigation; San Diego–El Paso as an investigation, without presupposing a Mexican optimum. |
| Disconnected / excluded | Honolulu–Hilo, Juneau–Anchorage, island–mainland without ferry service in the graph, isolated permitted service road in both directions, nearest restricted road guard, ocean unsnappable, out-of-envelope point. |
| Geographic edge cases | Aleutian endpoints on both longitude signs, high-latitude Alaska nearest-road scan equivalence, zero/partial endpoints and source crossings without shared nodes. |

Normal and difficult groups must be frozen before timing. Run each normal pair
twice per worker at one/two/four workers, then a sustained ten-minute four-worker
pass; include complete HTTP responses and report errors separately from success
latency. Record snapping, search, geometry and encoder timings, response bytes,
labels, queue peak, expansions, transfer counts, allocation and memory samples.
Keep difficult cases visible rather than letting numerous short routes hide them.

For feasible selected local, state-boundary and medium-distance cases, use
ordinary Dijkstra with `max(1e-6 seconds, abs(reference seconds) × 1e-10)`.
Predeclare 120 seconds per offline reference; any override (currently at most
five minutes) must be logged. Timeouts are unverified cases, not passes.
Independently check every returned source adjacency, direction, restriction,
destination phase, partial geometry and distance/cost sum. Report weak components
and directed reachability separately. If the current hierarchy misses national
budgets, optimize measured search work while preserving the same frozen endpoint
evidence, reference cases and cost model; do not change travel-time semantics.

Retain Newport coordinate/address/mixed and legacy cycles, Oregon sensitivity
and boundary detours, and full multistate source suites as regression gates.
Rebuild affected outputs independently, comparing ordered logical records, source
IDs, graph digests and prepared bytes wherever contracts are unchanged. Verify
one national independent generation at a time and retain only the two needed for
replacement/rollback. A new encoding or preprocessing contract requires explicit
versioning and retained v1/v2 readability.

Exercise four real HTTP encoders, fifth-request rejection, cancellation, failed
loading, atomic replacement, response-lifetime leases, rollback and retirement
using temporary deployment state. Production startup must neither reconstruct
nor silently fall back. Capture heap, cumulative allocations, virtual mappings,
RSS, charged/private/compressed memory, file cache where available, faults and
storage traffic as separate measures. A successful build, correct/fast routes,
physical-memory qualification and future address routing are four separate gates.

There are no executable national build/prepare/serve instructions yet: the full
PBF has not been acquired or SHA-256 pinned, endpoint scope is not finalized and
the host fails capacity preflight. Regional commands remain usable. Supplying a
larger host removes the admission blocker; it does not pre-approve the national
graph, layout dimensions, correctness or performance.
