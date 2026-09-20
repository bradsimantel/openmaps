# Viewport-aware candidate retrieval correction

> **Historical note:** Very small viewports could still lose locality context
> when they contained no source address point. That case was subsequently
> corrected in [0068](0068-tight-viewport-nearest-results.md).

**Date:** 2026-09-19

**Base revision:** `87dddcde316fc321906d4c88e84a043175d570c6`

**Scope:** correction to the viewport-aware Places Autocomplete work recorded
in [0066](0066-viewport-aware-search.md), prompted by a live Chicago search for
`ups store`.

## Failure observed

The first deployed implementation applied the viewport only after the ordinary
national lookup had selected five candidates. A Chicago rectangle around
`41.64,-87.94` to `42.02,-87.52` therefore returned stores in Jonesboro,
New York, Providence, Miami and Hayward: no Chicago candidate had entered the
five-result set for the geographic ranking stage to promote.

A control request for `ups store chicago il` returned Chicago-area stores,
confirming that the retained catalog contained appropriate results and that the
defect was candidate retrieval rather than missing source data.

## Correction

For a non-degenerate viewport no larger than two degrees of latitude or
longitude, the store now uses the existing source-backed `address_spatial`
projection to find nearby addresses. It materializes a bounded set of at most
eight candidates and extracts the first usable locality and region, such as
`chicago il`. Autocomplete performs this contextual lookup concurrently with
the ordinary national text lookup and, for explicit street names, the nearby
street lookup. Contextual and national candidates are deduplicated and merged
before coordinate materialization and soft geographic ranking.

This preserves the API semantics:

- the viewport can introduce a nearby text match before the response cutoff;
- national candidates are retained, so results outside the rectangle remain
  eligible;
- text relevance remains ahead of geographic proximity; and
- the locality context comes from lookup source records, not PMTiles or the
  selected basemap.

Degenerate rectangles, rectangles spanning more than two degrees in either
dimension and rectangles without a usable source address fall back to the
ordinary candidate set plus the existing exact-street behavior. The bounded
city-scale rule avoids treating a continent-scale rectangle as one locality and
keeps the additional source materialization predictable.

## Deterministic verification

The DuckDB fixture now includes ten identically named `Parcel Store` entities.
The sole Maine entity is deliberately outside the ordinary top five, while the
other nine are in Oregon. A source-backed address inside the Maine viewport
supplies `portland me` context. The test proves that:

- an un-biased request excludes the Maine entity, exercising the pre-limit
  failure mode;
- the same request with a Maine viewport introduces and ranks the Maine entity
  first; and
- the five-result response still contains outside-viewport entities, proving
  bias rather than restriction.

The complete working tree passed:

```text
gofmt on all changed Go files
go test ./...
go vet ./...
go test -race ./internal/placesgeocoding/duckdb -run TestStructuredAutocompleteContext -count=1
node --check public/app.js
node --check public/routing.js
git diff --check
```

There is no frontend package-manager or browser-unit test suite.

## Deployment and live verification

The corrected binary is deployed at `http://88.99.93.246:8080/` as
`/srv/openmaps/bin/openmaps-demo-viewport-context-20260920`, managed by the
active transient unit `openmaps-demo-viewport-context.service`. Startup took
about 7m29s because the service revalidated the complete national generation
before opening the listener. The unit has zero restarts and `/healthz` reports
`status: ok`.

The deployment retained lookup generation
`/srv/openmaps/data/national-catalog-research-2cfd7fc` with manifest SHA-256
`7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`.
The source and served US PMTiles paths retained the same inode (`30601737`) and
size (`20,772,330,499` bytes); a public range request returned HTTP 206 and
`Content-Range: bytes 0-126/20772330499`.

Serial public HTTP smoke measurements were:

| Operation | Viewport | First result | Time |
| --- | --- | --- | ---: |
| Autocomplete `ups store` | Chicago (`41.64,-87.94` to `42.02,-87.52`) | The UPS Store, 1708 W Chicago Ave Apt 1, Chicago (`41.896254,-87.669976`) | 3.02s |
| Autocomplete `ups store` | New York City (`40.49,-74.27` to `40.92,-73.68`) | The UPS Store, 1514 Broadway, New York | 4.46s |
| Autocomplete `Main Street` | New York City (`40.4774,-74.2591` to `40.9176,-73.7002`) | `om_0121d6b5e3c88873ac64519b3ecd3186` at `40.897494,-74.040122` | 18.82s |
| Autocomplete `Main Street` | Los Angeles (`33.70,-118.70` to `34.34,-118.15`) | `om_0bb597d681ad0730d11806b288b19d5f` at `34.009632,-118.490262` | 29.46s |
| Forward `50 Bellevue Ave` | New York City | NJ candidate `om_9235de15d7c5c734d302a97c64446a3d` | 1.56s |
| Forward `50 Bellevue Ave` | Los Angeles | CA candidate `om_ef25b29ff8a74467ef5f4f07ea380f92` | 1.28s |

Chicago returned five Chicago-area UPS Stores instead of the prior Jonesboro,
New York, Providence, Miami and Hayward set. The identical text in New York
returned five New York stores. Both forward responses retained all 19 exact
candidates while changing their first result, confirming bounds bias rather
than restriction. The deployed `app.js` contains the `map.getBounds()` Places
and geocoding request paths.

## Remaining limitations

- Contextual candidate generation depends on a usable nearby source address;
  sparse areas may retain only the ordinary national candidates.
- The contextual query adds locality and region tokens. It does not yet use a
  compact general-purpose spatial Places projection; building that projection
  remains the durable path for broader and faster geographic retrieval.
- Exact ambiguous street lookup retains its separate bounded spatial path and
  remains expensive on the national generation.
