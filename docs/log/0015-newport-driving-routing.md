# First Newport driving-routing milestone

2026-09-07 (America/Los_Angeles; verification extends into September 8 UTC) ·
Working tree based on `c4a6023a78044855d18002c6be0147167569704b`.

Historical implementation and verification record. Current behavior and commands
are maintained in [routing.md](../routing.md), [refresh.md](../refresh.md) and
[README](../../README.md). This milestone uses the existing lookup bundle and
retained Geofabrik extract. No supplemental address acquisition/enrichment,
public-ID changes, commit, push or active deployment switch was performed.

## Contract and implementation decisions

Selected Google **Routes REST v2**, `POST /directions/v2:computeRoutes`, with two
coordinate waypoints, driving, traffic-unaware behavior, explicit GeoJSON encoding
and a required distance/polyline field mask. The implementation returns a single
shortest-distance route, not Google's ranking or fastest-route semantics. It
rejects durations, traffic, address/place-ID waypoints, intermediates, modifiers
and other unavailable behavior. Empty routes plus a local explanation report
unreachable endpoints; geographic/snap errors identify the failing endpoint.
Legacy snapshots return routing unavailable rather than fabricated data.

The maintained contract links the current official Google Compute Routes,
Waypoint and field-mask references inspected on this date. Compatibility covers
only the named request/response subset, not every documented Google field.

Routing lives in `internal/routing`, independently of Places and geocoding. The
API translates Google shapes; the UI waits for explicit lookup/candidate selection
and passes coordinates. Existing ambiguous address results remain distinct.

Chose a directed adjacent-node graph with full source geometry, and Dijkstra over
last-edge plus restriction-history state. Prohibited-path prefix/failure links
handle both node turns and via-way sequences. This keeps one Go service and uses
existing PBF and SQLite dependencies; no routing engine binary, service, library,
frontend build system or runtime infrastructure was introduced.

Endpoint coverage stays the inclusive preview rectangle
`[-71.33,41.47,-71.29,41.51]` in longitude/latitude. Routing coverage is the whole
retained Rhode Island extract, so an origin/destination pair can take a detour
outside the lookup bounds. Nearest eligible segment snapping has a 100 m inclusive
limit, reports both gaps and excludes motorway/trunk mainlines and links as
endpoints. Route distance is metres along the road geometry, excluding snap gaps.
The driver profile and conservative exclusions are detailed in the maintained guide.

## Actual retained source findings

Input: `data/rhode-island-260801.osm.pbf`, Geofabrik 2026-08-01, SHA-256
`49a96292a8c6ee308c4d5c18f0462482170dfff12ff1dbaafd4fa171b6c4dfa9`.
The importer verifies this existing source-lock pin before parsing. It does not
fetch or accept an unpinned replacement.

The existing `readStreets` adapter only retained named highways touching the
preview and skipped relations. Its 880 lookup street entities were therefore
unsuitable as a driving graph. A separate concrete adapter now reads all highway
ways and restriction relations, then makes a second pass for needed nodes. It
keeps the named-street lookup import and all published IDs intact.

Complete inspection found:

- **5,809,658 nodes, 512,496 ways, 6,928 relations**; 132,120 highway ways.
- **1,349 restriction relations**, including **68 with via-way members**.
- **4,392 highway ways** with a node in the preview, across driving and other
  classes; 413 explicit `oneway=yes` and 22 explicit `oneway=no` among these.
- **24 restriction relations** touching preview highway ways. These include two
  conditional relations, two via-way relations and one malformed relation.
- Preview barrier nodes include 1,047 kerbs, 197 gates, 100 bollards, four lift
  gates, two unspecified barriers, a chain and a swing gate. These are raw nodes
  inside the rectangle, including pedestrian features, not counts of blocked
  driving junctions.
- The full source has public, private, customer, destination, delivery, permit,
  unknown and mode-specific access values; reverse one-way and alternating roads;
  conditional access/direction rules; and vehicle dimension/weight limits. These
  fields informed the concrete profile rather than being silently discarded.

Specific preview restriction evidence:

| Relation | Source maneuver | Implemented handling |
| --- | --- | --- |
| 8127113 | America's Cup Avenue → Poplar Street, no left turn Mo–Fr 06:00–09:00 | Prohibited at all times; condition is not evaluated |
| 8127119 | America's Cup Avenue → Elm Street, same morning condition | Prohibited at all times |
| 20445534 | Memorial Boulevard West, via Spring Street, back to Memorial Boulevard West; no U-turn | Three-directed-segment prohibited path |
| 20750731 | America's Cup Avenue, via unnamed way 1523271080, to the opposite avenue way; no U-turn | Nine-directed-segment prohibited path |
| 13427601 | `only_straight_on`, via node 201111331 and to way 410216581, **missing from member** | Conservative closure at the via node, retained in source decisions |

