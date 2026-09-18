# National catalog memory investigation

**Date:** 2026-09-18

**Original failed revision:** `a952f8ac3b8da0424c0fb19b9e6e10045e0e96be`

**Qualified server revision:** `2cfd7fcb2427a13fc1e979dec535865f285234fc`
(the server-applied equivalent of local revision `f108deb`)

## Readiness result

The research build completed catalog construction and final artifact validation
at actual national cardinality under the existing 32 GB decimal DuckDB catalog
limit and 48 GiB process supervisor. It did not publish to the production path
or change the selected lookup generation.

The result is not yet a production-retry authorization. The candidate still
requires the exact-revision loopback query checks recorded below and an operator
review of this evidence. No production national build was started by this
investigation.

## Historical failure and root cause

The failed `a952f8a` build passed normalization and then executed the complete
contents of `schema-sharded.sql` in one `database/sql` call. DuckDB reported
that it could not allocate another 256 KiB at 29.8 GiB of its 32 GB decimal
limit after approximately 53 minutes. The process reached 40,977,666,048 bytes
of sampled RSS. Because all schema statements shared one call and the failed
derived database was intentionally removed, the retained log cannot prove
which statement was active. The ordering and later timings make the global
`names` distinct/order the leading candidate, but that is an inference rather
than an exact historical attribution.

The broader root cause was nevertheless explicit in the old SQL. It contained
national-cardinality global sorts, distincts, joins, aggregations, and windows
for entity order, names, tokens, final postings, prefix scoring, and address
order. Completing or correcting any one of those did not bound the next. The
same pattern also existed in final verification through global distincts and
referential-integrity joins.

The corrected builder executes named statements and reports their elapsed time,
so a future error identifies its exact phase. It also replaces every listed
national-cardinality blocker with disjoint bounded partitions.

## DuckDB behavior relevant to the failure

