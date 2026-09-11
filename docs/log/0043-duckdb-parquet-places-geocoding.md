# DuckDB and normalized Parquet for nationwide lookup

> **Historical note:** The compact-SQLite recommendation in this investigation
> was superseded by the bounded
> [DuckDB token-prefix proof](0045-duckdb-token-prefix-proof.md), which passed the
> regional autocomplete gate with a purpose-built DuckDB posting index.

| Field | Value |
| --- | --- |
| Date | 2026-09-10 |
| Repository revision | `a90dd3e409566adaa488c1290b9d6a158dd8479f` (`main`, clean before the experiment) |
| Host | Apple M5, 10 logical CPUs, 16 GiB RAM, macOS 26.4.1 arm64 |
| Go / SQLite | Go 1.26.1; SQLite CLI 3.51.0 |
| DuckDB | CLI v1.5.5, commit `d8cdaa33fd`; Go client `github.com/duckdb/duckdb-go/v2` v2.10505.0 |
| DuckDB extensions | `fts` `6814ec9`; `spatial` `eb1e57c`; `httpfs` `827222f`; `sqlite_scanner` `f79b1db` |
| New local-data cap | 20 GiB; peak retained experiment directory was 3.8 GiB |

## Recommendation

The recommended target is **normalized immutable Parquet plus a compact SQLite
serving index**, but the server should **not migrate yet**. DuckDB should be rejected
as a runtime dependency for this workload. It may remain an optional build and audit
tool while a production artifact writer continues to use Go libraries.

The storage result is strong enough to justify one smaller follow-on. On the
704,693-entity Rhode Island-envelope prototype, a current-style SQLite snapshot was
2.117 GB. The normalized Parquet base plus compact SQLite index was 482 MB, a 77%
reduction. The equivalent DuckDB indexed layout was 412 MB, but that 70 MB advantage
did not compensate for an incompatible autocomplete access pattern, CGO and
cross-compilation costs, version-coupled extensions, and index-memory risk.

None of the nationwide candidates is ready to serve. Both Parquet-only and indexed
DuckDB missed the provisional autocomplete target on only 704,693 records. Compact
SQLite preserved prefix semantics, but a common two-character prefix had 166 ms warm
p95 and the mixed four-request workload had 1.07 s p95. The current Newport service
remains the implementation to retain until a compact prefix-ranking design passes the
same correctness and concurrency workload.

The smallest justified next implementation step is a maintained, non-default Newport
builder that emits two artifacts:

1. one checksum-addressed Parquet base containing entities, complete source records,
   attribute provenance, relationships, rejections, release identity, and attribution;
2. one SQLite index containing FTS5 search documents, exact-address keys, a bounded
   spatial locator, and `public_id -> Parquet shard/row-group` locators, with no complete
   raw source JSON.

That step should use the existing Go Parquet and SQLite dependencies, not DuckDB in the
server. It should first reproduce all maintained Newport API fixtures, then rerun the
Rhode Island common-prefix and four-request workload. A production migration is only
justified if that result resolves the short-prefix tail without a national-size
precomputed projection.

## Why this is the relevant boundary

The current importer groups all normalized source records in Go maps, constructs a
monolithic JSON bundle, then writes four classes of duplicated data into SQLite:
normalized entities, full source records, attribute-provenance paths, and search
indexes. The current schema also carries relationship integrity and stable IDs. The
Places store performs SQLite primary-key and FTS5 queries, while geocoding loads every
address and its parsed source evidence into Go memory at startup and performs a full
address scan for reverse geocoding. These are useful Newport choices, but neither the
bundle construction nor the geocoder's in-memory scan can grow to national cardinality.

DuckDB is designed to push filters and projections into Parquet and to skip row groups
using zonemaps.[^1] Ordering therefore makes exact lookup, provenance fetch, and spatial
cell filtering plausible without expanding source data. ART indexes are intended for
very selective point queries, but must fit in memory when created and duplicate indexed
data.[^2] R-tree buffers are loaded lazily but are not evicted until the index is
dropped.[^3] These properties make DuckDB worth measuring, not a presumptive fit.

## Prototype scope and acquisition

Two scopes were used:

