# Open Maps

Open Maps is a Go and SQLite replacement for selected Google Maps HTTP API
operations, with MapLibre GL JS and Protomaps for display.

**Places and Newport geocoding work:** autocomplete businesses, standalone addresses,
streets and areas in downtown Newport, Rhode Island; retrieve details using the
returned ID; and display the selected result on a map. Dedicated forward geocoding
matches full address labels;
map-click reverse geocoding finds a supported address point within 100 metres.
Both show source precision and preserve ambiguous identities. It uses real regional
imports, not demo data embedded in the client.

Repeatable snapshot builds, identity review, comparison, live selection and rollback
are implemented in the [Newport refresh workflow](docs/refresh.md). The original
August data remains the baseline; the second pinned release is a historical July
rehearsal, not a newer Overture release.

**Driving routing is implemented in separate candidate snapshots:** supply addresses, existing
lookup coordinates or arbitrary map points in one request, calculate an estimated-time driving
route, and see its geometry, road distance, estimated duration, requested/road endpoints and separate
unverified snap gaps. The passenger-car profile interprets vehicle limits and
supports strictly qualified destination-only access without through shortcuts. The graph uses the wider
retained Rhode Island extract for detours. The active August baseline is unchanged
and has no routing graph. See [the routing contract and candidate build](docs/routing.md).
Separate Oregon and full Oregon–Washington–Idaho coordinate-routing candidates
cover regional scaling and boundary detours. [Storage and scaling](docs/routing-scale.md)
describes the recursive junction-cell overlay, directly loadable prepared snapshots, concurrent
snapshot leases, compact prepared edges with dense internal endpoints, streaming
rollback verification and residency measurements. These regional candidates have no address coverage. Nearby place search and general
text search remain future milestones.

The [national candidate plan](docs/routing-national.md) proposes 50-state/DC
coordinate coverage, explicit connectivity limits and construction/serving budgets.
The PBF/SQLite pipeline fails national capacity preflight on this host; no
national publication has been built through that pipeline. See the [historical preflight](docs/log/0026-national-routing-preflight.md)
for source metadata, measured regional evidence and conservative build-resource estimates.

A separate [experimental Scout backend](docs/routing-scout.md) now reads pinned
Valhalla tiles directly through bounded Go page caches, prepared turn indexes,
and Go-owned search. Its isolated national coordinate candidate passes 83 frozen
cases across all states/DC, including long routes, Canada/Mexico road legs,
Alaska, Hawaii and Aleutian roads on both sides of the dateline. The
[historical qualification report](docs/log/0034-national-scout-qualification.md)
records source-path checks, HTTP replacement, resource measurements and known
source limitations. It uses an explicit
experimental profile because the tiles cannot reproduce all `driving-time-v4`
source, access, snapping and address behavior.

Offline construction now bounds segment/guard identity-index scratch and streams
publication encoding. The importer also avoids a duplicate segment-membership
map. Full source parsing, graph arrays and hierarchy construction still require
graph-sized memory; see [construction limits](docs/routing-prepared.md).

## Run the Newport demo

Prerequisite: Go **1.26.1+**. Allow several minutes for first-time tool and data
downloads. All commands below run from the repository root. No API keys,
Docker, external database server or system SQLite installation are required.

```sh
go run ./cmd/prepare -fetch

go run ./cmd/import -routing-pbf data/rhode-island-260801.osm.pbf

go run ./cmd/basemap

go run ./cmd/routing-prepare -db data/openmaps.sqlite -out data/prepared-routing

go run ./cmd/server -routing-prepared data/prepared-routing
```

The pipeline is Go code in this repository. If a C compiler or zlib headers are
unavailable, prefix Go commands with `CGO_ENABLED=0` to use pure Go PBF decoding.

Open **http://127.0.0.1:8080**. Try `White Horse`, `50 Bellevue`, `Thames`, or
`Newport`. Click a suggestion; keyboard users can press Down from the input and
Enter to select. Names, coordinates, available address and website come from the
selected entity's details response.

For geocoding, choose **Forward geocoding**, enter `50 Bellevue Ave`, and press
**Find address**. Click the map for reverse lookup. The result shows source
coordinates, approximate precision and reverse distance. `364 Bellevue Avenue`
requires choosing among eight distinct points; apartment requests are explicitly
unsupported. See [the maintained geocoding contract](docs/geocoding.md).

