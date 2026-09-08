# Driving routing

Open Maps implements **Google Routes API, REST v2 Compute Routes** as a small
address/coordinate subset at `POST /directions/v2:computeRoutes`. It returns one
**estimated-time-optimal** driving route on graph v4, using the explicit,
uncalibrated speed model below. Distance and duration exclude off-road gaps.
This is not Google's ranking, traffic prediction, navigation guidance or SDK
compatibility. Retained graph v1–v3 snapshots still optimize distance and have no
duration estimates. The Newport source is Geofabrik's retained Rhode Island OSM PBF,
2026-08-01. Address routing uses existing lookup records; no supplemental address
source is acquired. A separate Oregon coordinate-only evaluation uses pinned
Oregon and neighboring detour coverage; see
[storage, scaling and Oregon scope](routing-scale.md).

Contract checked against current official Google documentation on 2026-09-08:
[Compute Routes](https://developers.google.com/maps/documentation/routes/reference/rest/v2/TopLevel/computeRoutes),
[Waypoint](https://developers.google.com/maps/documentation/routes/reference/rest/v2/Waypoint),
[address locations](https://developers.google.com/maps/documentation/routes/specify_location)
and [response field masks](https://developers.google.com/maps/documentation/routes/choose_fields)
and [traffic-unaware routing](https://developers.google.com/maps/documentation/routes/traffic-opt).
Those references establish the endpoint, address/coordinate waypoint union, metre distance,
GeoJSON option, duration strings in seconds, equality of `duration` and
`staticDuration` for `TRAFFIC_UNAWARE`, and the field-mask mechanism. The narrower choices below are Open Maps
implementation decisions, not claims about everything Google supports.

## API contract

```sh
curl -sS http://127.0.0.1:8087/directions/v2:computeRoutes \
  -H 'Content-Type: application/json' \
  -H 'X-Goog-FieldMask: routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline.geoJsonLinestring' \
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
`polylineEncoding: "GEO_JSON_LINESTRING"`. Each waypoint contains exactly one of
`address` (a string) or
`location.latLng` (both `latitude` and `longitude`, finite numbers in WGS84
decimal degrees). Address/address, coordinate/coordinate and either mixed order
are supported. Address strings use the existing geocoder’s exact house-number
and complete-street subset, including its context checks and explicit unit errors.
Optional `travelMode`, `routingPreference` and
`polylineQuality` accept only `DRIVE`, `TRAFFIC_UNAWARE` and `HIGH_QUALITY`,
respectively. Omission uses those implemented choices. All source geometry
vertices are returned; the overview/simplification and encoded-polyline options
are unsupported. JSON is limited to 16 KiB; nulls, duplicate keys, unknown fields,
wrong types and trailing JSON are rejected.

One response mask is required: `X-Goog-FieldMask`, `fields`, or `$fields`.
Supported paths are `routes.distanceMeters`, `routes.duration`,
`routes.staticDuration`, `routes.polyline`, and
`routes.polyline.geoJsonLinestring`, individually or comma separated. Broad `*`
and `routes` masks are rejected so they cannot imply unavailable navigation or
traffic fields.
GeoJSON is selected as a complete object, not through individual coordinate paths.
An optional query `key` or `X-Goog-Api-Key` header is accepted without
authentication, consistent with the local demo. Other query parameters and
duplicate query parameters are rejected.

Place IDs (Google or Open Maps), intermediate waypoints, heading,
side-of-road, vehicle stopover, travel modes other than driving, routing modifiers,
avoidances, alternatives, departure/arrival times, traffic models, language/units,
route matrix and toll prices are unsupported. The API resolves
addresses and selects road endpoints automatically in the same
request, or returns a structured failure. No candidate-selection or entrance-selection
exchange is required. Google’s `geocodingResults` response field, region bias and
plus codes remain unsupported; local source identity is in `openmaps`.

A v4 success has `routes: [{distanceMeters, duration, staticDuration, polyline: {geoJsonLinestring}}]`, projected
by the mask. `distanceMeters` is the rounded integer road distance in metres;
`duration` and `staticDuration` are equal strings such as `"139s"`, rounded
once to the nearest whole second after summing the selected road travel. They
exclude live and historical traffic, departure-time predictions and off-road
connectors. Fractional internal arithmetic does not establish second-level
accuracy. GeoJSON is a `LineString` whose coordinates are **[longitude, latitude]**. It
starts and ends on the snapped road, not necessarily at the requested points.
The always-present local `openmaps` extension reports `outcome: "routed"`,
`profile`, `snap_limit_meters`, both snaps (`requested`, `point`, `distance_meters`,
`nearest_distance_meters`, reproducible source segment reference; optional
`selection_reason` and `destination_access`), source release and OSM attribution/URI. Requested
coordinates and source entity identities are not changed. Equal snapped endpoints
return zero distance, `"0s"` duration when requested/available, and a two-position LineString with equal positions.

| Outcome | HTTP / envelope |
| --- | --- |
| Supported route | 200, one route, `openmaps.outcome=routed` |
| Snapped endpoints cannot connect | 200, `routes: []`, `openmaps.outcome=unreachable` and an explanatory message plus both snaps |
| Outside endpoint coverage | 400, `error.status=INVALID_ARGUMENT`, `openmaps.outcome=outside_coverage` and `endpoint` |
| No suitable road within snap limit | 400, `INVALID_ARGUMENT`, `openmaps.outcome=unsnappable` and `endpoint` |
| Address has no unique exact match | 400, `INVALID_ARGUMENT`, `openmaps.outcome=address_resolution_failed`, `endpoint`, `reason=no_match` or `ambiguous`, and `candidate_count` |
| Address has no acceptable road association | 400, `INVALID_ARGUMENT`, `openmaps.outcome=endpoint_association_failed` and `endpoint` |
| Invalid/unsupported request, including units | 400, `INVALID_ARGUMENT`, `openmaps.outcome=unsupported_input` |
| Address on retained graph v1/v2 | 503, `UNAVAILABLE`, `openmaps.outcome=address_routing_unavailable` |
| Duration mask on retained graph v1/v2/v3 | 503, `UNAVAILABLE`, `openmaps.outcome=time_estimate_unavailable`; distance-only requests still work |
| Snapshot has no graph | 503, `error.status=UNAVAILABLE` |
| Concurrent routing budget exhausted | 429, `RESOURCE_EXHAUSTED`, `Retry-After: 1`; server default is four in-flight requests |
| Internal calculation failure | 500, `error.status=INTERNAL` |
| Wrong method | 405 with `Allow: POST` |

Unreachable destinations use Google's empty-route convention; the reason extension
and geographic failures are explicit local behavior. No-route is not proof that
no legal real-world journey exists. There are no billing, quota or authentication
error implementations.

## Automatic address endpoints

```sh
curl -sS http://127.0.0.1:8087/directions/v2:computeRoutes \
  -H 'Content-Type: application/json' \
  -H 'X-Goog-FieldMask: routes.distanceMeters,routes.polyline' \
  -d '{"origin":{"address":"26 Marlborough Street"},"destination":{"address":"1 Resolute Road"},"polylineEncoding":"GEO_JSON_LINESTRING"}'
```

`internal/api` calls the existing geocoder independently for each address, in
origin/destination order. It requires **exactly one** retained identity after
normalized number/street and supplied context matching. Equal labels do not
establish equal identities. There is no retained preference or verified primary
structure for the eight Bellevue or twelve Connell records; these requests fail
as ambiguous. No first-ID selection, centroids, route-success ranking, nearby
house substitution, unit stripping or street/locality fallback is used. Explicit
units and unsupported address syntax fail. The standalone geocoding endpoint
still returns all its existing candidates unchanged.

The API compares the resolved source street with OSM `name`, using the existing
label normalization, and filters containing-area candidates against any retained
`addr:housenumber`/`addr:street` tags. Conflicting tags disqualify that area. Empty
tags are unknown. TIGER name components, POI names, postal codes and proximity to
a business are not street/access evidence. Routing receives coordinates and source
way/area IDs; it performs containment, snapping and graph operations without text
search. No public ID, source coordinate or lookup relationship is rewritten.

Graph **3 and 4** evaluate each address endpoint once, before searching for a route:

1. **Mapped access association.** The source point must lie inside a complete
   retained building or parking ring (1 mm boundary-rounding tolerance). A parking
   entrance must share an actual node with that containing parking ring and a
   permitted graph segment. Restricted parking-area tags also disqualify entrance
   associations and projections into the containing restricted parking area. A driveway must have an endpoint inside/on the
   containing building/site ring. Follow only source-connected driveway geometry,
   at most **150 m** and **32 settled nodes**, stopping at public-road junctions.
   A driveway arrival must be on an unrestricted, non-service, non-elevated road
   whose name matches the address street. The driveway may be private: its geometry
   associates the site with a public junction, but **none of the private approach
   is driven**. Barriers and closed approaches stay excluded from driving. The
   source-to-selected-point straight-line distance must be **≤100 m**. Choose the
   nearest supported point independently of trip success; distinct access nodes
   within **1 m** of equal distance fail as competing associations. Identical-node
   representations use source ordering. A mapped parking point can qualify its
   destination zone; a public boundary does not.
2. **Ordinary fallback.** If there is no supported access point within those bounds,
   consider the **eight nearest** eligible road segments within **50 m**. Exclude
   elevated roads and motorway/trunk snapping. A closer excluded motor-road guard
   or unqualified destination road by more than **0.1 m** rejects the endpoint.
   Choose nearest geometry; distance ties within **1e-8 m** use stable segment
   reference. Distinct competing ways within **1 m** fail unless their projections
   share a source junction within **20 m** along those segments, or the existing
   safe service-to-street check applies. A matching named street can replace a
   nearby service projection only under that existing check (≤30 m, ≤10 m farther,
   ≤20 m along roads, ≤32 nodes, bidirectional unrestricted surface roads).
   Crossing another mapped motor road or excluded guard rejects the connector.
   These rules do not establish an entrance or the legality of any off-road gap.
3. **Destination-only named street.** An off-road address may qualify only when its
   source street matches the **nearest** motor road’s explicit OSM `name`, the road
   is destination-only, and the projection is **≤40 m** away. The same guards,
   competition and connector-crossing checks apply. A different nearby street,
   unnamed restricted aisle or destination zone alone does not qualify. This is
   source-backed street arrival, not a claim about a property’s entrance. Private,
   customer, delivery and permit restrictions remain closed. Destination-zone
   prefix/suffix rules below prevent through shortcuts.

Limits are inclusive and are local policy choices, not surveyed accuracy claims.
No fallback searches another road after an unreachable route. Source node identity,
one-ways, prohibited maneuvers, barriers and destination phases govern all driving.
The retained parking data has no proved address-to-parking-entrance association for
the benchmark’s nearby waterfront addresses; a nearby entrance is not enough.
Simple rings are supported; multipolygon/site/associatedStreet relation inference,
parcel ownership, unseen fences, water obstacles and walking paths are not modeled.

Each routed/unreachable endpoint retains the existing snap fields and additionally
reports `selection_method`, `evidence` (OSM source references for address selections),
`uncertainty`, and `off_road_gap_meters`. For an address, `resolved_address` includes
`id`, `formatted_address`, `source_coordinate` (**[longitude, latitude]**),
`partial_context` and source `attributions`. `requested` remains the exact source
coordinate; `point` is the selected road coordinate. Methods are `coordinate_snap`,
`nearest_road`, `address_street`, `destination_address_street`,
`mapped_parking_entrance`, or `mapped_driveway_public_junction`.
`nearest_distance_meters` is omitted for mapped associations, which rank access
points rather than nearest-road projections. Global metadata gives coordinate
`snap_limit_meters=100`, `address_snap_limit_meters=50`, and
`access_point_limit_meters=100`.

`routes.distanceMeters`, `routes.duration` and `routes.staticDuration` include
**road travel only**. Gaps are straight-line
source-to-road displacement, not driving/walking distance or verified connections.
No entrance, access right or confidence percentage is fabricated. Geographic
association failures preserve any resolved address metadata without inventing a
selected road point. Address resolution failures have no resolved identity.

## Coordinate endpoints and graph coverage

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

Each coordinate endpoint is evaluated independently, before route search. The closest eligible
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
For coordinate requests there is no heading, entrance, level or building-containment inference, and no complete
assessment of obstacles between an off-road point and its snap. A nearest road
can therefore be unsuitable as an actual property entrance. The displayed distance
excludes these off-road gaps; they are reported separately. Immediate reversals
on the same segment are disallowed everywhere, including dead ends; choosing an
origin supplies a fresh initial heading. Longer legal loops remain possible.

## Driving profile and connectivity

`driving-time-v4` retains the v3 ordinary passenger-car access and endpoint profile. It uses OSM node identity
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

**Destination-only access** is retained per directed edge. A qualifying **coordinate** endpoint
must lie on the mapped restricted road, within **0.1 m** of its projection solely
for coordinate rounding. At a source node shared with any unrestricted motor-road
direction, the endpoint does **not** qualify: a public boundary is no reason to
travel through the restricted area. Otherwise an interior segment point or internal
restricted-road node qualifies. Off-road arbitrary/POI coordinates do not qualify by proximity or presumed intent.
Address requests use the separate evidence policy above; their mere existence
does not authorize a destination zone.
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

## Estimated-speed and elapsed-time model

Graph **4**, profile **`driving-time-v4`**, cost model **`estimated-driving-v1`**
persists one `WayCost` per included source way in the existing SQLite graph payload.
Each direction has effective `kph`, optional numeric `limit_kph`, and assumption
`notes`; original OSM tags, source version and checksum remain separate provenance.
`limit_kph` is the interpreted ceiling (including the conservative minimum of
conditional ceilings), not a claim that it applies at every time. Absence is
unknown, never an invented legal default. All driving restrictions above still
apply before costs are considered. Costs cannot open an excluded edge.

These are **local, uncalibrated model assumptions**, not measured Newport averages
or statutory speed limits. Missing speed information uses the following effective
road-travel speeds in **km/h**:

| Road class | Effective speed |
| --- | ---: |
| motorway / trunk | 80 / 65 |
| primary / secondary / tertiary | 40 / 35 / 30 |
| residential / unclassified | 25 |
| motorway_link / trunk_link | 40 / 35 |
| primary_link / secondary_link / tertiary_link | 30 / 25 / 25 |
| service, including alley or no subtype | 10 |
| service driveway / parking_aisle / drive-through / slipway | 7 |
| other service subtype / living_street | 7 |

Mapped `junction=roundabout` or `circular` roads cap movement at **20 km/h**,
including high-class ways. This conservative circulatory-road assumption avoids
pricing a trunk-class roundabout as an open mainline; it is not an extra
intersection delay and adds nothing per geometry vertex. [RIDOT’s roundabout
guidance](https://dot.ri.gov/safety/roundabout_safety.php) describes reduced
circulating speeds; it does not calibrate this 20 km/h effective-speed assumption,
which may understate progress on larger circular roads.

Numeric limits cap the effective speed at **80% of the limit**, or the lower
class/surface default. This separates expected progress from continuous travel at
the posted maximum. The 80% factor allows headroom for ordinary interruptions; it
is not a traffic observation or an assertion of local calibration. It is constant,
not fitted to benchmark output. Road classification and service geometry justify
slower local/access-road defaults, but do not establish actual journey speeds.

Surfaces cap that estimate: paving stones, sett, brick, metal, compacted and fine
gravel **20**; unpaved, gravel, ground, dirt and cobblestones/pebblestones **15**;
grass, sand and dirt/sand **7 km/h**. Asphalt, paved and concrete (including
lanes/plates) add no cap. Missing surface retains the class assumption with a note;
unrecognized nonempty surface caps at **10** with an uncertainty note. These caps
change cost, not the existing access profile.

Provider parsing stays in `internal/importer`; the domain speed defaults, elapsed
costs and path search stay in `internal/routing`:

- Bare positive decimals mean **km/h**, including in US source data. `km/h`, `kph`,
  `mph` (×1.609344) and `knots` (×1.852) are supported with a separating space.
  No geographic unit inference, decimal comma, list, exponent or guessed repair.
  Values above 300 km/h, zero, negatives and nonfinite values are unresolved.
- Source-order `maxspeed:forward`/`:backward` overrides the nondirectional limit
  at the same scope. Specificity is motorcar, motor_vehicle, vehicle, generic.
  Reverse-only ways still use the backward estimate. Unrelated HGV, bus, PSV,
  bicycle, foot, emergency, motorcycle and trailer tags do not constrain this car.
- `none` means no numeric maximum; retain the class/surface assumption, with a
  note. `walk` uses an explicitly assumed **4 km/h**, without inventing a numeric
  legal walking speed. Other symbolic values (`signals`, country/type codes),
  malformed values and unsupported applicable keys (such as lane limits) cap the
  estimate at **5 km/h** and record uncertainty. This is a fallback estimate, not
  proof of compliance with an unknown legal ceiling. Raw values are not discarded.
  `maxspeed:type`, `:source`, `:signed`, `:reason` and `:practical` do not themselves
  supply a numeric legal maximum.
- Numeric `maxspeed:advisory` values cap expected speed directly and are not stored
  as legal limits. Applicable advisory variants are intersected conservatively.
- No conditions are evaluated. Split value/condition pairs at semicolons **outside
  parentheses**, preserving opening-hours semicolons. Apply every numeric
  potentially applicable conditional ceiling at all times, alongside the base
  ceiling, including directional/car conditions. Higher conditional values never
  increase speed. Unknown values or malformed expressions use the 5 km/h fallback
  and a note. There is no calendar, flashing-sign state or departure-time input.
- A retained point speed tag cannot establish a complete speed zone. For the sparse
  speed-tagged nodes on retained road ways, conservatively cap the **incident way**
  in both directions using the point’s estimates; record its node ID and unknown
  direction/zone extent. Do not infer a way-wide legal limit from a sign point.
  This bounded policy may overextend the lower estimate within that source way
  and cannot reconstruct an untagged school-zone extent beyond it.

Current primary OSM references checked 2026-09-08:
[maxspeed and direction](https://wiki.openstreetmap.org/wiki/Key:maxspeed),
[units](https://wiki.openstreetmap.org/wiki/Map_features/Units),
[conditional restrictions](https://wiki.openstreetmap.org/wiki/Conditional_restrictions),
[advisory speed](https://wiki.openstreetmap.org/wiki/Key:maxspeed:advisory),
[surface](https://wiki.openstreetmap.org/wiki/Key:surface), and
[service classifications](https://wiki.openstreetmap.org/wiki/Key:service).
These explain source semantics; the numerical effective-speed defaults are Open
Maps policy choices.

Elapsed seconds are **sum(road metres × 3.6 / effective km/h)**, including only
traversed portions of endpoint segments. There is **no separate preference score**,
intersection delay or turn penalty. Inspection found mixed junction/approach-node
signal and stop representations, incomplete directions, and no cycle timings.
The [signal](https://wiki.openstreetmap.org/wiki/Tag:highway%3Dtraffic_signals) and
[stop](https://wiki.openstreetmap.org/wiki/Tag:highway%3Dstop) tagging conventions
allow multiple representations. Adding a delay per tagged node risks charging the
same junction twice, and adding delay per geometry vertex is unjustified. Class
speeds allow for ordinary interruptions without inventing individual control
states. This can still underprice a particular turn, queue or signal; small
modeled savings should not be treated as observed improvements.

The accelerated search uses nonnegative deterministic costs and preserves last
edge, prohibited-path history and destination phase. Geometry-chain and conservative junction preprocessing
and an admissible A* bound reduce work; the original Dijkstra remains an internal
correctness reference. V4 adjacency is ordered by stable segment reference,
forward before reverse. See [search semantics and tie handling](routing-scale.md#endpoint-indexing-and-search).
No epsilon discards a small cost improvement. Endpoints are selected once,
independently of route cost or trip success. `RouteDistanceEndpoints` remains an
internal comparator under identical endpoint and elapsed-time assumptions.

The larger Northwest evaluation exposed a concrete model limitation: unsupported
`maxspeed:variable` metadata, including `no`, invokes the existing 5 km/h fallback.
On I-90 this prices tens of kilometres at that fallback and can favor long US 2
or I-5/I-84 detours. A separate `maxweight:hazmat` suffix also produces a conservative
closure under the current unsupported-dimension rule. These are retained profile
limitations, not hierarchy errors or observed driving conditions. See the
[historical source-backed investigation](log/0020-junction-hierarchy-and-mapped-query-data.md).
This scaling change preserves `estimated-driving-v1`; any revised interpretation
needs separate profile/cost-model review and version handling.

The response’s `openmaps.cost_model` identifies these semantics;
`openmaps.time_estimate_note` states uncalibrated speeds, missing traffic and
excluded gaps. Health adds `routing_duration_available`. Retained v1–v3 graphs do
not acquire inferred costs at load time. Unknown graph/profile/cost-model versions,
missing/duplicate way costs and invalid speeds fail loading and validation.

## Storage, builds and snapshots

`internal/importer/routing.go` owns PBF interpretation. It first reads highway ways
and restrictions, then reads only needed nodes on a second pass, discarding
unneeded extract nodes. Missing references fail the build. The Go service and
runtime dependencies remain unchanged. The separate Oregon evaluation acquires
pinned regional extracts and uses Osmium for complete-way corridor extraction
and merging; see [its source scope and build commands](routing-scale.md).

New builds store a checksummed, versioned manifest in `routing_graph` and bounded
binary record chunks in `routing_chunks`, separating query data from raw
provenance. Runtime coordinates and adjacency use contiguous arrays; source
references, full geometry, directional costs, prohibited-path history and
endpoint evidence retain their meanings. Spatial indexes replace regional
endpoint/guard scans. The graph adds about 34 MiB to the Newport SQLite candidate;
query data still consumes substantially more memory than its compressed storage.
The optional `-routing-cache` backend moves numeric arrays to verified read-only
files; it does not yet bound full startup memory. The server defaults to four
concurrent routing requests; see [routing scale](routing-scale.md).

Retained JSON formats 1–4 remain readable and validated without changing their
source data, profile, cost model or public IDs. New storage and preprocessing
versions are separate from graph/profile versions. Corruption or an unknown
version rejects the candidate. See [layout, algorithms, memory tradeoffs, Oregon
builds and verification](routing-scale.md). Full national hierarchy and bounded
national query-data loading remain unfinished.

Build a separate candidate offline from the unchanged lookup bundle:

```sh
mkdir -p data/time-routing

go run ./cmd/refresh build \
  -baseline data/openmaps.sqlite \
  -bundle data/newport.json \
  -checksum imports/newport.bundle.sha256 \
  -routing-pbf data/rhode-island-260801.osm.pbf \
  -candidate data/time-routing/candidate-v4-final.sqlite

go run ./cmd/refresh compare \
  -baseline data/openmaps.sqlite \
  -candidate data/time-routing/candidate-v4-final.sqlite \
  -report data/time-routing/comparison.json

go run ./cmd/server -db data/time-routing/candidate-v4-final.sqlite -listen 127.0.0.1:8087
```

Outputs must be new filenames. Use another candidate name if these already exist.
`cmd/import` also accepts `-routing-pbf` for a fresh non-refresh build. The PBF must
match the lookup manifest's filename and pinned SHA-256. Builds publish only after
verification; no source lock or existing database is rewritten. Byte-identical
graph manifests and chunks and stable graph identifiers are expected from identical inputs and
profile; SQLite file bytes need not be identical.

Graph semantics **4** pairs only with `driving-time-v4` and `estimated-driving-v1`, adding
per-way directional cost records to the unchanged v3 graph/access geometry.
Independent rebuilds must reproduce these records and their assumption notes.

Retained graph format **3** pairs only with `driving-distance-v3` and adds endpoint evidence:
complete local building/parking rings, driveway geometry, parking-entrance nodes,
road names and original source records. A third PBF pass retains local geometry
inside the preview plus 0.002 degrees; incomplete rings/driveways are omitted.
Names of longer roads are retained without clipped geometry. Node order is checked
against retained source ways, and shared node coordinates must agree. Format **2**
/ `driving-distance-v2` remains readable with its coordinate policy and directional
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

Enter **Origin address** and **Destination address** in Driving route and click
**Calculate driving route**. The browser sends one Compute Routes request and
displays the returned identities, source/road points, methods and gaps. A failed
address produces an error without any candidate-selection controls.

Existing selected Places/geocoding results and map points can still supply
coordinates for either endpoint, allowing mixed requests. Those explicit selections
retain coordinate semantics. The separate geocoding preview still lets users inspect
its candidates. The default map action remains reverse address lookup.

**Calculate driving route** shows a blue route, A/B endpoint markers, road distance,
estimated duration (rounded to minutes, with “less than 1 min” for short trips),
and both snap gaps. The estimate is labeled as excluding live traffic. Requested points are A/B; snapped road points are A′/B′ with separate
coordinates. Dashed orange connectors show the unverified off-road gaps and are
excluded from road distance and duration. A farther snap explains its selection and nearest
distance. Unreachable results show snaps and an explanation without a driving line.
Endpoint changes and new map-selection actions immediately
clear the old route and abort in-flight requests. Loading disables calculation;
failures leave no stale geometry. **Clear route** resets endpoints, geometry and
map action. Lookup markers and source precision retain their existing meaning.
Lookup-only snapshots show routing unavailable. Retained graph v1–v3 snapshots
show distance routing with time estimates unavailable. Map-library failures still leave
lookup, distance and available duration responses usable, but geometry cannot be displayed.

Routine `go test ./...` fixtures are small and offline. They cover directed travel,
partial-edge snapping, exact junction endpoints, snap limits, disconnected/crossing
roads, barriers, access precedence, only/no turns, via-way history, conservative
conditional/malformed restrictions, API masks/errors, checksums and atomic rollback.
The source-backed trip suite is explicit and does not acquire data:

```sh
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/time-routing/candidate-v4-final.sqlite" \
  go test -tags=integration ./internal/routing -run TestNewportRouting -count=1 -v
```

The [31-case benchmark](../internal/routing/testdata/newport.json) checks
source-tag evidence, required roads, forbidden destination shortcuts, geometric
distance bounds (not copied router outputs), off-rectangle detours, source-node adjacency,
geometry/distance agreement, one-way direction, prohibited paths, the API and an
isolated deployment cycle in a temporary directory. It also accepts a retained
routing graph as baseline to verify v3 → v4 → v3 profile, duration availability and rollback. It leaves the real deployment
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


The [22-case address-to-address benchmark](../internal/routing/testdata/newport-addresses.json)
uses 37 unchanged retained source records. It verifies identity/provenance, address
failure policy, geometry/direction/restriction invariants, road distance, source-to-road
displacement and HTTP responses. With a retained v3 `OPENMAPS_ADDRESS_BEFORE`,
it compares the old address route and v4 route under the **same candidate time
model**, checks unchanged endpoint resolution and independently sums duration.
For older v1/v2 snapshots, the geocoded-coordinate observation remains historical;
those APIs did not support one-request address routing.

```sh
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/time-routing/candidate-v4-final.sqlite" \
OPENMAPS_ADDRESS_BEFORE="$PWD/data/address-routing/candidate-v3.sqlite" \
  go test -tags=integration ./internal/routing -run '^TestNewportAddressRouting$' -count=1 -v
```

Run both existing routing and geocoding benchmarks too. The address integration test
uses an isolated loopback HTTP server and reads snapshots without mutating them.
See the [historical address-routing findings](log/0017-newport-address-routing.md).

The time comparison also checks that the distance-minimizing route is no longer than the time-optimal route,
and the time optimum is no slower under the same model, with identical
endpoints. Two source-backed ordinary-road alternatives must avoid service
through-travel within a 30% distance allowance. This is a declared practical-route
criterion, not independent travel-time evidence. See the [historical estimated-time
evaluation](log/0018-newport-estimated-driving-time.md).
