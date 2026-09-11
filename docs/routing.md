# Routing architecture and HTTP contract

Scout is the sole routing implementation. See the maintained [Scout contract,
profile, preparation and serving workflow](routing-scout.md).

Go decodes pinned Scout Valhalla 3.4.0 tiles, snaps coordinates, enforces retained
access/turn rules and returns full geometry with provider edge-speed estimates.
Ordinary traversal remains the correctness reference; national serving uses
one-sided directed landmark A*. The API layer owns Google request/response
translation, explicit masks, errors and metadata.

`POST /directions/v2:computeRoutes` implements a Routes REST v2 subset checked
against Google's current [Waypoint](https://developers.google.com/maps/documentation/routes/reference/rest/v2/Waypoint),
[location](https://developers.google.com/maps/documentation/routes/specify_location)
and [error](https://developers.google.com/maps/documentation/routes/handle-errors)
documentation on 2026-09-10. Origin and destination each require exactly one of:

- `location.latLng.latitude` and `longitude`, finite WGS84 decimal degrees;
- `placeId`, containing an existing Open Maps ID from the selected Places snapshot;
- `address`, containing one exact, uniquely supported standalone-address label.

Forms may be mixed. Other waypoint options, intermediates and navigation tokens
remain unsupported. The request subset otherwise retains `DRIVE`,
`TRAFFIC_UNAWARE`, required `GEO_JSON_LINESTRING`, optional `HIGH_QUALITY`, and no
extra body parameters. Response masks support `routes.distanceMeters`,
`routes.duration`, `routes.staticDuration` and
`routes.polyline.geoJsonLinestring` (including the supported polyline parent).
Broad `*` and `routes` masks are rejected. `key`, `fields` and `$fields` retain
existing parsing and duplicate rejection. Invalid or duplicate JSON, unsupported
fields and malformed coordinates return 400. API keys are not authenticated.

The API resolves Place IDs with Places details without changing the entity kind.
Businesses, standalone addresses, individual street segments and areas retain
their own identities and source points. Address strings call the existing exact
forward geocoder: no fuzzy search, incomplete-street or locality fallback, unit
inference, plus-code handling or fabricated address data is added. One request
holds a shared lookup lease through both resolutions, route calculation and JSON
encoding. A live lookup replacement therefore cannot mix endpoint coordinates or
close their lookup generation during the request. Lookup replacement remains
independent of the Scout snapshot selection.

Google may disambiguate address strings and documents an unresolved address as a
200 response with empty routes plus `geocodingResults`. Open Maps intentionally
does neither: multiple exact identities are not ranked, and its current response
mask subset does not expose `geocodingResults`. Resolution failures use the normal
Google-shaped error envelope plus these explicit `openmaps.outcome` values:

| HTTP / status | Outcome | Meaning |
| --- | --- | --- |
| 400 `INVALID_ARGUMENT` | `unknown_place_id` | ID absent from the selected Places snapshot |
| 400 `INVALID_ARGUMENT` | `unsupported_address_syntax` | Invalid, unit-bearing or otherwise unsupported exact-geocoder input |
| 400 `INVALID_ARGUMENT` | `unresolved_address` | Valid syntax, but no exact address identity matched |
| 400 `INVALID_ARGUMENT` | `ambiguous_address` | Multiple exact standalone-address identities matched; `candidate_count` is returned |
| 503 `UNAVAILABLE` | `lookup_unavailable` | Required Places or exact-geocoding data is unavailable |
| 503 `UNAVAILABLE` | `unavailable` | Scout routing snapshot is unavailable; retained for coordinate-response compatibility |
| 400 `INVALID_ARGUMENT` | `unsnappable` | Resolved source coordinate or coordinate input has no eligible Scout road within 100 m |

Failures identify `origin` or `destination`. Successful Place/address routes add
`origin_resolution` or `destination_resolution` metadata with the Open Maps ID,
unchanged entity kind, WGS84 source point in `[longitude,latitude]` order and
precision. Coordinate-only response bodies remain unchanged. Resolution metadata
is an Open Maps extension independent of the route response mask.

The profile remains `osm-scout-public-auto-v1`, cost model `scout-edge-speed-v1`.
It cannot recreate the retired `driving-time-v4` source semantics. Missing tiles
remain 503 `incomplete_data`; unsnappable endpoints return 400; exhaustive
unreachable networks return 200 with empty routes. Budget exhaustion, cancellation,
deadlines and admission remain distinct. Route metadata identifies the snapshot,
both snaps, unverified gaps, attribution and source limitations independently of
masks.

Lookup Parquet/DuckDB generations, basemap tiles and routing pages remain
separate. Resolved POI, address, segment and area locations are source points,
not surveyed entrances,
verified property access points or additions to the graph. Scout independently
snaps those points using the same 100 m policy as literal coordinates; distance
and duration still exclude the unverified off-road gap. The server can serve all
three data products together; routing snapshot replacement is independent of
lookup refresh and both retain leases through response encoding. There is no
legacy graph importer, overlay engine, mapped graph cache or legacy startup
flag.

The historical investigations in [0015](log/0015-newport-driving-routing.md)
through [0034](log/0034-national-scout-qualification.md) describe prior milestones;
[0035](log/0035-scout-go-migration.md) records their migration to the sole backend.
