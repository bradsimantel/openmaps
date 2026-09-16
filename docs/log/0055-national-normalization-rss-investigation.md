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

## Findings

Reducing DuckDB's managed query-memory limit by 8 GB did not materially reduce
whole-process RSS. The two sampled peaks differ by about 75 MiB, or less than
0.2 percent. The memory responsible for the supervisor failure is therefore not
controlled adequately by this setting alone. DuckDB's limit does not include
every native allocation, allocator overhead, Go or Parquet memory, and sampled
RSS may include file-backed mappings. The available evidence does not yet
separate those categories.

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

## Current conclusion

There is no confirmed code or configuration fix yet. The checked-in 16 GB
normalization limit remains unchanged, and the proposed 8 GB and connection-pool
changes were removed rather than presenting them as corrections. Before another
national attempt, diagnostics need to distinguish anonymous RSS, file-backed
mappings and other native allocations during normalization. That evidence can
then support either a bounded pipeline change or an explicit revision of the
external supervisor policy.

The 48 GiB supervisor ceiling, 60 GiB disk reserve, 4 GiB Go soft limit, 4 GB
preparation limit and 32 GB catalog limit were unchanged in both attempts. The
checkpoint and both failed-run reports remain operational evidence. A new
revision cannot resume the failed revision's checkpoint because build identity
is deliberately exact; a corrected national build must stage fresh input after
passing the gate again.
