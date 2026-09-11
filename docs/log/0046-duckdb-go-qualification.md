# DuckDB Go lookup qualification

> **Historical note:** The separately authorized
> [production migration](0047-duckdb-places-geocoding-migration.md) completed
> the cutover and removed the compact-SQLite path. Statements below that the
> production runtime remains SQLite describe this qualification's original
> scope, not the current architecture.

| Field | Value |
| --- | --- |
| Date | 2026-09-11 |
| Starting repository revision | `a90dd3e409566adaa488c1290b9d6a158dd8479f` |
| Host | Apple M5, 10 logical CPUs, 16 GiB RAM, macOS 26.4.1 arm64 |
| Go | 1.26.1 |
| DuckDB Go driver | `github.com/duckdb/duckdb-go/v2` v2.10505.0 |
| DuckDB core / bindings | 1.5.5 (`d8cdaa33fd`), bindings v0.10505.0 |
| Status | Qualification complete; production remains on SQLite pending an explicit migration decision |

## Recommendation

Use **normalized immutable Parquet plus a compact DuckDB serving catalog** for
the next Places/geocoding implementation. Do not retain SQLite as a parallel
runtime fallback. The maintained, non-default Go candidate passes the bounded
qualification gates set by the historical
[token-prefix proof](0045-duckdb-token-prefix-proof.md): complete Newport API
parity, exact statewide ranking parity, sub-target single and concurrent
latency, bounded serving-index construction, offline operation without DuckDB
extensions, native packaging, and immutable replacement and rollback.

This recommendation does not switch `cmd/server`. The current SQLite path is
still the production implementation because the original investigation did not
authorize a migration. A migration also needs a streaming normalized-Parquet
producer; the current JSON bundle and `importer.Resolve` still collect the
regional input in memory. The DuckDB serving-index build itself is bounded and
was qualified directly from a 704,693-row Parquet projection.

The compact SQLite experiment remains useful comparative evidence, not a
component to ship alongside DuckDB. Its strongest advantages are a small Go
binary, no CGO, and very fast Newport point lookups. DuckDB now wins the
architectural comparison because its posting design is much faster for the
statewide common-prefix workload, its catalog is less than half the measured
compact SQLite index size, and it removes a second serving-index technology
from the proposed Parquet pipeline.

## Maintained candidate

The isolated `internal/placesgeocoding/duckdb` package and
`cmd/places-geocoding-duckdb` command implement the qualified shape:

- immutable Parquet files contain entities, complete original source records,
  attribute provenance, relationships, release identities and attribution;
- `serving.duckdb` contains only query-specific projections and Parquet
  locators, never complete raw source JSON, provenance or relationships;
- a sorted token dictionary and token/entity postings implement conjunctive
  token-prefix matching and the existing FTS5-compatible BM25 ordering;
- bounded one- and two-character result heads avoid ranking national-scale
  common prefix sets at request time;
- exact addresses are ordered by normalized address key;
- reverse candidates use a deterministic 0.001-degree grid followed by the
  existing exact spherical distance test and 100-metre limit;
- details and evidence resolve a public ID to one entity row and contiguous
  source/provenance spans in Parquet;
- the runtime uses read-only DuckDB connections, one DuckDB thread per
  connection and at most four connections;
- build and runtime disable known-extension auto-install and autoload. FTS,
  spatial, HTTPFS, ART and R-tree extensions or indexes are not required.

All persistent DuckDB tables are physically ordered on their selective key.
DuckDB zonemaps prune storage blocks for token IDs, public IDs, address keys and
grid cells. There are zero entries in `duckdb_indexes()`. Avoiding ART is
important: ART construction must fit in memory, while the selected global sorts
can spill under the explicit 256 MB DuckDB memory limit. Postings are generated
in deterministic 32,768-entity chunks before the final sort.

