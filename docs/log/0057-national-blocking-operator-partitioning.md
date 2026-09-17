# National blocking-operator partitioning

**Date:** 2026-09-17

**Failed revision:** `684a733c28a0678ce45ed6b0586e1e4471e7f13b`

## Failure

The national checkpoint resume completed all sixteen entity buckets and all
sixteen relationship endpoint-key buckets. Entity buckets remained stable at
approximately eleven minutes each, and endpoint-key buckets completed in four
seconds each after the first twelve-second bucket. This confirms that both
earlier corrections bounded the operators they replaced.

The following query still joined the 3,703,480 relationships against the
complete compact endpoint map twice, grouped them, and globally ordered the
result. It reached 51,756,781,568 bytes (48.20 GiB) of sampled RSS before its
phase observation. The unchanged 48 GiB supervisor stopped the process after
11,311 seconds. No candidate or audit was published. The 218 GiB input
checkpoint was preserved, all RAID members remained healthy, and
550,211,194,880 bytes of disk remained free.

The five-state gate contained only 104,016 relationships. Its successful 24.52
GiB peak established behavioral equivalence but was not a sufficient
cardinality test for the national join. Calling the endpoint-key change a
complete memory correction was therefore premature.

## End-to-end normalization correction

Relationship normalization now has three bounded materialization steps:

1. Distinct endpoint keys are resolved in sixteen source-key hash buckets.
2. Relationship sources and targets are resolved independently in sixteen hash
   buckets each, so neither join builds a complete endpoint map.
3. The already-resolved rows are deduplicated and ordered in sixteen leading
   public-ID buckets. Those buckets are emitted in hexadecimal order, preserving
   the previous global `(from_id,to_id,kind,evidence)` order.

Each endpoint bucket verifies that every referenced source key was mapped, and
the two materialized row counts must equal the staged relationship count before
publication. Relationship kind validation remains on the emitted rows.

The later 63,610,167-row rejection sort is also partitioned before it can become
the next national blocking operator. A recursive planner subdivides source keys
one lexical character at a time until each disjoint range contains at most four
million rows. Exact short keys are kept separate from longer keys sharing their
prefix. Processing those ranges in lexical order preserves the prior exact
`(source_key,reason)` row order and therefore the normalized checksum. Identity
metadata uses the same bounded lexical planner.

Routine integration tests still require byte-identical normalized output from
the streaming and regional implementations, now observe every relationship
source, target and output bucket, and test exact-key/prefix ordering. An opt-in
`OPENMAPS_NATIONAL_NORMALIZATION_STRESS=1` test materializes and consumes exactly
3,703,480 relationship rows and 63,610,167 rejection rows under the production
8 GB DuckDB limit. It is kept out of the default suite because it is an explicit
large-data qualification, not a routine deterministic fixture.

The 8 GB DuckDB limit, 48 GiB sampled RSS ceiling, 60 GiB disk reserve, 4 GiB Go
soft limit and checkpoint-preservation behavior remain unchanged.
