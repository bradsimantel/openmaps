# OpenSearch Places Autocomplete bakeoff

**Date:** 2026-09-20 through 2026-09-21

**Implementation baseline:** `c72254dcac964a0343aea9ef2b3463d92a9211c4`

**Scope:** opt-in national Places Autocomplete candidate retrieval. The active
DuckDB selection, public demo, public port and production server were not
changed.

## Question and evaluation contract

This experiment asks whether replacing only the online DuckDB candidate search
with a text/geographic index can provide consistently subsecond national
autocomplete while retaining the normalized source data, `om_` public IDs,
Google API translation, existing Details implementation and maintained
relevance expectations.

The provisional gates were fixed before the national run:

- retain all 71 currently passing cases from the unchanged 99-case suite;
- pass all six maintained viewport cases and resolve every returned ID through
  Details;
- keep warm single-client p95 below 200 ms, concurrency-16 p95 below 300 ms,
  concurrency-16 p99 below 750 ms and every ordinary request below two seconds;
- keep each ten-minute load phase below 0.1% errors;
- restore query readiness within five minutes of an OpenSearch restart;
- build the full index within 12 hours and keep one serving index below 150 GB;
  and
- avoid OOM, swap thrashing and the 60 GiB free-disk reserve.

These are evaluation criteria, not permission to change golden expectations.

## Exact inputs and isolation

