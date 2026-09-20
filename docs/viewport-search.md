# Viewport-aware lookup

Open Maps implements a documented viewport-bias subset of **Google Places API
(New) REST v1 Autocomplete** and **Google Geocoding API v3 JSON**. The contract
was checked against Google's current official documentation on 2026-09-19:

- [Places Autocomplete (New) request](https://developers.google.com/maps/documentation/places/web-service/place-autocomplete)
- [Places REST `places.autocomplete` reference](https://developers.google.com/maps/documentation/places/web-service/reference/rest/v1/places/autocomplete)
- [Geocoding v3 request and viewport biasing](https://developers.google.com/maps/documentation/geocoding/guides-v3/requests-geocoding)

Both supported rectangles are **soft biases**. They can change the order of
otherwise comparable matches but never make an outside result ineligible. The
basemap source and `basemap` URL choice are not request inputs and do not affect
lookup results; only the numeric viewport sent by the client is a ranking
signal.

## Places Autocomplete subset

`POST /v1/places:autocomplete` accepts the existing `input`, English
`languageCode` and `sessionToken` fields plus this optional JSON form:

```json
{
  "input": "Main Street",
  "locationBias": {
    "rectangle": {
      "low": {"latitude": 40.49, "longitude": -74.27},
      "high": {"latitude": 40.92, "longitude": -73.68}
    }
  }
}
```

`low` is the south/west corner and `high` is the north/east corner. Coordinates
are WGS84 and each object is **latitude, longitude**; this differs from common
GeoJSON and MapLibre arrays, which are longitude, latitude. Both corners and
both numeric fields are required.

Only `locationBias.rectangle` is supported. `locationBias.circle`,
`locationRestriction` and the other unimplemented Places request fields return
`INVALID_ARGUMENT`; they are not silently ignored. Omitting `locationBias`
preserves the established non-viewport result behavior. Unlike Google, Open
Maps does not add an IP-derived bias when the field is omitted.

For a non-degenerate, city-scale rectangle (at most two degrees in each
dimension), the backend resolves the nearest usable source address within five
kilometres of the viewport center to locality and region tokens. The lookup
radius is independent of the rectangle's size, so a tightly zoomed viewport
that contains no address or matching place does not lose its geographic
context. Those tokens retrieve a larger contextual candidate pool before the
five-result cutoff. The backend merges that pool with the ordinary national
candidates, then preserves text-match classes and prefers candidates inside the
closed rectangle and nearer its center. This allows nearby common businesses
such as `The UPS Store` to enter the candidate set and be ordered by actual
distance even when every result is outside the visible rectangle. The context
is derived from lookup source data, not the basemap.

Exact ambiguous street names also use the rectangle center to choose the
displayed representative segment. Degenerate or larger rectangles, and
city-scale rectangles without usable nearby address context, still bias the
ordinary candidate set without contextual expansion. In every case this is a
relevance signal, not proof that a street is wholly inside the viewport, and
not a distance field in the response.

## Forward-geocoding subset

Forward requests accept the optional Google v3 query form:

```text
bounds=southwest_latitude,southwest_longitude|northeast_latitude,northeast_longitude
```

For example:

```sh
curl -sSG http://127.0.0.1:8080/maps/api/geocode/json \
  --data-urlencode 'address=10 Shared Street' \
  --data-urlencode 'bounds=41.47,-71.33|41.51,-71.29'
```

The bounds only reorder exact address candidates. Every exact candidate remains
in the response, including candidates outside the rectangle. An explicit place
or locality in the address text remains part of the exact/context match and is
not discarded to satisfy the bias.

`bounds` with reverse `latlng` is rejected because Open Maps supports it only
for forward address geocoding. Reverse geocoding continues to use the clicked
WGS84 `latitude,longitude` and its documented 100 metre rule. Geocoding v4
`locationBias`, Places-style JSON bodies and all other previously unsupported
v3 parameters remain unsupported.

## Rectangle validity and antimeridian behavior

The shared internal viewport is a closed region with these rules:

- latitude is finite and in `[-90, 90]`; longitude is finite and in
  `[-180, 180]`;
- south latitude must not exceed north latitude;
- west less than or equal to east is the ordinary longitude interval;
- west greater than east crosses the antimeridian, so `170` to `-170` includes
  longitudes near both `+180` and `-180`;
- west `-180` and east `180` includes every longitude;
- west `180` and east `-180` is empty and rejected; and
- zero-height or zero-width closed rectangles, including one point where both
  corners are equal, are accepted as degenerate biases, matching the Places
  viewport contract.

The browser reads `map.getBounds()` immediately before each autocomplete or
forward-geocoding request. It wraps MapLibre longitudes into the API range and
uses `-180` to `180` for a viewport spanning the whole world. Reverse requests
do not include bounds.
