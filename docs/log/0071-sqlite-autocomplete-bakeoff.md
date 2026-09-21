# SQLite FTS5/RTree Places Autocomplete bakeoff

**Date:** 2026-09-21–22

**Comparison baseline:** `74c3259d6dcb92e705ec9bdfc7b62fd820888a0a`

**Qualified adapter revision:** `4b0c69bb560e856a9a241d54b79bf54774087318`

**National builder revision:** `c6ebf0342c94c24ad760a57e0500d7e032003db4`

**Scope:** opt-in national Places Autocomplete candidate retrieval. The active
DuckDB selection, public demo, public port and production server were not
changed.

## Question and fixed gates

This experiment asks whether replacing only online DuckDB candidate retrieval
with SQLite FTS5 plus RTree can retain the established relevance set while
providing consistently subsecond national autocomplete at materially lower
operational cost than the completed OpenSearch experiment. The gates are the
ones fixed in the request and in the [OpenSearch historical comparison](0070-opensearch-autocomplete-bakeoff.md):

- retain every one of the 71 historical DuckDB passes, pass all maintained
  viewport cases, and resolve every returned ID through existing Details;
- keep warm concurrency-1 p95 below 200 ms, concurrency-16 p95 below 300 ms,
  concurrency-16 p99 below 750 ms, all ordinary requests below two seconds and
  each ten-minute phase below 0.1% errors;
- restore full adapter/database query readiness within five minutes;
- build within 12 hours, keep one serving generation below 150 GB and preserve
  the 60 GiB free-disk reserve with two generations; and
- avoid OOM, active swap thrashing, integrity failure and reserve violations.

A higher total relevance score does not compensate for an established
regression.

## Exact input and isolation

