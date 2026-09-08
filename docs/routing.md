# Newport driving routing

Open Maps implements **Google Routes API, REST v2 Compute Routes** as a small
coordinate-only subset at `POST /directions/v2:computeRoutes`. It returns one
**shortest-distance** driving route on the supported static OSM graph. This is
not Google's ranking, a fastest route, navigation guidance or SDK compatibility.
No traffic, travel time, duration, speed, turn instructions or unavailable fields
are inferred. The source is Geofabrik's retained Rhode Island OSM PBF,
2026-08-01; address enrichment is not part of routing.

Contract checked against current official Google documentation on 2026-09-07:
[Compute Routes](https://developers.google.com/maps/documentation/routes/reference/rest/v2/TopLevel/computeRoutes),
[Waypoint](https://developers.google.com/maps/documentation/routes/reference/rest/v2/Waypoint)
and [response field masks](https://developers.google.com/maps/documentation/routes/choose_fields).
Those references establish the endpoint, coordinate waypoint shape, metre distance,
GeoJSON option and field-mask mechanism. The narrower choices below are Open Maps
implementation decisions, not claims about everything Google supports.

## API contract

```sh
curl -sS http://127.0.0.1:8082/directions/v2:computeRoutes \
  -H 'Content-Type: application/json' \
  -H 'X-Goog-FieldMask: routes.distanceMeters,routes.polyline.geoJsonLinestring' \
  -d '{
    "origin":{"location":{"latLng":{"latitude":41.49138952,"longitude":-71.31373108}}},
    "destination":{"location":{"latLng":{"latitude":41.48654393,"longitude":-71.30830418}}},
    "travelMode":"DRIVE",
    "routingPreference":"TRAFFIC_UNAWARE",
    "polylineEncoding":"GEO_JSON_LINESTRING",
    "polylineQuality":"HIGH_QUALITY"
  }'
```

Required inputs are `origin`, `destination`, and
`polylineEncoding: "GEO_JSON_LINESTRING"`. Each waypoint must contain exactly
`location.latLng.latitude` and `location.latLng.longitude`, finite numbers in
WGS84 decimal degrees. Optional `travelMode`, `routingPreference` and
`polylineQuality` accept only `DRIVE`, `TRAFFIC_UNAWARE` and `HIGH_QUALITY`,
respectively. Omission uses those implemented choices. All source geometry
vertices are returned; the overview/simplification and encoded-polyline options
are unsupported. JSON is limited to 16 KiB; nulls, duplicate keys, unknown fields,
wrong types and trailing JSON are rejected.

One response mask is required: `X-Goog-FieldMask`, `fields`, or `$fields`.
Supported paths are `routes.distanceMeters`, `routes.polyline`, and
`routes.polyline.geoJsonLinestring`, individually or comma separated. Broad `*`
and `routes` masks are rejected so they cannot imply fields such as duration.
GeoJSON is selected as a complete object, not through individual coordinate paths.
An optional query `key` or `X-Goog-Api-Key` header is accepted without
authentication, consistent with the local demo. Other query parameters and
duplicate query parameters are rejected.

Address strings, place IDs (Google or Open Maps), intermediate waypoints, heading,
side-of-road, vehicle stopover, travel modes other than driving, routing modifiers,
avoidances, alternatives, departure/arrival times, traffic models, language/units,
route matrix, toll prices and duration fields are unsupported. The API does not
silently resolve addresses or pick an ambiguous address candidate. The browser
coordinates existing Places/geocoding results and sends the chosen coordinates.

A success has `routes: [{distanceMeters, polyline: {geoJsonLinestring}}]`, projected
by the mask. `distanceMeters` is the rounded integer road distance in metres;
GeoJSON is a `LineString` whose coordinates are **[longitude, latitude]**. It
starts and ends on the snapped road, not necessarily at the requested points.
The always-present local `openmaps` extension reports `outcome: "routed"`,
`profile`, `snap_limit_meters`, both snaps (`requested`, `point`, `distance_meters`,
`nearest_distance_meters`, reproducible source segment reference; optional
`selection_reason` and `destination_access`), source release and OSM attribution/URI. Requested
coordinates and source entity identities are not changed. Equal snapped endpoints
return zero distance and a two-position LineString with equal positions.

| Outcome | HTTP / envelope |
| --- | --- |
| Supported route | 200, one route, `openmaps.outcome=routed` |
| Snapped endpoints cannot connect | 200, `routes: []`, `openmaps.outcome=unreachable` and an explanatory message plus both snaps |
| Outside endpoint coverage | 400, `error.status=INVALID_ARGUMENT`, `openmaps.outcome=outside_coverage` and `endpoint` |
| No suitable road within snap limit | 400, `INVALID_ARGUMENT`, `openmaps.outcome=unsnappable` and `endpoint` |
| Invalid/unsupported request | 400, Google-style `error` with code, status `INVALID_ARGUMENT`, and message |
| Snapshot has no graph | 503, `error.status=UNAVAILABLE` |
| Internal calculation failure | 500, `error.status=INTERNAL` |
| Wrong method | 405 with `Allow: POST` |

Unreachable destinations use Google's empty-route convention; the reason extension
and geographic failures are explicit local behavior. No-route is not proof that
no legal real-world journey exists. There are no billing, quota or authentication
error implementations.

## Endpoint and graph coverage

Supported endpoints are arbitrary coordinates in the inclusive Newport preview
rectangle: longitude **−71.33 to −71.29**, latitude **41.47 to 41.51**. This is
not a municipal boundary. Water, private sites and disconnected roads do not
become routable merely by lying inside the rectangle.

The routing import reads the **entire retained Rhode Island extract**, without
clipping ways to the lookup rectangle. This lets a journey leave and reenter the
preview, including Beacon Hill/Brenton Road detours south of the preview and connections
north of Park Holm. Its measured road-node extent is approximately
`[-71.8610008, 41.1488574, -71.1029644, 42.035139]`; this extent is not a claim of
continuous graph coverage throughout that rectangle. The extract has a finite
Geofabrik boundary, islands and disconnected components. Endpoints outside the
preview remain unsupported even when graph data is present there. Basemap tiles
are separate and can run out before the routing graph does.

Each endpoint is evaluated independently, before route search. The closest eligible
segment is found within **100 metres inclusive**, using local equirectangular
projection and spherical distance (Earth radius 6,371,008.8 m). Distance ties within
1e-8 m use the lexicographically smallest stable segment reference. Motorway/trunk
mainlines and links allow through travel but never snapping; proximity must not
create an entrance. Source node identity, not a crossing on the map, joins roads.

Graph v2 examines at most the **eight nearest eligible segments**. A farther street
may replace the nearest service-road snap only under all these conditions:

- At most **30 m** from the request and **10 m farther** than the nearest snap.
- Both roads are bidirectional, unrestricted, and not mapped as elevated/tunnel
  roads. Destination-only roads are never eligible for this preference.
- A shared source junction is reached along the **same service way**, then the
  street segment, within **20 m of road geometry** between projections. The local
  walk examines at most **32 nodes**; geometry vertices do not require a new way.
- No visited junction participates in a prohibited maneuver; no closed segment
  or barrier approach can bridge the walk. The farther off-road connector cannot
  strictly cross a third mapped road or excluded motor-road segment.

Eligible alternatives are considered by distance and reference, never by whether
they produce a trip or shorten it. If none passes, retain the nearest snap. A
nearest disconnected road remains disconnected; a different component, divided
carriageway, grade-separated crossing, forbidden turn or gated approach does not
justify a jump. Failure is explicit `unreachable`, without repeated route-driven
resnapping. Selecting a farther street reports its reason and nearest distance.

The importer retains excluded motor-road geometry as **snap guards**, including
private/customer roads and removed barrier approaches. If one is closer to the
request than the best eligible snap by more than **0.1 m**, snapping fails. An
ineligible destination-only segment acts as a guard too. The 0.1 m guard tolerance
allows common source junctions; it does not grant restricted access. These checks
cannot establish entrances, property boundaries, fences, water crossings or a
legal off-road path. All displayed connectors remain explicitly unverified.

Interior snaps create partial directed edges for the request. A junction snap
permits any legal initial heading; a one-way interior origin cannot drive backward.
Snapping neither splits persistent graph identity nor changes the source entity.
There is no heading, entrance, level or building-containment inference, and no complete
assessment of obstacles between an off-road point and its snap. A nearest road
can therefore be unsuitable as an actual property entrance. The displayed distance
excludes these off-road gaps; they are reported separately. Immediate reversals
on the same segment are disallowed everywhere, including dead ends; choosing an
origin supplies a fresh initial heading. Longer legal loops remain possible.

## Driving profile and connectivity

`driving-distance-v2` is an ordinary passenger-car profile. It uses OSM node identity
for junctions, never a geometry crossing or equality of coordinates. Bridges and
tunnels stay separate unless they share an OSM node. Adjacent source vertices
form segments, preserving full geometry and unnamed roads. Roundabouts, split
carriageways and ordinary intersections use those same rules.

Included highway classes: motorway, trunk, primary, secondary, tertiary, their
`_link` variants, residential, unclassified, living_street and service. Footways,
paths, pedestrian roads, cycleways, tracks, ferries, construction/proposed roads,
areas and impassable/closed roads are excluded. Unnamed service roads are included
when their tags permit the profile; absence of an access tag is treated as the
road-class default, not independent verification of public ownership.

Access precedence is `motorcar` → `motor_vehicle` → `vehicle` → `access`.
For each direction, a directional value wins over the nondirectional value at
the same specificity. Allowed values are absent, yes, permissive and designated.
Other values (including no, private, permit, customers, delivery, discouraged and
unknown) remain excluded. A coordinate request or selected lookup result supplies
no private, customer, delivery or permit authorization.

**Destination-only access** is retained per directed edge. A qualifying endpoint
must lie on the mapped restricted road, within **0.1 m** of its projection solely
for coordinate rounding. At a source node shared with any unrestricted motor-road
direction, the endpoint does **not** qualify: a public boundary is no reason to
travel through the restricted area. Otherwise an interior segment point or internal
restricted-road node qualifies. Off-road property/address/POI coordinates do not
qualify by proximity, guessed containment, a lookup relationship or presumed intent.
Choosing an on-road point still does not establish a property entrance.

Restricted segments connected by source nodes form destination zones; public roads
do not merge zones. The route may use its origin zone as a prefix, leave for public
roads, then enter its destination zone as a suffix. Once entered from public roads,
it cannot leave the destination zone for another public shortcut. Other restricted
zones are forbidden. A trip with both endpoints inside one zone may remain there
or leave and return. One-ways, barriers and full turn history still apply in every
phase. Destination-tagged node barriers remain conservatively closed; no gate
permission is inferred from the endpoint rule.

`oneway=yes/1/true`, `-1`, and `no/0/false` are supported. Motorcar/motor-vehicle/
vehicle direction overrides take precedence over general `oneway`. Roundabouts,
motorways and motorway links default to forward only unless explicitly overridden.
Alternating, reversible and unknown directions cause closure. Bicycle and bus
exceptions do not authorize cars.

A restrictive node access tag blocks incident travel. Gates, lift/swing gates,
bollards, chains, raised/unknown kerbs and unknown barriers block incident segments.
A gate with explicit allowed access and absent/`no` lock tagging is passable;
unknown lock values remain closed. Toll booths, cattle
grids and flush/lowered kerbs are passable unless access prohibits them. Physical
bollards are not opened by an access label. This conservative incident-segment
removal can remove a short approach as well as the actual barrier crossing.

Supported turn relations contain one `from` way, one `to` way and either one
`via` node or an ordered sequence of `via` ways. Directed contiguous via-way paths
are enumerated from actual shared nodes; all their intermediate segments are
retained in the restriction. `no_left_turn`, `no_right_turn`, `no_straight_on`,
`no_u_turn` and the corresponding `only_*` values compile into prohibited paths.
Only-turns block other departures, including exits along a via-way sequence.
For a same-way node U-turn restriction, reversing the same segment is distinguished
from continuing straight. Applicable car/vehicle restriction keys and `except`
values are respected. This is not a lane-aware maneuver model.

**Conditions are not evaluated.** A single recognized conditional turn restriction
applies at all times, including the two Newport morning no-left-turn relations.
Conditional access, one-way and vehicle-dimension rules close the affected way or
node. Unsupported compound turn expressions, malformed members and unknown turn
values close their known via nodes; without a usable via node they close member
ways. Source records retain the reason. An excluded only-turn target never frees
its approach to take an illegal alternative exit. Enumeration limit failures
stop the build, rather than omit a restriction.

The fixed loaded-car assumptions are **1.9 m height, 2.0 m width including mirrors,
5.0 m length, 1.8 metric tonnes actual total weight, and 1.1 tonnes maximum load on
any axle**. No trailer, roof load, adjustable vehicle parameters or extra clearance
margin is modeled. These are implementation assumptions, not measurements of the
user's car or a guarantee that every passenger vehicle fits.

`maxheight`, `maxwidth`, `maxlength`, `maxweight` and `maxaxleload` apply to ways and
relevant nodes. Numeric limits allow equality (1e-9 conversion tolerance); lower
limits close the direction. Dimension defaults are metres; mass defaults are metric
tonnes, including in US data. Supported explicit units are `m`, `t`, `kg`, `st`
(short ton = 0.90718474 tonnes), `lt` (long ton = 1.0160469088 tonnes), and `lbs`
(0.00045359237 tonnes). Dimensions also accept complete `feet'inches"` notation
with inches less than 12; `14'9"` means 4.4958 m. Zero/negative values, decimal
commas, unitless lists, ambiguous unit names, incomplete feet/inches and unknown
values close the affected direction. No guessed repairs are made.

Directional values override nondirectional values at the same scope; legal and
`:physical` limits both apply. Vehicle/motor-vehicle/motorcar dimension scopes are
also checked conservatively alongside generic limits. Unsupported applicable
suffixes, including lane lists and conditions, close the feature. `:signed` metadata
is not itself a limit. Explicit HGV, articulated-HGV, bus, PSV and emergency limits
are unrelated to this car profile, including their conditions. `none`/`default`
retain the ordinary-car default assumption; other nonnumeric values do not.
A `height_restrictor` node is passable only with an explicit compatible height
limit and otherwise permitted access; bollards and other physical barriers retain
their existing conservative behavior. Nodes with directional limits are closed
if either direction is incompatible because node travel direction is not modeled.

Current OSM semantics were consulted on 2026-09-08:
[units](https://wiki.openstreetmap.org/wiki/Map_features/Units),
[height](https://wiki.openstreetmap.org/wiki/Key:maxheight),
[width](https://wiki.openstreetmap.org/wiki/Key:maxwidth),
[length](https://wiki.openstreetmap.org/wiki/Key:maxlength),
[weight](https://wiki.openstreetmap.org/wiki/Key:maxweight),
[axle load](https://wiki.openstreetmap.org/wiki/Key:maxaxleload),
[access values](https://wiki.openstreetmap.org/wiki/Key:access), and
[destination access](https://wiki.openstreetmap.org/wiki/Tag:access%3Ddestination).
The car dimensions, strict parsing, endpoint qualification and snap bounds are
local policy choices; OSM does not establish those implementation thresholds.

## Storage, builds and snapshots

`internal/importer/routing.go` owns PBF interpretation. It first reads highway ways
and restrictions, then reads only needed nodes on a second pass; it does not keep
all 5.8 million extract nodes in memory. Missing references fail the build. No new
dependency, process, routing service or network acquisition was added.

A new snapshot may add a `routing_graph` SQLite table with one immutable,
independently versioned JSON payload and its SHA-256. The payload contains source
node IDs, node versions/coordinates, source-derived segment IDs (`way:ordinal`),
directions, snapping eligibility, prohibited paths, full highway/restriction raw
records, relevant tagged-node raw records, source decisions, release/URL/checksum,
measured bounds, counts and attribution. This is a graph payload, not a lookup
entity table: it does not assign new public Places IDs or merge roads and addresses. Segment
references reproduce the pinned way and node ordinal; they are not permanent
public entity IDs when a later source edit changes a way’s node sequence.
The retained verified PBF supplies full original node metadata for rebuilds.

The loader checks the payload checksum, format/profile, valid nodes, unique segment
IDs, node/source references and directed adjacency of restriction paths. It builds an
immutable in-memory adjacency graph and a prefix/failure-link automaton for prohibited
paths. Dijkstra searches states that retain the last directed edge, relevant
restriction history and destination-access phase, minimizing accumulated spherical road length. Endpoint scans
are bounded by the snapshot and snap distance; no spatial database extension is
required at this scale. The graph adds roughly 180 MiB to the SQLite snapshot and
uses substantial startup memory; planet-scale loading and production concurrency
are outside this milestone.

Build a separate candidate offline from the unchanged lookup bundle:

```sh
mkdir -p data/routing-quality

go run ./cmd/refresh build \
  -baseline data/openmaps.sqlite \
  -bundle data/newport.json \
  -checksum imports/newport.bundle.sha256 \
  -routing-pbf data/rhode-island-260801.osm.pbf \
  -candidate data/routing-quality/candidate-v2.sqlite

go run ./cmd/refresh compare \
  -baseline data/openmaps.sqlite \
  -candidate data/routing-quality/candidate-v2.sqlite \
  -report data/routing-quality/comparison.json

go run ./cmd/server -db data/routing-quality/candidate-v2.sqlite -listen 127.0.0.1:8082
```

Outputs must be new filenames. Use another candidate name if these already exist.
`cmd/import` also accepts `-routing-pbf` for a fresh non-refresh build. The PBF must
match the lookup manifest's filename and pinned SHA-256. Builds publish only after
verification; no source lock or existing database is rewritten. Byte-identical
graph payloads and stable graph identifiers are expected from identical inputs and
profile; SQLite file bytes need not be identical.

Graph format **2** pairs only with `driving-distance-v2` and includes directional
destination flags, service/elevation classifications and excluded-road snap guards.
Retained format **1** / `driving-distance-v1` graphs remain readable with their
original nearest-segment policy and original compiled exclusions. The API reports
the loaded snapshot's profile. Unknown versions or mismatched profiles explicitly
fail loading and snapshot validation; they are never silently downgraded.

Snapshots without the routing table remain readable. They explicitly report
`routing_available: false` in `/healthz`, and route requests return 503. A corrupt
or partially present table is a loading error, never silently “unavailable.”
Comparison reports optional baseline/candidate routing metadata and graph digests,
including graph removal; whole-database review checksums still cover every byte.
The existing [review and rollback workflow](refresh.md) validates routing too,
and atomically loads all three domains before replacement. Old reports comparing
two snapshots without routing retain their existing serialization. No production
or active demo selection is changed merely by building or serving a candidate.

## Browser and verification

Choose a Places result or an explicit geocoding candidate, then **Use selection as
origin/destination** in Driving route. Alternatively change **Map click** to set
an exact origin or destination and click the map. The default map action remains
reverse address lookup. Ambiguous addresses never populate routing automatically.

**Calculate driving route** shows a blue route, A/B endpoint markers, road distance
and both snap gaps. Requested points are A/B; snapped road points are A′/B′ with separate
coordinates. Dashed orange connectors show the unverified off-road gaps and are
excluded from road distance. A farther snap explains its selection and nearest
distance. Unreachable results show snaps and an explanation without a driving line.
Endpoint changes and new map-selection actions immediately
clear the old route and abort in-flight requests. Loading disables calculation;
failures leave no stale geometry. **Clear route** resets endpoints, geometry and
map action. Lookup markers and source precision retain their existing meaning.
Retained snapshots show routing unavailable. Map-library failures still leave
lookup and route-distance responses usable, but geometry cannot be displayed.

Routine `go test ./...` fixtures are small and offline. They cover directed travel,
partial-edge snapping, exact junction endpoints, snap limits, disconnected/crossing
roads, barriers, access precedence, only/no turns, via-way history, conservative
conditional/malformed restrictions, API masks/errors, checksums and atomic rollback.
The source-backed trip suite is explicit and does not acquire data:

```sh
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/routing-quality/candidate-v2.sqlite" \
  go test -tags=integration ./internal/routing -run TestNewportRouting -count=1 -v
```

The [27-case benchmark](../internal/routing/testdata/newport.json) checks
source-tag evidence, required roads, forbidden destination shortcuts, geometric
distance bounds (not copied router outputs), off-rectangle detours, source-node adjacency,
geometry/distance agreement, one-way direction, prohibited paths, the API and an
isolated deployment cycle in a temporary directory. It also accepts a retained
routing graph as baseline to verify v1 → v2 → v1 profile loading and rollback. It leaves the real deployment
state untouched. These checks establish source fidelity and deterministic behavior,
not independently surveyed road legality or source completeness. See the
[historical initial findings](log/0015-newport-driving-routing.md) and
[historical route-quality evaluation](log/0016-newport-driving-quality.md).

To record old-graph observations without treating them as v2 quality expectations,
set `OPENMAPS_ROUTING_OBSERVE=1`, choose the old graph as `OPENMAPS_CANDIDATE`, and
run only `-run '^TestNewportRouting$'`. Source evidence, adjacency, one-way, banned
path and distance/geometry checks still run; v2 policy assertions do not. A passing
observation run is not a v2 quality pass. Historical v1 distances in the fixture
are observations only. Generated before/after output belongs in ignored `data/`.