The normalized Parquet writer uses Zstandard and 32,768-row groups. The Newport
generation has one entity row group. The larger serving build used the retained
49.18 MiB normalized search/address input created by the bounded range-read
extraction in logs 0043 and 0045. No national download or build was performed;
the 20 GiB experiment cap was never approached.

## Correctness and failure behavior

The complete maintained Newport Places and geocoding suites matched the current
SQLite implementation. This includes short and long autocomplete prefixes,
multiple tokens, businesses, addresses, streets and areas, exact and ambiguous
addresses, missing addresses, unsupported units, malformed input, dense and
sparse reverse cases, the 100-metre cutoff, coverage boundaries, closed-place
exclusion, details, attribution, original source records and attribute
provenance. Location bias remains unsupported by the public API and continues
to fail explicitly rather than being silently ignored.

The Rhode Island envelope reproduced the SQLite FTS5 oracle's exact ordered
five IDs for `ma`, `white horse`, `main street`, `providence`, `50 bellevue`,
`dunkin`, `rhode island`, and a missing two-token query. The final fix separates
street-label deduplication from non-street entity sequences; without that type
component, an address sequence could collide numerically with a street name ID.

Artifact tests establish:

- identical normalized Parquet checksums after input records and relationships
  are reversed;
- zero logical row differences across independently built DuckDB catalogs;
- refusal to overwrite an artifact;
- full SHA-256 and Parquet row-count verification before open;
- uniqueness and coverage of entity/search locators;
- token/posting, exact-address and spatial coverage integrity;
- source-to-entity, provenance-to-source, and relationship endpoint integrity;
- corrupt Parquet rejection before a reader becomes visible;
- checksum-bound absolute generation references, atomic synced selection,
  open-new-before-swap, in-flight request leases, concurrent replacement and
  rollback;
- rejection of a corrupt replacement while the previous generation continues
  serving.

DuckDB database files are not byte-reproducible at this scale even when built
with one thread: two catalogs of 143,929,344 bytes had different file hashes but
bidirectional `EXCEPT` checks found zero differences in all ten logical tables.
Normalized Parquet shards are byte-deterministic. Each derived DuckDB file is
still individually checksum-bound for corruption detection. The current
selection reference deliberately binds that exact manifest instance, while a
future logical release identity must derive from the normalized Parquet
generation rather than assume DuckDB's physical serialization is canonical. A
one-thread build doubled elapsed time without solving this, so the qualified
builder keeps four build threads.

The current bundle has no per-record rejection relation, so no artifact writer
can preserve rejection rows that do not exist in its input. The future
streaming normalized format must add that relation before national production.

## Newport measurements

The pinned input was `data/newport.json`, SHA-256
`3dc0a699fb51087c7a3a4068adb59e0aa2c0628bf40a39e3bbff700b5a3de514`.
The current SQLite comparator was 35,950,592 bytes. The DuckDB candidate totaled
9,079,051 bytes: 4,348,171 bytes of Parquet and manifest plus a 4,730,880-byte
serving catalog. It contains 12,343 entities, 12,343 source records, 39,435
attribute-provenance rows, 1,184 relationships and two metadata rows.

A compiled builder completed in 1.19 seconds with 238,551,040 bytes peak process
RSS, including verification. Each operation class contains 100 warm requests;
the mixed workload uses four workers. The OS page cache was not purged.

| Operation | p50 | p95 | p99 |
| --- | ---: | ---: | ---: |
| Autocomplete | 2.17 ms | 3.27 ms | 3.39 ms |
| Details | 2.46 ms | 2.76 ms | 2.87 ms |
| Forward geocoding | 4.43 ms | 38.54 ms | 46.77 ms |
| Reverse geocoding | 4.85 ms | 5.42 ms | 5.47 ms |
| Provenance | 4.69 ms | 5.10 ms | 5.18 ms |
| Four-worker mixed | 4.97 ms | 43.85 ms | 45.46 ms |

