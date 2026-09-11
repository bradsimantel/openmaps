# Compact Parquet/SQLite Newport proof

> **Historical note:** The larger autocomplete gate identified here was later
> passed by the [DuckDB token-prefix proof](0045-duckdb-token-prefix-proof.md).
> The later [production migration](0047-duckdb-places-geocoding-migration.md)
> repeated the API parity gate and removed this experimental command/package.

| Field | Value |
| --- | --- |
| Date | 2026-09-11 |
| Starting repository revision | `a90dd3e409566adaa488c1290b9d6a158dd8479f` |
| Scope | Existing normalized Newport bundle and retained SQLite baseline |
| Status | Non-default implementation proof; not a production migration decision |
| Go / SQLite | Go 1.26.1; SQLite CLI 3.51.0; existing `parquet-go` 0.32.0 and `modernc.org/sqlite` 1.45.0 dependencies |

## Outcome

The first implementation gate proposed by the [DuckDB/Parquet investigation](0043-duckdb-parquet-places-geocoding.md)
passes for Newport. A maintained Go builder now emits normalized Parquet tables
and a compact SQLite serving index, and a Go reader reproduces the maintained
Places and geocoding behavior. The production server still opens the original
single SQLite snapshot; no server flag, deployment switch, or production
migration was added.

The built generation occupied 11,579,955 bytes including its manifest, versus
35,950,592 bytes for `data/openmaps.sqlite`, a 67.8% reduction. Parquet-backed
details and provenance remained comfortably below the provisional 100 ms p95
target. Exact two-character prefix heads removed the query-time common-prefix
sort at bounded storage cost: `ma` measured 0.021 ms p95 on Newport. This is a
promising mechanism, but Newport is too small to settle its Rhode Island or
nationwide build cost and ranking coverage.

The next gate remains a bounded larger extraction. In particular, the current
proof consumes the existing monolithic normalized JSON bundle and therefore
does not yet establish bounded-memory national construction.

## Implemented artifact

`cmd/places-geocoding-compact` verifies the pinned bundle checksum and refuses
to replace an existing directory. It resolves identity, attribute conflicts,
provenance, kinds, and relationships through the same importer logic as the
current SQLite writer. A temporary sibling directory is verified and then
renamed into place.

One generation contains:

| File | Rows | Bytes | Purpose |
| --- | ---: | ---: | --- |
| `entities.parquet` | 12,343 | 619,910 | Complete provider-independent serving entities, including attribution |
| `source-records.parquet` | 12,343 | 2,829,786 | Source IDs, releases, normalized attributes, paths, and complete raw records |
| `attribute-provenance.parquet` | 39,435 | 586,557 | Winning attribute-to-source paths |
| `relationships.parquet` | 1,184 | 37,035 | Business-address and area-parent relationships |
| `metadata.parquet` | 2 | 6,110 | Source manifest and identity mappings |
| `serving.sqlite` | 12,343 entity locators | 7,499,776 | Search, address, spatial, and Parquet locator structures |
| `manifest.json` | 6 file entries | 781 | Fixed names, row counts, SHA-256 checksums, and coordinate order |
| **Total** |  | **11,579,955** |  |

Parquet uses Zstandard and 32,768-row groups. This generation has only one
entity row group; the locator nevertheless stores the row group and row within
that group so the same reader path can be exercised before a larger sharded
build. Source and attribute-provenance spans are stored as start/count locators.

SQLite deliberately has no `source_records`, `attribute_provenance`, or
`relationships` tables and no complete raw source JSON. It contains:

- a small entity locator and autocomplete display projection;
- contentless FTS5 with the established tokenizer and two-, three-, and
  four-character prefix indexes;
- exact address key, source-context, coordinate, and entity-ID projections;
- an R-tree over supported address points;
- five final ranked IDs per populated two-character token prefix.

The two-character prefix table has 409 prefixes and 1,532 rows for Newport. Its
table and two indexes use 188,416 bytes. It is built from the ordinary FTS query,
including closed-place exclusion, kind priority, BM25, stable-ID ties, and
repeated-street-label collapse. Longer or multi-token queries continue through
FTS5. The cache is bounded by populated two-rune prefixes times five results,
not by the number of matching entities, although national Unicode vocabulary
cardinality still needs measurement.

Details follows the SQLite locator to the exact Parquet row group and validates
the returned public ID. Provenance follows source and winning-attribute spans
and returns the retained raw record. Forward and reverse geocoding keep only
candidate keys, context, coordinates, and the R-tree in SQLite; final entities,
attribution, source records, and address components are materialized from
Parquet. This avoids hiding complete geocoding responses in the serving index.

## Correctness and integrity

The integration proof built directly from the pinned `data/newport.json`
(SHA-256 `3dc0a699fb51087c7a3a4068adb59e0aa2c0628bf40a39e3bb9ff700b5a3de514`)
and compared against `data/openmaps.sqlite`
(SHA-256 `5cce6cf3a97b65ead56fc1da5cf810470a9f1f0197746503ab8faf7d2b5242b8`).

- All eight maintained Newport autocomplete checks matched in ordered IDs and
  autocomplete fields. Every returned detail entity matched exactly.
- The complete maintained 26-case geocoding suite matched the current store,
  including unique and ambiguous forward results, unsupported inputs, exact
  100-metre reverse behavior, ties, no-match results, and coverage boundaries.
- Synthetic tests compared public API response bodies byte for byte for Places,
  forward geocoding, reverse geocoding, no-match, and explicit-error cases.
- Four workers completed 100 interleaved groups (400 autocomplete, Parquet-
  detail, forward, and reverse operations) with the same results as the current
  store and no race report.