- The existing Newport snapshot supplied the correctness oracle: 12,343 entities and
  source records, 1,184 relationships, 39,435 winning-attribute provenance rows, and
  the maintained Places and geocoding fixtures.
- The larger envelope was longitude `[-71.8620,-71.1200]`, latitude
  `[41.1460,42.0190]`. It covers Rhode Island and Providence, while intentionally being
  described as an envelope rather than a political boundary. Exact point predicates
  were applied after catalog/bounding-box pruning; road segments used
  `ST_Intersects` with the closed envelope.

The pinned `2026-08-19.0` catalog selected one Parquet asset for each relevant kind.
Overture documents that release and direct Parquet access as the current address-data
interface, and also warns that address IDs do not yet have a stable GERS matcher.[^4]
The local catalog SHA-256 was
`35ebf80fa9ae679ce353d7b8a58a95c1522471aec093967614fb1bc96254c71d`.

| Kind | Rows in selected upstream asset | Row groups | Asset bytes | S3 ETag |
| --- | ---: | ---: | ---: | --- |
| address | 14,790,752 | 256 | 645,316,034 | `af61d5b48d5cc7106fd3da7364a0393f-10` |
| division | 4,658,700 | 256 | 578,060,705 | `56a4893e0eb9e8a69ab63794194ebc26-9` |
| place | 4,629,273 | 256 | 692,217,497 | `4555dfb459773b64556f4f1d245b0a22-11` |
| segment | 2,685,964 | 128 | 594,580,348 | `a10dc4e63c94473436c866815bd25527-9` |
| **Logical selected assets** |  |  | **2,510,174,584** |  |

DuckDB `httpfs` issued range requests against those immutable URLs. Nothing resembling
a whole upstream asset was saved locally. The experiment did not instrument actual
wire bytes, so 2.51 GB is reported as the conservative logical input size. It remains
well below the 20 GiB cap even if every selected asset had been transferred in full.

The exact filtered result was 553,029 addresses, 67,716 businesses, 83,379 named road
segments, and 569 direct division points: 704,693 records total. The division prototype
did not recursively add out-of-envelope parents, and the road prototype used DuckDB's
line midpoint rather than the production clipped, spherical-length midpoint. Those two
choices make the larger dataset suitable for storage and query scaling, not for API
correctness. Newport retained the exact current records and relationships.

## Normalized Parquet layout

Each base row retained the stable Open Maps ID, source-qualified ID, release, entity
kind, normalized serving attributes, attribution, complete source row as JSON,
attribute paths, a deterministic 0.1-degree cell, and an ID prefix. IDs were computed
as the existing `om_` plus the first 16 bytes of SHA-256 over
`openmaps:entity:v1:<source-key>`. Source records were streamed from remote Parquet
through `COPY`; no Go slice or map held the extraction.

The bounded prototype used one base file per kind with 32,768-row groups. Search used
one 32,768-row-group file, and the address spatial projection used 16,384-row groups.
Two deliberately more fragmented variants tested 16 one-hex-digit ID partitions and
64 populated 0.1-degree address-cell partitions. At national scale the intended shard
target would be approximately 256–512 MiB per file with 32,768–131,072 rows per group,
adjusted after real national metadata measurements. DuckDB's own guidance favors
moderate files, multiple row groups, and ordering by selective filter columns.[^5]

No H3, S2, or similar dependency was introduced. The simple deterministic grid was
sufficient for the experiment.

| Base kind | Records | Row groups | Bytes | Bytes / record |
| --- | ---: | ---: | ---: | ---: |
| address | 553,029 | 17 | 101,760,309 | 184.01 |
| area | 569 | 1 | 135,117 | 237.46 |
| business | 67,716 | 3 | 22,376,456 | 330.45 |
| street | 83,379 | 3 | 28,703,348 | 344.25 |
| **Base total** | **704,693** | **24** | **162,975,230** | **231.27 overall** |

| Projection | Records | Files | Row groups | Bytes |
| --- | ---: | ---: | ---: | ---: |
| one-hex ID partition | 704,693 | 16 | 32 | 41,098,339 |
| normalized search | 704,693 | 1 | 22 | 29,739,583 |
| ordered address spatial | 553,029 | 1 | 34 | 15,680,130 |
| cell-partitioned address spatial alternative | 553,029 | 64 | 80 | 16,199,335 |