Five checksum- and integrity-verifying open-plus-first-details samples were
16.03–16.51 ms. The selection reader verifies one generation once and reuses
that verified manifest
when opening it, rather than hashing every artifact file twice.

The compact SQLite Newport proof was faster for point operations and produced a
10.36 MB combined artifact. Both are well below every provisional latency
target. DuckDB's extra few milliseconds come from its embedded connection and
the Go Parquet evidence fetch, not full-table API scans.

## Rhode Island measurements

The scope and source acquisition are unchanged from logs 0043 and 0045:
longitude `[-71.8620,-71.1200]`, latitude `[41.1460,42.0190]`, Overture
2026-08-19.0. It contains 704,693 entities: 553,029 addresses, 67,716
businesses, 83,379 named road segments and 569 areas. The retained normalized
qualification projection SHA-256 is
`7e1d2a1dac216e4f1c17377373d1381f8f12f1121c9370b903146999599e8973`.

With four build threads and `memory_limit=256MB`, the final posting-based
catalog was 144,453,632 bytes. `/usr/bin/time -l` measured 6.25 seconds build
elapsed and 462,798,848 bytes peak process RSS. A separately sampled build
observed 1,066,240 KiB peak spill data and 147,468 KiB peak allocated database
blocks; the spill directory was removed by DuckDB after checkpoint. With
`memory_limit=128MB`, construction failed cleanly at 121.9 of 122.0 MiB instead
of growing without bound. The builder therefore pins 256 MB.

| Workload | p50 | p95 | p99 |
| --- | ---: | ---: | ---: |
| Autocomplete, one worker | 24.59 ms | 37.96 ms | 38.18 ms |
| Autocomplete, four workers | 28.74 ms | 43.47 ms | 44.22 ms |

The historical compact SQLite statewide workload had 182.20 ms aggregate warm
p95 and 1,071.03 ms at four requests; its common `ma` case was 166 ms p95. The
final DuckDB posting catalog is also 54.8% smaller than the 319.26 MB compact
SQLite index. The former current-style SQLite snapshot was 2.117 GB.

## Plans and scanned rows

`EXPLAIN ANALYZE` and JSON profiling on the retained statewide catalog confirmed
the intended sorted-table zonemap behavior:

| Query | Physical rows scanned | Rows entering result stage | Profile latency |
| --- | ---: | ---: | ---: |
| Public-ID locator | 110,592 of 704,693 | 1 | 3.01 ms |
| Exact `50 bellevue avenue` address | 122,880 of 548,947 | 5 | 3.18 ms |
| Representative reverse grid cell | 83,968 of 548,947 | 18 | 1.28 ms |
| `main street` autocomplete | 615,304 across both posting ranges, token dictionary, names and corpus stats | 15,080 intersected candidates; 5 returned | 34.76 ms |

For `main street`, only 122,880 posting rows were scanned for the 13 `main*`
tokens and 368,640 for the three `street*` tokens, from 4,615,653 total
postings. The token table scans 41,997 narrow rows per term; the name-prefix
range scanned 39,789 storage rows and returned 29 names. No 704,693-row rank
table participates in the query. After the five winning entity sequences are
known, the reader fetches five display rows. Details then reads one known row
from one 32,768-row Parquet row group. Evidence reads only the entity's recorded
source and provenance spans. The serving queries open no source Parquet globs,
so increasing the number of normalized source shards does not broaden their
scan set.

The current profile still reports `Sequential Scan` operators because no ART or
R-tree is present; physical order and zonemaps make those scans block-selective.
This is deliberate. A national run must recheck that common-token ranges do not
grow enough to violate the latency target, but memory no longer depends on a
complete rank-table scan.

## Nationwide extrapolation

The same mechanical factor as log 0043 is used:
`125,800,000 / 553,029 = 227.475`, implying about 160.3 million entities at the
observed Rhode Island kind mix.

