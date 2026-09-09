# National closure and reverse edge identity

Date: 2026-09-09. Scope: uncommitted Scout candidate implementation after baseline
`d24c604`. This is a historical construction and correctness milestone, not a
claim of complete OSM coverage. It follows [0031](0031-scout-national-graph-and-landmarks.md).
Long-route and HTTP qualification are recorded separately when completed.

## Expanded acquisition and independent rebuild

The complete provider digest has 2,930 package IDs; regional catalog selections
omit 34. The initial US/Canada/Mexico selection plus those omissions acquired 620
packages. Actual graph references exposed a southern boundary dependency, so 23
Central American packages were added from the same pinned generation. The final
643 packages occupy 9,172,913,195 compressed bytes. All retained payloads have
provider MD5 and local SHA-256 verification. The lock is
`imports/valhalla-scout-national.lock.json`; the generation is
`2026-06-20_07:12`, tile version 3.4.0, dataset ID 183131145. These facts do not
establish the exact OSM cutoff or provider production build configuration.

The expanded graph was independently decompressed from all 643 retained packages
into a new directory. It contains 34,300 tiles, 122,534,709 nodes and 284,770,767
directed edge records. Its 24,478,089,216-byte `tiles.bin` has SHA-256
`e74c63c6285be57521950a9dc4a271ee7dfc86121ec95de95c6fa4b75948177a`;
its receipt hash is
`019cf0813d9187f317a25456c1f6df6909688cb94bf830df0ff53a30964c9bf6`.
All 32,933 common tile IDs and payload hashes match the earlier independent
preparation; 1,367 tiles were added, with no removed or changed common tile.
Public entity IDs are not introduced: internal graph references remain qualified
by this immutable source snapshot.

Construction took 636.262 seconds with 41,369,600 bytes sampled peak process RSS
(58,441,728 bytes child maximum reported by macOS). Forward turns took 20.343 s,
reverse turns 19.462 s, and geographic potential preparation 111.158 s. These
runs obeyed a sampled 4 GiB process RSS ceiling and at least 32 GiB free disk.
The RSS sampling is not a hard physical-memory bound. Retained inputs and both
prepared graphs remain on disk; no active deployment was changed.

## Full audit: target closure and outside-target defects

The full audit completed all tiles in 241.802 s, with 373,014,528 bytes sampled
peak RSS (423,346,176 child maximum). It exited nonzero for three source geometry
mismatches. They must not be reported as an audit pass or repaired by inventing
coordinates:

- Way 620313011, edge `2/421920/94`, has a 370,975.945 m start gap near Fiji and
  also refers to absent tile `2/421919`. Its package is included by the provider's
  Hawaii catalog selection, demonstrating that catalog labels are not polygons.
- The opposing records of way 655299344 near `[134.5, 7.3593]` have 1.5449 m gaps.

There are 685 distinct missing references in the acquired extras, principally
Caribbean/other-continent fragments and two mainland references into Colombia.
None of their tile boxes intersects the 50-state/DC Census polygons used below.
No returned path may silently skip a required absent tile. Runtime outcomes keep
`incomplete_data`, `unsnappable` and exhaustive `unreachable` distinct.

## Frozen short-route checks

The 72 short cases in `imports/scout-national-cases.json` (excluding four long
routes and two Mexican cases) all pass: 64 routed, two unreachable, and six
unsnappable. Each of the 64 returned paths passes the separate source-path
verifier and matches ordinary Dijkstra's cost. The verifier walks actual edges,
access, node transitions, simple restrictions, raw complex restriction sequences,
partial endpoints, summed costs and every returned geometry vertex. It does not
certify upstream OSM fidelity and shares the low-level record/shape decoders.

The 51 urban request pairs cover all states and DC; both endpoints are inside
the expected Census state polygon. Additional cases cover rural roads, state
boundaries, Utqiagvik, Unalaska and Adak. Utqiagvik's route spans latitude 71.29°;
Adak's verified 6,639.413 m route uses source-selected road points. Initial
approximate points more than 100 m from eligible roads remain explicit negative
snapping fixtures. The service snap radius was not increased to make them pass.
Honolulu–Hilo and Utqiagvik–Anchorage exhaust their road components and correctly
return unreachable with ferries excluded. Point Roberts–Blaine returns 40,373.200 m
and reaches latitude 49.093962°, establishing an actual US-to-US Canadian detour.

