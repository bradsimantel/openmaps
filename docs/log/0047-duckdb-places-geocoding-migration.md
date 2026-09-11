# DuckDB Places/geocoding production migration

| Field | Value |
| --- | --- |
| Date | 2026-09-11 |
| Starting revision | `d276f007a9cb2367ce7ae30027704242d8047148` |
| Scope | Production lookup cutover; Newport compatibility and retained Rhode Island scale gate |
| DuckDB Go driver | `github.com/duckdb/duckdb-go/v2` v2.10505.0 |
| Status | Migration implemented and verified; national source download/build not performed |

## Outcome and decision

Normalized immutable Parquet is now the authoritative Places/geocoding
snapshot. A DuckDB file contains only query-specific serving projections and
Parquet evidence locators. DuckDB is the normal server runtime for autocomplete,
details, forward and reverse geocoding, provenance lookup, and routing waypoint
resolution. Compact SQLite was removed rather than retained as a parallel
fallback. The older SQLite importer and readers remain only as test oracles.

Rollback means selecting the previous verified DuckDB generation. Lookup,
Scout routing and the Protomaps basemap retain independent artifacts and
lifetimes.

## Artifact and construction

Schema 2 manifests bind coordinate order, a deterministic checksum of all
normalized relations, and the checksum and row count of every individual file.
Relations are entities, complete source records, winning-attribute provenance,
validated relationships, explicit rejections, and metadata containing the
pinned source manifest and identity mappings. The serving catalog contains the
qualified sorted token dictionary and postings, one/two-character result heads,
exact-address projection, deterministic spatial grid, and evidence locators.

The normalizer consumes the provider-record JSON array incrementally into a
temporary DuckDB stage. DuckDB externally sorts identity groups and
relationships; Go retains only one entity's contributing records while writing
Parquet. Parquet uses Zstandard and bounded 32,768-row groups. Relations rotate
near 384 MiB at a row-group boundary, while small regional relations remain one
file. The normalization stage uses one thread and a 128 MB DuckDB memory limit;
the serving build retains the qualified four threads, 256 MB limit, 32,768-entity
posting chunks and external spilling.

Normalized files are byte-deterministic. Two independent Newport builds had the
same logical checksum and identical checksums for all six normalized relations;
their `serving.duckdb` checksums differed, as expected. Derived DuckDB bytes are
therefore never used as the logical release identity, but each physical catalog
is still checksum-bound in its own manifest.

The Newport generation contains 12,343 entities, 12,343 source records, 39,435
provenance rows, 1,184 relationships, two metadata rows, and 3,138 explicit
rejections. Rejections cover one out-of-bounds address, 295 divisions outside
the selected hierarchy, 2,835 unnamed road segments and seven non-road segments,
each with its original source record.

## Runtime and lifecycle

`cmd/server` accepts `-lookup` for a directly opened generation and
`-lookup-selection` for the normal live-selection mode. Startup and reload verify
every manifest checksum and Parquet row count plus entity, source, provenance,
relationship, posting, exact-address and spatial integrity. A bad initial
generation fails startup; a bad replacement leaves the last verified generation
active and degrades health with the reload error. There is no SQLite fallback.

Replacement opens the new generation before taking the swap lock. HTTP and
routing-coordinate-resolution requests hold a shared generation lease through
response encoding. Retirement and graceful shutdown wait for those leases before
closing DuckDB or Parquet handles. Health and response headers expose the exact
selected manifest checksum.

Refresh comparison now joins normalized Parquet with DuckDB rather than loading
snapshot maps in Go. Review binds the exact comparison report, current selection
and candidate manifest; activation is refused if any changes before publication.
The selection file is written atomically and synced. The compact-SQLite command,
package and non-production selector were removed; logs 0043–0046 remain historical
evidence.

## Compatibility and failure verification

The complete maintained Newport integration suite matched the retained SQLite
baseline byte for byte for HTTP status, headers and bodies. It covered short and
multi-token autocomplete, all four entity kinds, ordering and stable IDs, closed
places, details and attribution, complete evidence, exact/ambiguous/missing
forward geocoding, malformed and unsupported units, reverse dense/sparse/no-match
and coverage cases, and the exact 100-metre rule. Four-worker mixed lookup passed.

Routing tests confirmed that both place IDs and exact addresses resolve through
one leased generation before coordinates reach Scout. Unit gates covered
reordered-input determinism, logical equality despite different DuckDB bytes,
missing/corrupt shards, rejected reload reporting, open-new-before-swap,
in-flight request retirement, rollback and canceled-build cleanup. Unsupported
location bias and other unimplemented Google parameters remain explicit errors.

The retained 704,693-entity Rhode Island gate passed exact ordered five-ID parity
for its representative queries. Its plans retained selective zonemap ranges:
`main street` intersects posting ranges and never scans the complete rank table;
details, exact addresses and reverse lookup use their narrow ordered projections.
No ART, R-tree, FTS, spatial, HTTPFS, H3 or S2 extension/index was added.

## Measurements

The production Newport build from the pinned input completed in **2.56 s** with
**248,610,816 bytes** peak process RSS. The generation is **10,310,936 bytes
(9.83 MiB)**: 4.82 MiB normalized Parquet/manifest and 5.01 MiB DuckDB. The explicit
rejection relation is 705,836 bytes. Temporary staging/spill files were removed
before publication; 10 ms sampling observed **45,780 KiB** peak temporary
generation data during a separate final build.

Newport warm measurements (100 operations per class) were:

| Operation | p50 | p95 | p99 |
| --- | ---: | ---: | ---: |
| Autocomplete | 2.21 ms | 3.18 ms | 3.48 ms |
| Details | 2.48 ms | 2.78 ms | 2.86 ms |
| Forward geocoding | 4.16 ms | 38.47 ms | 38.62 ms |
| Reverse geocoding | 4.78 ms | 5.35 ms | 5.42 ms |
| Provenance | 4.62 ms | 5.01 ms | 5.06 ms |
| Four-worker mixed | 4.79 ms | 43.49 ms | 44.71 ms |

Five checksum/integrity-verifying open-plus-first-details samples were
16.58–17.63 ms.

The final Rhode Island serving catalog built under the 256 MB limit in **5.91 s** and
was **143,929,344 bytes**. Autocomplete measured 24.02/36.82/37.04 ms p50/p95/p99
with one worker and 28.01/42.21/42.51 ms with four workers. The retained
qualification input remained 51.6 MB and no new experiment approached the 20 GiB
cap.

## Commands and deployment implications

The normal local workflow is:

```sh
go run ./cmd/places-geocoding-prepare -fetch
go run ./cmd/places-geocoding-import
go run ./cmd/server
```

The controlled refresh and rollback commands are documented in
[`docs/refresh.md`](../refresh.md). DuckDB requires CGO and a native C/C++
toolchain. Darwin arm64 was built and smoke-tested locally. CI now performs the
required native Linux CGO server/import builds and help smoke tests in addition
to the complete Go test, race and vet gates.

The rollback rehearsal initialized generation A, compared and reviewed
independently built generation B, activated B, then selected A again. Status
showed A as current and B as previous, both bound by their exact manifest hashes;
a live server then returned healthy lookup identity and successful details and
forward-geocoding responses from A.

## Limits

No national dataset was downloaded or built. The Rhode Island run qualifies the
bounded serving design, not nationwide latency, disk usage or source diversity.
The current source-preparation adapter still produces the deterministic regional
provider-record transport file before the streaming normalization stage; it is
not authoritative and is not loaded as a whole by the production builder. A
future national acquisition path should write provider records directly into the
same bounded staging interface instead of first producing that regional transport
file.