Node sharing, not geometry intersection, determines connectivity. A bridge or
crossing at another level does not get a junction from the drawing alone.
Access and barriers remove prohibited travel; explicit one-way direction and
roundabout defaults control the remaining directed edges. Unknown/conditional
access or direction closes the affected feature. Simple recognized conditional
turns apply continuously; malformed/compound restrictions close known via nodes
or member ways. Only-turn targets excluded by the profile do not free their
approaches to take other exits.

The final graph has **620,177 nodes**, **654,511 segments**, and **81,172 included
ways**, of which **47,961 are unnamed**. The source measured bounds are
`[-71.8610008,41.1488574,-71.1029644,42.035139]`; this is not continuous endpoint
coverage. There are 1,156 compiled prohibited paths from 1,113 enforced relations:
1,055 unconditional and 58 enforced at all times. Twenty-seven unsupported or
malformed relations cause conservative closures; 209 have no traversable maneuver
under the profile (with only-turn approaches still constrained). There are 1,157
blocked relevant nodes, including conservative restriction closures.

The payload retains all 132,120 highway raw records, restriction records and
relevant tagged-node records, their IDs/versions and decisions; source nodes keep
IDs, versions and coordinates. Full original node metadata remains in the pinned
PBF. Segment references identify a source way and adjacent-node ordinal within
this pinned source; they are not new permanent public entity IDs across topology
edits.

## Candidate and reproducibility

Final candidate: **`data/routing/openmaps-routing-v2.sqlite`**, served separately
on **127.0.0.1:8082**. The earlier first build and independent rebuild remain in
ignored `data/routing/`. The new `-routing-pbf` option works through both fresh
import and refresh builds, inside unpublished temporary files.

The optional `routing_graph` table holds an independently versioned immutable
payload and content hash. Snapshot loading validates both the checksum and graph
references; a missing table supports old snapshots, whereas present but malformed
or unsupported data fails loading. Comparison exposes both routing summaries and
hashes and still covers the whole file with its existing fingerprint.

| Artifact | SHA-256 |
| --- | --- |
| Retained August baseline | `b76ee297a476a67ed9883c1d5f8fb6ede17e5c4fba5525a3677600be824e3d13` |
| Final routing candidate database | `6f187db342763d43ef33159801bd3cb2de642a2627b107e78a635bd0ac3b4278` |
| Independently rebuilt database | `c067843682093ffd828e4b8e9fe4e37c4e7fca2e6ff7196f223dea1cc59a70ea` |
| Graph payload in both builds | `aada54b83c375638325421a148ea591f89dfea7b4afd4727e5a3e36ee1e5fd50` |
| Baseline → final candidate comparison | `f60092a7075de515a88fb8f4b39d8fd2f3153d7655374e88ac68011fcf021eae` |

The two final-code builds have **byte-identical graph payloads** and identical
logical rows in entities, source records, attribute provenance, relationships,
metadata and FTS. Different SQLite file hashes are expected; file byte identity
is not the snapshot rebuild contract. Source-derived graph references and all
existing public IDs reproduce.

The full comparison reports **11,602 continuing public IDs, zero additions,
absences, entity changes, relationship changes or validation violations**.
Counts remain 2,173 businesses, 8,545 addresses, 880 lookup streets and four areas.
No existing source record, identity mapping, source lock or active snapshot was
modified.

The integration test performs baseline → candidate → baseline using a **temporary
isolated state file**. It exercises actual report comparison, reviewed report
fingerprints, validation, live loading and rollback; Places IDs and eight-way
Bellevue ambiguity survive. Routine fixtures also reject corrupt graph selection
and verify that legacy rollback changes routing availability back to false.
This is test-only selection, not activation of the user's deployment.
`data/deployment.json` remains the original August/current and historical
July/previous selection; its SHA-256 remains
`d7070c4c86ff7221ea2691aae07fe37436b5d37fc878e12c7b2c5aa1c2e90aff`.

## Source-backed route evaluation

The checked-in nine-case suite is in
`internal/routing/testdata/newport.json`. Six trips produce routes, two water or
waterfront points are unsnappable, and one point is just beyond the eastern endpoint
boundary. Coordinates include retained source address/business points and
arbitrary boundary coordinates; they are not hard-coded supported routing pairs.