- Source generation:
  `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Expected entities: 165,759,144
- Experiment directory:
  `/srv/openmaps/data/experiments/sqlite-autocomplete-20260921-74c3259`
- Experimental HTTP ports: 18091–18094 on loopback only, used sequentially for
  adapter revisions. The public demo remained on port 8080 and the exact source
  artifact.

Preflight reconfirmed the manifest checksum and count, 64 GiB RAM, 8 GiB swap,
308,819,677,184 free bytes, the sole active Open Maps demo process, unused
experimental ports and a healthy public `/healthz`. No system service, kernel,
firewall, package repository, public process or host-wide cache state was
changed. The build and all qualification work ran below the bounded supervisor
with a 60 GiB disk reserve.

## SQLite and Go identity

The experiment adds the pure-Go `modernc.org/sqlite` driver at v1.45.0. The Go
module tag resolves to Git commit
`b8975b7dcf269f7c09929073c1feb64701066f41`. Its Linux/amd64 build embeds SQLite
3.51.2 with source ID
`2026-01-09 17:27:48 b270f8339eb13b504d0b2ba154ebca966b7dde08e40c3ed7d559749818cb2075`.
Runtime `PRAGMA compile_options` confirmed `ENABLE_FTS5` and `ENABLE_RTREE`.
This makes the SQLite library and extensions part of the pinned Go build rather
than depending on the host CLI or shared library.

The design follows current official SQLite documentation for
[FTS5](https://www.sqlite.org/fts5.html),
[RTree](https://www.sqlite.org/rtree.html), and
[compile options](https://www.sqlite.org/compile.html). The current upstream
release was reviewed, but the experiment deliberately records and qualifies
the older exact library embedded by the pinned Go driver instead of implying
that host or current-upstream version was used.

## Serving layout and construction

The finalized layout is a small fixed set of four SQLite databases. Entity
Parquet shards are assigned deterministically round-robin to one writer and
database. Four was chosen as the smallest layout that uses bounded parallel
construction and avoids a single SQLite writer while giving concurrent reads
independent connections. It is an experiment-specific fixed choice, not a
general sharding framework. The national build and load measurements below are
the deciding evidence; no production choice follows merely from the fixture
measurement.

Each database contains an ordinary typed `entities` table, a contentless FTS5
table, an RTree and a bounded one/two-character prefix head. Public `om_` IDs
are unique text values in `entities`; SQLite row IDs are private per-shard
join keys and are never exposed. The source Parquet generation remains the
authority for complete entities, source records, provenance and Details.

FTS5 indexes normalized primary name, aliases, address and hierarchy using
`unicode61 remove_diacritics 2` with two-, three- and four-character prefix
indexes. Contentless storage avoids a second copy of those strings while
retaining token-size data for BM25. Completed query tokens are exact phrases;
only the final token receives `*`. The final adapter uses bounded typed-name
lookups first, including exact primary names and leading-`The` variants. It
uses FTS when primary-name retrieval has no candidates. Each SQL branch has a
fixed result cap; the adapter returns at most five materialized Details
entities. RTree stores every entity point and supplies a separate viewport
candidate pool; global candidates are always retained. This is the measured
revision, following four adapter-only investigations; the national database
was never rebuilt or its expectations changed.

Fuzzy fallback runs only when strict retrieval returns zero results.
It queries the FTS5 vocabulary by the misspelled token's first two runes, reads
at most 256 vocabulary terms per shard, applies edit distance at most two to
that bounded set, keeps at most eight alternatives per token and reissues a
bounded FTS query. It never scans entity rows or adds edit-distance work to the
ordinary strict path.

Construction uses WAL with `synchronous=FULL`, explicit 10,000-entity
transactions, `temp_store=FILE`, a 256 MiB cache per writer, disabled mmap and
10,000-page automatic checkpoints. Finalization runs the FTS5 integrity
command, `PRAGMA integrity_check`, `PRAGMA optimize`, truncates WAL and switches
to the delete journal before hashing. Serving opens the finalized hashes with
`mode=ro`, `immutable=1`, `query_only=ON`, a 64 MiB cache per connection,
in-memory temporary storage and disabled mmap. Unsafe durability settings and a
space-amplifying `VACUUM` were not used.

The builder refuses an existing output. It verifies the entire source
generation before construction, creates `INCOMPLETE.json`, streams Parquet and
source spans without intermediate JSON, and removes that marker only after
attempted, accepted, source and final counts agree exactly and all integrity
checks pass. A finalized manifest binds the source checksum, database hashes,
row counts, logical/allocated sizes, SQLite identity, compile options, pragmas
and resource counters.

## Deterministic coverage

Generated fixtures exercise exact primary names, completed and final-prefix
tokens, aliases, repeated names, ambiguous areas, viewport bias, a nearby
candidate outside the global top five, an exact global primary match over a
weaker local alias, closed places, stable IDs and ties, bounded fuzzy fallback,
incomplete-generation rejection, exact count reconciliation, SQLite/FTS
integrity, query plans and Details materialization for every returned result.

## Query plans

The retained `query-inspection-v5.json` includes the exact plans. Representative
plan lines are:

| Query | National plan |
| --- | --- |
| Exact primary name | `SEARCH entities USING COVERING INDEX entities_exact_name (normalized_name=?)` |
| Contextual area | `SEARCH entities USING COVERING INDEX entities_exact_name (normalized_name=? AND kind=? AND closed=?)` |
| FTS prefix fallback | `SCAN entity_fts VIRTUAL TABLE INDEX 0:M4`; row-ID lookup in `entities` |
| FTS viewport fallback | `SCAN r VIRTUAL TABLE INDEX 2:D3B2D1B0`; row-ID lookup; FTS match |

The viewport plan above is the FTS fallback, not the final adapter's primary
viewport path, which starts from `entities_exact_name` and joins the RTree by
row ID. The latter removed the earlier viewport timeouts but did not preserve
the established result IDs. `dbstat` inspection of all four shards took
40m12s including manifest-bound hash verification and a full page walk.

Each shard has a unique public-ID index and `entities_exact_name`; there is no
second explicit public-ID index. Final read-only pragmas were observed as
`journal_mode=delete`, `query_only=1`, `temp_store=2` (memory),
`cache_size=-65536` (KiB), and `mmap_size=0`. Pages are 4,096 bytes.

## National construction

The builder consumed **165,759,144** source entities: attempted, accepted and
final counts were each **165,759,144**, with **zero rejected and zero retried**
across 16,609 explicit batches. The FTS5 integrity command and SQLite
`integrity_check` returned `ok` on every shard. The incomplete marker was
removed only after final count reconciliation; the manifest binds the four
database hashes and source manifest checksum. A subsequent local test found
and closed the crash window in which both a manifest and an incomplete marker
could exist: serving now rejects that combination.

The supervised command completed in **8h58m32s**, including **1h36m23s** of
sequential finalization. It passed the 12-hour gate. The build report's own
elapsed field was sampled before final checksum/write completion and is about
8 minutes shorter; the external supervisor time is used for this gate.

| Storage component | Bytes | Decimal GB |
| --- | ---: | ---: |
| Four main SQLite databases, logical | 97,194,438,656 | 97.19 |
| Allocated main files | 97,194,487,808 | 97.19 |
| FTS5 tables and shadow objects (`dbstat`) | 30,948,360,192 | 30.95 |
| RTree tables and shadow objects (`dbstat`) | 9,428,926,464 | 9.43 |
| Typed `entities` table (`dbstat`) | 40,268,546,048 | 40.27 |
| Combined WAL/journal/temporary high-water reported by builder | 1,644,077,888 | 1.64 |
| Final WAL and journal | 0 | 0 |

FTS and RTree are included in, not additional to, the main-file total.
Remaining pages contain typed indexes, prefix heads, SQLite metadata and free
pages. The builder revision did not sample WAL and journal high-water
separately; its combined auxiliary peak is the defensible bound. The external
supervisor's 99.23 GB “temporary path” peak includes the growing database
files and is not a separate spill allocation. It observed **7.00 GB peak RSS**,
284.96 GB process reads, 4.58 TB cumulative process writes and 50,707 CPU
seconds. Those byte counters are cumulative I/O, not final storage. Minimum
free disk was **209.03 GB**, well above the 60 GiB reserve. The public demo
remained available during the build, demonstrating construction while its
previous generation was in service.

At qualification end, 210.55 GB remained free with one SQLite generation
present. A second copy of 97.19 GB would leave about **113.36 GB free** on
the same filesystem, above the 60 GiB reserve; this is a capacity calculation,
not a second measured build. Four shards provided bounded parallel writers,
but no one-shard national control was run, so the experiment does not prove
four is superior to one. The choice remains limited to this experiment.

## Relevance and Details

The final adapter revision `4b0c69b` was frozen before load qualification.
The exact benchmark driver, seed `1`, checks, shuffled request order, 2.5-second
HTTP client timeout and Details probes from the OpenSearch experiment were
reused without changing expectations. The final first-pass and warm runs each
passed **69/99** national checks and **0/6** maintained viewport checks.
Mechanical comparison against the retained exact 71 DuckDB pass names found
**64/71 retained** and seven regressions:

`Alcatraz Island`, `Beale Street, Memphis, TN`, `Gateway Arch`,
`Grand Canyon National Park`, `Hoover Dam`, `In-N-Out Los Angeles`, and
`Walt Disney World`.

Five former failures became passes: `00000`, `Broadway`, `Main Street`,
`Wall Street`, and `Yellowstone National Park`. The contextual subset was
**24/25** on both passes: Portland, Springfield and `Pennsylvania Avenue,
Washington, DC` passed, while `Beale Street, Memphis, TN` returned no result.
All **829/829** suggestion IDs in the relevance run and **844/844** in the
separate load run resolved through the unchanged Details endpoint.

The adapter-only iterations exposed three distinct limitations. An initial
FTS ranking query scanned broad prefixes and timed out. Typed primary-name
retrieval brought the suite from 19/99 to 43/99, then source-code and
coordinate-based contextual fixes brought it to 69/99. Geographic source rows
did not consistently carry a materialized parent-region string in this serving
projection, so contextual matching used the exact region entity and nearest
same-name locality, with a bounded nearby street pool. The final typed-name
viewport path removed timeouts but still picked different IDs in all six
maintained cases. No golden expectations or source entities were changed.

Final keystroke traces were mostly 70–500 ms. `ups st` timed out at the fixed
2.5-second client limit; `Pennsylvania Avenue, Washington, DC` returned one
suggestion in 69 ms; the synthetic no-result query returned none in 4 ms.
The separate first-pass suite had about 124/972 ms p50/p95; warm was about
66/291 ms. These are observed HTTP times from the final relevance report,
not SQL-only timings. Five ordinary requests in that run exceeded two seconds.

## Latency, load and restart

Each mixed HTTP load phase ran for ten minutes through the final Go adapter.
The driver sent the same query mix and counted each completed request, timeout
and latency in the same way as the retained OpenSearch driver. Its 2.5-second
client timeout caps the observed maximum near that value; timed-out server work
may continue after the client gives up.

| Concurrency | Requests | p50 | p95 | p99 | Max | Requests/s | Errors | >2 s |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 6,150 | 66 ms | 239 ms | 624 ms | 2,502 ms | 10.25 | 46 (0.748%) | 46 |
| 8 | 15,737 | 192 ms | 982 ms | 2,500 ms | 2,504 ms | 26.23 | 169 (1.074%) | 351 |
| 16 | 16,413 | 436 ms | 1,600 ms | 2,501 ms | 2,505 ms | 27.35 | 389 (2.370%) | 521 |
| 32 | 17,266 | 951 ms | 2,501 ms | 2,501 ms | 2,505 ms | 28.78 | 1,480 (8.572%) | 2,553 |

Warm single-client p95, concurrency-16 p95/p99, the ordinary-request maximum
and every phase's error rate fail their provisional gates. Throughput plateaus
near 29 requests/s. The load supervisor recorded **7.01 GB peak RSS** across
the complete adapter lifetime, including startup validation; after load it
was about 1.51 GB and during load it was observed near 2.35 GB. It recorded
15,484 CPU seconds across startup and load. The host's sampled `vmstat` file
and pre/post memory snapshots are retained; one-second samples during load
showed zero swap-in and swap-out KiB/s. There was no OOM, integrity
failure or disk-reserve violation. Disabled mmap avoids mapping the full
97 GB index into a process, but host page cache and multi-terabyte build write
amplification remain operational concerns.

A clean final adapter/database process restart took **about 12m18s** from
launch to the log's query-ready line; the next recorded `Providence` HTTP
query succeeded with five suggestions in **308 ms**. The first-query probe was
issued after readiness, not in the same instant, so readiness is measured from
the logged line. Required verification of both the existing Details artifact
and the additional four SQLite hashes dominates startup. The five-minute
full-restart gate fails. No host-wide cache was dropped.

## Operational comparison

Retained OpenSearch machine reports were used directly; the historical DuckDB
figures are from the same artifact and host under a different cache state. No
new DuckDB control was run because it would compete with the public demo.

| Measure | SQLite FTS5/RTree | OpenSearch | DuckDB historical |
| --- | ---: | ---: | ---: |
| Indexed entities | 165,759,144 | 165,759,144 | 165,759,144 source entities |
| Construction | 8h58m32s | under 6h including merge | existing artifact |
| Serving index | 97.19 GB | 131.9 GB | part of existing ~110 GiB generation |
| National relevance | 69/99; seven historical regressions | 72/99; three historical regressions | 71/99 |
| Viewport | 0/6 | 2/6 | 6/6 |
| Details checks | 844/844 in load run | 1,040/1,040 | established serving path |
| Load c1 p95 | 239 ms | 191 ms | no directly comparable load phase |
| Load c16 p95 / p99 | 1,600 / 2,501 ms | 608 / 2,077 ms | no directly comparable load phase |
| Load c16 errors | 2.370% | 0.161% | no directly comparable load phase |
| Saturation | ~28.8 requests/s | ~44.5 requests/s | not measured comparably |
| Full adapter readiness | ~12m18s | 7m41s | ~7m28s |
| Engine memory | ~1–2.4 GB steady adapter; 7.01 GB observed startup peak | ~20–30 GB OpenSearch RSS, 16 GiB fixed heap | ~6.7 GiB startup validation; 19.6 GiB maximum during historical qualification |

The historical DuckDB 99-case HTTP suite took **252.14 seconds** with a
16.14-second maximum, and its separate cold contextual run took **87.72
seconds**. These runs used a different client timeout and cache condition and
should not be read as a simultaneous load comparison. SQLite is smaller and
uses substantially less steady process memory than OpenSearch, but because it
also needs the unchanged Details generation, the 97.19 GB is additional
storage. Lower storage/RSS did not translate into acceptable throughput or
latency on this host.

## Gate result and recommendation

| Gate | Result |
| --- | --- |
| Every historical 71 pass | **Fail:** 64/71 retained |
| Every returned ID resolves | **Pass:** 844/844 in load qualification |
| Maintained viewport suite | **Fail:** 0/6 |
| Warm c1 p95 <200 ms | **Fail:** 239 ms |
| c16 p95 <300 ms; p99 <750 ms | **Fail:** 1,600 / 2,501 ms |
| No ordinary request >2 s | **Fail:** 521 at c16 alone |
| Each phase errors <0.1% | **Fail:** 0.748–8.572% |
| Full restart <5m | **Fail:** ~12m18s |
| Build <12h and generation <150 GB | **Pass:** 8h58m32s, 97.19 GB |
| Two generations with 60 GiB reserve | **Pass by capacity:** about 113.36 GB would remain |
| OOM, active swap thrash, integrity/reserve failure | **Pass:** none observed |

SQLite candidate retrieval is technically feasible for all current public IDs:
the immutable full national catalog builds, verifies and resolves Details.
It does **not** preserve established relevance, and latency/concurrency gates
fail more substantially than OpenSearch on this host. Its lower storage and
steady RSS are real, but insufficient to justify replacing or supplementing
the active DuckDB candidate path. A further SQLite iteration would need a
measured redesign of high-frequency FTS fallback, stronger source-backed
landmark ranking and exact viewport semantics before another national build.
The current evidence does not justify activating this generation. Pelias
remains a separate later evaluation for parsing, OpenAddresses coverage and
interpolation, not a substitute for this candidate-retrieval bakeoff.

## Retained evidence

The finalized databases and all raw reports remain under the experiment path.
Key files are `sqlite-generation-v2/manifest.json`, `reports/build-v2.json`,
`reports/build-v2-resources.json`, `reports/qualification-v5-relevance.json`,
`reports/qualification-v5-load.json`, `reports/query-inspection-v5.json`,
`reports/adapter-v5c-resources.json`, the restart first-query response,
`reports/load-v5-vmstat.txt`, `reports/final-demo-health.json`, and
`reports/key-artifacts.sha256` (nine key artifact checksums). The manifest
contains the four database SHA-256 hashes; the raw OpenSearch comparison
reports remain under its existing experiment directory. All experimental
adapter processes were stopped, loopback ports 18091–18094 were closed,
210.55 GB was free and the public demo `/healthz` still referenced the original
source artifact. No SQLite generation was activated or published.
