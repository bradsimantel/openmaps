# Open Maps

Open Maps is a Go and SQLite replacement for selected Google Maps HTTP API
operations, with MapLibre GL JS and Protomaps for display.

**The first milestone works:** autocomplete businesses, standalone addresses,
streets and areas in downtown Newport, Rhode Island; retrieve details using the
returned ID; and display the selected result on a map. It uses real regional
imports, not demo data embedded in the client.

Routing, dedicated forward/reverse geocoding, nearby search and text search are
future milestones. They are not implemented.

## Run the Newport demo

Prerequisite: Go **1.26.1+**. Allow several minutes for first-time tool and data
downloads. All commands below run from the repository root. No API keys,
Docker, external database server or system SQLite installation are required.

```sh
go run ./cmd/prepare -fetch

go run ./cmd/import

go run ./cmd/basemap

go run ./cmd/openmaps
```

The pipeline is Go code in this repository. If a C compiler or zlib headers are
unavailable, prefix Go commands with `CGO_ENABLED=0` to use pure Go PBF decoding.

Open **http://127.0.0.1:8080**. Try `White Horse`, `50 Bellevue`, `Thames`, or
`Newport`. Click a suggestion; keyboard users can press Down from the input and
Enter to select. Names, coordinates, available address and website come from the
selected entity's details response.

`data/` is ignored by Git.
The OSM regional PBF is about 52 MB; canonical Overture subsets and SQLite add
further local storage. The Protomaps cutout is about 3.8 MB. Internet is required
for initial downloads, esm.sh browser libraries and Protomaps-hosted fonts/sprites.
Lookup APIs
and local basemap tile requests work without external services after import.

The browser imports MapLibre GL JS 5.0.1, PMTiles 4.2.1 and Protomaps basemaps
5.7.2 directly from [esm.sh](https://esm.sh/); MapLibre CSS uses the same version.
There is no frontend install, build step or vendored asset directory. Dependency
versions are explicit in `web/app.js` and `web/index.html`. A CDN failure leaves
search and details available, but the map cannot initialize.

Data snapshots are pinned. Overture and Protomaps may expire old hosted releases;
archive the verified local inputs for long-term rebuilds. A download or checksum
failure never falls back to a newer dataset. Normal setup must not use
`-write-lock` or edit expected checksums to bypass a mismatch.

Already have source files? Rebuild offline into a **new** database:

```sh
go run ./cmd/prepare
go run ./cmd/import -db data/openmaps-next.sqlite
go run ./cmd/openmaps -db data/openmaps-next.sqlite -listen 127.0.0.1:8081
```

The import refuses to overwrite an existing database. Stop the earlier service
before reusing its port. The service defaults to loopback and supports `-db`,
`-listen`, `-web` and `-tiles`; `go run ./cmd/openmaps -help` lists defaults.

## Supported API

Compatibility target: **Google Places API (New), REST v1**, checked against
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
| `GET /healthz` | Local process health |
| `GET /tiles/newport.pmtiles` | Separate regional basemap file with HTTP range support |

Details exposes IDs, display name, coordinates, conservative types, attribution,
and address/website when available. Masks support parent and leaf paths, or `*`.
Unsupported parameters and fields return `INVALID_ARGUMENT`; unknown IDs return
`NOT_FOUND`. No ratings, photos, opening hours, entrances or other unavailable
attributes are fabricated. API keys are accepted for client compatibility but
are **not authenticated**; there are no billing, quota or production access
controls.

The [initial API target decision](docs/log/0001-places-api-target.md) records the
first milestone’s request, field-mask and error contract, with links to Google’s
official references.

## Region, sources and identity

Launch rectangle: longitude **−71.33 to −71.29**, latitude **41.47 to 41.51**.
It covers downtown Newport and nearby streets, not the full municipality.

| Imported data | Pinned source | Records |
| --- | --- | ---: |
| Businesses and POIs | Overture Places 2026-08-19.0 | 2,173 |
| Standalone address points | Overture Addresses 2026-08-19.0, NAD-derived | 8,545 |
| Settlements and administrative context | Overture Divisions 2026-08-19.0 | 4 |
| Named street ways | Geofabrik Rhode Island OSM PBF 2026-08-01 | 880 |

51 explicitly closed businesses are excluded from autocomplete but retain details.
There are 1,181 conservative business/address links and three area-parent links.
The four areas include available parents outside the launch rectangle.

[The initial regional inspection](docs/log/0002-newport-data-and-import-design.md) records field completeness,
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
renumbering existing entities. Highest source priority wins each nonempty
attribute; ties use source key order. All contributing values remain stored.
There is no fuzzy identity merging or speculative provider plugin framework.
See the [initial matching and conflict rules](docs/log/0002-newport-data-and-import-design.md#matching-identity-and-conflict-resolution).

The [basemap lock](imports/basemap.lock.json) separately pins a Protomaps
2026-09-06 regional cutout at zooms 0–15. The Go service serves it locally; tiles
are never used as lookup data. Upstream notices are linked on the demo's
[attribution page](web/attribution.html).

## Code layout

```text
cmd/openmaps/       Go HTTP service
cmd/prepare/        Pinned acquisition, regional normalization and audit
cmd/import/         Checksum-verified SQLite builder
cmd/basemap/        Verified regional extraction using the pinned Go PMTiles CLI
internal/places/    Domain entities, autocomplete, details and search normalization
internal/api/       Google request/response translation and errors
internal/importer/  Concrete Go source adapters, schema, identities and provenance
imports/           Source locks, bundle checksum and identity mappings
web/               Browser ES modules and styles; libraries loaded from esm.sh
```

One Go service reads SQLite with FTS5. The owned Go import pipeline uses
`parquet-go` for cloud GeoParquet and `paulmach/osm` for PBF decoding; provider
parsing stays in `internal/importer`. The basemap command invokes a pinned Go
PMTiles extractor in a separate module to keep its cloud SDKs out of the service
dependencies.
Routing will remain independent of text search and address resolution. Its
engine and graph representation are still undecided.

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
and source enrichment/replacement without changing existing IDs. No regional
source downloads are part of `go test ./...`.

Browser verification uses a running demo and the Codex browser connection once
connected. The connection is currently unresolved; there is no browser test
runner or Node.js tooling in this repository. Browser libraries, fonts and
sprites require network access.

Check a real business, address, street and area through autocomplete, details and
map placement. Also check keyboard selection, empty results, fast input changes,
mobile layout and JavaScript errors. Blocking esm.sh should leave search and
details available. See [previous verification results](docs/log/0003-first-milestone-verification.md).

## Current limitations and next work

- Limited geography and English request options; no global/IP bias, spatial or
  type filters, translation, typo tolerance, plus-code support or Google ranking.
- Search uses normalized token prefixes with exact-name/name-prefix priority,
  then area/street/business/address precedence, FTS ranking and stable ID ties.
- Source labels can be incomplete or duplicated. NAD locality and units are
  missing here; address ranges are retained verbatim. Coverage is not certified.
- Streets are source ways, with a representative vertex. Area locations are
  labels. Neither means an entrance, rooftop guarantee, boundary or routing snap.
- Freshness follows pinned snapshots; no automated updates or source-ID churn
  reconciliation. Identity mappings must be retained across rebuilds.
- The local map cutout is finite; zooming or panning far outside Newport can show
  missing tiles. Browser libraries, fonts and sprites use external hosts.
- Deployment hardening, continuous coverage evaluation and richer data are
  later work. Dedicated geocoding and routing remain future milestones.
