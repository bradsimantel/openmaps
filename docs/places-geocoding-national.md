# National Places/geocoding builds

Open Maps has a bounded-memory path for building normalized Places/geocoding
Parquet and its DuckDB serving catalog directly from pinned Overture Parquet.
It does not create a nationwide `Bundle`, GeoJSON export, or transport JSON
file. The Newport JSON workflow remains the default regional demonstration.

## Reproducible scope and pins

[`config/places-geocoding-us.json`](../config/places-geocoding-us.json) is the
production candidate definition. It pins Overture release `2026-08-19.0`, the
SHA-256 of that release's STAC catalog, each provider dataset prefix, and the
SHA-256 of the sorted exact Parquet-object URLs selected from the catalog. A
second digest pins each selected URL together with its content length and ETag.
Changing a release, catalog, object set, object version, scope, batch size, or
memory limit is a reviewed configuration change. Remote Parquet is range-read
with its ETag in `If-Match`; a changed object or a server that ignores byte
ranges aborts.

The initial national scope means the 50 states and District of Columbia. Three
closed WGS84 longitude/latitude envelopes cover the contiguous states, Alaska,
and Hawaii. Addresses and divisions must carry Overture country `US`.
Businesses with a country-bearing first address must also carry `US`; a
business without source country is retained when its point is inside an
envelope. Named road segments have no supported country field, so they are
included when their actual centerline intersects an envelope. This can retain
short Canadian or Mexican road portions near a rectangular border. That known
scope imprecision must be reviewed before public national activation; it does
not make memory or identity behavior unsafe.

Puerto Rico, the U.S. Virgin Islands, Guam, American Samoa, and the Northern
Mariana Islands are excluded from this initial definition. They require an
explicit later scope/configuration decision. Offshore points and line portions
inside an included envelope are retained; those outside are not. A line that
crosses an envelope is clipped at the closed boundary. Its display point is the
spherical-length midpoint of all retained portions, preserving source edge
order and linearly interpolating longitude/latitude within the selected edge.
It is a search marker, not a routing snap.

Included entity classes are Overture places as businesses, standalone
addresses, divisions as administrative/locality areas, and named
Transportation `subtype=road` segments as streets. Buildings are not currently
imported as lookup entities and are not conflated with addresses or businesses.
Unnamed roads, non-road segments, out-of-scope points, records without a
required display label, and labels/aliases/addresses that normalize to no
search token are explicit rejection rows with their original source record.
Routing remains the independent Scout snapshot; basemap tiles remain
independent Protomaps data.

Every division asset row is streamed into a temporary bounded DuckDB relation
so recursive parent closure can retain name-bearing parents outside the initial
envelope. Only selected areas, their selected parents, and relationships are
published. A required provider parent without a primary name cannot become a
valid area and is rejected rather than assigned a fabricated name.

## Bounded construction and output

The adapter submits at most 2,048 provider records, relationships, or
rejections per call. Temporary DuckDB tables own business/address matching and
division ancestry. Business links require exact normalized number/street and
five-character postcode, one candidate within 50 spherical metres, and no
second qualifying candidate. Normalization externally sorts public identity
groups and retains only one entity's contributing source records in Go. The
configuration caps both preparation and normalization DuckDB instances at
1 GB and the Go runtime has a 1.5 GiB soft memory limit. Parquet dictionaries
fall back to plain encoding at 4 MiB per column. Serving-catalog construction
uses one DuckDB thread and a separately pinned 3 GB limit. A process supervisor is still
required because these component limits are not a hard whole-process RSS bound.

Public IDs continue to hash immutable source-qualified identity anchors.
Provider source IDs, release, original records, winning-attribute paths,
attribution, identity mappings, relationships, and rejection evidence are
retained. Normalized files use deterministic ordering, Zstandard, 32,768-row
groups, and rotate at a row-group boundary near 384 MiB. The output layout is
the same `entities*`, `source-records*`, `attribute-provenance*`,
`relationships*`, `rejections*`, `metadata.parquet`, `serving.duckdb`, and
`manifest.json` layout documented in [refresh and rollback](refresh.md).
Normalized Parquet checksums are reproducible; physical DuckDB files are bound
by their own checksums but are compared logically rather than assumed to be
byte-reproducible.

## Commands

Check RAM and disk, download/verify only the small pinned catalog, and inspect
the exact remote asset set plus conservative candidate-row workspace estimate:

```sh
sysctl -n hw.memsize
df -h data
go run ./cmd/places-geocoding-prepare \
  -config config/places-geocoding-us.json -data data -fetch -preflight
```

The preflight reports geographic candidate rows separately from rows that must
actually be read. Division reads include the global parent set, so the workspace
coefficient named `estimated_peak_bytes_per_candidate_row` is conservatively
applied to the larger rows-read count, then increased by a measured 10% safety
margin. The preflight does not start a build. A
build is refused when its conservative estimate would leave less than the
checked-in 60 GiB free-disk floor. Do not
run the following until that preflight passes on a suitably provisioned host:

```sh
go build -o /tmp/openmaps-places-prepare ./cmd/places-geocoding-prepare
go build -o /tmp/openmaps-run-bounded ./cmd/scout-run-bounded
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-build-resources.json" \
  -rss-mib 6144 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us.json -data data \
  -stream-out data/openmaps-us-20260819 \
  -audit data/openmaps-us-20260819-audit.json
```

That one atomic command performs range acquisition, provider preparation,
normalization, catalog construction, checksum verification, and referential
integrity validation. It publishes only after every phase succeeds and refuses
an existing output. Remote Parquet is not retained locally; retaining an
independently archived copy for long-term rebuilds is an operator decision that
is not implemented by this command. Interrupted builds currently restart the
range-read preparation phase; partial publication is never resumed.

Before activation, run representative queries against the candidate, compare
it with the selected generation using reviewed scope-appropriate query
expectations, and follow the review/activation commands in
[refresh and rollback](refresh.md). The maintained comparison command defaults
to Newport expectations, so a national operator must pass an explicit
`-queries` file:

```sh
go run ./cmd/places-geocoding-refresh compare \
  -candidate data/openmaps-us-20260819 -queries config/us-query-checks.json \
  -report data/us-refresh-report.json
go run ./cmd/places-geocoding-refresh review \
  -report data/us-refresh-report.json -review data/us-refresh-review.json \
  -reviewer NAME -reason 'Reviewed national scope and query changes'
go run ./cmd/places-geocoding-refresh activate \
  -candidate data/openmaps-us-20260819 -report data/us-refresh-report.json \
  -review data/us-refresh-review.json
```

The query file path above is an operator-supplied reviewed artifact; no national
expectation set is checked in yet. Authentication, billing, full Google field coverage, exact
non-rectangular national road clipping, buildings, territories, and a
production hosting topology remain unsupported or undecided.
