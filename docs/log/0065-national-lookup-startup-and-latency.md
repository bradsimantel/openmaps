# National lookup startup and latency qualification

**Date:** 2026-09-19

**Baseline revision:** `16bf359fdaf396447a897205a3e05fb736131758`

**Qualified implementation revision:**
`46952013f57dd69e6bf60dcbefb9da5551be53ad`

**Scope:** cold direct-generation startup and worst-case Places Autocomplete
latency against the retained national artifact. No artifact was rebuilt,
activated or published.

## Investigation

The direct `-lookup` server path performed two complete validations of the same
generation. `Open` verified every manifest-bound file and catalog invariant,
then `Describe` repeated that work only to construct the health-check reference.
An observed baseline verification/open cycle took 7 minutes 27.24 seconds. The
two cycles account for the approximately 14 minute 57 second server startup
recorded in [historical log 0064](0064-national-landmark-ranking.md).

The exact baseline artifact is 110 GiB, while the host has 64 GiB of RAM, so the
full generation cannot remain in filesystem cache. One baseline cycle separated
the existing validation boundaries as follows:

| Phase | Baseline duration |
| --- | ---: |
| Manifest, checksums, Parquet row metadata and layout | 5m33.32s |
| DuckDB open plus locator/search entity integrity | 1m43.07s |
| Posting/prefix integrity | 0.001s |
| Address/spatial integrity | 8.58s |
| Bounded evidence/locator integrity | 0.30s |
| Verified runtime open | 1.48s |
| **One verification/open cycle** | **7m27.24s** |

The checksum stream and catalog entity integrity dominate. Parquet footer
loading and the parent-area graph were not separately observable before this
change, which was itself one of the startup instrumentation gaps.

An opt-in exact-baseline query profiler opened a new store for each cold query,
repeated the query against the same store, and timed the internal boundaries.
It established two independent query causes:

- `In-N-Out Los Angeles` spent 18.89 seconds in token-prefix postings SQL.
  Normalization produces the completed tokens `in n out los angeles`; treating
  every token as an open prefix made the one-letter `n` prefix especially broad.
  Exact area lookup, source evidence, details and response translation were not
  material.
- `Main Street` also spent 14.78 seconds in postings SQL. Its exact area,
  business-candidate and source-evidence work together remained below one
  second. The retained change does not materially improve this catalog scan.
- Cold `Providence` spent 18.01 seconds in the combined exact-area join for 59
  candidates. Area source evidence took 0.11 seconds and postings took 0.22
  seconds. The same query was 0.21 seconds after the bounded candidate cache was
  populated.
- Individual details reads were normally 12-38 milliseconds, with a 53
  millisecond maximum among these baseline probes. The HTTP and direct-store
  results were close enough to rule out Google response translation as a
  significant contributor.

An initial locator-first experiment for every exact name improved Providence
but made small candidate sets slower. In particular, cold `Main Street`
increased to about 30 seconds because separate locator and search-row scans were
more expensive than the existing join for its three exact areas and 41 exact
businesses. That experiment was discarded. Measurements placed the useful
crossover between 41 and 59 candidates, so the retained general path uses a
48-candidate threshold.

## Implemented behavior

- Direct server startup now obtains the canonical path and manifest checksum
  from the same verified open operation. It hashes the manifest both before and
  after validation to reject a concurrent manifest change, but it does not
  rehash the 110 GiB generation merely to populate health metadata.
- Required file SHA-256 verification, Parquet row-count checks, stable-ID and
  catalog integrity validation, source/provenance coverage, and parent-area
  loading are unchanged. Startup is still gated until all checks pass.
- Startup logs now time manifest parsing, complete artifact checksums, Parquet
  metadata, artifact layout, DuckDB opening, entity/search integrity,
  posting/prefix integrity, address/spatial integrity, evidence locators,
  reference hashing, runtime Parquet metadata, runtime catalog opening, catalog
  metadata and parent-area graph construction independently.
- Multi-token autocomplete treats completed tokens as exact and only the final
  token as a prefix. This is the normal incremental-input boundary and avoids
  expanding short completed tokens such as `n` in `In-N-Out`.
- Exact primary-name sets with at least 48 candidates use the existing compact
  sequence and locator tables before materializing search rows. Smaller sets
  retain the faster combined join. The threshold depends only on cardinality,
  not a query name, entity ID or golden expectation.