The practical Parquet-only serving layout used the base, search projection, and one
spatial projection: 208,394,943 bytes. The ID projection did not earn its 41 MB of
duplication because exact lookup against the ordered base was already below 2 ms warm
p95. Cell partitioning reduced candidate rows but did not improve latency at this
shard count.

The manifest listed each Parquet shard by SHA-256 in sorted path order. Its own digest
was `cada8aef3804c2eb1d01ecb3596169992143942411608a6235de4d87c0d790da`.
The four base hashes were:

| Kind | SHA-256 |
| --- | --- |
| address | `c239a16409f9b3bac19abe485e4f21ebae80b8a1114e17fab386bf1ce2d04b74` |
| area | `b2ef9ab500b067f2199e7245c904912fb95fef3d24e528fabb050f0ace185948` |
| business | `20ab937fc1932f4d4176d0a5902b7937d5e253e0e39695217336f6c99afcf338` |
| street | `f1d6a6d4ad4dafa69c88f906865e4c9da1673cddcf8bc21c4acf91f92aabf4e3` |

## Build measurements

All builds used four threads. The Parquet build had a 4 GiB DuckDB memory limit.
Elapsed and RSS are `/usr/bin/time -l` measurements. “Peak temporary disk” is the
largest observed output plus DuckDB spill directory during sampling; no DuckDB spill
file remained. SQLite `VACUUM` temporary bytes were not separately observable and are
therefore called out as an uncertainty rather than reported as zero.

| Build | Output | Elapsed | Peak RSS | Peak observed output + spill |
| --- | ---: | ---: | ---: | ---: |
| Newport Parquet, all tables/projections | 4.32 MB | 0.16 s | 107 MB | 4.32 MB |
| RI normalized base + projections | 260 MB | 15.06 s | 2.48 GB | 259 MiB |
| DuckDB materialized projections + indexes | 248.52 MB | 2.48 s | 604 MB | 248.52 MB |
| compact SQLite index | 319.26 MB | 4.98 s | 210 MB | at least 319.26 MB |
| current-style RI SQLite | 2.117 GB | 20.62 s total | 1.35 GB max | at least 2.117 GB; `VACUUM` extra unmeasured |

The current-style RI database contains normalized entity rows, FTS5, complete raw
source JSON, duplicated normalized attributes and provenance paths, provenance rows,
and the serving address projections. Its largest allocations were 1.213 GB for
`source_records`, 266 MB for `attribute_provenance`, 175 MB for entities, and 139 MB
for provenance/identity indexes. The comparison is intentionally close to the current
duplication pattern, but the larger prototype did not build business-address or
area-parent relationships.

DuckDB index construction, staged after materialization, showed where its 248 MB came
from:

| Stage | Stage elapsed | Database bytes after checkpoint | Increment |
| --- | ---: | ---: | ---: |
| materialized tables only | 0.98 s | 125,054,976 | 125,054,976 |
| entity-ID and address-key ART | 0.32 s | 206,581,760 | 81,526,784 |
| FTS materialization | 1.23 s | 234,631,168 | 28,049,408 |
| address R-tree | 0.17 s | 247,476,224 | 12,845,056 |

The combined 604 MB peak at this scale is not a national guarantee. DuckDB explicitly
requires an ART to fit in memory during construction,[^2] and its index buffers are not
currently evicted by the buffer manager.[^6] Linear scaling of the observed ART bytes
alone is about 17 GiB; build memory could exceed 32 GiB before the other projections
are considered. That risk is unnecessary because Parquet details and exact-address
lookups were already fast without ART.

## Benchmark method

The repeated workload used the official DuckDB Go client through `database/sql`, one
thread per DuckDB connection, up to four connections, and the existing modernc SQLite
driver. Each aggregate class contained 100 deterministic iterations. The concurrency
run divided the same 100 operations across four simultaneous goroutines. Every query
consumed its rows. A five-second context timeout counted as an error; no measured query
errored.

Autocomplete cases covered `ma`, `white horse`, `main street`, `providence`,
`50 bellevue`, `dunkin`, `rhode island`, a missing two-token query, and two Providence-
biased cases. This includes short/long, common/rare, multi-token, business, address,
street, area, missing, and biased shapes. Location bias is experimental here because
the maintained API subset does not currently accept it.

