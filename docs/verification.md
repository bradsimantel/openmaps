# Milestone verification

Historical verification performed locally on 2026-09-07, macOS ARM64, Go 1.26.1,
Node 25.8.1, Chromium 140 (Playwright 1.55.1).

- `gofmt -w cmd internal`, `go test ./...`, `go vet ./...`: passed.
- `CGO_ENABLED=0 go test ./...`: passed, including pure Go PBF decoding.
- Go adapter tests: passed. Synthetic GeoJSON, multilingual nullable Parquet
  maps and a tiny dense-node OSM PBF cover geometry, aliases, retained provenance,
  unique/ambiguous business-address matching, overlap auditing, checksum rejection,
  cancellation and deterministic preparation. HTTP tests verify conditional
  ranges, caching and rejected downloads without using the network.
- A separate Go fetch of all three Overture themes reproduced the committed
  export checksums and normalized bundle checksum
  `9fa4a04f6ec97dda30807102ccd95d3adb6d50359eadb5d7e8327eb694491331`.
  The Geofabrik PBF cache passed its original source checksum.
- The Go pipeline was compared against the preceding database: all 11,602
  entities, 37,943 attribute-provenance rows and 1,184 relationships were exactly
  equal. Source keys, attributes and paths also matched exactly; all raw records
  retained equivalent content after normalizing JSON numbers and map-pair order.
  Public IDs did not change. The corrected OSM/standalone-address overlap audit
  reports 108 of 138 labels; this correction affects no imported relationship.
- Two complete regional SQLite builds had identical logical rows: 11,602 entities,
  11,602 source records, 37,943 winning attribute-provenance rows, 1,184 relationships
  and three metadata entries. `integrity_check` returned `ok` and
  `foreign_key_check` returned no rows.
- An independent extraction through `go run ./cmd/basemap` reproduced SHA-256
  `dce3186c1659cbf2f76fea756c6fc80269ea53b2e8312e28a551271cdbbad9a0`.
  The pinned PMTiles CLI's `verify` command passed.
- Browser flow passed against the Go-built database: real tiles rendered; White Horse Tavern details and marker;
  `50 Bellevue` standalone address ranked ahead of the business at that address;
  keyboard selection; Thames Street; Newport area; no-results feedback; rapid
  successive input; mobile layout; no uncaught JavaScript errors.
- The same browser flow passed with versioned esm.sh imports and CSS after
  removing local frontend dependencies and copied assets. A separate browser
  check blocked esm.sh and confirmed autocomplete and details remain available.
- Desktop and mobile screenshots were visually reviewed. Generated evidence is
  local in `data/browser-place.png`, `data/browser-address.png` and
  `data/browser-mobile.png` (excluded from Git).

The in-app browser tool failed before connection with an environment metadata
error. These previous browser checks used locally installed Playwright Chromium.
The standalone browser tests and their npm tooling have since been removed at
the project owner’s request. Future browser verification will use the Codex
browser connection once it is working; that connection remains unresolved.
An initial direct link to the Protomaps demo bucket failed due to missing CORS
headers; the implemented client now reads the pinned local PMTiles cutout through
Go's HTTP range-capable file serving. Fonts and sprites use Protomaps' asset host;
browser libraries and MapLibre CSS use esm.sh.

## Optional regional rebuild comparison

After preparing a real dataset and building its database, compare a fresh build
against it offline. This test is excluded from `go test ./...` and does not
download sources. Absolute paths are required because Go runs tests in the
package directory. To compare across a pipeline change, point the baseline at a
retained database from before that change.

```sh
OPENMAPS_DATA="$(pwd)/data" OPENMAPS_BASELINE="$(pwd)/data/openmaps.sqlite" \
  go test -tags=integration ./internal/importer -run TestRegionalRebuild -v
```

## Representative real-data queries

Each suggestion from these queries was resolved through HTTP details and checked
for the same ID. Five consecutive local HTTP requests per query yielded these
median times in the initial milestone verification, including the HTTP client
round trip. These are a small local
smoke check, not a production benchmark or Google relevance comparison.

| Input | First suggestion | Suggestions | Median ms |
| --- | --- | ---: | ---: |
| White Horse | White Horse Tavern | 1 | 0.41 |
| 50 Bellevue | 50 BELLEVUE Avenue | 2 | 0.42 |
| Thames | Thames Street | 5 | 1.75 |
| Newport | Newport | 5 | 5.99 |
| Redwood | Redwood Street | 5 | 0.31 |
| 26 Marlborough | 26 MARLBOROUGH Street | 2 | 0.32 |
| zzxnoresult | None | 0 | 0.25 |

`Redwood` puts the street before the library under the documented kind precedence;
`Redwood Library` narrows to the institution. This is an initial deterministic
ranking, with further relevance evaluation left for later milestones.

No Google API credentials or live Google response comparison were used: contract
verification used current official documentation and fixture-based HTTP tests.
No routing or dedicated geocoding checks apply to this milestone.