| Trip | Road distance | Snap gaps, origin / destination | Leaves preview |
| --- | ---: | ---: | --- |
| White Horse Tavern → 50 Bellevue | 1,239 m | 8.44 / 21.75 m | No |
| 50 Bellevue → White Horse Tavern | 966 m | 21.75 / 8.44 m | No |
| South boundary, Beacon Hill → 50 Bellevue | 3,541 m | 17.11 / 21.75 m | Yes |
| North boundary, Park Holm → east boundary, Valley Road | 2,303 m | 3.30 / 24.72 m | Yes |
| Valley Road → Park Holm | 2,303 m | 24.72 / 3.30 m | Yes |
| South Beacon Hill → east Valley Road | 7,025 m | 16.98 / 24.74 m | Yes |

The first pair's asymmetry follows the directed source network. The nearest
White Horse road is Farewell Street, and the Bellevue source point snaps to
Redwood Street; neither snap claims the actual property entrance. The southern
trip goes through Beacon Hill/Brenton/Harrison before returning through Carroll,
Thames and the inland streets. The northern trip uses Eisenhower, Hillside,
Dexter and West Main Road before returning toward Valley. These demonstrate why
clipping the driving graph to the preview would lose useful detours.

Every route's geometry is checked against source-adjacent node pairs, legal edge
direction, road distance and all compiled prohibited paths. HTTP response shape
and distance are exercised too. Initial local HTTP samples took roughly 27–58 ms
for successful trips; the integration test includes extra geometry and HTTP
checks and is not a production latency benchmark. The fixtures and comparisons
establish deterministic source fidelity, not independently surveyed road legality,
coverage completeness or property-access rights.

## Browser verification

The installed in-app browser rendered the separate candidate's real MapLibre/
Protomaps map. Observed through real UI actions:

- White Horse autocomplete → details → origin, then standalone 50 Bellevue →
  destination → **1.24 km**, blue geometry, A/B markers and 8.4/21.8 m snap gaps.
- `364 Bellevue Avenue` in forward geocoding still showed **eight distinct
  candidates**, with routing-selection buttons disabled until a candidate was
  chosen. Choosing the second preserved its ID and the ambiguity notice.
- The chosen address at 41.478658, −71.306828 plus a map destination at about
  41.47954, −71.30471 produced **647 m**, with 14.2/3.6 m snap gaps.
- Switching the map action cleared existing route geometry before another
  endpoint was chosen. Both origin and destination could be set directly by map
  interaction, without any geocoding fallback or preset pair.
- A water origin at about 41.49299, −71.32389 showed **“No suitable driving road
  within 100 metres of the origin”**, with no route line. Loading was visible and
  calculation disabled during the request.
- Clear route removed both endpoint markers and restored default reverse lookup
  map action. Existing lookup source markers kept their separate meaning.
- A separate read-only server on the retained baseline showed **routing unavailable
  in this snapshot** and disabled calculation, while retaining lookup controls.

The narrow visible viewport was checked with screenshots of route geometry and
controls. No JavaScript errors were reported in inspected logs. The pre-existing
MapLibre missing `townhall` sprite warning remained, without blocking map or route
rendering. Browser libraries, fonts and sprites still require their existing
external hosts. No standalone browser runner or npm tooling was added.

## Verification and remaining limits

Passed `gofmt`, `go test ./...`, `go vet ./...`, `git diff --check`, content/link
review, the nine-case source-backed routing suite and isolated snapshot cycle,
and the existing **26-case geocoding benchmark on both baseline and candidate**.
Routine synthetic tests include PBF-decoded access, barriers, only/no turns,
conditional and malformed restrictions, via-way approaches/side entrances,
excluded only-turn targets, same-way U-turns, one-way and partial-edge travel,
disconnected roads, crossings without a shared node, snapping, API errors and
snapshot corruption/rollback. Network acquisition is absent from routine tests.

Remaining limitations are deliberate and documented: shortest distance rather
than speed/comfort ranking; no travel time/traffic/instructions; no heading or
entrance inference; no private/customer/destination-access routing; conservative
exclusion of vehicle-dimension-tagged roads and unevaluated conditions; no
immediate segment U-turns; finite source/endpoint coverage and basemap extent;
source incompleteness; substantial immutable-graph startup memory. The first
milestone does not establish vehicle-specific navigation or production capacity.

Generated data, PBF inspection evidence, graph/rebuild fingerprints, comparisons,
trip results, regression outputs and server logs are retained under ignored
`data/routing/`. Maintained documentation explains current behavior; this entry
records the decisions and observations for this milestone.
