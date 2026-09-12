# Open Maps

Open Maps is a Go replacement for selected Google Maps HTTP API operations,
with immutable normalized Parquet, a compact DuckDB serving catalog, MapLibre
GL JS and Protomaps.

**Places and Newport geocoding work:** autocomplete businesses, standalone addresses,
streets and areas in downtown Newport, Rhode Island; retrieve details using the
returned ID; and display the selected result on a map. Dedicated forward geocoding
matches full address labels;
map-click reverse geocoding finds a supported address point within 100 metres.
Both show source precision and preserve ambiguous identities. It uses real regional
imports, not demo data embedded in the client.

Repeatable snapshot builds, identity review, comparison, live selection and rollback
are implemented in the [Newport refresh workflow](docs/refresh.md). Overture
2026-08-19.0 remains the baseline release; the second pin is a historical July
rehearsal, not a newer Overture release.

**Scout is the sole routing backend.** Go reads pinned Scout Valhalla 3.4.0
tiles and owns coordinate snapping, estimated edge-speed costs, restrictions,
search, geometry, preparation and snapshot lifetime. The national graph passes
83 frozen representative cases across all states/DC, including Alaska, Hawaii,
Aleutian roads and international road legs. This is sampled qualification, not
exhaustive source coverage. See [Scout routing](docs/routing-scout.md) and the
[historical qualification](docs/log/0034-national-scout-qualification.md).

The explicit `osm-scout-public-auto-v1` profile excludes ferries and destination
access and cannot reproduce discarded source tags or verify the exact OSM cutoff.
Scout still receives coordinates only: the API layer can resolve Open Maps place
IDs and exact supported address strings through the selected lookup snapshot before
calling it. Places and geocoding remain available in the same service. Lookup
generations and routing snapshots have independent selection and lifetime. Acquisition,
preparation and verification run with Go tools; Python and Valhalla's routing engine
are not required.

## Run the Newport demo

Prerequisites: Go **1.26.1+**, CGO, and a native C/C++ toolchain. Allow several minutes for first-time tool and data
downloads. All commands below run from the repository root. No API keys,
Docker or external database server are required.

```sh
go run ./cmd/places-geocoding-prepare -fetch

go run ./cmd/places-geocoding-import

go run ./cmd/basemap

go run ./cmd/server
```

The acquisition, GeoParquet decoding, normalization, deterministic Parquet and
DuckDB serving-index build pipeline is Go code in this repository. The importer
refuses to overwrite a generation directory and atomically initializes or
advances `data/lookup-selection.json`. DuckDB build and runtime disable extension
autoload and use no downloadable extensions.

Nationwide candidate builds use a separate checked-in scope and stream pinned
Overture Parquet directly through spillable DuckDB preparation and the same
normalized-Parquet/catalog builder; they never create a national transport
JSON document. The [national build guide](docs/places-geocoding-national.md)
defines its pins, geographic rules, safety preflight, limitations, and guarded
commands. The regional Newport workflow above is unchanged.

Open **http://127.0.0.1:8080**. Try `White Horse`, `50 Bellevue`, `Thames`, or
`Newport`. Click a suggestion; keyboard users can press Down from the input and
Enter to select. Names, coordinates, available address and website come from the
selected entity's details response.

For geocoding, choose **Forward geocoding**, enter `50 Bellevue Ave`, and press
**Find address**. Click the map for reverse lookup. The result shows source
coordinates, approximate precision and reverse distance. `364 Bellevue Avenue`
requires choosing among eight distinct points; apartment requests are explicitly
unsupported. See [the maintained geocoding contract](docs/geocoding.md).

