# Newport geocoding

Open Maps implements a limited **Google Geocoding API v3 HTTP JSON** surface at
`GET /maps/api/geocode/json`. It resolves existing standalone address entities,
using their existing public IDs and source coordinates. It does not geocode
business names, interpolate addresses or return street/locality fallbacks.
Places autocomplete and details remain separate operations.

## Requests and responses

Supply exactly one query parameter:

```sh
curl -sSG http://127.0.0.1:8080/maps/api/geocode/json \
  --data-urlencode 'address=50 Bellevue Ave'

curl -sSG http://127.0.0.1:8080/maps/api/geocode/json \
  --data-urlencode 'latlng=41.48656,-71.30835'
```

`address` contains 1–200 Unicode characters. Use an ASCII numeric house number,
optionally followed by one attached letter, then the **complete street label**
starting with a letter. Leading number zeroes, case, accents, whitespace and
street punctuation normalize; `st`, `ave`, `rd`, `blvd`, `ln`, `dr`, `ct`, `pl`
expand using the existing Places normalization. No prefixes, spelling correction,
number words, intersections, plus codes or range/fraction interpretation.
`050 bÉlleVue AVE.` matches `50 BELLEVUE Avenue`; `50 Bellevue` does not.

Optional context follows a comma. Context tokens must occur in the stored
formatted address suffix, in order; omission of context is allowed. `Rhode Island`
and `USA`/`United States` normalize to `RI` and `US`. For example, `, RI, 02840, US`
is supported; a wrong postcode or foreign locality produces no match. A leading
`Newport` context token is accepted as the preview's scope. Because the source
address locality is missing, that request returns `partial_match: true` and an
explicit context note; it does **not** verify address membership in Newport or add
a locality component. Full address context without the separating comma is not
supported and will not match. This is a conservative label grammar, not general
postal address parsing or address validation.

`latlng` is WGS84 **latitude,longitude**, in decimal degrees. Both values must be
finite and in their global ranges. Reverse lookup first checks the manifest's
inclusive preview rectangle, then returns the closest **supported address point
within 100 metres, inclusive**. Distance is spherical straight-line distance
(mean Earth radius 6,371,008.8 m), not a walking route. All points within one
millimetre of the nearest distance remain candidates; order is distance then
public ID. An outside-rectangle query returns no results even if a point just
inside is less than 100 metres away. The rectangle is not a municipal boundary.

Optional `language=en` or `en-US` is accepted; there is no translation or
Accept-Language/IP bias. Optional `key` is accepted but not authenticated, as in
Places. Duplicate query parameters, malformed queries, empty explicit language,
bodies and other parameters are rejected. Unsupported parameters include
`components`, `bounds`, `region`, `result_type`, `location_type`, `place_id`,
`extra_computations`, field masks (`fields`, `$fields`, `X-Goog-FieldMask`) and
session tokens. XML, v4, Google IDs and Google SDK compatibility are unsupported.
No parameters that would change a result are silently ignored.

Responses contain `status`, a `results` array and the local `openmaps` extension.
This is a subset, not a complete Google response schema:

| Field | Meaning |
| --- | --- |
| `place_id` | Existing Open Maps address ID; resolves through Places details |
| `formatted_address` | Stored label, with no inferred components |
| `types` | `['street_address']` |
| `address_components` | Available source number, route, locality, state, country and postcode; missing parts omitted |
| `geometry.location` | Stored `lat`, `lng`; never moved to the query point |
| `geometry.location_type` | Always `APPROXIMATE`; source accuracy is not independently established |
| `partial_match` | Present and true when Newport context could only be accepted as preview scope |
| result `openmaps.precision` | `source_address_point`, distinct from a street or locality result |
| result `openmaps.unit_precision` | `unknown` |
| result `openmaps.distance_meters` | Reverse-only numeric distance to the input, in metres |
| result `openmaps.attributions` | Existing entity attributions |
| result `openmaps.context_note` | Explains unverified Newport context when applicable |
| top-level `openmaps` | `outcome`, `candidate_count`; reverse also includes `reverse_limit_meters` |

No rooftop, entrance, building containment, unit, viewport, geometry bounds,
plus code or administrative component is invented. Raw source records, winning
attribute provenance and existing business/address relationships remain in
SQLite unchanged; geocoding does not merge or infer relationships. The runtime
uses normalized entity labels for matching. At load time, an importer adapter
projects components from the retained source selected by the `address` attribute's
provenance. Its result IDs
identify addresses, not nearby businesses or buildings.

Each component has `long_name`, `short_name` and `types` using the v3 response
shape. `364 Bellevue Avenue` has number `364`, route `BELLEVUE Avenue`, state
`RI`, country `US` and postcode `02840` on all eight points. State and country
also carry the `political` type. English long names expand `RI` (in the US) to
`Rhode Island` and `US` to `United States`; other values retain source spelling
in both name fields. The US locality slot and unit are empty in the retained
Overture data and stay absent from responses, including when the request supplies
Newport context. No county is inferred.

562 retained records also have `postal_city=Newport East CDP`. This raw field
is not projected into components: it agrees with the inspected NAD census-place
field, whose separate postal-city field is unknown. A CDP label does not establish
a verified postal city or municipal locality. See the [historical source
comparison](log/0013-address-source-comparison.md).
The request parameter `components` remains unsupported.

