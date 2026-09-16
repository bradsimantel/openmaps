# Parallel national input staging

**Date:** 2026-09-15

**Scope:** next national Places/geocoding build; the already running build is
unchanged

## Evidence

The first four-source-worker national attempt still spent 12 hours 19 minutes
in input staging. Source Parquet decoding was concurrent, but every prepared
batch entered a single writer goroutine and both DuckDB staging databases were
limited to one open connection. Address, business and division auxiliary rows
were additionally inserted one row at a time while holding their shared batch
mutex. The 2,048-row batch setting required more than 100,000 main staging
appender lifecycles at national row counts.

## Change

The national and qualification profiles now use 16,384-row batches. Prepared
records and rejections are consumed by four bounded writer goroutines, with up
to four DuckDB connections. Auxiliary batches release their mutex before I/O
and use the DuckDB bulk Appender rather than prepared row-by-row `INSERT`
statements. Normalization and publication still sort at their existing
boundaries, so concurrent arrival order does not define the normalized output.

The batch queue remains bounded. The preparation database retains its 4 GB
limit, the normalization database retains 16 GB, and the process supervisor
retains the 48 GiB RSS ceiling. The larger batch is a reviewed tuning change:
it changes source-manifest metadata and checkpoint identity but must reproduce
the gate's pinned retained-data checksum before another national build.

The in-progress server build must not read these working-tree files and is not
restart-compatible with a checkpoint produced by this revision. It may finish
normally; this change applies to a separately built binary for the next run.
