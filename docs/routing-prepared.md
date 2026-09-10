# Prepared routing snapshots

Scout is the sole prepared routing format. The [maintained preparation workflow](routing-scout.md#acquisition-and-preparation)
uses Go to validate pinned metadata and receipts, stream nested package bytes into
an immutable tile spool, prepare turn/potential/reverse indexes, and build directed
landmarks. No Python or Valhalla routing engine is required.

Publication detects corrupt bytes and generation mismatches, refuses existing
outputs, and syncs receipts before selection. Graph extension verifies retained
packages and payloads; landmark reindexing requires proof that additions do not
connect to finite old components. A failed proof requires full recomputation.
Resource limits and the sampled supervisor are documented with the workflow.

The prior SQLite `routing-prepared-v*` files, mapped numeric arrays and
`cmd/routing-prepare` are removed. Their construction and residency reports remain
historical records in `docs/log/0021` through `docs/log/0025`.
