# Viewport-aware search implementation and deployment

> **Historical note:** The general-place candidate limitation recorded below
> was subsequently corrected after a live Chicago `ups store` report. See
> [0067](0067-viewport-candidate-retrieval.md).

**Date:** 2026-09-19

**Base revision:** `87dddcde316fc321906d4c88e84a043175d570c6`

**Scope:** current `main` working tree; Places Autocomplete rectangle bias,
forward-geocoding bounds bias, demo integration, deterministic verification and
deployment against the retained national lookup. This record does not change
the lookup snapshot, public IDs, routing snapshot or basemap archive.

## External contracts checked

The implementation was checked on 2026-09-19 against Google's current official
[Autocomplete (New) guide](https://developers.google.com/maps/documentation/places/web-service/place-autocomplete),
[REST method reference](https://developers.google.com/maps/documentation/places/web-service/reference/rest/v1/places/autocomplete)
and [Geocoding v3 request guide](https://developers.google.com/maps/documentation/geocoding/guides-v3/requests-geocoding).

The supported Places wire form is only
`locationBias.rectangle.low/high.latitude/longitude`. It is a soft bias;
`locationRestriction` and circle bias remain unsupported and return
`INVALID_ARGUMENT`. The supported Geocoding v3 wire form is the forward-only
query parameter
`bounds=southwest_latitude,southwest_longitude|northeast_latitude,northeast_longitude`.
It is also a soft bias. Reverse geocoding rejects `bounds` and continues to use
only `latlng`.

The maintained [viewport contract](../viewport-search.md) records coordinate
order, validation, closed/degenerate rectangles, antimeridian crossing and the
full unsupported-parameter boundary. Open Maps adds no IP-derived bias when a
viewport is absent.

## Implementation

- The API package alone parses Google wire formats. It translates them to a
  provider-independent `places.Viewport` whose fields name south, west, north
  and east explicitly.
- Longitude intervals with west greater than east cross the antimeridian.
  `-180` to `180` means all longitudes; `180` to `-180` is empty and rejected.
  Closed lines and single points are accepted.
- Ordinary autocomplete retains its established behavior when the viewport is
  absent. With a viewport, text-match classes remain primary; candidates inside
  the rectangle and then nearer its center are preferred. Candidates are never
  filtered by this signal.
- An exact ambiguous street such as `Main Street` uses the existing bounded
  exact-street candidate reader to choose a representative near the viewport
  center when one is within 100 km. The ordinary national text query and this
  independent proximity pass run concurrently. This avoids adding coordinates
  to the compact catalog or rebuilding the 110 GiB generation.
- Forward geocoding materializes every exact candidate as before, then stably
  orders inside/nearby candidates first. No result is truncated because of
  bounds.
- The demo reads `map.getBounds()` immediately before every autocomplete and
  forward request. MapLibre longitudes are wrapped into `[-180,180]`; a world
  width of at least 360 degrees becomes `-180` to `180`. Reverse requests carry
  no rectangle.
- `basemap=newport`, `basemap=us` and `basemap=global` affect display tiles and
  initial view only. No basemap identifier reaches either lookup endpoint.

## Deterministic verification

Routine tests use generated small DuckDB/Parquet fixtures and cover:

- byte-for-byte-equivalent result ordering with no viewport;
- a nearby Portland result ahead of an otherwise similar distant Portland;
- Oregon and Maine `Main Street` representatives selected from their respective
  viewports;
- an outside-viewport street remaining eligible;
- Places rectangle parsing, missing/unknown fields, unsupported circle and hard
  restriction, coordinate ranges, inverted latitude and the empty antimeridian
  interval;
- valid antimeridian crossing, all-longitude, line and point rectangles;
- forward-geocoding candidate reordering without candidate loss; and
- rejection of malformed bounds and bounds on reverse geocoding.

The final working tree passed:

```text
gofmt on all changed Go files
go test ./...
go vet ./...
go test -race ./internal/placesgeocoding/duckdb -run TestStructuredAutocompleteContext -count=1
node --check public/app.js
node --check public/routing.js
git diff --check
```

There is no frontend build or package-manager test suite; browser modules remain
direct versioned imports.

## National deployment and smoke results

The deployment retained:

- lookup generation `/srv/openmaps/data/national-catalog-research-2cfd7fc`;
- lookup manifest SHA-256
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`;
- US PMTiles `/srv/openmaps/data/us-basemap-20260919.pmtiles`; and
- served hard link `/srv/openmaps/public/tiles/us.pmtiles`.

Both PMTiles paths still had inode `30601737` and size `20,772,330,499` bytes
after deployment. A live range request returned HTTP 206 with
`Content-Range: bytes 0-126/20772330499`. The active transient unit is
`openmaps-demo-viewport-concurrent.service`, serving
`openmaps-demo-viewport-concurrent-20260920` on `0.0.0.0:8080`. It reached
health readiness in about 7m29s after complete generation validation and has
zero restarts. `/healthz` reports `status: ok`; routing remains unavailable as
it was before this lookup/demo deployment.

Representative public HTTP measurements were made serially except for the two
independent forward requests:

| Operation | Viewport | First result | Time |
| --- | --- | --- | ---: |
| Autocomplete `Main Street` | New York City (`40.4774,-74.2591` to `40.9176,-73.7002`) | route `om_0121d6b5e3c88873ac64519b3ecd3186`, `40.897494,-74.040122` | 14.95s |
| Place details for that route | — | same ID and coordinates | 0.40s |
| Autocomplete `Main Street` | Los Angeles (`33.70,-118.70` to `34.34,-118.15`) | route `om_0bb597d681ad0730d11806b288b19d5f`, `34.009632,-118.490262` | 28.79s |
| Place details for that route | — | same ID and coordinates | 0.39s |
| Forward `50 Bellevue Ave` | New York City | NJ candidate `om_9235de15d7c5c734d302a97c64446a3d`, `40.790918,-74.177874` | 1.53s |
| Forward `50 Bellevue Ave` | Los Angeles | nearest retained CA candidate `om_ef25b29ff8a74467ef5f4f07ea380f92`, `37.311927,-121.872658` | 1.53s |
| Reverse `41.48654,-71.30830` | no bounds | Newport `50 BELLEVUE Avenue`, 0.56 m from click | 0.42s |

Both forward responses retained all 19 exact candidates. The California result
is outside the Los Angeles rectangle but nearer than the other retained exact
matches, directly demonstrating bias rather than restriction. Both
autocomplete top routes fall inside their respective viewports.

The first implementation ran the ordinary text query and exact-street proximity
pass serially. New York took 25.23s and Los Angeles crossed the server's 35s
write deadline, producing an empty client reply without a process restart. The
retained concurrent implementation reduced the measured paths to 14.95s and
28.79s. This investigation is part of the implementation record, not a claim
that national street autocomplete is generally low latency.

## Remaining limitations

- The compact serving catalog has no general spatial index for Places. The
  viewport reorders the ordinary five-result candidate set; exact street names
  additionally use the bounded existing street reader. A nearby text match that
  never enters that candidate set cannot be promoted.
- The Los Angeles `Main Street` request remains slow and close to the 35-second
  server write deadline. A durable latency improvement requires a compact
  spatial search projection in a future generation, not a broader in-memory
  cache or weaker artifact validation.
- Street coordinates are representative points on retained segments. A selected
  point does not claim that the complete named street lies in the viewport.
- Forward geocoding remains exact-label matching with no interpolation or
  street/locality fallback. Bounds do not compensate for missing source data or
  unsupported address syntax.
- A map that has not initialized cannot supply a viewport. Search still works
  with the established un-biased behavior.
