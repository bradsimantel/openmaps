# DuckDB token-prefix serving proof

> **Historical note:** The Go, bounded-build, deployment and snapshot gates
> listed here passed in the later
> [DuckDB Go qualification](0046-duckdb-go-qualification.md). Compact SQLite is
> no longer the recommended runtime fallback.

| Field | Value |
| --- | --- |
| Date | 2026-09-11 |
| Starting repository revision | `a90dd3e409566adaa488c1290b9d6a158dd8479f` |
| Scope | Overture 2026-08-19.0, Rhode Island envelope from log 0043 |
| DuckDB | CLI 1.5.5 (`d8cdaa33fd`); Python benchmark client 1.5.0 |
| Status | Bounded architecture proof; not a production migration |

## Outcome

This proof changes the recommendation in the historical
[DuckDB/Parquet investigation](0043-duckdb-parquet-places-geocoding.md). DuckDB
can meet the provisional lookup targets while preserving the current
conjunctive token-prefix and BM25 ordering semantics. The recommended next
candidate is now **normalized immutable Parquet plus an immutable DuckDB serving
catalog**, without a SQLite sidecar.

This is not evidence that arbitrary SQL over Parquet is an adequate serving
index, and it does not make DuckDB's built-in FTS prefix-aware. The passing
design materializes a purpose-built token dictionary and posting list in
DuckDB. A production switch remains conditional on a maintained Go reader,
complete API fixture parity, bounded-memory larger construction, and deployment
qualification.

The same bounded source selection as log 0043 reproduced 704,693 distinct
entities exactly: 553,029 addresses, 67,716 businesses, 83,379 named road
segments, and 569 areas. The four upstream Parquet objects were range-read; the
2.51 GB of logical source objects was not downloaded in full.

## Serving design

The failed prototype scanned a 704,693-row search projection for each unusual
prefix. This proof instead stores:

| Structure | Rows | Purpose |
| --- | ---: | --- |
| `search_entities` | 704,693 | Display projection and five-row details fetch |
| `rank_entities` | 704,693 | Narrow ID, kind, normalized name, closed flag, and document length |
| `tokens` | 41,961 | Sorted unique normalized tokens |
| `postings` | 4,522,827 | Token/entity pairs and name, alias, and address term frequencies |
| `short_prefix_head` | 3,830 | Five final IDs for each of 927 populated two-character prefixes |
| `address_lookup` | 548,947 | Supported exact-address keys, context, points, ART, and R-tree |

Arbitrary prefixes use a range over the sorted token dictionary, then the
corresponding ordered postings. Multi-token input intersects one posting result
per input token. It does not materialize every possible prefix/entity pair.
Two-character single-token input uses the bounded final-result table because a
common prefix is exactly the case where even a posting list contains too many
candidates.

Each posting retains term frequency by field. Candidate ranking reproduces the
FTS5 formula with `k1=1.2`, `b=0.75`, corpus document count and average document
length, prefix document frequency, and the established name/address/alias
weights of 10/1/5. Exact name, name-prefix, kind priority, stable-ID ties, closed
place exclusion, and repeated-street-label collapse remain outside BM25 in the
same order as the current store.

Ranking reads the narrow projection. Only after selecting five IDs does a
second primary-key query fetch display rows. Keeping this as one broad join made
DuckDB choose a hash join over the wide entity projection and raised ordinary
query p95 by roughly 15–20 ms.

## Correctness

An independent SQLite 3.51 FTS5 oracle was built from the same normalized rows.
The DuckDB index matched the oracle's exact ordered five IDs for all eight
representative cases:

- `ma`
- `white horse`
- `main street`
- `providence`
- `50 bellevue`
- `dunkin`
- `rhode island`
- `zzzzz missing`

The first field-mask-only ranking attempt matched token-prefix membership but
only four of eight ordered result lists; `main street` overlapped in one of five
positions. That attempt was rejected. Adding actual field term frequencies,
document-length normalization, and prefix document frequency produced eight of
eight exact matches in every final benchmark run.

This is representative ranking parity, not complete API parity. The extraction
was deliberately limited to the lookup projection and did not rebuild complete
source records, attribute provenance, identity mappings, or relationships. Log
0044 remains the complete maintained Newport parity proof for those contracts.

## Build measurements

Both builds used four threads and a 4 GiB DuckDB memory limit. Elapsed time and
RSS are `/usr/bin/time -l` measurements.

| Build | Elapsed | Peak RSS | Output |
| --- | ---: | ---: | ---: |
| Bounded normalized lookup Parquet | 9.24 s | 966 MB | 29.73 MB kind partitions; 46.51 MB temporary combined projection |
| Complete DuckDB serving catalog | 4.85 s | 1.85 GB | 353,382,400 bytes (337.0 MiB) |

