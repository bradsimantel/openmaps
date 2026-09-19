# Autocomplete context cold qualification

> **Follow-up:** Revision `2277306fe708fc79c41788e362d8978409c5cf87`
> adds source-backed prominence and type-aware ordering for raw city and state
> names while preserving this 25/25 cold contextual result; see
> [log 0063](0063-national-geographic-prominence.md).

**Date:** 2026-09-19

**Implementation revision:** `5030e0b491ad1d5112530750a3105f80f9fc43e2`

**Scope:** Places Autocomplete interpretation and ranking for `city, state` and
`street, city, state` under cold, order-independent execution. Forward
geocoding and MESSY STREETS behavior were not changed.

## Investigation

The ordered 99-query result recorded in log 0061 was reproducible, but its
contextual cases shared a bounded primary-candidate cache. A diagnostic rerun
put `Pennsylvania Avenue, Washington, DC` first and cleared that cache before
every contextual query. It passed 24 of 25 checks; Pennsylvania Avenue reached
the 20-second deadline after 20.52 seconds. `Washington, DC` alone took about
12 seconds in that run. The earlier ordered suite had warmed the `Washington`
area candidates before requesting Pennsylvania Avenue, so the passing street
result was order-dependent.

The expensive step was materializing all exact area-name candidates through
the wide search table before applying the requested state. For `Washington`,
that path returned 124 candidates in 17.96 seconds in an isolated diagnostic.
Reading the same exact posting sequences and their compact locator rows took
5.68 seconds. Exact area candidate counts for the target localities were:

| Name | Candidates |
| --- | ---: |
| Washington | 124 |
| Springfield | 80 |
| Charleston | 27 |
| Columbus | 27 |
| Portland | 26 |
| St Louis | 9 |
| Anchorage | 9 |
| New York | 8 |
| Boise | 4 |
| Kansas City | 4 |
| Las Vegas | 3 |

This established a narrow optimization boundary: only Washington and
Springfield exceed 64 candidates among the milestone cases.

## Implemented behavior

- Structured locality resolution first reads exact candidate sequences from the
  existing names, tokens and postings tables.
- Names with at most 64 candidates retain the established full-candidate path.
  This preserves the fast behavior of ordinary city/state requests.
- Names with more than 64 candidates resolve compact locator rows first, apply
  the retained `parent_area` hierarchy and requested region, and materialize
  full entities only for survivors. The same locator helper is shared with the
  already implemented street paging path.
- The existing bounded cache remains useful but is no longer required for any
  checked contextual request to finish within its timeout.
- Golden expectations, public IDs, source data, artifact schema and Google API
  translation were not changed.

The deterministic generated fixture now includes 69 identical locality names.
The intended Oregon locality has the greatest stable public ID and the other 68
belong to Maine, exercising region filtering after the locator-first cutoff.
The repository also contains an opt-in national qualification that places the
previously slowest query first, reverses the remaining order, clears the cache
before every query, and applies the same 20-second timeout.

## Measured evidence

All national measurements used a separate exact-revision checkout on the
Hetzner host. The service listened on loopback only and was stopped after the
run. No generation was activated or published.

- Golden set: `config/us-query-checks.json`
- Golden-set SHA-256:
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- Artifact: `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Exact source revision:
  `5030e0b491ad1d5112530750a3105f80f9fc43e2`

The cold, order-independent contextual run passed **25/25** in 83.62 seconds:
`city_state` was 16/16 and `street_context` was 9/9. There were zero failures
and zero timeouts. Pennsylvania Avenue ran first and was the slowest case at
13.65 seconds; cold Washington and both cold Springfield checks were between
5.68 and 5.73 seconds.

- Retained cold output:
  `/srv/openmaps/data/national-context-cold-20260919-5030e0b.test.log`
- Cold-output SHA-256:
  `5210e1427ccb97c084f0fd7dba283e5ff8e1665051bd3a26ba72a7dc7366e1b8`

The exact-revision HTTP server became ready in 906 seconds. The unchanged
99-query suite then completed in 244.52 seconds. Every request used the existing
20-second client timeout; there were **zero timeouts**.

- Retained full-suite output:
  `/srv/openmaps/data/national-relevance-20260919-5030e0b.test.log`
- Full-suite output SHA-256:
  `f7d3fcd8b246c0392e1e3607d998082f55a2cec26c88fd5ae60ae066b88b3fe7`

| Category | Baseline | After | Total |
| --- | ---: | ---: | ---: |
| Raw city names | 1 | 1 | 19 |
| State and federal-district names | 2 | 2 | 6 |
| `city, state` | 0 | 16 | 16 |
| Raw ZIP codes | 0 | 0 | 10 |
| ZIP with city/state context | 0 | 0 | 5 |
| Raw street names | 7 | 7 | 10 |
| `street, city, state` | 0 | 9 | 9 |
| National landmarks and major places | 6 | 6 | 19 |
| Business with city context | 1 | 1 | 3 |
| Negative queries | 1 | 1 | 2 |
| **Total** | **18** | **43** | **99** |

All 18 baseline passes remain passing. The 56 remaining failures are unchanged
and outside this contextual milestone:

- **Raw city names (18):** New York, Los Angeles, Chicago, Houston, Phoenix,
  Philadelphia, San Antonio, San Diego, Dallas, Boston, Miami, Denver,
  Anchorage, Honolulu, Boise, Providence, Cheyenne and Portland.
- **State names (4):** California, Texas, Rhode Island and Alaska.
- **Raw ZIP codes (10):** 10001, 90210, 60601, 94105, 02108, 02840, 99501,
  96813, 83702 and 82001.
- **ZIP with context (5):** `10001, New York, NY`,
  `90210, Beverly Hills, CA`, `02840, Newport, RI`,
  `99501, Anchorage, AK` and `96813, Honolulu, HI`.
- **Raw street names (3):** Broadway, Main Street and Wall Street.
- **Landmarks (13):** White House, Statue of Liberty, Golden Gate Bridge, Space
  Needle, Yellowstone National Park, Mount Rushmore, Liberty Bell, Alcatraz
  Island, Fenway Park, Wrigley Field, Central Park, Disneyland and Walt Disney
  World.
- **Business with context (2):** Starbucks Seattle and Apple Park Cupertino.
- **Negative queries (1):** 00000.

Routine verification also passed on the implementation host:

```text
gofmt on changed Go files
go test ./...
go vet ./...
git diff --check
```

## Decision and future proposals

The contextual milestone is now qualified both cold and in the complete ordered
suite. The national artifact remains **NOT APPROVED FOR ACTIVATION** because the
overall gate is still only 43/99.

A manifest-bound administrative-membership projection could reduce cold
locality latency further in a future artifact. That is a proposal, not
implemented behavior or an activation decision.
