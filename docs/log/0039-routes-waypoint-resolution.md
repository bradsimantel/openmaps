# Routes waypoint resolution

Date: 2026-09-10 (America/Los_Angeles). Scope: implementation based on `f6d1d30`;
verification used the retained Newport lookup and regional Scout snapshots. This
is a historical implementation record. Current behavior is maintained in
[routing.md](../routing.md), [geocoding.md](../geocoding.md), and
[routing-scout.md](../routing-scout.md).

## Contract decision

`POST /directions/v2:computeRoutes` now accepts exactly one of a WGS84
`location.latLng`, an Open Maps `placeId`, or an exact supported `address` for
each origin and destination. The forms may be mixed. This follows the current
official Google Routes v2 [Waypoint union](https://developers.google.com/maps/documentation/routes/reference/rest/v2/Waypoint)
and [location forms](https://developers.google.com/maps/documentation/routes/specify_location),
checked 2026-09-10. Required-route-parameter failures retain the documented
Google-shaped `INVALID_ARGUMENT` envelope.

Open Maps does not adopt Google's broader geocoder or its documented ability to
search for an address interpretation. Address waypoints use the existing exact
geocoder unchanged and must produce one standalone-address identity. Unsupported
syntax, no match and multiple exact identities are distinct 400 failures. This is
an intentional difference from Google's documented unresolved-address response,
which can contain empty routes plus `geocodingResults`. The maintained response
mask subset does not add that field.

Place IDs resolve by exact ID through Places details. The business, address,
street-segment or area kind is preserved; a Place ID never turns one entity into
another or borrows a related address. Unknown IDs fail. Source labels, public IDs,
relationships, provenance and stored coordinates are unchanged.

## Architecture and implementation

Google request parsing and both lookup operations remain in `internal/api`.
`internal/routing` and the prepared Scout graph are unchanged. After resolution,
Scout receives the stored WGS84 point and applies its ordinary coordinate snap,
restrictions, search, geometry, distance and duration behavior.

Static service setup now gives the route API the same Places and geocoding stores
as the lookup endpoints. In deployment mode, the route handler runs inside the
existing shared lookup snapshot lease. Both endpoints therefore resolve from the
same Places/geocoding pair, the `X-OpenMaps-Dataset` fingerprint names that pair,
and replacement cannot retire the SQLite connection until response encoding
finishes. Lookup and Scout selection remain independent; neither operation changes
the other's snapshot or admission budget.

Successful non-coordinate routes expose an Open Maps resolution object for each
such endpoint: input form, place ID, unchanged entity kind, source coordinate,
source precision and an uncertainty statement. Address resolution also retains
the geocoder's partial Newport-context flag. Coordinate-only requests take the
unchanged path and add no resolution fields.

Explicit resolution outcomes are `unknown_place_id`,
`unsupported_address_syntax`, `unresolved_address`, `ambiguous_address` (with a
candidate count), and `lookup_unavailable`. They identify the origin or
destination. Existing routing-snapshot `unavailable`, Scout `incomplete_data`,
`unsnappable`, `unreachable`, budget, deadline, cancellation and admission
outcomes remain distinct.

## Verification

Routine deterministic coverage includes:

- coordinate, business Place ID, standalone-address Place ID and exact-address
  routes over the small Scout fixture;
- the frozen coordinate-only HTTP route remains 309 m / 37 s with the same full
  five-position GeoJSON value and no lookup-resolution metadata;
- autocomplete → details → Place ID route, exact geocode → address route, and
  coordinate parity, comparing the complete masked `routes` value;
- identity-kind metadata, unknown IDs, repeated/ambiguous labels, no-match,
  unsupported units/syntax, missing lookup and missing routing data;
- unsupported/multiple waypoint forms, duplicate JSON, existing field masks,
  endpoint-specific snap failure and an off-road Place source point;
- a blocked in-flight route across lookup selection replacement, proving that
  both endpoint source points and the response fingerprint come wholly from the
  old snapshot, followed by a wholly new-snapshot response.

Downloaded-data verification used `data/openmaps.sqlite` and
`data/scout-national-20260909/regional-indexed` through a temporary isolated HTTP
server. It exercised White Horse Tavern autocomplete/details/Place-ID routing,
unique 26 Marlborough Street and 50 Bellevue Avenue geocoding/address routing,
standalone address details and coordinate-route parity. No lookup or routing
selection was changed, no graph was rebuilt, and existing services were not
restarted.

Final commands and results:

- `gofmt` on changed Go files: passed.
- `go test ./...`: passed.
- `go vet ./...`: passed.
- `OPENMAPS_LOOKUP_DB="$PWD/data/openmaps.sqlite"
  OPENMAPS_SCOUT_PREPARED="$PWD/data/scout-national-20260909/regional-indexed"
  go test -tags integration ./cmd/server -run
  '^TestDownloadedNewportWaypointFlows$' -count=1 -v`: passed through an
  `httptest` loopback server on its isolated ephemeral port.
- The retained regional graph's existing `TestScoutHTTP` integration also passed,
  including coordinate routes, masks, absent graph data and explicit missing
  lookup data.
- `git diff --check`: passed.

## Limitations

Resolved POI and address coordinates are provider source points, not surveyed
entrances, navigation points or verified property access. Street Place IDs use an
individual Overture segment's search marker and area IDs use label points; neither
represents the full feature. Address requests add no fuzzy matching, incomplete
street/locality fallback, unit inference, interpolation, plus codes or fabricated
components.

Scout still snaps independently to the nearest eligible retained shape within
100 m. It cannot use Places relationships or address data to choose a driveway,
entrance, correct side of road or safe/legal property approach. The unverified
off-road gap is excluded from route geometry, distance and duration. Existing
traffic, turn-delay, ferry, destination-only-access, source-cutoff and coverage
limitations remain. A successful route validates neither address accuracy nor
entrance/access suitability.