- Reordering source records and relationships produced identical manifest file
  checksums. The builder refuses overwrite, and opening a corrupted Parquet
  generation fails checksum verification.
- Build-time resolution retains the importer's duplicate-source, stable-ID,
  cross-kind, provenance, relationship, coordinate, and website checks. The
  artifact verifier checks every checksum and Parquet row count plus SQLite
  integrity, foreign keys, search coverage, spatial coverage, and schema.

The recorded generation checksums were:

| File | SHA-256 |
| --- | --- |
| `entities.parquet` | `41a550ac8098c9a0fbea40c8cbba762eae11cd2e08bec291ac3a0ce8df4f4d81` |
| `source-records.parquet` | `3c2dfae890008f66825f26b2ade9497f817e4bd6715a15d2bb9ff26d3d1a055f` |
| `attribute-provenance.parquet` | `77f25c8885289474c8e3576dfc18609b6a278a8f9b5b5e78579046a2238e7201` |
| `relationships.parquet` | `0cdabb70e4eae8d79b65440ad6aa2ade432a6a682df96eecc79d738f615f030c` |
| `metadata.parquet` | `e0364a457c536d7787dfc4900875ce233028c81953931f950899b4c4791e5938` |
| `serving.sqlite` | `a762326bfefaa895c44bc6b7e5f4afdfa928860c4700cda168f16471f5dc7a03` |

## Measurements

The artifact was built with a compiled command on the same Apple M5 host as log
0043. `/usr/bin/time -l` reported 1.22 seconds elapsed and 178,159,616 bytes
maximum resident set size. This includes artifact verification inside `Build`
and a second command-level verification. It does not represent a bounded-memory
larger build because decoding `newport.json` and resolving the bundle still
materialize all Newport records.

Each warm class contains 100 deterministic operations. The mixed class divides
100 operations among four workers and includes the common prefix, other
autocomplete shapes, details, exact/ambiguous/missing forward requests, reverse
points, and provenance. The OS page cache was not purged.

| Operation | p50 | p95 | p99 |
| --- | ---: | ---: | ---: |
| Cached `ma` autocomplete | 0.018 ms | 0.021 ms | 0.025 ms |
| Mixed autocomplete shapes | 0.092 ms | 2.929 ms | 2.953 ms |
| Parquet-backed details | 1.019 ms | 1.260 ms | 1.302 ms |
| Forward geocoding | 2.637 ms | 25.209 ms | 25.415 ms |
| Reverse geocoding | 3.001 ms | 3.392 ms | 3.436 ms |
| Complete source/provenance fetch | 3.014 ms | 3.334 ms | 3.383 ms |
| Four-worker mixed workload | 1.512 ms | 26.600 ms | 30.734 ms |

The forward tail is the intentionally ambiguous `364 Bellevue Avenue`: eight
distinct results each materialize retained evidence from Parquet. It remains
well below the provisional target but identifies an obvious future batched-span
optimization if larger ambiguous groups are common.

Five checksum-verifying opens followed by one details request took 66.44, 64.55,
64.10, 63.91, and 64.15 ms. This verifies all 11.6 MB on every `Open`; a future
selection implementation should verify once before publishing a leased reader,
not reopen it per request.

SQLite query plans showed the intended access paths:

- cached `ma` used the `short_prefix_head(prefix,rank)` primary key and the
  search-entity ID index;
- longer FTS used the FTS5 virtual index plus integer row lookup, with the known
  temporary B-tree for final ranking;
- exact forward lookup used the covering `address_key` index;
- reverse candidates used the R-tree virtual index plus integer row lookup.

## Reproduction

```sh
go run ./cmd/places-geocoding-compact \
  -bundle data/newport.json \
  -config config/places-geocoding.json \
  -out data/openmaps-compact

OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_BUNDLE="$PWD/data/newport.json" \
  go test -tags=integration ./internal/placesgeocoding/compact \
  -run TestNewportParity -count=1 -v

OPENMAPS_BUNDLE="$PWD/data/newport.json" \
  go test -tags=integration ./internal/placesgeocoding/compact \
  -run TestNewportMeasurements -count=1 -v

go test ./...
go vet ./...
```

The default output name is intentionally outside the production server's
`data/openmaps.sqlite`. The command refuses an existing directory.

## Remaining gates

- The builder starts from the monolithic JSON bundle and resolves it in memory.
  A national candidate needs a streaming normalized-row boundary and bounded
  Parquet writers; this proof does not satisfy that requirement yet.
- The removed Rhode Island experiment data was not reacquired. The 704,693-row
  common-prefix and four-request workload from log 0043 must be rerun against
  the maintained two-character prefix-head design. Its build time, prefix
  vocabulary, SQLite size, and ranking parity are the deciding evidence.
- Newport produces one entity shard and row group. Multi-shard selection,
  cross-row-group source spans, row-group pruning, and file-count behavior are
  unmeasured.
- The current normalized bundle has no per-record rejection relation. The proof
  cannot preserve data its input does not carry; a streaming normalized format
  must make rejection records explicit before national construction.
- Relationships are retained and verified at build time but are not yet exposed
  through the compact reader. Snapshot selection, leases, replacement, rollback,
  and refresh metadata have not been adapted to directory manifests.
- Two-character heads solve only populated single-token two-rune queries. One-
  character, longer, multi-token, and future location-biased ranking continue to
  use ordinary serving structures and require the larger workload.
- Full-file checksum verification is appropriate before selection but scales
  with artifact bytes. Verification time must be measured on larger generations
  and kept out of request paths.

These limits keep the architectural recommendation provisional. The Newport
proof justifies the bounded larger rerun; it does not yet justify switching the
server or deleting the current SQLite build path.
