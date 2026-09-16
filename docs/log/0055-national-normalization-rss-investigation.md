# National normalization RSS and checkpoint investigation

**Date:** 2026-09-16

**Failed revision:** `e27ee2fe2e7b699c526804017d6920c1b623ae98`

## First failure: 16 GB DuckDB limit

The checkpoint-enabled national build completed input staging in 2 hours 20
minutes and atomically published its input checkpoint. During the first
normalization sort, before any Parquet row group was emitted, the unchanged 48
GiB supervisor stopped the process. Sampled RSS was 51,623,837,696 bytes, 48.08
GiB. The resource report recorded 415,953,289,216 bytes as minimum free disk,
well above the 60 GiB reserve. The completed checkpoint, failed-run log and
resource report were retained; no candidate or audit was published.

## Second failure: 8 GB DuckDB limit

An operator-authorized resume reused the completed input checkpoint with the
normalization memory limit reduced from 16 GB to 8 GB. The checkpoint's original
marker was preserved before its operational identity was migrated. All source,
output, thread and build identity fields remained unchanged, and the exact clean
`e27ee2f` binary was used. A proposed local connection-pool change was not
deployed.

The resume was stopped by the same 48 GiB supervisor after 1,855 seconds.
Sampled RSS was 51,544,813,568 bytes, only 79,024,128 bytes below the 16 GB
attempt. Minimum free disk was 305,523,634,176 bytes, still well above the 60 GiB
reserve. The checkpoint was retained; no candidate or audit was published.

## Instrumented reproduction

A third 8 GB resume sampled `/proc/PID/smaps_rollup`, process status, I/O and
host memory every five seconds. During external-sort run generation, process RSS
remained near 9 GiB while DuckDB wrote approximately 270 GiB of spill. At about
30 minutes the spill stopped growing and the merge began. RSS then climbed from
roughly 9 GiB to the 48 GiB supervisor boundary in about two minutes. The
supervisor stopped the process after 1,954 seconds at a sampled peak of
51,766,226,944 bytes.

The growth was anonymous/private memory. Near the boundary, anonymous RSS was
approximately 48 GiB while file-backed RSS remained about 45 MiB. The process
had not begun emitting normalized Parquet. This isolates the failure to
DuckDB's merge/materialization of the first global `resolved_keys` sort, not the
filesystem page cache, Go entity grouping, or Parquet encoding.

## Findings

Reducing DuckDB's managed query-memory limit by 8 GB did not materially reduce
the failed global merge's whole-process RSS. The first two sampled peaks differ
by about 75 MiB, or less than 0.2 percent. The instrumented reproduction showed
that the limit held run generation near 9 GiB but did not bound anonymous memory
during the merge. DuckDB's limit governs its buffer manager, not every native
allocation used by a blocking operator.

Revision `e27ee2f` also widened the staging `database/sql` pool to four
connections so bounded writer goroutines could append concurrently. The same
pool limit remained after reopening for normalization even though that phase
consumes one ordered result stream. `database/sql` opens connections lazily, so
the configured maximum is not evidence that normalization opened four
connections. The checkpoint resume also performed no concurrent staging. There
is no evidence that narrowing this configured maximum would address the RSS
peak, so that proposed change was not retained.

The five-state gate did not expose the full-scale RSS effect. It reproduced the
retained-data checksum and all artifact checks, but its smaller staging tables
peaked at 26,343,493,632 bytes.

## Correction

The global `CREATE TABLE resolved_keys ... ORDER BY entity_id,source_key` is
replaced with an unsorted key-mapping table. Entity normalization processes the
sixteen leading hexadecimal public-ID buckets in lexical order and sorts only
one bucket at a time by the original complete ordering. Because every entity ID
belongs to exactly one bucket and buckets are processed in lexical order, the
resulting row stream remains globally deterministic while each external-sort
merge is bounded to approximately one sixteenth of the national records.

The gate and national normalization memory limit is 8 GB. The lower limit is
retained as headroom for DuckDB allocations outside the buffer manager; it is
not treated as a whole-process ceiling. Existing streaming determinism tests
exercise every bucket and require the partitioned output to match the regional
normalizer's checksums and rows.

The 48 GiB supervisor ceiling, 60 GiB disk reserve, 4 GiB Go soft limit, 4 GB
preparation limit and 32 GB catalog limit remain unchanged. The checkpoint,
failed-run reports and five-second RSS samples remain operational evidence. A
checkpoint identity migration across the correction requires explicit operator
review because build identity is deliberately exact; otherwise a corrected
national build must stage fresh input after passing the gate again.