Details used eight deterministic IDs distributed across ID prefixes plus one missing
ID. Forward geocoding used unique, common/ambiguous, and missing normalized address
keys. Reverse cases represented Providence density, southwestern sparsity, an empty
area, and a 0.1-degree cell boundary. Provenance fetched and measured one complete raw
source row plus its attribute paths by public ID.

“Cold” means a new process and fresh database connection; the OS page cache was not
purged. Five process starts were measured and the median reported. Warm values are
wall-clock percentiles from the repeated workload. These numbers are comparative local
measurements, not service-level objectives.

## Latency results

| Architecture / operation | Warm p50 | Warm p95 | Warm p99 | Four-request p95 |
| --- | ---: | ---: | ---: | ---: |
| Parquet-only details | 1.42 ms | 1.81 ms | 1.99 ms | 2.24 ms |
| Parquet-only autocomplete | 157.79 ms | 173.92 ms | 174.81 ms | 192.05 ms |
| Parquet-only exact forward | 1.83 ms | 1.94 ms | 2.15 ms | 2.66 ms |
| Parquet-only reverse candidates | 1.20 ms | 1.27 ms | 1.32 ms | 1.68 ms |
| Parquet-only provenance | 6.68 ms | 7.82 ms | 9.17 ms | 10.16 ms |
| Parquet + DuckDB indexes details | 0.06 ms | 0.81 ms | 1.01 ms | 0.20 ms |
| Parquet + DuckDB indexes autocomplete | 129.02 ms | 132.35 ms | 138.85 ms | 147.85 ms |
| Parquet + DuckDB indexes exact forward | 0.10 ms | 5.12 ms | 5.91 ms | 7.20 ms |
| Parquet + DuckDB indexes reverse | 0.22 ms | 0.50 ms | 1.39 ms | 0.73 ms |
| Parquet + DuckDB indexes provenance | 6.58 ms | 8.19 ms | 9.27 ms | 10.17 ms |
| Parquet + compact SQLite details | 1.41 ms | 1.53 ms | 1.65 ms | 2.11 ms |
| Parquet + compact SQLite autocomplete | 5.49 ms | 182.20 ms | 182.95 ms | 1,071.03 ms |
| Parquet + compact SQLite exact forward | 0.01 ms | 0.25 ms | 0.73 ms | 0.30 ms |
| Parquet + compact SQLite reverse | 1.79 ms | 1.88 ms | 2.56 ms | 1.96 ms |
| Parquet + compact SQLite provenance | 7.75 ms | 8.85 ms | 11.01 ms | 11.66 ms |

The provisional targets were met for details, exact forward geocoding, reverse
geocoding, provenance, startup, and ordinary four-request reads. Autocomplete was the
deciding failure.

| Autocomplete case | Parquet-only p95 | DuckDB table-scan p95 | Compact SQLite FTS5 p95 |
| --- | ---: | ---: | ---: |
| `ma` | 10.26 ms | 24.63 ms | 166.41 ms |
| `white horse` | 163.95 ms | 129.75 ms | 0.38 ms |
| `main street` | 10.09 ms | 25.12 ms | 48.30 ms |
| `providence` | 156.71 ms | 129.02 ms | 21.93 ms |
| `50 bellevue` | 161.28 ms | 129.59 ms | 0.69 ms |
| `dunkin` | 147.54 ms | 116.79 ms | 0.40 ms |
| `rhode island` | 158.08 ms | 129.70 ms | 4.42 ms |
| missing two-token query | 160.04 ms | 128.64 ms | 0.07 ms |
| `ma`, Providence bias | 168.69 ms | 130.03 ms | 185.23 ms |
| `main street`, Providence bias | 175.09 ms | 132.86 ms | 49.34 ms |

Parquet's surprisingly fast `ma` and `main street` cases came from ordered input,
Top-N dynamic filtering, and early useful matches. Rare, missing, and biased queries
had to inspect the complete search projection. That is the wrong scaling direction for
global autocomplete.

