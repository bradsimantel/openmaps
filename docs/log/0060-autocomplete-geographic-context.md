# Autocomplete geographic context

**Date:** 2026-09-18

**Starting revision:** `08aaa01660f1b1049ef69909dbc7f82e07976176`

**Scope:** Places Autocomplete `city, state` and `street, city, state`
interpretation and ranking. Forward geocoding and the MESSY STREETS diagnostic
were not changed.

## Investigation

The autocomplete entry point normalized punctuation before candidate
generation. Commas therefore had no structural meaning. Its postings query
also required every normalized token to occur on the same entity. A business
could satisfy `Portland, OR` or `Market Street, San Francisco, CA` through its
name and formatted address, while an area has no state text and a street has no
locality or state text in its search document. Those intended area and street
entities were commonly ineligible rather than merely ranked too low.

The normalized artifact already retained the evidence needed to resolve this:
division `parent_area` relationships, stable entity locators, exact primary
names, entity kinds and source coordinates. A serving-catalog rebuild or a new
provider field was not required.

## Implemented behavior

- The Places domain recognizes comma-separated `city, state` and
  `street, city, state` forms for the 50 US states and District of Columbia.
  State abbreviations, full names and punctuation such as `D.C.` normalize to
  the source region name. Numeric first components remain on the existing
  unstructured/address path rather than being mistaken for streets.
- Structured candidate generation requires an exact primary name and the
  requested entity kind. Address text and aliases cannot make a business a
  locality or street candidate.
- `city, state` follows retained area-parent relationships transitively and
  accepts locality, macrohood or neighborhood entities in the requested
  region, preferring `locality`.
- `street, city, state` first resolves that locality and then chooses the exact
  street-name segment nearest its source label point. Entity locators read only
  the matching street rows from normalized Parquet. A 100 km ceiling rejects a
  same-name street whose closest segment is outside the resolved locality's
  broad geographic context.
- Ordinary queries without recognized comma structure keep the prior prefix,
  kind and BM25 ranking path. Google request and response translation remains
  in `internal/api`; the new interpretation and ranking are in the Places
  domain and DuckDB Places store.

The implementation does not change source records, public IDs, normalized
Parquet, serving-catalog schema or the national golden expectations. It does not
activate or publish a generation.

## Deterministic verification

The small generated fixture covers two same-name Portlands in different
states, a same-name neighborhood, transitive county/state ancestry, same-name
streets in both cities, businesses containing all query words, full state names,
street abbreviations, a missing street and a same-name street more than 100 km
away. Parser cases separately cover state-code punctuation and rejection of ZIP
and unsupported-region forms. Existing unstructured search tests remain in the
same default suite.

The routine checks passed:

```text
go test ./...
go vet ./...
git diff --check
```

## Measured evidence

### Exact national baseline

The unchanged baseline from the historical exact-artifact run is:

| Category | Before | Total |
| --- | ---: | ---: |
| Raw city names | 1 | 19 |
| State and federal-district names | 2 | 6 |
| `city, state` | 0 | 16 |
| Raw ZIP codes | 0 | 10 |
| ZIP with city/state context | 0 | 5 |
| Raw street names | 7 | 10 |
| `street, city, state` | 0 | 9 |
| National landmarks and major places | 6 | 19 |
| Business with city context | 1 | 3 |
| Negative queries | 1 | 2 |
| **Total** | **18** | **99** |

That run used `/srv/openmaps/data/national-catalog-research-2cfd7fc`, manifest
SHA-256
`7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`.

The exact generation and qualification host were not mounted or addressable
from the implementation host. Consequently there is no measured national
after-count, no defensible list of remaining national failures and no national
timeout result for this revision. The implementation host did not substitute a
different artifact and claim the primary 16/16 and 9/9 gate as measured.

### Retained five-state gate

The available retained generation was
`data/us-gate-20260819`, covering Washington, Oregon, Idaho, Montana and
Wyoming. Its manifest SHA-256 is
`87093d4344cadf88ed046d673370bf0e849dc7e9bfbf2be5198108117e37eef0` and
its normalized SHA-256 is
`901e9349ea9e976bf63022744cd4eb565f4cb4fe06c16b167882fb69eea090bd`.

Running all 99 national expectations against this deliberately incomplete
artifact produced the following non-national diagnostic. There were no
20-second timeouts:

| Category | After on five-state gate | Total |
| --- | ---: | ---: |
| Raw city names | 2 | 19 |
| State and federal-district names | 1 | 6 |
| `city, state` | 2 | 16 |
| Raw ZIP codes | 0 | 10 |
| ZIP with city/state context | 0 | 5 |
| Raw street names | 8 | 10 |
| `street, city, state` | 0 | 9 |
| National landmarks and major places | 3 | 19 |
| Business with city context | 0 | 3 |
| Negative queries | 1 | 2 |
| **Total** | **17** | **99** |

The two in-scope maintained city/state cases both passed:

| Query | First result | Elapsed |
| --- | --- | ---: |
| `Portland, OR` | `Portland` area at 45.5202471, -122.6741940 | 24.6 ms |
| `Boise, ID` | `Boise` area at 43.6166163, -116.2008860 | 27.8 ms |

A supplemental contextual road query, `Main Street, Boise, ID`, selected a
`Main Street` segment at 43.4557801, -116.5391113 in 4.16 seconds. The absent
`Broadway, Portland, OR` case returned no result in 0.79 seconds because the
closest exact-name segment in this artifact was beyond the 100 km ceiling.

The remaining maintained contextual failures on this five-state artifact were
all outside its configured source scope. City/state failures were `Portland,
ME`, `Springfield, IL`, `Springfield, MA`, `Kansas City, MO`, `Kansas City, KS`,
`Washington, DC`, `Charleston, SC`, `Charleston, WV`, `Columbus, OH`,
`Columbus, GA`, `St. Louis, MO`, `Las Vegas, NV`, `New York, NY`, and
`Anchorage, AK`. All nine maintained street-context targets are outside the
five-state gate: `Broadway, New York, NY`, `Market Street, San Francisco, CA`,
`Pennsylvania Avenue, Washington, DC`, `Sunset Boulevard, Los Angeles, CA`,
`Michigan Avenue, Chicago, IL`, `Bourbon Street, New Orleans, LA`, `Beale
Street, Memphis, TN`, `Wall Street, New York, NY`, and `Lombard Street, San
Francisco, CA`. These are scope absences, not evidence of post-change national
ranking failures.

## Remaining qualification and decision

The next national qualification run must use the exact retained national
generation, the unchanged `config/us-query-checks.json`, a 20-second timeout
that counts as failure, and category plus individual-failure reporting. The
16 city/state and nine street-context expectations must all pass, and all 18
baseline passes must remain passes before this milestone has measured national
approval.

If exact-name street materialization exceeds the timeout on the national host,
a later proposal may add a manifest-bound coordinate projection to a newly
built serving catalog. That is not an implemented project decision and is not
needed by the current artifact-compatible runtime path.

The national research artifact remains **NOT APPROVED FOR ACTIVATION**.
