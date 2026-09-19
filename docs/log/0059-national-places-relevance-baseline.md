# National Places relevance baseline

**Date:** 2026-09-18

**Lookup generation:** `/srv/openmaps/data/national-catalog-research-2cfd7fc`

**Manifest SHA-256:**
`7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`

## Purpose

The national build and serving checks proved artifact integrity, bounded
construction and endpoint availability. They did not prove that nationally
ambiguous names rank the entity a user is likely to mean. This record introduces
the first checked-in national autocomplete expectation set and establishes the
current artifact's relevance baseline before any activation decision.

The maintained input is `config/us-query-checks.json`. Its 99 cases cover:

| Category | Cases |
| --- | ---: |
| Raw city names | 19 |
| State and federal-district names | 6 |
| `city, state` disambiguation | 16 |
| Raw ZIP codes | 10 |
| ZIP with city/state context | 5 |
| Raw street names | 10 |
| Street with city/state context | 9 |
| National landmarks and major places | 19 |
| Business with city context | 3 |
| Negative queries | 2 |

## Expectation model

The file records intended behavior rather than copying the candidate's current
first result. An expectation can constrain the first public ID, internal entity
kind, exact display name and an expected WGS84 vicinity/radius, or require an
empty result. The location constraint is important: a same-name locality or
business in the wrong state is not a relevance success.

National expectations use a semantic combination of name, kind and location
instead of freezing an arbitrary opaque ID where several source records can
represent the intended place. Stable public-ID and continuing-source identity
remain separate generation-comparison invariants.

The initial live run found and corrected a test-harness error: JSON was decoded
into a slice already populated with Newport defaults. Omitted fields in the
first eight national records therefore retained unrelated Newport IDs and an
`empty` flag. `importer.ReadQueryChecks` now always decodes a fresh slice,
rejects trailing JSON, and has regression coverage. The results below are from
the corrected loader.

## Initial national result

The exact-generation live check passed 18 of 99 expectations and failed 81.
Every returned suggestion inspected before an expectation failure continued to
resolve through Place Details under the same lookup snapshot.

| Category | Passed | Total |
| --- | ---: | ---: |
| Raw city names | 1 | 19 |
| State and federal-district names | 2 | 6 |
| `city, state` disambiguation | 0 | 16 |
| Raw ZIP codes | 0 | 10 |
| ZIP with city/state context | 0 | 5 |
| Raw street names | 7 | 10 |
| Street with city/state context | 0 | 9 |
| National landmarks and major places | 6 | 19 |
| Business with city context | 1 | 3 |
| Negative queries | 1 | 2 |

Representative failures:

- Most raw major-city names rank a smaller same-name locality outside the
  expected metro. `Seattle` is the sole passing raw-city case in this set.
- Every `city, state` case ranks a business whose name contains both tokens
  rather than the intended locality.
- Raw ZIPs rank individual addresses or businesses; ZIP-context queries rank
  businesses. The current entity model has no postal-code area result.
- Every contextual street case ranks a business above the requested street.
- `White House` ranks a Pennsylvania locality; `Statue of Liberty` ranks a
  Pennsylvania business; `Space Needle` ranks a Michigan street; and
  `Disneyland` ranks a Canadian street.
- `00000` returns businesses and malformed address records instead of remaining
  empty.

`St. Louis, MO`, `Market Street, San Francisco, CA`, and `Lombard Street, San
Francisco, CA` exceeded the live suite's 20-second request timeout. Slow results
are failures even when a later response might have the desired kind.

Passing examples include `Seattle`, `Hawaii`, `District of Columbia`, `Empire
State Building`, `Grand Canyon National Park`, `Gateway Arch`, `Pike Place
Market`, `Hoover Dam`, `Times Square`, `In-N-Out Los Angeles`, and the synthetic
no-result query. Raw street passes establish type behavior only; without query
location or textual context, the suite deliberately does not assert which
same-name national segment should win.

Raw evidence retained on the qualification host:

- `/srv/openmaps/data/national-relevance-20260918.queries.json`, SHA-256
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- `/srv/openmaps/data/national-relevance-20260918.observed.jsonl`, SHA-256
  `5ec1b55085685a01554a54eba75e659a92f71bbd085daf2f50d2d58c6ff487b1`
