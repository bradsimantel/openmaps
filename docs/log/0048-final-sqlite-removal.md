# Final SQLite removal

| Field | Value |
| --- | --- |
| Date | 2026-09-11 |
| Starting revision | `7e55aabf6094a762a105ff11be1a07584013eb50` |
| Scope | Final Places/geocoding implementation, test, dependency, documentation and local-artifact cleanup |
| Status | Implemented and locally verified; pushed CI result is reported with the completion commit |

## Outcome

SQLite is no longer an Open Maps implementation or test dependency. Production
Places/geocoding remains normalized immutable Parquet plus one derived DuckDB
serving catalog. Routing remains the independent Scout implementation, and the
basemap remains an independent PMTiles artifact.

The remaining dependency had been structural rather than operational. The
`places` package combined provider-independent entity types and normalization
with its retired store, while `geocoding` combined the maintained grammar and
distance rules with its retired reader. Import and serving commands therefore
reached `modernc.org/sqlite` merely by importing the shared domain packages.
Those packages now contain only reusable domain behavior. The legacy store,
geocoder reader, importer database builder and schema, snapshot reader/comparer,
refresh metadata writer, and their tests were removed.

`go mod tidy` removed `modernc.org/sqlite` and its unused transitive modules from
`go.mod` and `go.sum`. Package dependency listings for `cmd/server`,
`cmd/places-geocoding-import`, `cmd/places-geocoding-prepare`, and
`cmd/places-geocoding-refresh` contain no SQLite driver or `modernc.org` package.

## Replacement tests

Small fixtures now build real Parquet/DuckDB generations. Import tests inspect
DuckDB details and Parquet-backed evidence to verify stable public IDs, source
enrichment, conflict winners and provenance. Places, geocoding and API tests use
the same DuckDB fixtures for ordering, all entity kinds, closed-place behavior,
details, attribution, malformed and unsupported input, exact and ambiguous
addresses, coverage, ties and exact 100-metre semantics.

The Newport integration test no longer compares against a retained runtime
oracle. It builds a fresh generation and checks the recorded 26-case golden
suite directly: expected outcomes, ordered IDs, partial matches, reverse
distances, boundary and no-match cases, plus all 25 original source-evidence
records. The historical parity measurement remains unchanged in log 0047.

Generation tests continue to cover one- and four-worker reads, normalized-file
determinism, logical equality despite differing derived catalog bytes, missing
and corrupt shards, interrupted construction, comparison, activation,
replacement, rollback, rejected reloads and concurrent leases. Server tests
continue to prove that place-ID and exact-address route endpoints resolve under
one lookup-generation lease before Scout receives coordinates.

## Documentation and historical record

Current README, configuration, geocoding, refresh, deployment and routing pages
describe only normalized Parquet/DuckDB lookup generations. Historical
`docs/log/**` records were not rewritten and remain the evidence for earlier
implementations and measurements. Outside those logs, a case-insensitive source
and documentation search finds no SQLite reference.

## Recoverable local cleanup

The exact ignored artifacts found in the workspace were moved, not deleted, to
`/Users/bs/.Trash/openmaps-sqlite-removal-2026-09-11/`:

- `data/openmaps.sqlite` (35,950,592 bytes), now recoverable as
  `openmaps.sqlite` in that Trash folder.
- `data/newport-2026-07-22/openmaps-overture-transportation.sqlite`
  (37,445,632 bytes), now recoverable as
  `openmaps-overture-transportation.sqlite` in that Trash folder.

A second exact workspace scan found no remaining `*.sqlite` artifact. No other
source data, routing data or basemap data was moved.

## Verification

The following local gates passed:

```sh
gofmt -w <changed Go files>
go test -race ./internal/placesgeocoding/...
go test ./...
go vet ./...
git diff --check
```

The pinned Newport preparation was rebuilt offline from the four retained
regional inputs. Its bundle checksum remained
`588ec72c92b6e167fd9cad53559569f7e3b298a9fdd6ce57dbd4c7ba806e9af4`,
and independent DuckDB builds each produced 12,343 entities. The direct Newport
golden and regional provenance tests passed.

The retained Rhode Island qualification gate rebuilt the 704,693-entity serving
index under the 256 MB DuckDB limit. It passed all frozen ordered five-ID
expectations and the one- and four-worker workloads. This run built the catalog
in 6.34 seconds at 144,977,920 bytes; query timing remains diagnostic rather
than a release contract.

Native Darwin CGO server and import binaries built successfully. The native
importer built a fresh 12,343-entity Newport generation, and the native server
served healthy lookup identity, White Horse autocomplete/details with
attribution, and exact `50 Bellevue Avenue` geocoding from that generation.

No national dataset was downloaded or built.