The concrete adapter in `internal/importer/addressdata` supports retained Overture
address records. It interprets the documented two-element US `address_levels`
layout as state and locality; other countries/layouts do not receive inferred
administrative types. It checks source labels against the selected entity, and
does not combine components from losing source records. Unknown providers or
conflicting labels return an empty component array. This read-only projection
works with both retained schema-1 snapshots and newly built snapshots; components
are not a new persisted attribute or a change to Places responses. See the
[historical component decision and verification](log/0012-geocoding-address-components.md).

## Outcomes and ambiguity

| Status / outcome | Behavior |
| --- | --- |
| `OK` / `matched` | One supported source address point |
| `OK` / `ambiguous` | All exact forward matches, or nearest reverse ties; none is a verified unique answer |
| `ZERO_RESULTS` / `no_match` | No exact label/context match; does not prove that the address does not exist |
| `ZERO_RESULTS` / `no_nearby_address` | Inside preview but no supported point within 100 m |
| `ZERO_RESULTS` / `outside_coverage` | Reverse input outside the preview rectangle |
| `INVALID_REQUEST` | Invalid/unsupported request or address syntax, including explicit units |
| `UNKNOWN_ERROR` | Internal/unavailable geocoder |

Forward ambiguity is ordered by public ID, not confidence; there is no result
truncation or arbitrary collapse of distinct address identities. For example,
`364 Bellevue Avenue` returns eight points and `199 James T Connell Memorial Rd`
returns twelve in both retained snapshots. A missing unit is never filled or
stripped from input. Explicit `apt`, `apartment`, `unit`, `suite`, `ste`, `floor`,
`fl`, `room` and `#` markers are rejected, even if a base address could match.
A bare base-address match makes no claim that a unit is known or unnecessary.

Normal success, no-match and invalid-request envelopes use HTTP 200; clients
must check `status`. Errors contain `error_message`, `results: []` and an outcome
(`invalid_input`, `unsupported_input`, `unsupported_or_invalid_request` or
`unavailable`); they do not contain candidate counts. Wrong methods use HTTP 405
with `Allow: GET`; internal failures use HTTP 500/503. Authentication, quotas,
billing and Google's related status behaviors are not implemented. Unknown
endpoint paths use the existing API 404 envelope.

## Browser and snapshot operation

Choose **Forward geocoding**, enter `50 Bellevue Ave`, then **Find address** or
Enter. A single result places a green marker at its source coordinates. Multiple
results require choosing a candidate, with coordinates and distinct IDs visible.
Click the map to reverse geocode: a blue dot marks the input; the green marker
shows the selected address. The details show distance, precision and uncertainty.
Typing a new input clears the preceding result and markers. Empty, unsupported
and out-of-coverage outcomes do not leave a stale address selected.

At startup, `internal/geocoding` loads supported address labels from a read-only
SQLite connection into an immutable map for forward lookups and a slice for
reverse scans. At this scale (~8,500 points), a scan needs no spatial index.
It requires a valid non-dateline manifest `bbox`. There are no schema changes,
new source imports, migrations, sidecar databases or writes to retained snapshots.
Deployment reload opens both Places and geocoding successfully before replacing
the handler. Requests carry the existing `X-OpenMaps-Dataset` fingerprint in
deployment mode. Activation and rollback use the [refresh workflow](refresh.md).

Both retained snapshots have 8,545 standalone addresses, of which 8,407 satisfy
the implemented grammar. The other 138 retain Places behavior but are excluded
from both geocoding directions. These are mostly fractional, ranged or leading
letter/gate labels. This exclusion is an implementation limit. Missing unit and
locality slots, duplicate labels and uncertified positional accuracy are source
limitations. No completeness or real-world accuracy percentage is claimed.

## Deterministic quality benchmark

The checked-in [26-case suite](../internal/geocoding/testdata/newport.json)
contains expected IDs, distances and 25 source evidence points. It includes real
addresses, normalization, context, missing units, repeated labels, unsupported
syntax, nearby/distant coordinates, a water gap and south/north/east boundary cases. The west water gap is inside the
preview. Expectations were checked against raw source numbers, street labels and
GeoJSON coordinates; this verifies source fidelity, not independent ground truth.

Routine tests are synthetic and offline. The retained-data benchmark is an
explicit integration test, also offline after preparation:

```sh
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/newport-2026-07-22/openmaps-reviewed.sqlite" \
  go test -tags=integration ./internal/importer -run TestNewportGeocoding -count=1 -v
```

It exercises the HTTP handler, status and result fields, distances, identity and
source coordinates on both files. Five executions per case provide a small
local timing sample. Snapshot validation checks referential integrity and public
anchors; all address entities are compared across snapshots and changes counted.
Large inputs and generated reports stay in ignored `data/`. It does not download
data, activate snapshots or claim browser coverage. Real browser verification
uses the installed Codex in-app browser, not an npm or standalone runner.

See the historical [contract decision](log/0008-geocoding-contract.md) and
[verification findings](log/0009-geocoding-verification.md).

Against a running deployment, repeat the suite and existing Places checks after
activation and rollback; each live suite requires a consistent dataset header:

```sh
OPENMAPS_URL=http://127.0.0.1:8080 \
  go test -tags=integration ./internal/api -run 'TestLive(Geocoding|Demo)$' -count=1 -v
```