The posting staging table was temporary. This avoided leaving approximately 23
MiB of free blocks in the final database. The final catalog had 351,535,104
used bytes and 1,835,008 free bytes after its checkpoint. Its SHA-256 was
`929305c7211969ee204f39b32adfcf0330f7a132b483ce56e79d64b3f9e26e1f`.

The comparable compact SQLite serving index from log 0043 was 319.26 MB.
DuckDB is therefore about 34.1 MB or 10.7% larger for the complete regional
serving projection. Combining either with the previously measured 162.98 MB
normalized Parquet base gives approximately 516.36 MB for DuckDB versus 482.24
MB for compact SQLite. Both remain far below the 2.117 GB current-style SQLite
snapshot. DuckDB's storage penalty is modest, but its 1.85 GB regional build RSS
is materially higher than compact SQLite's measured 210 MB and is the leading
nationwide-build risk.

## Query measurements

The benchmark used one DuckDB thread per connection and consumed all returned
rows. Each single-request class contained 100 warm operations. Three complete
autocomplete runs were made; the table reports the median of their p95 values
and the observed p95 range. The OS page cache was not purged.

| Autocomplete case | Median p95 | p95 range | Historical DuckDB scan p95 |
| --- | ---: | ---: | ---: |
| `ma` | 0.618 ms | 0.594–0.757 ms | 24.63 ms |
| `white horse` | 22.387 ms | 21.646–23.054 ms | 129.75 ms |
| `main street` | 43.215 ms | 42.946–46.260 ms | 25.12 ms |
| `providence` | 27.189 ms | 26.531–28.335 ms | 129.02 ms |
| `50 bellevue` | 14.095 ms | 13.200–14.184 ms | 129.59 ms |
| `dunkin` | 21.400 ms | 21.195–22.712 ms | 116.79 ms |
| `rhode island` | 23.738 ms | 23.497–24.350 ms | 129.70 ms |
| Missing two-token query | 2.332 ms | 2.319–2.422 ms | 128.64 ms |
| Four-connection autocomplete mix | 53.202 ms | 53.188–55.786 ms | 147.85 ms |

`main street` is slower than the old scan because correct BM25 ranks 15,080
intersected candidates; it still remains below the provisional 100 ms p95
target. The largest individual prefix lists in that query contain 18,847
`main*` documents and 259,223 `street*` documents. By contrast, `white horse`
intersects 1,174 and 150 documents, while the missing query stops at an empty
posting range.

A separate mixed run exercised autocomplete, details, ambiguous exact forward
geocoding, and R-tree reverse candidates:

| Operation | p50 | p95 | p99 |
| --- | ---: | ---: | ---: |
| Cached `ma` autocomplete | 0.579 ms | 0.731 ms | 0.844 ms |
| `main street` autocomplete | 43.054 ms | 45.867 ms | 54.159 ms |
| Details by public ID | 0.109 ms | 0.119 ms | 0.204 ms |
| Eight-result exact forward | 6.942 ms | 7.366 ms | 8.330 ms |
| Reverse candidates within 100 metres | 0.782 ms | 0.830 ms | 0.876 ms |
| Four-connection mixed workload | 2.337 ms | 64.227 ms | 84.777 ms |

The reverse query must materialize the constant-envelope R-tree candidates
before applying spherical distance. Combining distance and intersection in one
predicate caused a 548,947-row scan; the materialized candidate CTE used
`address_space` and visited 13 index candidates in the profiled query.

Ten open-plus-first-query samples had a 4.76 ms median for `ma` and 48.77 ms for
`main street`; their maxima were 5.41 and 53.98 ms respectively on a warm OS
cache.

## Interpretation and next gate

The architectural objection from log 0043 was autocomplete, not details,
Parquet scans during builds, or immutable snapshot publication. This proof
removes that objection at regional scale. DuckDB now has a concrete advantage
beyond general Parquet affinity: one embedded runtime can build and query the
normalized files, keep exact lookup and spatial indexes, and serve correct
autocomplete without a second database format.

The result does not justify copying the experimental SQL directly into the
server. The next implementation gate is a maintained non-default Go DuckDB
reader and builder that:

1. consumes the complete normalized Parquet generation and verifies its
   checksums before publication;
2. passes all maintained Newport Places and geocoding fixtures byte for byte;
3. repeats the Rhode Island workload through the real Go API and connection
   pool;
4. builds postings in bounded partitions rather than one 4.5-million-row
   temporary relation, then measures a substantially larger bounded sample;
5. qualifies the DuckDB Go binding, CGO/cross-build packaging, immutable
   snapshot leases, replacement, and rollback.

Until those gates pass, the production server remains on its current SQLite
snapshot. For the candidate architecture, however, compact SQLite is no longer
the default recommendation; it is the fallback if DuckDB's Go deployment or
nationwide build-memory gate fails.
