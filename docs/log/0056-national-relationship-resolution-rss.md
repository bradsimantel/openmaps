# National relationship-resolution RSS correction

**Date:** 2026-09-17

**Failed revision:** `3d6b50bc24832f752bc200ebe7da54409a38e86d`

## Failure

The bounded national checkpoint resume completed all sixteen entity-ID buckets.
Each bucket took approximately eleven minutes, confirming that the correction
recorded in the [earlier investigation](0055-national-normalization-rss-investigation.md)
bounded the original global entity sort.

The next relationship-resolution query joined 3,703,480 staged relationships
against the 165,759,144-row resolved source-key table twice, then grouped and
sorted the result. Before that phase emitted its completion observation, whole-
process RSS reached 51,813,396,480 bytes (48.25 GiB). The unchanged 48 GiB
supervisor stopped the process after 11,385 seconds. The 8 GB DuckDB limit had
not constrained native allocations outside its managed buffer pool. No
candidate or audit was published, and the input checkpoint was retained.

The four configured database threads are execution parallelism, not four
independent 8 GB memory budgets. The failure therefore must not be described as
an expected `8 GB * 4` allocation. It was a second unbounded national join that
the five-state gate was too small to expose.

## Correction

The resolved source-key table now includes a sixteen-way hash bucket used only
during normalization. Relationship endpoint keys are deduplicated one bucket at
a time and joined only to the corresponding resolved-key bucket. The resulting
compact endpoint map contains at most the distinct keys referenced by the 3.7
million relationships. Final relationship validation, deduplication and lexical
ordering join against that compact map rather than hashing the full national key
table twice.

The partition is an internal execution detail and does not change relationship
semantics or output order. Streaming integration tests require all sixteen
relationship-key phases to run and continue comparing the complete normalized
artifact checksums with the regional implementation.

The 8 GB DuckDB memory limit, 48 GiB sampled RSS ceiling, 60 GiB disk reserve,
4 GiB Go soft limit and checkpoint-preservation behavior remain unchanged.