- `/srv/openmaps/data/national-relevance-20260918.details.jsonl`, SHA-256
  `3f8b5870070a0594185615e1ce523a22e2581ddf560c69f4f7f4128f0432f1e2`

## MESSY STREETS address diagnostic

Open Maps does not depend on the MESSY STREETS software. The external gold-tier
release is used only as a checksum-pinned data corpus through
`config/benchmarks/messy-streets-gold.json` at upstream revision
`4a015b2da2bb4f155ccea19dfe3c47cf6c698bc0`, SHA-256
`532cc4a8ebb7a9da743adfd53b79eaaf39ccb0911dffe561c7ba20c00cc0a07a`.
The compressed corpus remains under ignored `data/`; the pin retains the
authors' citation and the release's Web Data Commons, OpenStreetMap and
OpenAddresses terms.

The opt-in Open Maps benchmark evaluates explicit-US gold records twice:
verbatim as published, and as comma-separated component-canonical inputs. It
reports parser outcomes, matches, ambiguity and coordinate agreement at 100 m,
1 km and 10 km. This is an address-robustness diagnostic, not an autocomplete
oracle or a default test. The published JSONL uses bare `NaN` values for missing
fields; the local reader normalizes those tokens to JSON `null` without changing
the published query text. For an ambiguous response, distance bands use the
closest returned candidate; the report retains both the first and closest IDs.

The first full-corpus run read all 10,000 records and evaluated the 4,283 records
whose country field explicitly identifies the United States. All evaluated
records had valid target coordinates.

| Input mode | Queries with results | Ambiguous | Within 100 m | Within 1 km | Within 10 km |
| --- | ---: | ---: | ---: | ---: | ---: |
| Published verbatim | 2 / 4,283 | 0 | 2 | 2 | 2 |
| Component-canonical | 27 / 4,283 | 6 | 21 | 25 | 27 |

For the verbatim inputs, outcomes were 2 matched, 3,145 no-match, 1,135
unsupported-input and 1 invalid-input. Component-canonical inputs produced 21
single matches, 6 ambiguous matches, 3,123 no-match and 1,133 unsupported-input
outcomes. Every returned canonical result was within 10 km of the corpus target;
21 of 27 were within 100 m. Verbatim-result distance had a 2.1 m median and
3.0 m p95; component-canonical distance had a 16.1 m median and 1,050.1 m p95.

This is predominantly a coverage and accepted-grammar result, not a coordinate
accuracy result. Adding component separators raises returned results from 2 to
27, but 99.4% of the explicit-US corpus still returns no result. Unit/suite
designators, non-numeric or decorated house numbers and other forms outside the
maintained parser grammar account for the unsupported inputs. The much larger
no-match count shows that parser broadening alone cannot close the gap: the
current normalized national sources and exact address index do not contain most
of these web addresses under the same keys and context.

Raw evidence is retained at
`/srv/openmaps/data/benchmarks/messy-streets-openmaps.json`, SHA-256
`18f1fb13d8e8f37436e21f25be69adc5436187e352fe5b3f984c863954f26fbf`.
The report includes both returned-result samples (all 2 verbatim and all 27
canonical results) and 50 bounded nonmatching samples per mode. The successful
run took 8 minutes 24 seconds wall time, used 6,884,776 KiB maximum resident
memory and exited zero. The downloaded source archive retained its configured
SHA-256
`532cc4a8ebb7a9da743adfd53b79eaaf39ccb0911dffe561c7ba20c00cc0a07a`.

## Decision

The national artifact remains technically valid and useful for development,
but it is **NOT READY FOR ACTIVATION** under the new relevance gate. The largest
corrective themes are geographic prominence, structured interpretation of
city/state and street/locality context, postal-code entities or explicit postal
behavior, and type-aware ranking between areas, streets and POIs.

Future ranking work must improve this fixed expectation set; it must not rewrite
the expectations to match an unreviewed candidate. Additions and intentional
policy changes should record their rationale and a new measured result.