SQLite FTS5 was excellent for selective prefix searches. The common two-character
prefix still produced enough matches that deterministic BM25 and tie-breaking required
a large temporary ordering step. SQLite documents that prefix indexes are separate
structures, and that prefix searches outside configured lengths become token-range
scans.[^7] The prototype used the current `prefix='2 3 4'` configuration, so the
problem is result cardinality and ranking rather than an absent two-character index.

The existing Newport implementation remains fast: autocomplete was 3.91 ms warm p95
single-request and 13.43 ms at four requests; details was 0.02/0.08 ms; forward was
below the timer's 0.01 ms resolution; reverse was 0.60/0.71 ms. Its startup cost was
1.73 ms for Places plus 87.95 ms to load 8,545 geocoding addresses, and its first
autocomplete was 0.36 ms.

Scaling the current geocoder shape to the 553,029-address prototype required 458 ms to
load, 122.6 MB live Go heap (178.2 MB process RSS), and a full reverse scan took
33.5 ms p95 single-request and 37.2 ms with four requests. Linear address-count scaling
would approach 28 GB of heap and roughly 7.6 seconds per reverse request at 125.8
million addresses. Regardless of exact constants, this violates the requirement that
memory not grow with the complete national record count.

## Startup, plans, and pruning

| Architecture | Median open | Median ready | First details, median / max |
| --- | ---: | ---: | ---: |
| Parquet-only DuckDB process | 4.50 ms | 4.50 ms | 2.21 / 4.77 ms |
| DuckDB indexes with FTS + spatial loaded | 4.73 ms | 37.65 ms | 0.55 / 1.81 ms |
| compact SQLite + Parquet DuckDB reader | 4.18 ms | 4.18 ms | 1.94 / 1.99 ms |

All are far below the provisional one-second cold-first-query target on a warm OS cache.
Full checksum verification of the 93 Newport and RI Parquet files took 0.46 s and 7.2
MB RSS.

`EXPLAIN ANALYZE` confirmed:

- DuckDB used an ART index scan for exact entity ID and exact address key, reading one
  indexed row in each case.
- DuckDB used `RTREE_INDEX_SCAN` for the constant-envelope reverse query. Thirteen rows
  reached the exact spatial filter. The profiler's scan counter reported the table's
  553,029 rows, so candidate cardinality—not that counter—describes R-tree pruning.
- DuckDB autocomplete used a sequential scan over all 704,693 search rows for a rare
  missing query. It took 153 ms in the profiled run.
- The Parquet rare autocomplete scan read all 704,693 rows and one search file. Exact
  forward lookup had one eligible 32,768-row group. The ordered spatial file had four
  eligible row groups (65,536 rows) before latitude/longitude filtering.
- Exact public-ID/provenance lookup against the four kind files had four zonemap-eligible
  groups totaling 49,333 rows because a hashed ID falls within one ID range per kind.
  Projection pushdown and late raw-column fetch still kept it below 9 ms p95.
- One-hex ID partitioning reduced this to one file and one 32,768-row group, but its
  profiled cold latency was 2.55 ms versus 4.92 ms for the unpartitioned base. The 41 MB
  duplicated projection was not warranted.
- Cell partitioning read three files and 20,580 rows for the representative boundary
  query, versus one file/four eligible row groups in the ordered layout. Both profiled
  at about 2.8 ms; 64 small files supplied no practical gain.
- SQLite used its FTS5 virtual index plus rowid lookup for autocomplete, B-tree index
  for exact address, and `(cell_x,cell_y)` index for reverse candidates. The ranking
  queries still required a temporary B-tree.

DuckDB FTS did not solve autocomplete. `PRAGMA create_fts_index` rejected an external
Parquet view with `external_search is not an table`, so the complete search projection
had to be materialized. The extension's documented interface is token BM25 over a
materialized corpus and must be rebuilt after changes.[^8] In the prototype `white`
matched 923 documents, while `whit` and `whit*` matched zero; `main street` behaved as
a broad token query rather than the API's conjunctive token-prefix query. Exact-token
FTS had 79.79 ms p95 single-request and 97.51 ms at four requests, but those timings do
not represent correct autocomplete semantics.

## Correctness and integrity

Newport was exported losslessly. Bidirectional `EXCEPT` checks found zero entity
differences. Source records, attribute provenance, and relationships also had zero rows
missing from Parquet. Rebuilding the same Newport entity file produced the same byte
SHA-256,
`9bef2dcd31669f589d3bbf93c4ec944a28de3ac6d16c316369ede8f198b6939b`.