- Existing caches remain bounded as before. No new dependency, persistent
  cache, artifact file, public ID, source record, ranking signal or API response
  contract was introduced.

Deterministic fixtures cover a completed token that must not broaden to another
token, locator-first unstructured results above the threshold, unchanged
structured context, and the single verified startup/reference path with every
new timing boundary observed exactly once.

## Startup qualification

The exact implementation server was launched on `127.0.0.1:18086`; readiness
was measured from process launch to a successful `/healthz` response.

| Phase | Qualified duration |
| --- | ---: |
| Manifest parsing | 0.0004s |
| Complete artifact checksums | 5m32.93s |
| Parquet footer and row-count metadata | 0.86s |
| Artifact layout and logical digests | 0.0004s |
| DuckDB catalog open | 0.09s |
| Locator/search entity integrity | 1m42.69s |
| Posting/prefix integrity | 0.002s |
| Address/spatial integrity | 8.55s |
| Bounded evidence/locator integrity | 0.29s |
| Reference manifest checksums | 0.0007s |
| Runtime Parquet metadata | 0.90s |
| Runtime catalog open and metadata | 0.018s |
| Parent-area graph | 0.61s |
| **Process launch to health readiness** | **7m27.91s** |

This is approximately 7 minutes 28 seconds, or about 50% below the historical
14m57s direct-server baseline and clears the desirable 10-minute target. The
per-cycle validation time itself is essentially unchanged; the improvement is
the removal of the redundant second cycle, not weaker validation. Filesystem
cache and competing host I/O can still move these numbers.

The baseline observed startup profiler reached 6,859,424 KiB maximum RSS. The
qualified server was observed at 6,735,548 KiB during catalog entity validation.
`/usr/bin/time` recorded a 19,569,808 KiB maximum over the server's complete
16-minute lifetime, including the full HTTP suite and repeated query profiling.
The optimization adds no unbounded or artifact-sized memory cache, so it does
not trade startup time for retained memory.

## Query qualification

| Query/phase | Baseline cold | Qualified cold | Baseline warm | Qualified warm |
| --- | ---: | ---: | ---: | ---: |
| `In-N-Out Los Angeles` autocomplete | 18.94s | 3.46s | 18.69s | 3.07s |
| `In-N-Out Los Angeles` postings/rows | 18.89s | 3.34s | — | — |
| `Main Street` autocomplete | 16.44s | 16.51s | 14.94s | 14.92s |
| `Main Street` postings/rows | 14.78s | 14.79s | — | — |
| `Providence` autocomplete | 18.70s | 9.29s | 0.21s | 0.20s |
| `Providence` exact-area candidates | 18.01s | 8.98s | — | — |

The complete 99-query HTTP suite finished in 252.14 seconds with zero request
timeouts. Its slowest request was the failing `Main Street` check at 16.14
seconds, below the 18-second target. The slowest passing raw requests were
`Market Street` at 12.65 seconds, `Beale Street` at 11.65 seconds, `Bourbon
Street` at 11.62 seconds and `Lombard Street` at 11.86 seconds. `In-N-Out Los
Angeles` passed in 3.31 seconds and Providence passed in 9.32 seconds.

Three additional warm autocomplete-only rounds measured:

- `Main Street`: 14.36-15.22 seconds;
- `In-N-Out Los Angeles`: 3.08-3.31 seconds;
- `Market Street`: 12.01-12.18 seconds;
- `Wall Street`: 11.63-11.81 seconds;
- Providence: 0.22-0.29 seconds; and
- Seattle control: 0.34-0.40 seconds.

These repeats keep the slowest result below 18 seconds but also show that
`Main Street` remains too close for a much tighter deadline. Host and OS cache
variability still apply. The HTTP test includes details retrieval for every
suggestion; the focused profiler shows that those reads are milliseconds, not
the remaining multi-second cost.

The cache-cleared, order-independent contextual qualification passed **25/25**
in 87.72 seconds: `city_state` was 16/16 and `street_context` was 9/9. There were
zero failures and zero timeouts. The slowest case was `Pennsylvania Avenue,
Washington, DC` at 14.25 seconds.

## Relevance preservation

The before and after subtest names were sorted and compared mechanically. Both
sets contain exactly 71 passes; the baseline-missing and newly-added sets are
both empty.