- National generation:
  `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Expected entities: 165,759,144
- National checks: `config/us-query-checks.json`, SHA-256
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- Viewport checks: `config/us-viewport-query-checks.json`, SHA-256
  `f587c6ad9abecb8d7d848072bff4800f5a3438e0f00892bc2cebfdfcc8ed149a`
- Experiment directory:
  `/srv/openmaps/data/experiments/opensearch-autocomplete-20260920-c72254d`
- Loopback ports: 19200 HTTP, 19201 transport and 18090 for the experimental
  Go adapter. The public demo remained on 8080.

The host had 64 GiB RAM, an existing 8 GiB swap file, more than 400 GiB free at
the start and `vm.max_map_count=1048576`. No kernel setting, firewall rule,
package repository or system service was changed. OpenSearch and the adapter
were direct experiment processes rather than installed services. OpenSearch
was bound to `127.0.0.1`, used one primary shard, no replicas and a fixed
`-Xms16g -Xmx16g` heap.

OpenSearch 3.8 also enabled Query Insights top-query collection by default and
created a 26 KiB `top_queries-*` index during integration probes. Before
qualification, its latency, CPU and memory collectors were disabled through
documented persistent cluster settings so the measured workload did not also
export query bodies and metrics. The already-created index was preserved.

OpenSearch 3.8.0 was obtained from the official
[versioned artifacts page](https://opensearch.org/artifacts/by-version/#release-v380)
as `opensearch-3.8.0-linux-x64.tar.gz`. Its exact download identity was
1,072,637,307 bytes and SHA-512
`cba25b10114e796273fa9399af27fe9c2daaf25a190c37e4b5feb9cfd088e371e0fbd3cddf2bc0fbb753c2e681c0b55f08c0926d11718bf76f77e830017b06ff`.
The archive was verified before extraction. A tar distribution was used because
the qualification host had no container runtime; this did not justify adding
one.

## Experimental implementation

`places-opensearch` is a separate `export`, `serve` and `benchmark` command. It
does not participate in normal server startup or lookup selection.

The exporter first applies the existing complete generation verification, then
streams entity and source Parquet shards through four bounded workers to the
OpenSearch Bulk API. Batches are limited by both 5,000 documents and 16 MiB.
There is no intermediate national JSON file. The `om_` public ID is the
OpenSearch `_id`. Only transport failures and HTTP/item statuses 429, 502, 503
and 504 are retried. Every read, accepted, rejected and retried item is counted;
success additionally requires both the exporter accounting and OpenSearch
`_count` to equal the manifest entity count.

The mapping is `dynamic: strict`, disables `_source`, and uses explicit keyword,
text, `search_as_you_type`, `geo_point`, boolean and numeric fields. It projects
kind/subtype, primary and normalized names, aliases, address and hierarchy
context, WGS84 location, closed state and the existing area/place ranking
evidence. Disabling `_source` was necessary to stay near the size gate. Location
needed by contextual lookup is read from the `geo_point` doc value; a real
OpenSearch integration test caught and rejected an earlier generic `fields`
request that OpenSearch 3.8 does not support when `_source` is disabled.

The query adapter preserves completed-token matching and treats only the final
token as a prefix. It gives exact primary names a large boost, uses aliases and
context, applies source-backed area/place evidence and excludes closed
entities. Analyzed viewport retrieval uses a soft Gaussian distance score. The
exact-street fast path first retrieves a radius-bounded local pool for speed,
then always retrieves the unfiltered global exact-name pool. It combines both
by score, retaining each local hit's distance boost while allowing a stronger
global hit to outrank it; the viewport therefore changes ordering without
excluding global candidates. A
bounded fuzzy query with `AUTO`, prefix length two and at most 20 expansions is
used only when strict retrieval returns fewer than five candidates. OpenSearch
returns at most 50 candidate IDs; the existing Details store materializes every
public result and the API returns at most five suggestions. The final query
uses source-backed type/prominence/destination weights and a stable `_doc` tie
within the immutable single-shard index. An earlier `_id` secondary sort was
rejected after real national requests showed that its field-data cost could
exceed 15 seconds. General analyzed matching is still used for prefixes,
aliases and context.

The experimental health endpoint performs a one-second bounded live OpenSearch
check. It therefore becomes unavailable while the search process is down
instead of reporting a static healthy state during restart measurement.

The benchmark sends the existing Google Places Autocomplete request through
the Go HTTP adapter. It records wall-clock HTTP latency, not OpenSearch `took`.
Details integrity is measured separately so load latency does not silently
include up to five extra Details requests per autocomplete.

Default tests use an HTTP fake and generated DuckDB/Parquet generation, not a
running OpenSearch process. Opt-in real-engine coverage creates and removes an
isolated process-specific index. Fixtures cover exact names, prefixes, aliases,
repeated names, ambiguous localities, viewport bias, a local candidate outside
the un-biased top five, closed places, stable ties and Details resolution.

## Build investigation and national result

Five immutable experimental index names were retained as evidence. The first
four were deliberately stopped after bounded samples established either
insufficient throughput or unacceptable projected size; none was reused as the
qualified index. The final mapping is `openmaps-places-autocomplete-v5`.

| Index | Change | Bounded outcome |
| --- | --- | --- |
| v1 | initial mapping, one exporter worker | stopped: about 5,000 documents/s |
| v2 | four workers | stopped: projected above 150 GB |
| v3 | combined analyzed field | stopped: full `_source` remained too large |
| v4 | restricted `_source` | stopped: still projected near/above the gate |
| v5 | `_source` disabled; geo-point doc values | complete: exact count, 4h31m33s |

The official Bulk API semantics and retry behavior were checked against the
[Bulk API documentation](https://docs.opensearch.org/latest/api-reference/document-apis/bulk/).
Mapping choices use the documented
[field types](https://docs.opensearch.org/latest/mappings/supported-field-types/index/)
and strict
[`dynamic` behavior](https://docs.opensearch.org/latest/mappings/mapping-parameters/dynamic/).

The final exporter read and accepted exactly **165,759,144** documents in
33,154 bulk batches, with zero rejected, retried or failed documents.
OpenSearch `_count` independently returned 165,759,144. The 4h31m33s exporter
duration includes the existing full artifact verification. The index became
write-blocked after completion.

The normal 57-segment merge-stable size was 151,590,617,953 bytes, narrowly
above the 150 GB gate. A post-write force merge to ten segments took 1h21m28s.
The resulting serving index is 131,887,008,079 bytes logically,
131,887,009,693 bytes apparent on the filesystem and 131,887,403,008 bytes
allocated. Full construction plus this merge remained below six hours. The
force merge followed the documented warning to run only after writes completed
and never approached the disk reserve. See the
[Force Merge API documentation](https://docs.opensearch.org/latest/api-reference/index-apis/force-merge/).

The minimum sampled free disk was 203,613,896,704 bytes (about 189.6 GiB).
Peak sampled OpenSearch RSS during export was 30,515,671,040 bytes; it was
21,923,565,568 bytes during load with a fixed 17,179,869,184-byte heap maximum.
The adapter peaked at 2,405,396,480 bytes RSS during load. Swap free was
constant at 8,552,738,816 bytes through export and varied by only 43 MB around
4.28 GB during the 40-minute load, so there was swap use but no observed active
thrashing. No OOM or OpenSearch error-level event was found.

During load, sampled average CPU was about 235% for OpenSearch and 375% for the
Go adapter on the eight-core host. The sampled `md2` device counters increased
by 29,282,304 read bytes and 49,184,768 write bytes; the workload was primarily
served from filesystem cache after the merge. With 307 GB free after
qualification, two 131.9 GB serving generations fit concurrently. Applying the
measured second-build and merge overhead would still leave roughly 95 GB, so
the 60 GiB rollback reserve also fits under the observed conditions.

## Relevance

The final randomized first-pass and warm runs were identical at **72/99**. A
final real-engine rerun after enforcing global fallback for viewport-biased
exact streets produced the same pass set, **2/6** viewport result and
**1,040/1,040** Details result. This does not satisfy the retention gate merely
because the total exceeds 71. A mechanical comparison with the exact historical
DuckDB pass names found three regressions (`California`, `Grand Canyon National
Park` and `Times Square`) and four new passes (`Main Street`, `Starbucks
Seattle`, `Wall Street` and `Yellowstone National Park`). Expectations were not
changed.

| Category | Historical DuckDB pass | OpenSearch pass | Total |
| --- | ---: | ---: | ---: |
| Raw city | 19 | 19 | 19 |
| State/federal district | 6 | 5 | 6 |
| City/state context | 16 | 16 | 16 |
| Raw ZIP | 0 | 0 | 10 |
| ZIP context | 0 | 0 | 5 |
| Raw street | 7 | 9 | 10 |
| Street context | 9 | 9 | 9 |
| Landmark/major place | 12 | 11 | 19 |
| Business context | 1 | 2 | 3 |
| Negative | 1 | 1 | 2 |
| **Total** | **71** | **72** | **99** |

The post-restart randomized contextual cases passed 25/25: city/state was
16/16 and street/city/state was 9/9. This includes Portland, Springfield and
`Pennsylvania Avenue, Washington, DC`. In the final soft-bias rerun, first-pass
national p50/p95/p99/max were 162.2/329.5/372.4/381.8 ms; randomized warm
values were 75.1/153.9/245.0/278.8 ms. The six viewport cases had a warm maximum
of 194.7 ms. The separately retained restart report was faster on its first
pass, but the conservative gate result does not depend on that difference.

Only **2/6** maintained viewport regressions passed. Wide Chicago `ups store`
and tiny Times Square passed. Tiny Chicago returned the expected first ID but
did not preserve complete distance order. New York `ups store` and New York/Los
Angeles `Main Street` chose plausible nearer entities rather than the exact
DuckDB-established IDs. This is a ranking-semantics failure, not missing source
data. Across the final run, all **1,040/1,040** returned suggestion IDs resolved
through the unchanged Details implementation.

The final keystroke trace was:

| Input | HTTP latency |
| --- | ---: |
| `u`, `up`, `ups` | 163, 420, 169 ms |
| `ups s`, `ups st`, `ups store` | 949, 234, 73 ms |
| `m`, `ma`, `main` | 165, 2,185, 182 ms |
| `main st`, `main street` | 157, 155 ms |
| In-N-Out Los Angeles | 79 ms |
| Pennsylvania Avenue, Washington, DC | 96 ms |
| Providence | 72 ms |
| Space Needle | 72 ms |
| synthetic no result | 16 ms |

The one-letter `ma` trace therefore violates the two-second ordinary-request
gate even though the maintained 99-case first/warm requests did not.

## Load and operations

Every phase below ran for ten minutes through the Go HTTP adapter. Latency does
not include separate Details integrity probes. The final soft-bias adjustment
only changes requests that contain a viewport and look like exact streets; the
load generator sends no viewport, so the retained load result exercises the
same code path as the final binary.

| Concurrency | Requests | p50 | p95 | p99 | Maximum | Throughput | Errors | >2s |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 5,518 | 71 ms | 191 ms | 847 ms | 2,160 ms | 9.20/s | 0 | 42 |
| 8 | 26,379 | 136 ms | 332 ms | 1,459 ms | 2,275 ms | 43.96/s | 0 | 206 |
| 16 | 26,696 | 309 ms | 608 ms | 2,077 ms | 2,459 ms | 44.49/s | 43 (0.161%) | 309 |
| 32 | 26,741 | 666 ms | 1,174 ms | 2,407 ms | 2,503 ms | 44.57/s | 263 (0.984%) | 399 |

The concurrency-16 p95, p99 and error-rate gates all fail. Throughput plateaus
near 44.5 requests/second between concurrency 8 and 32, showing host saturation
rather than useful scaling.

An OpenSearch-only restart, while the verified Details adapter stayed open,
restored its root endpoint in 20.4 seconds and the first successful adapter
query in 21.1 seconds; that query took 81.9 ms. A complete adapter process start
still performs the required 110 GiB generation verification and took 7m41s in
the final rerun (7m45s in the initial qualification).
Accordingly, an OpenSearch process restart passes five minutes, but a complete
stack restart does not.

The most recent exact DuckDB control on this same artifact and host remains the
historical 252.14-second 99-case HTTP run, with a 16.14-second slowest request,
and the separately labeled 87.72-second cold contextual run. A new full DuckDB
control was not run against port 8080 because it would compete with the active
public demo; stopping that demo or claiming an uncontended comparison was not
permitted. The comparison is therefore labeled historical, not presented as a
simultaneous warm control.

## Gate result and recommendation

| Gate | Result |
| --- | --- |
| Retain every historical 71 pass | **Fail:** three regressions, despite 72 total passes |
| Details integrity | **Pass:** 1,040/1,040 |
| Six viewport regressions | **Fail:** 2/6 |
| Warm single-client p95 <200 ms | **Pass:** 191 ms under ten-minute load; 154 ms in final suite |
| Concurrency-16 p95 <300 ms | **Fail:** 608 ms |
| Concurrency-16 p99 <750 ms | **Fail:** 2,077 ms |
| No ordinary request >2s | **Fail:** keystroke and load outliers |
| Ten-minute error rate <0.1% | **Fail at c16/c32:** 0.161% / 0.984% |
| Restart to query <5m | **Pass for OpenSearch only:** 21.1s; **fail for full stack:** 7m41s |
| Construction <12h | **Pass:** under six hours including post-write merge |
| Serving index <150 GB | **Pass:** 131.9 GB after merge; pre-merge was 151.6 GB |
| No OOM, swap thrash or disk violation | **Pass:** none observed; minimum free 189.6 GiB |

OpenSearch candidate retrieval is technically successful in the narrow sense:
the exact national projection builds reproducibly, stable IDs and Details are
preserved, single-client warm p95 is sub-200 ms, restart is fast and the final
index fits the size limit. It is **not suitable for activation** because the
fixed relevance/viewport contract is not preserved and concurrency-16 latency
and reliability miss the gates.

The operating cost is material: a 16 GiB heap, roughly 20-30 GB process RSS,
131.9 GB per merged generation and an additional 81-minute merge. The host can
retain two generations, but the measured throughput plateau leaves little
headroom for production traffic. Those costs are not acceptable for this
result quality.

A national SQLite FTS5/RTree comparison remains warranted. It should use this
same projection, exact pass-set comparison and HTTP load contract so lower
operational complexity is not purchased by weakening relevance. No SQLite
choice is established by this record.

Pelias remains a separate possible future evaluation for parsing,
OpenAddresses coverage and interpolation. It was not evaluated here and should
not be inferred from this candidate-retrieval bakeoff.

## Retained evidence

All raw evidence is under
`/srv/openmaps/data/experiments/opensearch-autocomplete-20260920-c72254d/reports/`.
The key files are:

- `export-v5.json`, SHA-256
  `b620818aa9a1d751eb407f03f79665626e593fe5a81ad12bb88a06d20db9c48a`;
- `post-merge-index-stats.json`, SHA-256
  `38536fff6cdd646333a94f79aec734c3fca42bd0ef2cc2880212edee5c2b2f31`;
- `final-index-settings.json`, SHA-256
  `2b57046b9fd7bcaf5cbb8a868a6f55af77ef287c30c1910b9ac5ca8cc83e2225`;
- `final-index-mapping.json`, SHA-256
  `12f19037886374774e4dc6424ffe64e99490955016a3ced09b93d4db0f10328f`;
- `qualification-soft-bias-ranked-final.json`, SHA-256
  `dbf69eea9ae9a05189014f6903664999c0d1996227fdb50f5e8e899bf6c7e25f`;
- `qualification-post-restart.json`, SHA-256
  `6416eed4c3a424844ff70c07d2348c1bedeea3e1f5a8e27ce0e1ba6b434e74f5`;
- `qualification-load.json`, SHA-256
  `34ef87252b864c494b4fe8e2fc2af5aa998cdfbbdf8ebfc208cc8a034c642a0e`;
- `soft-bias-ranked-key-sha256.txt`, which also records the final binary and
  opt-in integration-test hashes; and
- `key-report-sha256.txt`, which records the remaining summary/report hashes.

Partial v1-v4 indexes, the final v5 index and all reports were preserved. The
adapter stopped in 0.2 seconds and OpenSearch stopped in 2.0 seconds. Ports
18090, 19200 and 19201 were confirmed closed. The active public demo remained
healthy on port 8080 with the exact original lookup manifest. No experimental
backend was activated or published, and index/report deletion requires a
separate disk-safety decision.