For driving, enter **Origin address** and **Destination address**, then
**Calculate driving route**. The API resolves addresses and road arrivals automatically
or returns a clear failure in the same request. Selected lookup coordinates and
map points can also supply either endpoint.
**Clear route** resets the route and endpoints. Ambiguous address routes fail without a selection step. Duration uses conservative, uncalibrated speed estimates and excludes live traffic
and unverified off-road gaps. Retained older routing graphs provide distance only.
Existing databases are never overwritten: to add routing to retained data, build
and serve a [separate candidate](docs/routing.md#storage-builds-and-snapshots).

`data/` is ignored by Git.
The OSM regional PBF is about 52 MB; canonical Overture subsets and SQLite add
further local storage. The optional chunked Newport routing graph adds about
34 MiB to SQLite. The Protomaps cutout is about 3.8 MB. Internet is required
for initial downloads, esm.sh browser libraries and Protomaps-hosted fonts/sprites.
Lookup APIs
and local basemap tile requests work without external services after import.

The browser imports MapLibre GL JS 5.0.1, PMTiles 4.2.1 and Protomaps basemaps
5.7.2 directly from [esm.sh](https://esm.sh/); MapLibre CSS uses the same version.
There is no frontend install, build step or vendored asset directory. Dependency
versions are explicit in `public/app.js` and `public/index.html`. A CDN failure leaves
search and details available, but the map cannot initialize.

Data snapshots are pinned. Overture and Protomaps may expire old hosted releases;
archive the verified local inputs for long-term rebuilds. A download or checksum
failure never falls back to a newer dataset. Normal setup must not use
`-write-lock` or edit expected checksums to bypass a mismatch.

Already have source files? Rebuild offline into a **new** database:

```sh
go run ./cmd/prepare
go run ./cmd/import -db data/openmaps-next.sqlite -routing-pbf data/rhode-island-260801.osm.pbf
go run ./cmd/routing-prepare -db data/openmaps-next.sqlite -out data/prepared-next
go run ./cmd/server -db data/openmaps-next.sqlite -routing-prepared data/prepared-next -listen 127.0.0.1:8081
```

The import refuses to overwrite an existing database. Stop the earlier service
before reusing its port. The service defaults to loopback and supports `-db`,
`-listen`, `-public` and `-tiles`; `go run ./cmd/server -help` lists defaults.

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
| `POST /directions/v2:computeRoutes` | Routes REST v2 subset: address/coordinate origin/destination (including mixed), driving, GeoJSON geometry, road distance and estimated duration; requires routing data and response mask |
| `GET /healthz` | Process health and routing availability; in deployment mode, loaded database fingerprint and reload failures |
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
This is also the Newport candidate's routing endpoint rectangle; graph coverage
uses the whole retained Rhode Island extract to allow detours outside it.

| Imported data | Pinned source | Records |
| --- | --- | ---: |
| Businesses and POIs | Overture Places 2026-08-19.0 | 2,173 |
| Standalone address points | Overture Addresses 2026-08-19.0, NAD-derived | 8,545 |
| Settlements and administrative context | Overture Divisions 2026-08-19.0 | 4 |
| Named street ways | Geofabrik Rhode Island OSM PBF 2026-08-01 | 880 |

51 explicitly closed businesses are excluded from autocomplete but retain details.
There are 1,181 conservative business/address links and three area-parent links.
The four areas include available parents outside the launch rectangle.

[The historical initial regional inspection](docs/log/0002-newport-data-and-import-design.md) records field completeness,
duplicate labels, relationships, coverage gaps and the decision to defer a
supplemental source. It distinguishes address points from business address
strings and area label points from boundaries. `data/audit.json` is regenerated
by the import adapter.

[Source locks](imports/newport.lock.json) record versions, URLs, bounds,
checksums and attribution references. The [normalized bundle checksum](imports/newport.bundle.sha256)
is verified by the Go importer. Each entity keeps source-qualified identifiers,
original records and attribute provenance. Public IDs derive from permanent
identity anchors, independent of row IDs, import order or mutable attributes.

Additional sources can supply new records or enrich existing entities through
[explicit identity mappings](imports/identities.json), without API changes or
renumbering existing entities. Overture address IDs currently lack a stable
upstream matcher and are not in its GERS registry; changed source values can
require reviewed replacement mappings. See the [historical address-source
comparison](docs/log/0013-address-source-comparison.md). Highest source priority
wins each nonempty attribute; ties use source key order. All contributing values
remain stored.
There is no fuzzy identity merging or speculative provider plugin framework.
See the [historical initial matching and conflict rules](docs/log/0002-newport-data-and-import-design.md#matching-identity-and-conflict-resolution).

The [basemap lock](imports/basemap.lock.json) separately pins a Protomaps
2026-09-06 regional cutout at zooms 0–15. The Go service serves it locally; tiles
are never used as lookup data. Upstream notices are linked on the demo's
[attribution page](public/attribution.html).

## Code layout

```text
cmd/server/         Go HTTP service
cmd/prepare/        Pinned acquisition, regional normalization and audit
cmd/import/         Checksum-verified SQLite builder
cmd/routing-prepare/ Offline validation and prepared routing publication
cmd/basemap/        Verified regional extraction using the pinned Go PMTiles CLI
cmd/refresh/        Snapshot build, comparison, review, activation and rollback
internal/places/    Domain entities, autocomplete, details and search normalization
internal/geocoding/ Address label matching, bounded nearest address lookup and fixtures
internal/routing/   Immutable driving graph, road snapping and estimated-time routes
internal/api/       Google request/response translation and errors
internal/importer/  Source adapters, schema, identity history and refresh comparison
internal/dataset/   Atomic deployment selection and live HTTP handler replacement
imports/           Source locks, bundle checksum and identity mappings
public/            Browser ES modules and styles; libraries loaded from esm.sh
```

One Go service reads SQLite with FTS5. Geocoding loads an immutable address index
from the same read-only snapshot; reverse lookup scans the small regional set.
Routing-enabled snapshots also load an immutable directed graph and turn-restriction
index. Activation replaces all available domains together. The owned Go import pipeline uses
`parquet-go` for cloud GeoParquet and `paulmach/osm` for PBF decoding; provider
parsing stays in `internal/importer`. The basemap command invokes a pinned Go
PMTiles extractor in a separate module to keep its cloud SDKs out of the service
dependencies.
Routing remains independent of text search and address resolution. Its accelerated
search skips forced geometry chains and bounded cells of ordinary junctions, and uses A*
while preserving directed-edge turn history and destination access. Dijkstra
remains an internal correctness reference; verified read-only prepared
mappings, bounded SQLite chunks and spatial indexes use the existing import/snapshot
workflow. Server routing startup requires [offline preparation](docs/routing-prepared.md);
`-routing-legacy-load` explicitly enables the older reconstruction path. See
[the maintained routing design](docs/routing.md).

## Verification

Small synthetic fixtures and offline checks:

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
```

These cover API response shapes and errors, masks, search ranking, accents,
street abbreviations, repeated street labels, distinct repeated address labels,
closed-place behavior, dateline coordinates, multilingual Parquet and PBF parsing,
HTTP range validation, cancellation, checksums,
relationships, rejected imports, stable IDs across reordered/released imports,
and source enrichment/replacement without changing existing IDs. Refresh tests cover
reviewed replacements, split/merge ambiguity, absent-source history, search-index
corruption, stale reviews, failed switches and live rollback. Geocoding tests
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
on the retained baseline and candidate, and in-app browser forward/reverse flows
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
The [maintained scaling documentation](docs/routing-scale.md) explains the current
implementation and the unfinished work before national coverage.

## Current limitations and next work

- Limited geography and English request options; no global/IP bias, spatial or
  type filters, translation, typo tolerance, plus-code support or Google ranking.
- Search uses normalized token prefixes with exact-name/name-prefix priority,
  then area/street/business/address precedence, FTS ranking and stable ID ties.
- Source labels can be incomplete or duplicated. The retained Overture locality
  slot and units are empty; 562 raw records have a CDP-like `postal_city` value
  that is not returned as a locality or verified postal city. Address ranges are
  retained verbatim in Places. Geocoding supports
  8,407 of 8,545 source labels; 138 nonstandard forms remain excluded from both
  geocoding directions. Coverage and source positional accuracy are not certified.
- Streets are source ways, with a representative vertex. Area locations are
  labels. Neither means an entrance, rooftop guarantee, boundary or routing snap.
- Freshness follows pinned snapshots. Refreshes support reviewed one-to-one
  provider ID replacements and retain uncertain splits/merges as distinct IDs.
  No scheduling or automatic release discovery is implemented. Retain source
  locks, replacement evidence and snapshot identity history across rebuilds.
- The local map cutout is finite; zooming or panning far outside Newport can show
  missing tiles. Browser libraries, fonts and sprites use external hosts.
- Driving routing minimizes an uncalibrated estimated duration for the documented ordinary-car profile.
  Restricted-access roads and incompatible/unknown limits can be excluded; conditions are not
  evaluated. No live/historical traffic or navigation instructions; limited source-backed access associations,
  with explicitly unverified property entrances and off-road gaps.
  Snap limits and disconnected coverage can produce no route. See
  [the exact profile and remaining limits](docs/routing.md).
- Deployment hardening, continuous coverage evaluation and richer data are
  later work.