The 72-case run took 82.835 s including startup, with sampled peak RSS 378,814,464
bytes and child maximum 380,665,856. It overlapped landmark construction; its
latency is not an isolated serving benchmark. Application caches were cleared
between cases; the physical filesystem cache was not purged or measured cold.

The independent bin check scans every retained eligible stored shape. Five road
probes agree with the indexed nearest snap; the original Adak point is absent
from both within 100 m. Bering probes at ±180°, 65° have no retained nearby road,
but the indexed query needs an absent tile. They remain missing-data observations,
not successful national snapping or unreachable proofs. Synthetic fixtures cover
wrapped bins and dateline geometry math.

The external coverage reference is the official Census 2025 1:500,000 state
cartographic boundary archive, `cb_2025_us_state_500k.zip` (3,245,373 bytes), SHA-256
`9cbfe171dad1555e11770c981d8f4db9e687a65c86f5bdae684eeb487e2e9b80`.
Source: <https://www2.census.gov/geo/tiger/GENZ2025/shp/cb_2025_us_state_500k.zip>.
These simplified boundaries are a coverage cross-check, not legal surveys or
proof that every OSM road has been imported.

## A failed assumption in reverse traversal

The first national landmark build exposed a many-to-one opposing-index pattern.
In tile `2/800870`, outgoing edges 27157/27158 point from node 11917 to node 29837
and both select opposing edge 67750. Incoming 67749/67750 both select 27158.
These are generic service-road records (use 11, way 396126716), not parking
aisles (use 6). Incoming records retain different local indexes and must not be
collapsed: access or joining restrictions can distinguish identities. A reverse
search that assumes a bijection can omit a valid incoming edge.

The corrected reverse enumeration discovers neighboring source nodes through all
outgoing records, including shortcuts for discovery only, then enumerates every
ordinary incoming edge. No shortcut is traversed or used for route cost. A full
source audit checks opposing endpoint support. The landmark schema was advanced
to `openmaps-scout-landmarks-v2`; rejected v1 artifacts remain quarantined and
cannot be loaded as v2. Adversarial offline tests keep both parallel identities,
check reverse distances against the faster incoming edge and compare bidirectional
search with ordinary traversal.

A later bounded preprocessing scan certifies where the fast opposing-index view
is complete. Its 15,316,903-byte bitset marks only 87 exceptional nodes and is
pinned to the prepared graph receipt. It records 591 missing ordinary end
references in acquired extras. These are reported, not traversed or repaired.
The bitset's SHA-256 is
`6cf4b2d0b2c866ddb14df77ffc1dd02982bfe5e15c6fb59488f937230404040a`.
Preparation took 85.309 s with sampled RSS 190,447,616 and child maximum
239,566,848 bytes. Uncertified or exceptional nodes use exhaustive enumeration.

The v2 build initially used 128 MiB of graph pages. Seattle's first vector alone
issued 1,590,392,127,488 logical graph ReadAt bytes: a cache-thrashing measurement,
not physical disk traffic. Offline fixed-record construction now uses 256 MiB,
within the existing prepared reader ceiling, and recycles page buffers only in
that private construction mode. Shape decoding is forbidden in this mode because
it can retain borrowed record bytes across another load; a cross-page regression
test verifies the distinction. Serving readers retain their 128 MiB ceiling.

Completed vectors have independent checksummed receipts and are reused after
interruption. The first v2 run was deliberately cancelled at a checkpoint after
974.238 s to add the certificate and larger offline cache; its sampled peak was
2,430,107,648 bytes, with no resource-budget abort. A separate publication command
can validate and hardlink a completed prefix of landmark pairs into an immutable
snapshot while the original build continues. Neither publication overwrites a
completed vector or the original final manifest.

## Retained evidence

All measurement files are under ignored `data/scout-national-20260909/`:
`national-closure-acquisition.json`, `national-closure-prepared/`,
`independent-rebuild-tile-comparison.json`, `national-closure-audit.json`,
`national-short-geographic.jsonl`, `national-short-state-coverage.json`,
`adak-source-discovery.json`, the preparation resource reports, and
`national-landmarks-v2*.resources.json`. Full acquisition metadata and 643
compressed packages remain available for reproduction. The current behavior and
commands belong in [the maintained Scout documentation](../routing-scout.md).