| Category | Before | After | Total |
| --- | ---: | ---: | ---: |
| Raw city names | 19 | 19 | 19 |
| State and federal-district names | 6 | 6 | 6 |
| `city, state` | 16 | 16 | 16 |
| Raw ZIP codes | 0 | 0 | 10 |
| ZIP with city/state context | 0 | 0 | 5 |
| Raw street names | 7 | 7 | 10 |
| `street, city, state` | 9 | 9 | 9 |
| National landmarks and major places | 12 | 12 | 19 |
| Business with city context | 1 | 1 | 3 |
| Negative queries | 1 | 1 | 2 |
| **Total** | **71** | **71** | **99** |

The 28 failures are unchanged: 15 ZIP checks, three raw streets, seven
landmarks, two business-context checks and the `00000` negative check. Every
one of the prior 71 passes remains passing. No request timed out.

## Verification and retained evidence

The implementation host and clean detached exact-revision checkout both passed:

```text
gofmt on changed Go files
go test ./...
go vet ./...
git diff --check
```

- Source revision:
  `46952013f57dd69e6bf60dcbefb9da5551be53ad`
- Artifact: `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Golden set: `config/us-query-checks.json`
- Golden-set SHA-256:
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- Baseline startup profile:
  `/srv/openmaps/data/national-startup-baseline-20260919-16bf359.test.log`,
  SHA-256 `d26e710ae1bad5bf8e1efa4dccbe44e27f7835c46c269aacf4e7d65c65db7987`
- Baseline query profile:
  `/srv/openmaps/data/national-query-profile-baseline-20260919-16bf359.test.log`,
  SHA-256 `372b4b39da60e13ae11ccdfcb0d87c289bed08dc1fd1b295f0102175f32ff059`
- Startup readiness timing:
  `/srv/openmaps/data/national-startup-20260919-4695201.time.log`,
  SHA-256 `f361ddec72628e915181ece7becf608670fa48a1dbe3588683f6788bcd709332`
- Server phase log:
  `/srv/openmaps/data/national-server-20260919-4695201.log`,
  SHA-256 `84626d432e7004f14985b5676f17e12c7faac6480e3a492e09527f9edb8aec04`
- Server resource report:
  `/srv/openmaps/data/national-server-resources-20260919-4695201.time`,
  SHA-256 `729be47f7daa3bbad8780b812ed3e558bf2a7908a5c938232da1ff112878199c`
- Health response:
  `/srv/openmaps/data/national-health-20260919-4695201.json`,
  SHA-256 `efc624bc76249cc187f9d6ccf32c1d82bef4519f089f0576b1f036ae43f4ba07`
- Exact-revision query profile:
  `/srv/openmaps/data/national-query-profile-20260919-4695201.test.log`,
  SHA-256 `5fb0504a85900204d9cc4e93fcb76815ea7cddd4ce2223d5b3c3cd8be4b372e6`
- Full HTTP suite:
  `/srv/openmaps/data/national-relevance-20260919-4695201.test.log`,
  SHA-256 `b145cd46025ac4276e58a9c3de8068ec10936b57b23b99c5963ed10bbdc3c33e`
- Cold contextual qualification:
  `/srv/openmaps/data/national-context-cold-20260919-4695201.test.log`,
  SHA-256 `b061bb9e9f492bca602e4d192cb148ea751e41600d6807f55f0d4e4662cf15c1`
- Warm query repeats:
  `/srv/openmaps/data/national-query-repeats-20260919-4695201.log`,
  SHA-256 `fb43d084bbb6fec3b7edebf604e0f201f09c25faaead9e06c022ad6fbff9b704`

The exact HTTP server was stopped after qualification. Ports 18086 and 18087
were confirmed closed.

## Limitations and future work

Both retained improvements work with the exact existing artifact. Further
startup work can investigate parallel checksum throughput and a single-pass
bounded form of the 1m43s catalog entity validation, but neither is an
established decision and neither may weaken the current checks.

`Main Street` and the remaining slow raw streets are dominated by postings
intersection and ranking in the index-free catalog. A future catalog could
project a more selective completed-name or token intersection, or add a
manifest-bound serving index, but that would require a catalog rebuild. The
same is true for projecting the source ranking evidence discussed in
[historical logs 0063](0063-national-geographic-prominence.md) and
[0064](0064-national-landmark-ranking.md). Those are proposals, not current
architecture or permission to rebuild.

The national artifact remains **NOT APPROVED FOR ACTIVATION**. No artifact was
rebuilt, activated or published during this work.