For driving, add `-routing-snapshot PREPARED_DIRECTORY` to the server command after
following [Scout acquisition and preparation](docs/routing-scout.md#acquisition-and-preparation).
The browser uses selected lookup coordinates or map points. The HTTP API also
accepts an Open Maps ID returned by autocomplete/details or an exact supported
address for either endpoint. Address text retains the strict geocoding grammar and
must identify one standalone address. Duration uses uncalibrated provider speeds
and excludes traffic and unverified off-road gaps. Routes outside the Newport
basemap can still be returned, although the local basemap has no tiles there.

`data/` is ignored by Git.
The four canonical regional Overture exports and catalog are about 12 MB; the
normalized Parquet plus DuckDB lookup generation is about 10 MB. The Protomaps
cutout is about 3.8 MB. Internet is required
for initial downloads, esm.sh browser libraries and Protomaps-hosted fonts/sprites.
Lookup APIs
and local basemap tile requests work without external services after import.

The browser imports MapLibre GL JS 5.0.1, PMTiles 4.2.1 and Protomaps basemaps
5.7.2 directly from [esm.sh](https://esm.sh/); MapLibre CSS uses the same version.
There is no frontend install, build step or vendored asset directory. Dependency
versions are explicit in `public/app.js` and `public/index.html`. A CDN failure leaves
search and details available, but the map cannot initialize.

Data snapshots are pinned in the domain configs under `config/`. Overture and
Protomaps may expire old hosted releases;
archive the verified local inputs for long-term rebuilds. A download or checksum
failure never falls back to a newer dataset. Normal setup must not use
`-accept-reviewed-source-update` or edit expected checksums to bypass a mismatch.

Already have source files? Rebuild offline into a **new** generation:

```sh
go run ./cmd/places-geocoding-prepare
go run ./cmd/places-geocoding-import -out data/openmaps-next -selection data/lookup-next.json
go run ./cmd/server -lookup-selection data/lookup-next.json -listen 127.0.0.1:8081
```

The import refuses to overwrite an existing generation. Stop the earlier service
before reusing its port. The service defaults to loopback and supports `-lookup`,
`-lookup-selection`, `-listen`, `-public` and `-tiles`;
`go run ./cmd/server -help` lists defaults. `-lookup` opens one verified
generation directly; selection mode is the default and provides live activation
and rollback. Verification or open failure is fatal—there is no legacy fallback.

## Supported API

Places compatibility target: **Google Places API (New), REST v1**, checked against
current official documentation on 2026-09-07. This is a documented subset, not
full Google coverage, ranking or SDK compatibility. Open Maps IDs are independent
of Google place IDs.

```sh
curl -sS http://127.0.0.1:8080/v1/places:autocomplete \
  -H 'Content-Type: application/json' \
  -d '{"input":"White Horse"}'

curl -sS http://127.0.0.1:8080/v1/places/om_a5e3dc7692e4d3b90b71b94fba66ec5b \
  -H 'X-Goog-FieldMask: id,displayName,formattedAddress,location,types,websiteUri,attributions'
```

| Endpoint | Supported behavior |
| --- | --- |
| `POST /v1/places:autocomplete` | Required `input`; optional English `languageCode`, `sessionToken`, and response field mask; up to five place predictions |
| `GET /v1/places/{id}` | Required response field mask; optional English `languageCode` and `sessionToken`; every returned suggestion ID resolves here |
| `GET /maps/api/geocode/json` | Geocoding v3 JSON subset: exactly one of `address` or `latlng`; optional English `language` and unauthenticated `key` |
| `POST /directions/v2:computeRoutes` | Routes REST v2 subset: coordinate, Open Maps Place ID, or exact unique address origin/destination; driving, GeoJSON geometry, road distance and estimated duration; requires routing data, lookup data for non-coordinate forms, and a response mask |
| `GET /healthz` | Process health and routing availability; in selection mode, loaded lookup snapshot identity and reload failures |
| `GET /tiles/newport.pmtiles` | Separate regional basemap file with HTTP range support |

Details exposes IDs, display name, coordinates, conservative types, attribution,
and address/website when available. Masks support parent and leaf paths, or `*`.
Unsupported parameters and fields return `INVALID_ARGUMENT`; unknown IDs return
`NOT_FOUND`. No ratings, photos, opening hours, entrances or other unavailable
attributes are fabricated. API keys are accepted for client compatibility but
are **not authenticated**; there are no billing or per-client quotas. The server
limits concurrent routing work to four requests by default; excess routing
requests receive HTTP 429. This is not a production authentication system.

The [historical initial API target decision](docs/log/0001-places-api-target.md) records the
first milestone’s request, field-mask and error contract, with links to Google’s
official references.

Geocoding targets **Google Geocoding API v3 HTTP JSON**, checked 2026-09-07.
It uses a separate `status`/`results` envelope, not Places errors or field masks.
Only exact normalized number + complete street labels and nearest supported
address points are returned; there are no street/locality fallbacks. Results use
existing address IDs and `APPROXIMATE` geometry. Available structured source
components are returned in `address_components`. Explicit units, ranges, fractions,
filters and unsupported parameters fail visibly. Missing source locality is not
inferred; Newport context is marked partial. See [supported requests, outcomes
and source limitations](docs/geocoding.md) and the [historical contract decision](docs/log/0008-geocoding-contract.md).

## Region, sources and identity

Launch rectangle: longitude **−71.33 to −71.29**, latitude **41.47 to 41.51**.
It covers downtown Newport and nearby streets, not the full municipality.
This limits lookup imports. Scout routing uses its retained tile envelope,
independent of the Newport lookup rectangle.

| Imported data | Pinned source | Records |
| --- | --- | ---: |
| Businesses and POIs | Overture Places 2026-08-19.0 | 2,173 |
| Standalone address points | Overture Addresses 2026-08-19.0, NAD-derived | 8,545 |
| Settlements and administrative context | Overture Divisions 2026-08-19.0 | 4 |
| Named road segments | Overture Transportation 2026-08-19.0 | 1,621 |

51 explicitly closed businesses are excluded from autocomplete but retain details.
There are 1,181 conservative business/address links and three area-parent links.
The four areas include available parents outside the launch rectangle.

[The historical initial regional inspection](docs/log/0002-newport-data-and-import-design.md) records field completeness,
duplicate labels, relationships, coverage gaps and the decision to defer a
supplemental source. It distinguishes address points from business address
strings and area label points from boundaries. `data/audit.json` is regenerated
by the import adapter and records Transportation segment selection separately
from imported street counts.

The [Overture Transportation](https://docs.overturemaps.org/guides/transportation/)
export contains all 4,463 segments whose source bounding box
intersects the launch rectangle. Import then requires `subtype=road`, a nonempty
primary name and an actual centerline intersection with the inclusive rectangle.
This retains 1,621 named road segments; 2,835 unnamed roads and seven non-road
segments remain in the pinned source artifact but are not Places street entities.
Every retained segment keeps its own `overture:segment` source ID and public ID,
even when many segments have the same name. Autocomplete may collapse equal
labels in its five suggestions, but details always identifies one segment, not a
merged or complete street.

Street aliases include `names.common` values and `names.rules` values. The full
raw feature retains language, variant, geometric range, side and perspective
scope that the search index itself cannot express. A street's displayed point is
the distance midpoint of the portions of its centerline inside the rectangle:
edges are clipped to the closed rectangle, their spherical lengths are summed,
and the midpoint is linearly interpolated in WGS84 longitude/latitude. It is a
search marker for that segment, not an address, entrance, routing snap or claim
about the whole named street.

The retired direct OSM import produced 880 named ways with 541 distinct primary
labels. Overture produces more, shorter segments but 535 distinct primary labels,
so record counts are not a coverage target. Its primary labels omit six former
labels: `Clematis`, `Hammet Place`, `Katzman Place`, `Linden`, and `Willow` remain
searchable as scoped aliases on other named segments; `Colbert Plaza` is absent
from the pinned Transportation subset. No new primary label appears. This is a
meaningful source-model difference, not evidence that every road gained coverage.

[The Places/geocoding config](config/places-geocoding.json) records versions, URLs, bounds,
source checksums, the deterministic provider-record stream checksum and attribution references. The
Go importer verifies all of them. Each entity keeps source-qualified identifiers,
original records and attribute provenance. Public IDs derive from permanent
identity anchors, independent of row IDs, import order or mutable attributes.

Additional sources can supply new records or enrich existing entities through an
explicit identity mapping passed to `places-geocoding-prepare -identities`, without API changes or
renumbering existing entities. There are no mappings in the current configuration.
Overture address IDs currently lack a stable
upstream matcher and are not in its GERS registry; changed source values can
require reviewed replacement mappings. See the [historical address-source
comparison](docs/log/0013-address-source-comparison.md). Highest source priority
wins each nonempty attribute; ties use source key order. All contributing values
remain stored.
There is no fuzzy identity merging or speculative provider plugin framework.
See the [historical initial matching and conflict rules](docs/log/0002-newport-data-and-import-design.md#matching-identity-and-conflict-resolution).

The [basemap config](config/basemap.json) separately pins a Protomaps
2026-09-06 regional cutout at zooms 0–15. The Go service serves it locally; tiles
are never used as lookup data. Upstream notices are linked on the demo's
[attribution page](public/attribution.html).

## Code layout

```text
cmd/server/         Go HTTP service
cmd/places-geocoding-prepare/ Regional preparation plus national-safe pinned Parquet streaming/preflight
cmd/places-geocoding-import/ Streaming normalized Parquet and DuckDB generation builder/selector
cmd/places-geocoding-duckdb/ Direct production generation builder used for controlled builds
cmd/scout-acquire/  Complete provider metadata, selection, resumable downloads
cmd/scout-prepare/  Immutable routing graph and index publication
cmd/scout-landmarks/ Directed landmark construction and safe extension
cmd/scout-verify/   Frozen cases and independent source-path verification
cmd/scout-audit/    Source dependencies, geometry and snap audits
cmd/scout-coverage/ Pinned Census footprint verification
cmd/scout-http-verify/ Full HTTP body comparison and concurrency measurement
cmd/scout-run-bounded/ Sampled RSS/free-disk supervision
cmd/basemap/        Verified regional extraction using the pinned Go PMTiles CLI
cmd/places-geocoding-refresh/ Snapshot build, comparison, review, activation and rollback
internal/places/    Provider-independent entity types and search normalization
internal/geocoding/ Address grammar, context matching and distance semantics
internal/routing/   Scout decoder, search, indexes and snapshot leases
internal/routing/qualification/ Offline coverage and HTTP verification
internal/api/       Google request/response translation and errors
internal/importer/  Source adapters, stable identity and conflict/provenance rules
internal/importer/scout/ Provider acquisition, receipts and preparation handoff
internal/importer/addressdata/ Retained provider address decoding
internal/placesgeocoding/duckdb/ Production artifact, reader, comparison, selection and snapshot leases
config/            Pinned Places/geocoding, routing and basemap inputs
public/            Browser ES modules and styles; libraries loaded from esm.sh
```

One Go service reads a compact DuckDB catalog and normalized Parquet evidence.
The catalog owns the sorted token dictionary, posting lists, short-prefix heads,
exact-address projection, deterministic spatial grid and Parquet locators.
Lookup activation replaces Places and geocoding together. Routing independently
loads an immutable graph and turn-restriction index from its own snapshot. The owned Go import pipeline uses
`parquet-go` for cloud GeoParquet and decodes Overture Point and LineString WKB; provider
parsing stays in `internal/importer`. The basemap command invokes a pinned Go
PMTiles extractor in a separate module to keep its cloud SDKs out of the service
dependencies.

The removed predecessor lookup path remains documented only in historical log
0044.
The later bounded
[DuckDB token-prefix proof](docs/log/0045-duckdb-token-prefix-proof.md) and
[Go qualification](docs/log/0046-duckdb-go-qualification.md) met the regional
correctness, resource, concurrency and deployment gates. Normalized Parquet
plus an immutable DuckDB serving catalog is therefore the recommended lookup
architecture selected by the production server.
Routing remains independent of text search and address resolution. The API holds
one lookup snapshot lease while resolving both endpoints, then gives Scout WGS84
coordinates. Scout uses bounded graph pages and directed landmark A*, preserving
full turn history and source steps. Ordinary Go traversal and independent
source-path replay remain correctness references. See
[the maintained routing design](docs/routing-scout.md).

## Verification

Small synthetic fixtures and offline checks:

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
```

The same formatting, test, vet, whitespace, and maintained-JSON checks run in
[GitHub Actions](.github/workflows/ci.yml) on pushes and pull requests.

These cover API response shapes and errors, masks, search ranking, accents,
street abbreviations, repeated street labels, distinct repeated address labels,
closed-place behavior, dateline coordinates, multilingual Point and LineString
GeoParquet parsing, Transportation selection and clipped representative points,
HTTP range validation, cancellation, checksums,
relationships, rejected imports, stable IDs across reordered/released imports,
and source enrichment/replacement without changing existing IDs. Refresh tests cover
generation comparison, corrupt or missing shards, interrupted builds, stale
reviews, failed switches, concurrent replacement and live rollback. Geocoding tests
cover normalized address numbers/streets, context, duplicate
identities, explicit unit errors, invalid coordinates, distance cutoffs and
coverage, plus atomic snapshot selection and rollback. Routing adds one-way and
via-way restrictions, barriers/access, disconnected and crossing roads, snapping,
API contract/errors, speed units/directions/uncertainty, duration accumulation,
longer-but-faster routes, source-backed detour checks and isolated rollback. No regional
source downloads are part of `go test ./...`.

Browser verification now works through the Codex desktop in-app browser plugin.
On 2026-09-07, a real business and standalone address passed autocomplete,
details and visible map placement on both the August baseline and July candidate;
rollback restored August results. The external Chrome extension connection
remains unresolved. No standalone browser runner or npm tooling was added.
Browser libraries, fonts and sprites require network access. See the
[historical desktop browser verification](docs/log/0006-desktop-browser-verification.md)
and [historical refresh verification](docs/log/0005-newport-refresh-verification.md).

The geocoding milestone also passed a [26-case source-backed benchmark](docs/geocoding.md#deterministic-quality-benchmark)
against a fresh DuckDB generation, and in-app browser forward/reverse flows
with ambiguity, errors and rollback. See [historical geocoding verification](docs/log/0009-geocoding-verification.md).

Check a real business, address, street and area through autocomplete, details and
map placement. Also check keyboard selection, empty results, fast input changes,
mobile layout and JavaScript errors. Blocking esm.sh should leave search and
details available. See [historical first-milestone verification results](docs/log/0003-first-milestone-verification.md).

The [historical estimated-time verification](docs/log/0018-newport-estimated-driving-time.md)
records the 31-trip coordinate and 22-case address comparisons, reproducible
candidate builds, browser checks and measured startup-memory cost. No independent
travel-time observations were available; passing route invariants is not
validation of real-world estimate accuracy.

The [historical routing scale and Oregon evaluation](docs/log/0019-routing-scale-and-oregon.md)
records compact storage, indexed endpoint selection, search acceleration,
source-backed regional cases, concurrent replacement and measured resource use.
These earlier routing designs have been retired; [Scout](docs/routing-scout.md)
describes the current architecture.

The [local deployment guide](docs/deployment.md) describes reusable instance
startup, graceful shutdown, update and rollback requirements. Machine-specific
service evidence remains in historical logs.

## Current limitations and next work

- Limited geography and English request options; no global/IP bias, spatial or
  type filters, translation, typo tolerance, plus-code support or Google ranking.
- Search uses normalized token prefixes with exact-name/name-prefix priority,
  then area/street/business/address precedence, BM25-style token scoring and
  stable ID ties.
- Source labels can be incomplete or duplicated. The retained Overture locality
  slot and units are empty; 562 raw records have a CDP-like `postal_city` value
  that is not returned as a locality or verified postal city. Address ranges are
  retained verbatim in Places. Geocoding supports
  8,407 of 8,545 source labels; 138 nonstandard forms remain excluded from both
  geocoding directions. Coverage and source positional accuracy are not certified.
- Streets are individual Overture road segments, with a representative point on
  the in-region part of their centerline. Equal names are not merged into a
  street-wide identity. Area locations are labels. Neither means an entrance,
  rooftop guarantee, boundary or routing snap.
- Freshness follows pinned snapshots. Refreshes support reviewed one-to-one
  provider ID replacements and retain uncertain splits/merges as distinct IDs.
  No scheduling or automatic release discovery is implemented. Retain source
  locks, replacement evidence and snapshot identity history across rebuilds.
- The local map cutout is finite; zooming or panning far outside Newport can show
  missing tiles. Browser libraries, fonts and sprites use external hosts.
- Scout minimizes uncalibrated provider edge-speed cost. No traffic, turn delay,
  navigation instructions, destination-only access or ferries. Place/address
  coordinates are source points, not verified entrances or access points.
  Retained tiles do not establish complete source coverage or an exact OSM cutoff.
  Missing dependencies, unsupported snaps and disconnected networks remain
  distinct outcomes. See [the profile and limits](docs/routing-scout.md).
- Deployment hardening, continuous coverage evaluation and richer data are
  later work.