This repository pins `github.com/duckdb/duckdb-go/v2 v2.10505.0`, whose binding
release `v0.10505.0` pins DuckDB `v1.5.5` in its
[Makefile](https://github.com/duckdb/duckdb-go-bindings/blob/v0.10505.0/Makefile).
The corresponding engine release is
[DuckDB v1.5.5](https://github.com/duckdb/duckdb/releases/tag/v1.5.5).

The implementation and controls follow these primary-source constraints:

- DuckDB documents `memory_limit` as a limit for the buffer manager, not a hard
  whole-process RSS limit; vectors, query results and some aggregate state can
  allocate outside it ([limits](https://duckdb.org/docs/current/operations_manual/limits),
  [out-of-memory guidance](https://duckdb.org/docs/current/guides/performance/oom)).
- Hash aggregation, joins, sorting and window functions are blocking operators.
  Multiple concurrent blockers and some non-spillable aggregate state can
  exhaust memory even for larger-than-memory workloads
  ([workload tuning](https://duckdb.org/docs/current/guides/performance/how_to_tune_workloads),
  [window functions](https://duckdb.org/docs/current/sql/functions/window_functions)).
- DuckDB uses the configured temporary directory for spill, but temporary files
  do not make every operator spillable
  ([memory management](https://duckdb.org/2024/07/09/memory-management),
  [memory and temporary-file introspection](https://www.duckdb.org/docs/current/sql/meta/duckdb_table_functions)).
- Parallel aggregation uses thread-local hash tables before combination, making
  thread count part of the memory envelope
  ([external aggregation](https://duckdb.org/2024/03/29/external-aggregation)).
  DuckDB's environment guidance recommends roughly 1--4 GB per thread and notes
  higher requirements for joins
  ([environment](https://duckdb.org/docs/current/guides/performance/environment)).
- `preserve_insertion_order=false` can reduce pressure during loading but cannot
  bound an explicit `ORDER BY`. DuckDB's OOM guidance recommends reducing
  threads or the memory limit when allocator and buffer-manager pressure
  interact.
- The v1.5.5 physical hash-aggregate implementation is visible in
  [`physical_hash_aggregate.cpp`](https://github.com/duckdb/duckdb/blob/v1.5.5/src/execution/operator/aggregate/physical_hash_aggregate.cpp).
  Relevant upstream reports include high-RSS large-cardinality grouping
  ([issue 14584](https://github.com/duckdb/duckdb/issues/14584)) and the fact that
  the configured memory limit is not a process limit
  ([issue 8398](https://github.com/duckdb/duckdb/issues/8398)).

These facts are why the correction partitions work rather than raising the
limit. The external 48 GiB supervisor remains the process-level safety control.

## Operator inventory and bounds

| Stage | Former blocker | Current bound or justification |
| --- | --- | --- |
| Input validation | Global duplicate and overlap distinct/group operations | Materialize validation keys once, then validate 64 hash buckets; each bucket is enforced at no more than 4,000,000 rows. |
| Entity normalization | Global public-ID grouping and order | Sixteen disjoint leading-ID buckets, already qualified at national cardinality; output order is bucket order plus local order. |
| Relationships | Full endpoint-map joins and final group/order | Source-key, from-ID and to-ID work use disjoint hash buckets; final output uses public-ID buckets. |
| Rejections and identities | Global lexical sort | Recursive, disjoint lexical partitions, each at most 4,000,000 rows; exact keys are separated from longer strings sharing the prefix. |
| Parquet and checksums | Large materialization | Fixed 32,768-row groups, file rotation near 384 MiB, streaming SHA-256 and physical row-count checks. |
| Entity locator and search projection | Global ID sort and `row_number` | 256 two-hex public-ID ranges. Prefix order preserves the former global ID order. |
| Names | Global distinct, sort and window | Recursive normalized-name partitions capped at 4,000,000 input rows, with cumulative offsets preserving IDs. |
| Entity ranks | National join and order | The same lexical name partitions followed by entity-sequence ranges capped at 4,000,000 rows. |
| Token candidates | National unnest/distinct | 32,768-entity chunks. |
| Tokens | Global distinct, order and window | Recursive token lexical partitions capped at 4,000,000 candidate rows, with cumulative IDs. |
| Posting generation | Group/join/order per entity chunk, plus a wide stage | 32,768-entity chunks and a slim temporary stage containing only token/entity/frequency columns. |
| Final postings | Global token/entity sort plus rank join | Token-ID ranges containing at most 4,000,000 postings; an individual hot token is further split by entity sequence. |
| Short-prefix head | National group and two windows | Per-entity-chunk candidates retain only five rows per prefix; exact one- and two-character prefixes are consolidated independently with unchanged street collapse and top-five semantics. |
| Address staging | Three national joins | 256 two-hex entity-ID ranges. |
| Address lookup | Global lexical order | Recursive address-key partitions capped at 4,000,000 rows. |
| Address spatial | Global cell order | Numeric cell-ID ranges capped at 4,000,000 rows. |
| Catalog validation | Global joins and distincts | Entity checks use 256 ID ranges; search references use numeric range checks; prefix cardinality is intrinsically bounded to one- and two-character keys; normalization integrity is accepted only from the checksum-bound checkpoint attestation. |
| Checkpoint and manifest | Database flush and file enumeration | One explicit DuckDB checkpoint; manifest hashing and row counts are sequential by file. |
| Server startup | Repeats artifact verification | Uses the same bounded catalog checks and sequential artifact hashing before opening the reader. |

The maximum work admitted to a newly partitioned catalog sort, group, or window
is therefore four million input rows. The older sixteen-way entity-normalization
boundary is larger, but it is separately demonstrated by all sixteen completed
national phases under the same supervisor.

## Durable normalized/catalog boundary

Normalization now publishes an immutable `normalized-checkpoint.json` only
after the complete normalized manifest, checksums, and physical Parquet row
counts pass. Publication is atomic. The checkpoint identity binds the exact
build identity and source/configuration inputs. Catalog-only construction reads
the checkpoint without mutating it, creates a new generation, adds the serving
database and manifest, verifies the result, and atomically publishes only on
success.

The retained research checkpoint is:

`/srv/openmaps/data/national-normalized-research-2cfd7fc`

The completed research generation is:

`/srv/openmaps/data/national-catalog-research-2cfd7fc`

An intentionally cancelled deterministic integration test proves that a catalog
retry consumes the normalized checkpoint without invoking the producer again.
Reviewed compatibility with the older retained input checkpoint is exact: both
the former build identity and former output path must be supplied, the marker is
read-only, and the accepted checkpoint is preserved even when the new build
succeeds.

## National benchmark

The run used actual national input and data distribution, four catalog threads,
the existing 32 GB decimal DuckDB limit, a 48 GiB process-tree RSS ceiling, a
60 GiB free-disk reserve, and one-second resource sampling. The research output
path was unique and was not activated.

| Measurement | Result |
| --- | ---: |
| Input records / serving entity coverage | 165,759,144 |
| Rejections | 63,610,167 |
| Relationships | 3,703,480 |
| Total elapsed | 13h 58m 3s |
| Normalized Parquet phase | 3h 47m 30s |
| Catalog construction | 9h 54m 8s |
| Final artifact validation | 7m 32s |
| Sample interval / samples | 1 second / 50,282 |
| Peak process-tree RSS | 36,055,724,032 bytes (33.58 GiB) |
| Peak anonymous RSS | 36,021,575,680 bytes (33.55 GiB) |
| Peak file-backed RSS | 53,829,632 bytes |
| Peak sampled CPU | 592% |
| Process-tree CPU | 155,480.97 seconds |
| Read / write I/O | 3,982,579,675,136 / 1,781,332,332,544 bytes |
| Free disk before / minimum / after | 595,588,947,968 / 473,792,249,856 / 478,456,418,304 bytes |
| Serving database size | 38,517,616,640 bytes |
| Normalized checkpoint allocated size | 74 GiB |
| Published catalog allocated size | 36 GiB, excluding hard-linked normalized files already counted |

The RSS margin below the 48 GiB supervisor was 15,483,883,520 bytes (14.42
GiB). Minimum free disk remained 441.25 GiB, leaving 381.25 GiB beyond the
required reserve. Linux reported every member of `md0`, `md1`, and `md2` as
`[UU]` after the run.

### Catalog sub-phases

| Phase | Elapsed |
| --- | ---: |
| Entity locator | 2m 19s |
| Search entities | 5m 28s |
| Names | 2h 17m 31s |
| Entity ranks | 1h 19m 25s |
| Token candidates | 2m 41s |
| Tokens | 2h 22m 51s |
| Posting generation | 1h 58m 43s |
| Posting sort | 1h 16m 50s |
| Short-prefix head | 13m 13s |
| Address stage | 2m 7s |
| Address lookup | 7m 23s |
| Address spatial | 2m 49s |
| Bounded catalog validation | 52s |
| DuckDB checkpoint | 4s |

The 1/2/4-thread matrix in the original investigation plan was deliberately
narrowed after the intended four-thread configuration completed with a 14.42
GiB RSS margin. Full one- and two-thread builds would add an estimated 24--48
hours while answering only the fallback runtime/memory tradeoff; they are not
needed to establish that the intended production control works. Two threads
remain the first contingency if a later host has materially less memory. This
scope change means there is no measured cross-thread scaling table.

DuckDB JSON profiling was enabled for each catalog phase. The retained files
record process-global peak buffer-manager and spill counters, but DuckDB
overwrote each phase file with the final profiling-control statement rather
than retaining every loop statement's physical plan. Those files are useful as
raw counter evidence, not as per-partition query plans. The blocking-operator
inventory above is therefore based on the executed SQL and explicit partition
guards, not an overclaimed post-hoc plan attribution.

## Artifact evidence

The final manifest contains 199 files. Its normalized checksum is
`9f0926a9ccd3efa06d52623a674d3e2c9accd5f3e1a00ea1e9e067b43eddf83c`
and retained-data checksum is
`f3b8f02713e4129bad405761cadbd3124f081bd8ed439ebf725455493d6a055e`.

| Role | Physical files | Rows |
| --- | ---: | ---: |
| Entities | 43 | 165,759,144 |
| Source records | 104 | 165,759,144 |
| Attribute provenance | 19 | 502,852,430 |
| Relationships | 1 | 3,703,480 |
| Rejections | 30 | 63,610,167 |
| Metadata | 1 | 2 |
| Serving catalog coverage | 1 | 165,759,144 |

Verification recalculated every manifest checksum and physical Parquet row
count, checked exact entity coverage, bounded entity uniqueness/kind agreement,
contiguous token IDs, posting and prefix ranges, prefix top-five limits, address
coverage, the normalization attestation, and absence of unexpected persistent
indexes before publication.

## Raw evidence

- Resource summary:
  `/srv/openmaps/data/national-catalog-research-2cfd7fc.resources.json`
- One-second samples:
  `/srv/openmaps/data/national-catalog-research-2cfd7fc.samples.ndjson`
- Preparation audit:
  `/srv/openmaps/data/national-catalog-research-2cfd7fc-audit.json`
- DuckDB phase profiles:
  `/srv/openmaps/data/national-catalog-research-2cfd7fc-profiles`
- Historical failure archive:
  `/srv/openmaps/data/failed-runs/openmaps-us-20260819-catalog-oom-a952f8a-20260917`

## Exact-revision startup and representative queries

Pending final loopback measurements.

## Corrections and verification

Implementation adds the partitioned catalog builder, named phase observations,
one-second Linux process-tree sampling (RSS split into anonymous/file-backed,
CPU and I/O), preserved failure evidence, the immutable normalized checkpoint,
catalog-only resume, reviewed compatibility with the retained input checkpoint,
and bounded final verification. Small deterministic fixtures compare every
catalog table against the legacy builder and prove cancellation/resume and
read-only checkpoint compatibility.

Local verification at the final code state passed:

```text
gofmt (changed Go files)
go test ./...
go vet ./...
git diff --check
```

## Remaining risks and production envelope

- Exact historical statement attribution remains unavailable from the retained
  failed database because the old code did not create the evidence needed to
  distinguish statements. The global names distinct/order is the leading
  inference, not a proven fact.
- DuckDB's phase profile output did not retain per-loop physical plans. The
  code-level partition guards and successful national result prove the memory
  envelope, but a later harness improvement should give every executed query a
  unique profile filename.
- Only the intended four-thread catalog configuration was measured. There is no
  quantified lower-thread fallback.
- The initial national rectangular road scope can retain short cross-border
  portions; this is a data-scope review issue, not a build-memory issue.
- The build performs substantial I/O (approximately 3.98 TB read and 1.78 TB
  written at the process boundary). Storage health and at least the 60 GiB
  reserve must remain hard preconditions.

For the next equivalent run, budget approximately 14 hours end to end on this
host, with 18 hours as the planning envelope. Enforce the existing 48 GiB RSS
ceiling, 32 GB catalog limit, four threads, one-second sampling, and 60 GiB disk
reserve. Expected peak RSS is approximately 34 GiB; stop and preserve evidence
if it exceeds 48 GiB. Expected catalog time is approximately ten hours.

The exact retry command will be recorded here only after loopback serving checks
complete. Until then the concise decision is **NOT READY** because exact-revision
startup and representative API behavior are an explicit unresolved evidence
gap.
