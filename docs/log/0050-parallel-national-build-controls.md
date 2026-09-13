# Parallel national Places/geocoding build controls

**Date:** 2026-09-13

**Starting revision:** `bd30aa7dcd4fc29af884133d81f328db1e41ac2f`

**Status:** implemented locally with an automatically enforced retained-data
checksum; gate qualification on the 64 GB host is still required before a later
national rebuild

## Evidence and decision

The first national attempt was intentionally started with the controls qualified
by the 6.105% gate: one DuckDB thread, a 1 GB preparation/normalization limit, a
3 GB catalog limit, a 1.5 GiB Go soft limit, and a 6 GiB sampled RSS ceiling.
An in-progress observation approximately 14.2 hours into that attempt showed
about 95.8% of one logical CPU, 2.73 GiB RSS, 530.0 GB cumulative process writes,
76.0 GB in the build workspace, and about 805.5 GB free. This is operational
evidence of an underused four-physical-core, 64 GB host, not a completed-run
resource report or final performance result.

The next-run configuration uses four parallel source-asset workers and four
DuckDB threads. Four matches the host's physical cores; the additional logical
CPUs share execution resources and are not treated as independent capacity for
these compression, parsing, sorting, and hashing workloads. Source kinds remain
ordered, but independent Parquet assets within one kind can be decoded in
parallel. Publication boundaries retain explicit ordering so concurrent input
does not define normalized output order.

The national memory controls are now:

| Control | Value |
| --- | ---: |
| Preparation/normalization DuckDB limit | 16 GB per database |
| Serving-catalog DuckDB limit | 32 GB |
| Go runtime soft limit | 4 GiB |
| Sampled process RSS ceiling | 48 GiB |
| Free-disk reserve | 60 GiB |

Preparation's auxiliary database and the normalization staging database can
coexist, so their 16 GB settings are not interpreted as a 16 GB whole-process
limit. The 48 GiB supervisor ceiling is the aggregate safety control and leaves
roughly 14 GiB of the installed memory for the kernel, filesystem cache,
allocator overhead, and the interval between RSS samples. The existing disk
reserve and workspace estimate are unchanged.

## Implementation boundaries

- Schema 2 manifests now pin source-worker, normalization-thread,
  catalog-thread, and progress-log settings.
- Remote asset decoding uses a bounded worker pool. Worker output enters a
  bounded asynchronous queue and DuckDB's bulk Appender, rather than holding a
  global producer lock across thousands of individual SQL inserts. Shared
  audit and auxiliary-link staging remains synchronized; source row and byte
  totals are reduced in manifest asset order after workers finish.
- Normalization and catalog DuckDB connections use their manifest-selected
  thread counts. Existing single-threaded public build entry points remain as
  compatibility wrappers.
- A source progress line is emitted at least every 30 seconds and at each asset
  completion. Later build phases emit completion timings.
- The supervisor accepts the new 48 GiB ceiling while retaining the same sampled
  RSS and disk-abort behavior.

The artifact manifest now records a `data_sha256` over entities, source records,
attribute provenance, relationships, and rejections. It excludes only metadata
and the serving database, allowing the retained data to be compared across
runtime-only changes while the existing `normalized_sha256` continues to bind
the source-manifest metadata. The gate pins its previously qualified retained-
data digest, `9690930164a409293f0040d0f8c9f148ac9cf7bec8c69bdb7dbff852ca578896`,
and refuses publication on a mismatch.

The currently running national attempt is unchanged and must not be restarted
to obtain these improvements. Before a future national run, the updated gate
must reproduce that retained-data checksum using the same 4/4-thread,
16 GB/32 GB database, 4 GiB Go, and 48 GiB RSS controls as the national profile.
That run should also establish actual parallel peak RSS, temporary-disk high
water, and wall-time improvement; no speedup factor is claimed by this
implementation record.
