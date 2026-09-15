# National input checkpoint and resume

Date: 2026-09-15

Scope: national Places/geocoding phase-boundary recovery

The first four-worker national attempt at the optimized memory profile completed
roughly twelve hours of remote source ingestion, then crossed the supervisor RSS
limit during the transition into normalization. The failed process removed its
temporary DuckDB staging directory, so a corrected retry could not reuse that
completed ingestion work.

The streaming builder now supports an explicit durable checkpoint after source
ingestion. It runs DuckDB `CHECKPOINT`, records staging-table row counts and the
preparation audit, closes the database, and atomically renames a `.building`
directory to the configured checkpoint path. Only then does normalization begin.
The staging database and generated candidate use separate work directories, so
failed normalization or catalog output can be discarded without touching the
checkpointed inputs.

Resume is explicit rather than automatic. It requires the same output path,
manifest and source pins, identities, expected retained-data checksum, DuckDB
memory and thread controls, clean Git revision, and OS/architecture. It opens the
checkpoint, checks the recorded table counts, runs the normal staging integrity
checks, drops only derived normalization state, and rebuilds every candidate
artifact. The expected data checksum and complete artifact verification still
run before publication. A successfully published candidate removes its
checkpoint; a downstream failure preserves it.

This mechanism does not resume a partially read source asset. An interruption
before the phase-boundary marker exists must restart ingestion. Mid-source resume
would require a separately designed transactional asset or row-group ledger and
audit reconstruction, and is not part of this change.
