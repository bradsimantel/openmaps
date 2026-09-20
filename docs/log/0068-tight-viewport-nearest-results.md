# Tight-viewport nearest autocomplete correction

**Date:** 2026-09-19

**Base revision:** `87dddcde316fc321906d4c88e84a043175d570c6`

**Scope:** follow-up to [0067](0067-viewport-candidate-retrieval.md) for
high-zoom viewports containing no matching place or source address point.

## Failure observed

A very small rectangle centered in Chicago's Grant Park (`41.88269,-87.61901`
to `41.88271,-87.61899`) contained no source address point. The locality lookup
therefore produced no context, and `ups store` fell back to the national five:
Jonesboro, New York, Providence, Miami and Hayward. This reproduced the user
report even though a wider Chicago viewport worked.

## Correction

City-scale viewport context is now resolved from the closest usable source
address within five kilometres of the viewport center, rather than requiring
an address inside the rectangle. The lookup uses a latitude/longitude bounding
window with antimeridian-aware longitude predicates, spherical distance for
the final radius check and deterministic ID tie-breaking.

The contextual text lookup now retains up to 25 candidates before coordinate
materialization and geographic ranking, instead of truncating to five first.
The final public response remains limited to five. Ordinary national candidates
are still merged into the pool, so this remains a soft bias and does not make
outside results ineligible.

The fixture includes a tiny Maine viewport containing no matching store and no
address point, two Portland stores outside the rectangle at different
distances, and distant Oregon stores with the same name. It verifies that the
nearest Maine store ranks first, all returned stores may remain outside the
rectangle, and neither local store appears in the un-biased national top five.

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
`/srv/openmaps/bin/openmaps-demo-viewport-nearest-20260920`, managed by the
active transient unit `openmaps-demo-viewport-nearest.service`. Full artifact
validation took about 7m19s before the listener opened. The unit has zero
restarts and `/healthz` reports `status: ok` against the unchanged lookup
manifest SHA-256
`7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`.

The exact failing Grant Park request now returns:

| Rank | Result | Distance from viewport center |
| ---: | --- | ---: |
| 1 | The UPS Store, 323 E Wacker Dr | 0.52 km |
| 2 | The UPS Store, 301 W Grand Ave | 1.69 km |
| 3 | The UPS Store, 40 E Chicago Ave | 1.70 km |
| 4 | The UPS Store, 308 S Jefferson St | 2.04 km |
| 5 | The UPS Store, 1074 W Taylor St | 3.24 km |

All five are outside the approximately two-metre-wide test rectangle, as
intended for a soft bias, and are ordered by actual distance within the retained
text-match class. The response took 3.43s. A wider Chicago request took 3.08s.
A tiny Times Square viewport returned five New York stores in 4.51s; three
consecutive follow-up runs returned HTTP 200 in 4.61s, 4.65s and 4.59s.

One initial Times Square request immediately following the wider Chicago smoke
returned no client body. The service did not restart, health remained OK and
the four subsequent requests were stable; no repeatable server failure was
found.

The source and served US PMTiles paths remain hard links with inode `30601737`
and size `20,772,330,499` bytes.

## Remaining limitation

The five-kilometre context lookup requires a usable source address and the
contextual pool is bounded at 25. A compact spatial projection of searchable
places remains the durable way to guarantee a globally nearest text match in
sparse areas or across locality boundaries.
