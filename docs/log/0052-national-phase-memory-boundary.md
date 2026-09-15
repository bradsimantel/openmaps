# National build phase memory boundary

**Date:** 2026-09-15

**Starting revision:** `4e6639cc3a1ea00d2698dd2d383293faba128979`

## Failure evidence

The first national run using the four-worker profile completed input staging in
12 hours 19 minutes but was stopped by the unchanged 48 GiB supervisor before
normalization. Sampled RSS was 51,591,557,120 bytes (48.048 GiB), 49.5 MiB over
the ceiling. The supervisor stopped the run at its first over-budget sample, so
that observation is not an unconstrained peak. No candidate or audit was
published. Sampled free disk never approached the 60 GiB reserve.

The qualification slice peaked at 26,228,412,416 bytes. It proved the selected
controls and data digest on Linux/amd64, but its smaller preparation tables did
not reproduce the full-scale overlap. The national run showed that using the
same 16 GB limit for both concurrently open DuckDB databases did not leave
reliable process-level headroom for Go, Parquet, DuckDB native allocations, and
allocator overhead.

## Correction

Schema-2 manifests now distinguish `preparation_memory_limit` from the existing
normalization `memory_limit`. Omitted preparation limits retain the historical
behavior by defaulting to the normalization limit. The checked-in qualification
and national profiles set preparation to 4 GB while retaining 16 GB for
normalization, 32 GB for catalog construction, the 4 GiB Go soft limit, and the
48 GiB supervisor ceiling.

After input staging, the normalization database is checkpointed, closed, and
reopened before validation and normalized Parquet generation. This creates an
explicit native-memory phase boundary and releases ingestion buffers before the
large normalization sort. The supervisor ceiling is deliberately unchanged;
the correction reduces concurrent component budgets rather than weakening the
whole-process safety control.
