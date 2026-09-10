# Overture Transportation street-search import

Date: 2026-09-10
Scope: uncommitted local implementation replacing the Newport lookup street source

## Decision and implementation

Places street search now uses the pinned Overture Transportation `segment` input
from the same Overture release as places, addresses and divisions. The default
Newport lock uses release `2026-08-19.0`; the retained historical refresh lock
uses `2026-07-22.0`. This changes lookup data only. Scout remains the sole routing
graph and Protomaps remains the basemap.

The existing Go catalog and HTTP range reader now acquires the regional
Transportation GeoParquet assets and decodes both Point and LineString WKB. The
normalized source export is deterministic GeoJSON and remains checksum-pinned.
There is no DuckDB, Python, direct OSM PBF download or PBF decoder in the lookup
pipeline.

Selection follows Overture's documented model:

- Acquisition includes source rows whose GeoParquet bounding boxes intersect
  the Newport rectangle `[-71.33, 41.47, -71.29, 41.51]`.
- Import validates every LineString, then retains only `subtype=road` features
  with a nonempty `names.primary` whose actual centerline intersects the closed
  rectangle.
- Each retained `overture:segment:<id>` remains an independent street entity and
  identity. Equal primary names do not merge segments. Autocomplete still
  suppresses duplicate labels within one response, but details identifies the
  selected segment rather than an entire named street.
- Search aliases contain all `names.common` values and all `names.rules[].value`
  values. Rule scope and variants remain in the raw feature even though the
  normalized search index cannot express linear, side or perspective scope.
- The representative coordinate is the length midpoint across all centerline
  portions inside the closed rectangle. Edges are clipped to the rectangle,
  spherical edge lengths determine the midpoint, and coordinates are linearly
  interpolated in WGS84 longitude,latitude. The point is only a segment search
  marker, not an address, entrance, routing snap or street-wide center.

These choices follow the official [Transportation guide](https://docs.overturemaps.org/guides/transportation/),
[segment schema](https://docs.overturemaps.org/schema/reference/transportation/segment/),
[names schema](https://docs.overturemaps.org/schema/reference/common/names/), and
[attribution guidance](https://docs.overturemaps.org/attribution/). Overture
documents road segments as LineString centerlines, includes segment IDs in GERS,
and identifies Transportation as ODbL data built primarily from OpenStreetMap
with TomTom and other sources.

## Pinned Newport findings

The `2026-08-19.0` export contains 4,463 intersecting Transportation segments.
Selection imports 1,621 named roads and excludes 2,835 unnamed roads plus seven
rail/water segments. It produces 535 distinct primary road labels.

The retired direct Rhode Island OSM PBF import produced 880 named ways and 541
distinct primary labels. Overture's larger row count reflects different and often
shorter segmentation; it is not a comparable completeness measure. All 535
Overture primary labels occurred in the retired import. Six former primary labels
are not primary Overture labels: `Clematis`, `Hammet Place`, `Katzman Place`,
`Linden`, and `Willow` remain searchable as scoped aliases, while `Colbert Plaza`
is absent. This is the observed meaningful lookup gap; no equal-count requirement
was imposed.

The `2026-07-22.0` historical export contains 4,449 segments and selects the same
1,621 named roads and 535 distinct primary labels. Its refreshed bundle no longer
reuses a street snapshot from another release. Historical log entries describing
the former Geofabrik input remain historical and were not rewritten.

## Removal

Removed the direct PBF acquisition branch, OSM node/way decoder, OSM address-node
overlap audit, synthetic PBF generator, missing-node test, Geofabrik inputs in
both regional locks, and the exclusive `paulmach/osm`, `paulmach/orb`,
`paulmach/protoscan`, and direct protobuf dependencies. Existing ignored PBF and
old SQLite files were not deleted; they are no longer referenced by code, locks,
commands or maintained documentation.

## Verification

- The synthetic routine fixture covers named and unnamed roads, aliases,
  non-road segments, exact boundary crossing, geometrically outside features,
  repeated primary names, raw `sources` provenance, checksum rejection,
  deterministic preparation and deterministic source-derived IDs.
- A downloaded-data integration rebuild verified 12,343 source records, including
  1,621 independent street source/public IDs, raw source provenance, SQLite
  integrity and foreign keys. `Thames`, `Bellevue Avenue`, and `Marlborough`
  returned street details; alias query `West Broadway` returned
  `Dr Marcus Wheatland Boulevard`. Sixty-two `Thames Street` segments retained 62
  distinct IDs.
- The existing 26-case geocoding benchmark and its 25 raw address evidence checks
  passed against the former and rebuilt databases. All 8,545 address entities
  were identical; all 2,173 businesses, four areas and all relationships were
  also unchanged.
- A temporary loopback deployment of the rebuilt database passed the existing
  live HTTP suite for business, address, street and area autocomplete plus
  details, including the no-result case and stable business/address IDs.
- `gofmt -w cmd internal`, `go test ./...`, and `go vet ./...` passed. A residue
  scan found no `.osm.pbf`, `osmpbf`, Geofabrik configuration, or `paulmach`
  dependency outside historical logs; protobuf remains only as a transitive
  `parquet-go` dependency.

## Limitations

The search index does not expose or evaluate Overture name-rule scope. A scoped
alias can match the containing segment even when the representative point falls
outside that alias's linear subrange. Full centerline geometry is retained only
in raw source JSON; Places details returns a point. Selection is a rectangular
preview, not a municipal boundary or a street completeness claim. Overture
Transportation is not used for routing, road snapping or basemap rendering.
