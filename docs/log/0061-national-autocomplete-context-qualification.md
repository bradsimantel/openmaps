# National autocomplete context qualification

**Date:** 2026-09-19

**Baseline revision:** `08aaa01660f1b1049ef69909dbc7f82e07976176`

**Qualified implementation revision:**
`4244e7e821eba13e6b6535944229003ff8d5ec63`

**Scope:** Places Autocomplete interpretation and ranking for `city, state` and
`street, city, state`. Forward geocoding and MESSY STREETS behavior were not
changed.

## Correction and investigation

Log 0060 stated that the exact national generation and qualification host were
not addressable from the implementation host. That was an operational mistake:
the provisioned SSH path had not been inspected. The Hetzner host was available,
and it retained the exact artifact at
`/srv/openmaps/data/national-catalog-research-2cfd7fc`.

The first exact-artifact run of the initial structured-context implementation,
revision `944d61953a2f21121eedc5ff29e2d3b0b2874359`, passed 36 of 99 checks. It
preserved all 18 baseline passes, improved `city_state` to 15/16, and improved
`street_context` to 3/9. `St. Louis, MO` returned no locality because the shared
normalizer expanded leading `St.` to `street`, while the national locality is
stored as `Saint Louis`.

The six other target failures were 20-second timeouts for Broadway, Market
Street, Pennsylvania Avenue, Sunset Boulevard, Michigan Avenue and Wall Street.
The exact-name street candidate counts were:

| Street | Exact segments |
| --- | ---: |
| Broadway | 7,848 |
| Market Street | 7,633 |
| Pennsylvania Avenue | 4,845 |
| Sunset Boulevard | 1,597 |
| Michigan Avenue | 3,482 |
| Bourbon Street | 315 |
| Beale Street | 203 |
| Wall Street | 2,444 |
| Lombard Street | 445 |

None of those street segments had a retained `parent_area` edge. The initial
implementation therefore resolved every segment through the wide search table
and read every matching full Parquet entity before ranking by distance. SQL
candidate materialization, rather than distance calculation, dominated the
timeouts.

## Implemented behavior

- Locality context treats a leading `St.` or `St` as `Saint`, without changing
  street-name abbreviation handling. Locality display names stored as `Saint X`
  are presented consistently as `St. X` by autocomplete and details.
- Exact street names are resolved from the existing ordered `names`, `tokens`
  and `postings` tables. Candidate sequences are materialized through the
  schema-2 locator table's matching stable-ID order, without scanning the wide
  search table.
- Street candidates are processed in deterministic pages of 1,024. Coordinate
  reads project only `id`, `lat` and `lng`; only the selected entity is fully
  materialized. A segment within 25 km of the locality anchor is sufficient to
  represent that exact street in the requested locality. If a page has no such
  segment, later pages are considered and the closest segment within the
  existing 100 km ceiling is retained.
- Exact structured area candidates are cached as immutable copies, bounded to
  256 query keys. This avoids repeating expensive ambiguity resolution for a
  locality already used by an earlier `city, state` request while preventing an
  unbounded query cache.
- The existing unstructured autocomplete path, source records, public IDs,
  normalized Parquet, serving-catalog schema and golden expectations were not
  changed. No generation was activated or published.

## Deterministic verification

The generated fixture now also covers:

- `St. Louis` query normalization and consistent details display;
- more than 1,024 identical street names, with the only local segment placed
  after the first stable-ID page;
- cached candidate slices reused across state abbreviations and full names;
- the existing ambiguous locality, transitive hierarchy, type preference,
  unrelated-business and distant-street regressions.

Both the implementation host and the exact Hetzner checkout passed:

```text
gofmt on changed Go files
go test ./...
go vet ./...
git diff --check
```

## Exact national result

The final server ran on loopback only. Artifact verification and context-map
loading completed in 884 seconds. The checked-in 99-query suite took 244.37
seconds inside `go test` (248 seconds for the surrounding command). Each HTTP
request retained the suite's 20-second timeout, and there were **zero timeouts**.

- Golden set: `config/us-query-checks.json`
- Golden-set SHA-256:
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- Artifact: `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Retained output:
  `/srv/openmaps/data/national-relevance-20260919-4244e7e-final.test.log`
- Output SHA-256:
  `99474625b474c545f2a3ccafe6207d2fc3efb962161f905fc43c2890d7b88566`

| Category | Before | After | Total |
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

The primary milestone is met: all 16 `city_state` and all 9 `street_context`
expectations pass. Every one of the 18 baseline passes remains passing.

## Remaining failures

The 56 remaining failures are outside the structured geographic-context
milestone:

- **Raw city names (18):** New York, Los Angeles, Chicago, Houston, Phoenix,
  Philadelphia, San Antonio, San Diego, Dallas, Boston, Miami, Denver,
  Anchorage, Honolulu, Boise, Providence, Cheyenne and Portland.
- **State names (4):** California, Texas, Rhode Island and Alaska.
- **Raw ZIP codes (10):** 10001, 90210, 60601, 94105, 02108, 02840, 99501,
  96813, 83702 and 82001.
- **ZIP with context (5):** `10001, New York, NY`, `90210, Beverly Hills, CA`,
  `02840, Newport, RI`, `99501, Anchorage, AK` and
  `96813, Honolulu, HI`.
- **Raw street names (3):** Broadway, Main Street and Wall Street.
- **Landmarks (13):** White House, Statue of Liberty, Golden Gate Bridge, Space
  Needle, Yellowstone National Park, Mount Rushmore, Liberty Bell, Alcatraz
  Island, Fenway Park, Wrigley Field, Central Park, Disneyland and Walt Disney
  World.
- **Business with context (2):** Starbucks Seattle and Apple Park Cupertino.
- **Negative queries (1):** 00000.

## Decision and future work

The contextual interpretation milestone is qualified, but the national
artifact remains **NOT APPROVED FOR ACTIVATION**. The full gate is only 43/99,
and raw geographic prominence, postal-code behavior, landmark ranking and
business-with-context ranking still require separate investigation.

A future catalog generation could add a manifest-bound street-coordinate or
administrative-membership projection if broader latency requirements demand
it. That is a proposal, not an implemented project decision; the measured
milestone above uses the exact existing artifact without rebuilding or
publishing it.
