# Newport Geocoding API contract decision

2026-09-07 · Geocoding milestone working tree based on `ee8a74c`.

Historical decision and investigation. Current behavior is maintained in
[geocoding.md](../geocoding.md). This supersedes earlier milestone records'
deferral of dedicated geocoding; routing remains deferred.

## External contract

Selected **Google Geocoding API v3, HTTP JSON**, not Places v1 or Geocoding v4.
Current official references checked on this date:

- [v3 start guide](https://developers.google.com/maps/documentation/geocoding/guides-v3/start)
- [Forward request and response](https://developers.google.com/maps/documentation/geocoding/guides-v3/requests-geocoding)
- [Reverse request and response](https://developers.google.com/maps/documentation/geocoding/guides-v3/requests-reverse-geocoding)

Google's v3 interface uses `/maps/api/geocode/json`, query parameters, top-level
status and results. Forward uses `address`; reverse uses `latlng` in latitude,
longitude order. The references define result IDs, address components, geometry
location/type, optional partial-match information and status codes. Reverse can
produce several feature types. These facts establish the wire target; the local
subset and ranking below are project choices, not claims about Google's ranking.

We expose only address text and coordinate lookup, optional English language and
an unauthenticated compatibility key. Unknown or unsupported parameters fail
explicitly, including all bias, component and result filters. Use the familiar
v3 status envelope for invalid requests; local HTTP failure behavior is documented.
No live Google credentials or response comparison was used.

## Precision and scope choices

The normalized entity store has labels and coordinates; structured source fields
remain in provider raw records. Avoid a new import/release/schema merely to expose
components. Match conventional number + street labels using existing normalized
entity values, expose no structured components, and retain provider parsing in
import code. Missing locality is never inferred from nearest area labels. Accept
Newport only as preview context with an explicit partial-match note.

Choose address-only forward matching. Do not turn a failed house number into a
street or locality result. This makes a success an address-entity match without
claiming rooftop precision. `APPROXIMATE` describes the unverified source location;
`openmaps.precision=source_address_point` describes the entity/match precision.
Do not use geometric-center or interpolated labels for source points.

Choose an inclusive **100 m** reverse distance limit and an inclusive manifest
rectangle check before lookup. Nearest supported points are ranked by haversine
distance then stable ID, retaining all candidates within 1 mm of the minimum.
This is a proximity operation, without containment, street-side or entrance claims.
A query just outside coverage must not borrow an inside address.

Preserve all duplicate-label identities. The UI requires explicit selection for
ambiguity. Unit requests fail because units are missing, rather than discarding
the requested unit. Fractions, ranges and other nonstandard number forms remain
unsupported in both directions; their existing Places entities are untouched.

## Source inspection and storage decision

Inspected both retained August/July databases and original source JSON. Both have
8,545 address entities with the same IDs, names, formatted addresses, locations
and attributions. All 8,407 simple supported labels can be loaded from the existing
schema. The 138 excluded forms include `19 1/2 FREEBORN Street`,
`1 -550 AMERICA Street`, `A 146 BAINBRIDGE Road` and `Gate 2 THIRD Street`.
This is not evidence that those addresses are invalid.

`50 BELLEVUE Avenue` is latitude 41.486543927900414, longitude
−71.30830417812572; `26 MARLBOROUGH Street` is latitude 41.491355654497895,
longitude −71.3137306498567. The latter is a distinct entity and coordinate from
White Horse Tavern. Eight source points share `364 BELLEVUE Avenue`; twelve
share `199 JAMES T CONNELL MEMORIAL Road`. Missing units do not explain or resolve
these duplicates. All stay distinct and source-backed.

Use an immutable in-memory address map and full reverse scan, built from read-only
SQLite at startup/reload. The regional size does not justify a spatial schema
change. This lets the benchmark and activation/rollback run against the exact
retained database bytes and fingerprints. Keep Places and geocoding reload atomic.
No new source, service, routing dependency or speculative framework was added.
