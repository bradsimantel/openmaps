# Newport automatic address-to-road routing

2026-09-08 · Working tree based on `af98e93da47c77449fb53a8fd7c454be5d29a2fd`.

Historical implementation and verification record. Current contracts and limits
are maintained in [routing](../routing.md), [geocoding](../geocoding.md),
[refresh](../refresh.md) and the [README](../../README.md). This supersedes the
coordinate-only API and address destination-access limitation recorded in
[0015](0015-newport-driving-routing.md) and [0016](0016-newport-driving-quality.md).
Coordinate routing, geocoding, public identities and the active deployment remain
unchanged. No supplemental addresses, travel times or unrelated endpoints were added.

## Contract and source investigation

Consulted current official Google Routes REST v2
[Compute Routes](https://developers.google.com/maps/documentation/routes/reference/rest/v2/TopLevel/computeRoutes),
[Waypoint](https://developers.google.com/maps/documentation/routes/reference/rest/v2/Waypoint)
and [address-location guidance](https://developers.google.com/maps/documentation/routes/specify_location).
The waypoint union supports `address` or `location`; origin/destination may mix
representations. The local subset retains the existing explicit geometry/distance
mask and driving-only options. Google’s broader address search, plus codes,
`geocodingResults`, region bias and place-ID waypoints are not implemented.

Read README, AGENTS, maintained routing/geocoding/refresh documents and the relevant
historical address and routing investigations, especially 0008–0013 and 0015–0016.
Inspected the retained August lookup snapshot, graph-v2 candidate and raw
`data/rhode-island-260801.osm.pbf` before choosing the policy. The PBF checksum is
still `49a96292a8c6ee308c4d5c18f0462482170dfff12ff1dbaafd4fa171b6c4dfa9`.
No external address data was acquired or imported.

An independent PBF scan around the preview (0.001-degree margin) retained
119,078 local nodes, 20,878 intersecting ways and 111 referencing relations for
inspection. Findings include:

- Six `amenity=parking_entrance` nodes: **12987302828**, **12987302829**,
  **13238465837**, **4384858205**, **5002285117**, **8650628859**. The latter
  three share nodes with parking building **763017204** and motor access ways.
  Nearby address points are not automatically that parking building’s identity.
- **351 Thames Street** is near the first two parking entrances, about 13.5/14.0 m
  away, on separate service stubs **1413218723/1413218724**. Its containing building
  **1473898121** does not supply a shared parking-ring/entrance association.
  **24 Lee’s Wharf** is about 12.5 m from entrance **13238465837**, but proximity
  alone does not establish a parking association either.
- There are 446 ways tagged `service=driveway`, 303 parking aisles, 369 parking
  areas, 139 address-number-tagged nodes and 2,626 address-number-tagged ways in
  the inspection window. These are source element counts, not unique properties.
  Source relations include one incomplete `type=street` relation for Sunshine
  Court, not a usable general address-to-street relationship. Simple complete
  rings are supported; multipolygon/site-relation inference remains outside scope.
- **11 Leroy Avenue** retains ID `om_873a2471f1e39cf3e8fac5efb3de0cf6` at
  `[-71.30497606645231, 41.47225894654243]`. It lies in building **1472492430**,
  whose source nodes join private driveway **944310633** and covered piece
  **1472660190**. Connected driveway geometry reaches public Leroy Avenue at
  node **201121407**. The selected road point is `[-71.3048174, 41.472691]`,
  **49.828 m** from the source address. The private geometry supplies association
  evidence; none becomes a driving edge or a verified off-road connection.
- Named destination roads **19359216/19352279** (Resolute) and **1131282109**
  (Cloyne) provide explicit street evidence for nearby source addresses. The
  nearer Enterprise service aisle **19353128** has only legacy TIGER name fields,
  so it cannot qualify by borrowing the farther named street’s identity.
- Training Station private driveways and gate **201248257**, the isolated bridge
  component, private Bainbridge approaches, source one-ways and boundary detours
  retain their existing exclusions/topology. Address text grants no military,
  private, customer, delivery or permit permission. Parking-area restrictions are retained
  separately: 56 local parking rings require access/conditions this endpoint policy
  cannot establish. They cannot authorize an entrance or an internal fallback
  projection merely because an adjoining service way lacks an access tag.
- The retained eight **364 Bellevue Avenue** points still lack units, a preferred
  structure or a usable parent relationship. The historical structure evidence
  in 0010 does not identify a primary entrance/identity. The twelve Connell
  records likewise remain distinct. Neither source ID order nor route success
  is an address-disambiguation signal.

Inspection scripts, PBF extracts, raw query findings and the initial/final probes
are retained in ignored `data/address-routing/`.

## Implemented resolution policy

`internal/api` calls the existing geocoder and requires one exact identity after
its existing normalization/context checks. Zero matches and multiple identities
are structured `address_resolution_failed` failures, with `no_match`/`ambiguous`
reasons and counts. Unsupported units/syntax produce `unsupported_input`.
The API never asks for candidate or entrance selection. It preserves the original
ID, address coordinates, attribution and partial-context flag. Standalone
geocoding still exposes its unchanged multiple results.

API code matches source street names and rejects conflicting area address tags.
`internal/routing` owns geometry and topology operations, independent of text
search. It first considers supported containing-site/entrance or driveway/public
junction associations, within 100 m source displacement. A parking entrance must
share a node with a containing parking ring. A driveway endpoint must be in/on
the containing building/site ring; source-connected driveways are explored only
within 150 m and 32 settled nodes, stopping at public boundaries. Exhausting the
bound before completing that local candidate search cannot establish a winner.
The public road must match the address street; driving never includes excluded
private approaches. This is a public-road arrival policy, not an assertion that
the source point is an entrance or that the property can be entered.

Ordinary fallback considers eight nearest eligible segments within 50 m. Existing
excluded-road guards, road-crossing checks and bounded safe service-to-street
preference apply. Distinct near-equal roads fail unless they share a sufficiently
local source junction. An off-road address qualifies a destination-only road only
with an explicit matching street name, nearest-road evidence and ≤40 m displacement.
The origin-prefix/destination-suffix state machine continues preventing through
shortcuts. All thresholds, ranking, ties and failure behavior are maintained in
[routing](../routing.md#automatic-address-endpoints).

The response exposes source address identity/coordinate, selected road coordinate,
displacement, selection method, source references and uncertainty. Road distance
excludes all unverified off-road gaps. An association failure returns resolved
address metadata without a fabricated road point. Unreachable routes return an
empty route list with both selected endpoints. No confidence percentage, walking
connection, entrance or access permission is invented.

## Source-backed benchmark and regressions

The checked-in **22-case** suite uses **37 actual retained address records**,
including every ambiguous identity used in the cases. Expectations come from
source tags, exact identities, geometry and explicit restrictions/bounds. Tests
check source record keys, number/street labels, exact source coordinates, HTTP
responses, directed geometry, prohibited maneuvers, destination-through exclusion,
road-distance sums and detours. No endpoint is moved onto a road.

Before this change, Compute Routes rejected all address waypoints. The table’s
“before” values are explicitly **graph-v2 geocoded-coordinate pipeline observations**
for unique addresses, not a claim that the old API completed address requests.
Ambiguous/unsupported/absent addresses have no before-coordinate observation.
Metres below exclude off-road gaps; failed associations have no selected gap.
Unless specified otherwise, the other endpoint is 26 Marlborough Street, whose
source-to-road gap is 9.628 m.

| Case | Before coordinate pipeline → address result | Selected address gap(s), m |
| --- | --- | --- |
| 16 Farewell residence | 7 → 7 | 9.5 destination |
| 50 Bellevue / reverse | 1,243 / 963 → unchanged | 21.8 Bellevue |
| 1 Resolute / reverse | Unsnappable → 2,311 / 2,461 | 20.8 Resolute |
| 11 Resolute | Unsnappable → 2,390 | 28.8 destination |
| 1 Cloyne | Unsnappable → 2,145 | 33.2 destination |
| 10 Enterprise | Unsnappable → association failure | No selected destination |
| 11 Leroy / reverse | Unsnappable → 3,010 / 2,521 | 49.8 Leroy; public junction only |
| 151 Bainbridge | Unsnappable → association failure | No selected destination |
| 351 Thames competing parking | 1,332 → association failure | Old arbitrary nearest gap 13.5; no new selection |
| 24 Lee’s Wharf | 1,550 → 1,550 | 12.5; ordinary fallback, not entrance claim |
| 5 Washington | 1,139 → 1,139 | 19.9 destination |
| 3 Beacon Hill → 50 Bellevue | 3,545 → 3,545 | 13.5 / 21.8; south detour |
| 79 Park Holm → 357 Valley | 2,302 → 2,302 | 3.3 / 25.8; north detour |
| 2 Training Station / 1387 Hopkins | Unsnappable → association failure | Private/gated/disconnected approaches preserved |
| 364 Bellevue / 199 Connell | Address ambiguity failures | Eight / twelve distinct identities preserved |
| 50 Bellevue Apt 2 | Unsupported input | Unit not stripped |
| 99999 Bellevue | No-match resolution failure | No substitute address or street fallback |

Investigated initial failures rather than changing expectations:

- The first competition check rejected 50 Bellevue and the Valley boundary point
  because it mistook adjoining source-junction segments for unrelated competing
  roads. A bounded shared-source-junction exception restores their independently
  expected nearest-road fallback without changing headings or turn restrictions.
- The initial private driveway association followed a single OSM way and missed
  11 Leroy’s connected pieces. The bounded driveway-only source-node walk fixes
  that representation issue and still ends driving at public Leroy Avenue.
- The **351 Thames rejection is an intentional conservative difference** from the
  old coordinate pipeline. Its sub-metre difference between independent service
  stubs does not establish which parking entrance belongs to the address. The
  address service returns a clear failure; the existing coordinate request remains
  unchanged. This is not counted as an improvement in route coverage.

Final review added a conservative check for restricted parking-area tags in
addition to road/node tags. The initial candidate omitted this area evidence; a
synthetic customer-only parking fixture now verifies refusal. Rebuilt both final
snapshots and repeated evaluation: all 22 source-backed expectations and reported
distances remained unchanged. Earlier candidate files in `data/address-routing/`
are superseded investigation artifacts.

The unchanged existing **27-trip coordinate suite** passes, including isolated
Training Station travel/unreachability, barriers, divided/crossing roads, destination
through restrictions, one-ways, prohibited turns and detours. The **26-case geocoding
suite** passes on both retained August lookup and the candidate. Regional data and
network acquisition remain outside routine tests.

## Snapshot and verification

Candidate: `data/address-routing/candidate-v3.sqlite`; independent rebuild:
`data/address-routing/rebuild-v3.sqlite`. Both were built offline from the unchanged
lookup bundle/checksum and retained PBF using `cmd/refresh build`. The active
selection was never switched. Graph format **3**, profile **driving-distance-v3**,
adds endpoint evidence while keeping all v2 driving segments, bans and guards.
Versions 1 and 2 remain readable for coordinate requests, with explicit address
routing unavailability. Unknown/mismatched versions fail loading.

| Artifact | SHA-256 |
| --- | --- |
| Candidate SQLite | `3aac27bfb69e3c4e51973c71753b70fa2700655fb5a94185d394846b8cc2d1a6` |
| Rebuild SQLite | `59e59e229603415c11274e4dc7c954e1ca596866e574a3f8531e115881e9041b` |
| Identical graph payload | `304e3f1edcdfbae25e837875e252ada65e542daec0392c176e6d7d7d1da07724` |
| v2 → v3 comparison report | `2b42cecaebcc24891655e5f0f7c76de0791134afe8a48f1c3b9e716be36d6a15` |

Every logical table, including graph payload and lookup/provenance/history/FTS,
is identical between independent builds. Comparison preserves **11,602 IDs**,
with zero entity/relationship changes and zero validation violations. Lookup tables
also match the original August baseline; refresh metadata records the build history.
The graph retains **622,348 nodes, 657,190 segments, 1,173 banned paths, 856 destination
segments and 125,127 guards**. New evidence includes **11,942 complete local area
rings, 35,419 road evidence records** (including names of longer roads without
clipped geometry), and six parking entrances. All source raw records, source IDs,
versions, attribution and original PBF checksum remain available. Source-way node
order/tags and shared coordinates are validated at load.

Passed gofmt, `go test ./...`, `go vet ./...`, `git diff --check`, the address
HTTP benchmark, retained coordinate/geocoding benchmarks, reproducible logical-table
comparison and snapshot validation. Isolated temporary deployment cycles cover
lookup-only → v3 → lookup-only and v2 → v3 → v2, including actual loaded profile,
address availability, Places identity and eight Bellevue geocoding results.
No tests switch `data/deployment.json`. The retained v1 graph also passed the
existing coordinate observation suite under the current loader.

The production demo now accepts two address strings and submits one Compute Routes
request. Browser verification on the separate loopback candidate checks address
routing, source/road markers and gaps, explicit ambiguity/unit errors, mixed inputs,
loading and clearing. Property access and entrance uncertainty remain visible.
Generated reports, benchmark responses, source inspection and server logs remain
in ignored `data/address-routing/`. No commit or push was made.

Remaining limits: strict unique identities; sparse entrance associations; only
simple building/parking rings; no property ownership, complete obstacle/water or
walking-path model; conservative 50/40/100 m limits; finite endpoint/extract coverage;
fixed passenger-car profile; no traffic/time estimates; and substantial immutable
graph startup memory. Passing source-backed invariants does not establish surveyed
entrance accuracy, road legality, source completeness or production capacity.