A compact Newport SQLite copy with full source/provenance tables removed remained
6.04 MB. Together with its 4.32 MB Parquet artifact, it occupied 10.36 MB versus the
35.95 MB current SQLite database. All eight maintained autocomplete cases and every
returned details entity were byte-for-byte equivalent through the existing Places
store.

The existing 26-case Newport geocoding suite passed for both configured test openings,
including unique and ambiguous addresses, unsupported units/ranges, malformed forms,
100-metre reverse behavior, no-match points, and coverage boundaries. This proves that
the production behavior was unchanged. A Parquet-backed geocoder was deliberately not
wired into the server, so this experiment does **not** claim that the larger Parquet
adapter reproduces source component parsing, context handling, or the clipped street
midpoint. Those remain gates for the recommended follow-on.

Across the RI base, all 704,693 IDs were distinct, all matched the stable source-key
formula, and every row had raw source and provenance content. Counts were identical
across ID partitioning. The prototype did not exercise multi-source identity mappings,
rejection records, parent closure, or business-address relationship construction on the
larger envelope. Parquet cannot enforce uniqueness or foreign keys, so a production
manifest verifier must run the same explicit duplicate, stable-ID, kind, relationship,
and provenance checks now enforced by SQLite and `ReadSnapshot`.

## Snapshot and failure behavior

The current selection code already provides the right publication semantics: checksum
before open, atomic synced selection-file replacement, concurrent request leases,
open-new-before-swap, retention of the old snapshot during active reads, rollback, and
degraded health on reload failure. Its activation, rollback, geocoding-switch, and
concurrent-lease tests passed in the repository test suite.

A Parquet snapshot should adapt that mechanism to select a manifest and immutable
directory, not individual globs. Verification must happen before a reader is published.
The prototype manifest verified in 0.46 s. DuckDB produced explicit errors for a
missing shard (`No files found that match the pattern`) and a one-kilobyte truncated
shard (`No magic bytes found at end of file`). A checksum verifier would reject either
before query startup.

Four concurrent read-only connections completed every measured workload without
errors. DuckDB documents multiple read-only connections/processes as supported, while
keeping writes within one process.[^9] Snapshot replacement itself was not implemented
for Parquet. Safe replacement would construct a new reader/catalog over absolute
manifest paths, verify it, swap it under the existing lease, and retain the old
directory until all requests release it. Directly querying a mutable symlink glob would
not provide that guarantee.

## Deployment implications

The official Go client uses `database/sql`.[^10] In this experiment a current Open Maps
server binary was 20,847,794 bytes; the benchmark binary linking both modernc SQLite
and DuckDB was 68,513,698 bytes, a 47.7 MB increase before extension files. The pinned
arm64 extension binaries added 5.2 MiB for FTS and 57 MiB for spatial; `httpfs` added
15 MiB for build-time remote reads.

`CGO_ENABLED=0 go build` failed because the Darwin arm64 DuckDB binding had no eligible
files. A Darwin-to-Linux arm64 CGO build failed with the host compiler/SDK; it requires
a target C/C++ toolchain or native builds. The Go driver's repository documents static
prebuilt libraries for common targets and the larger binary consequence of static
linking.[^11] This is manageable in a release pipeline, but it is new deployment work
with no demonstrated serving benefit.

Extensions are tightly coupled to the DuckDB version and platform.[^12] The pinned
v1.5.5 FTS and spatial binaries loaded successfully with automatic install and autoload
disabled, so offline deployment is possible if the signed binaries and their checksums
are shipped with every target. It is not automatic portability. The benchmark used
DuckDB v1.5.5, the latest stable release on the investigation date.[^13]

The simplest operational outcome is to keep DuckDB out of the Go server. Its CLI can
remain a reproducible audit/build instrument, while production Parquet output and reads
use the already present Go Parquet library and SQLite retains the access patterns it
handles better.

## Nationwide extrapolation

The point estimate scales the RI mix by `125,800,000 / 553,029 = 227.475`, preserving
the observed ratio of other kinds to addresses. This implies 160.3 million total
entities. It is intentionally mechanical: nationwide business, street, area,
multi-source, rejection, and relationship ratios will differ. Compression also changes
with source diversity and ordering.