| Component | Regional bytes | National point estimate |
| --- | ---: | ---: |
| Complete normalized Parquet base from log 0043 | 162,975,230 | 37.07 GB |
| Final compact DuckDB serving catalog | 144,453,632 | 32.86 GB |
| **Lookup generation** | **307,428,862** | **69.93 GB** |
| Plus planned routing and basemap |  | **about 129.9 GB total** |

A prudent lookup band is 50–105 GB, or roughly 110–165 GB after the stated
40 GB routing and 20 GB basemap assumptions. Business/source diversity,
multi-source provenance, rejection ratios, token frequency, compression and
additional query projections are the main uncertainties. Two lookup
generations plus the measured roughly 1 GiB regional spill scaled mechanically
remain compatible with the 400–600 GB safe-build planning band, but peak
national temporary disk must be measured during the first explicitly
authorized larger build rather than inferred from this regional sample.

The serving-index memory limit is fixed at 256 MB and spill is external, so it
is compatible with the provisional 32 GiB RAM ceiling. This does not make the
current in-memory normalized JSON producer national-safe; replacing that stage
with streaming deterministic Parquet shards is the smallest required migration
step.

## Deployment qualification

The Darwin arm64 builder binary is 83,254,418 bytes; the current SQLite server
binary is 20,848,274 bytes. A Darwin amd64 cross-build succeeded at 86,468,408
bytes. `otool -L` shows only system libraries, CoreFoundation, Security and
`libc++`; DuckDB itself is statically included. The downloaded binding modules
occupy about 570 MiB in the Go module cache, which is a build-host cost rather
than a deployed runtime file.

`CGO_ENABLED=0` fails because the platform binding has no eligible files. The
local machine has no Linux cross-C/C++ toolchain, so Linux binaries were not
produced here. The documented deployment target is currently native Darwin
arm64 and is qualified. Any future Linux deployment must add a native or proper
cross-CGO build and smoke test before cutover.

No DuckDB extension files are deployed. This avoids the version-coupled FTS and
spatial extension packaging measured in log 0043. The version is still pinned
through `go.mod`, and upgrading it requires rebuilding and requalifying the
derived catalog.

## Reproduction

Run from the repository root with the already pinned Newport inputs:

```sh
go run ./cmd/places-geocoding-duckdb \
  -bundle data/newport.json \
  -config config/places-geocoding.json \
  -out data/openmaps-duckdb

OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_BUNDLE="$PWD/data/newport.json" \
  go test -tags=integration ./internal/placesgeocoding/duckdb \
  -run 'TestNewport' -count=1 -v

OPENMAPS_RI_ENTITIES="$PWD/data/duckdb-prefix-experiment/qualification-entities.parquet" \
  go test -tags=integration ./internal/placesgeocoding/duckdb \
  -run TestRhodeIslandServingIndex -count=1 -v

go test -race ./internal/placesgeocoding/duckdb
go test ./...
go vet ./...
```

The command refuses an existing artifact directory. The Rhode Island test is
explicitly integration-tagged and requires the documented disposable local
projection; it is not part of ordinary CI.

After measurement, generated DuckDB catalogs, SQLite oracles, binaries,
profiles, temporary scripts, the Python environment and the Newport candidate
were moved out of the workspace to the system Trash. Only the 51,568,290-byte
`data/duckdb-prefix-experiment/qualification-entities.parquet` input remains so
the bounded statewide Go gate can be rerun without reacquiring source data. It
is ignored by Git and its checksum is recorded above.

## Remaining migration work

The qualification question is resolved: DuckDB is suitable for the intended
lookup runtime, and compact SQLite is not needed as a shipped fallback. The
smallest separately authorized migration is to define and build the streaming
normalized-Parquet generation, add its rejection relation, make the server's
existing Places/geocoding selection choose a DuckDB generation, and run the
same HTTP comparison before activation. Until that work is requested and
reviewed, SQLite remains the selected production snapshot and rollback path.