| Layout | RI bytes / entity | Lookup point estimate | Plus 40 GiB routing + 20 GiB tiles |
| --- | ---: | ---: | ---: |
| Parquet base only | 231 | 34.5 GiB | 94.5 GiB |
| Parquet-only serving projections | 296 | 44.2 GiB | 104.2 GiB |
| Parquet + DuckDB indexed projections | 584 | 87.2 GiB | 147.2 GiB |
| Parquet + compact SQLite | 684 | 102.2 GiB | 162.2 GiB |
| current-style SQLite | 3,005 | 448.6 GiB | 508.6 GiB |

Reasonable planning bands are wider:

| Layout | Lookup uncertainty band | Total steady-state band | Main uncertainty |
| --- | ---: | ---: | --- |
| Parquet-only | 35–90 GiB | 95–150 GiB | extra projections and source diversity |
| Parquet + DuckDB indexes | 70–160 GiB | 130–220 GiB | materialized tables and ART/R-tree size |
| Parquet + compact SQLite | 80–190 GiB | 140–250 GiB | FTS/prefix projection and locator design |
| current-style SQLite | 300–800 GiB | 360–860 GiB | uncompressed raw JSON/provenance and indexes |

The Parquet plus compact SQLite point estimate validates the proposed 120–240 GiB
steady-state target as plausible, not guaranteed. Keeping an old and new lookup
generation plus SQLite build/VACUUM temporary space gives a central lookup-build need
around 300–400 GiB; adding routing, tiles, inputs, and safety margin makes the proposed
400–600 GiB build target plausible. Current-style SQLite would exceed that range before
a safe second generation and temporary copy were considered.

Parquet-only has the best footprint but fails the most important global query. DuckDB
indexes reduce point-query latency that was already adequate and introduce a national
index-build memory risk. Compact SQLite is larger than DuckDB at this bounded scale but
has the only compatible prefix index, preserves the existing operational model, avoids
CGO/extensions, and still fits the planning envelope. That is why it is the recommended
target despite the unresolved common-prefix tail.

## Reproduction outline

Disposable scripts and generated data were removed after measurements. The following
commands describe the exact toolchain and query shapes; all were run from repository
root.

```sh
# Pinned DuckDB CLI used for the experiment.
curl -fL \
  https://github.com/duckdb/duckdb/releases/download/v1.5.5/duckdb_cli-osx-arm64.gz \
  -o data/duckdb-experiment/bin/duckdb.gz
shasum -a 256 data/duckdb-experiment/bin/duckdb.gz
# Expected: 37144723eb43639a96b47207b44036e6dedc084f81afbf0c18625e7f3deac810

# Extensions were installed into a task-local absolute directory, then loaded
# with autoinstall_known_extensions=false and autoload_known_extensions=false.
data/duckdb-experiment/bin/duckdb -c \
  "SET extension_directory='$PWD/data/duckdb-experiment/extensions';
   INSTALL spatial; INSTALL fts; INSTALL httpfs; INSTALL sqlite;"

# Every remote input used both metadata pruning and an exact geometry predicate.
data/duckdb-experiment/bin/duckdb -c \
  "SET extension_directory='$PWD/data/duckdb-experiment/extensions';
   LOAD httpfs; LOAD spatial;
   SELECT count(*) FROM read_parquet('<pinned asset URL>')
   WHERE bbox.xmin < -71.1200 AND bbox.xmax > -71.8620
     AND bbox.ymin < 42.0190 AND bbox.ymax > 41.1460
     AND ST_X(geometry) BETWEEN -71.8620 AND -71.1200
     AND ST_Y(geometry) BETWEEN 41.1460 AND 42.0190;"

# Base writes used COPY ... ORDER BY id with Zstd and 32,768-row groups.
# Search used ORDER BY normalized_name,id. Spatial used ORDER BY
# cell_x,cell_y,lat,lng,id with 16,384-row groups. Partition tests used
# PARTITION_BY(id_bucket) and PARTITION_BY(cell_x,cell_y).

# Repeated benchmark and process/startup probes.
data/duckdb-experiment/bin/duckdb-bench \
  -root "$PWD/data/duckdb-experiment" -repeat 100
/usr/bin/time -l go run ./data/current-scale-bench \
  "$PWD/data/duckdb-experiment/ri/full-current-style.sqlite"

# Production behavior and repository verification.
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/openmaps.sqlite" \
  go test -tags=integration ./internal/importer \
  -run TestNewportGeocoding -count=1 -v
go test ./...
go vet ./...
```

The benchmark's DuckDB prefix predicate was conjunctive across normalized tokens:
`regexp_matches(' ' || normalized_name || ' ' || address || ' ' || aliases,
'(^| )' || token)`, followed by deterministic name/ID ordering and optional distance
ordering. SQLite used the current quoted-token `*` FTS5 expression, BM25 weights, and
deterministic ID tie-break. Exact lookup used `id = ?`; reverse first constrained a
100-metre latitude/longitude envelope and grid cells, then ordered candidates by
spherical or equivalent local distance.

## Limitations

- The larger scope is a Rhode Island envelope, not an exact state polygon. Providence
  is represented, but one region cannot model national language, density, and source
  diversity.
- Actual HTTP range-transfer bytes were not recorded. The full selected-asset size is
  reported as a conservative upper bound.
- The OS cache was not purged, so process-cold figures are not storage-device cold.
- SQLite `VACUUM` peak temporary space was not separately measured.
- The larger prototype did not implement parent closure, business-address links,
  relationship rows, rejection rows, multi-source conflicts, reviewed replacements,
  exact current street midpoint semantics, or the API adapter. Newport preserved and
  compared the existing versions of those data.
- Compact-SQLite details and provenance measurements used DuckDB to read the Parquet
  base. The recommended Go Parquet locator/reader remains to be implemented and
  benchmarked; these measurements do not establish its latency.
- Location bias is an experimental query shape, not currently supported API behavior.
- The compact SQLite prototype is intentionally not size-minimal: one 31.4 MB ID index
  was retained even though Parquet details were fast. A production candidate should
  remove it or replace it with a shard locator.
- National estimates scale one regional mix and should be replaced by metadata-only
  national counts and several bounded extractions before capacity purchase.
- Search relevance was checked for maintained Newport results and representative RI
  queries, not judged as a nationwide quality evaluation.

## Sources

[^1]: DuckDB. “[Reading and Writing Parquet Files](https://duckdb.org/docs/stable/data/parquet/overview).” Accessed 2026-09-10.
[^2]: DuckDB. “[Indexes](https://duckdb.org/docs/current/sql/indexes).” Accessed 2026-09-10.
[^3]: DuckDB. “[R-Tree Indexes](https://duckdb.org/docs/stable/core_extensions/spatial/r-tree_indexes.html).” Accessed 2026-09-10.
[^4]: Overture Maps Foundation. “[Addresses Overview](https://docs.overturemaps.org/guides/addresses/).” Updated 2026-08-19; accessed 2026-09-10.
[^5]: DuckDB. “[Parquet Tips](https://duckdb.org/docs/stable/data/parquet/tips).” Accessed 2026-09-10.
[^6]: DuckDB. “[Indexing](https://duckdb.org/docs/lts/guides/performance/indexing).” Accessed 2026-09-10.
[^7]: SQLite. “[SQLite FTS5 Extension](https://www.sqlite.org/fts5.html).” Accessed 2026-09-10.
[^8]: DuckDB. “[Full-Text Search](https://duckdb.org/docs/stable/guides/sql_features/full_text_search).” Accessed 2026-09-10.
[^9]: DuckDB. “[Concurrency](https://duckdb.org/docs/current/connect/concurrency).” Accessed 2026-09-10.
[^10]: DuckDB. “[Go Client](https://duckdb.org/docs/lts/clients/go).” Accessed 2026-09-10.
[^11]: DuckDB. “[duckdb-go README](https://github.com/duckdb/duckdb-go#linking-duckdb).” Accessed 2026-09-10.
[^12]: DuckDB. “[Installing Extensions](https://duckdb.org/docs/lts/extensions/installing_extensions).” Accessed 2026-09-10.
[^13]: DuckDB. “[Announcing DuckDB 1.5.5](https://duckdb.org/2026/07/22/announcing-duckdb-155).” 2026-07-22.
