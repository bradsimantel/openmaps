# National landmark ranking qualification

**Date:** 2026-09-19

**Baseline revision:** `11703bbfb88ee4a14284da24a0938c9c4c06b36a`

**Qualified implementation revision:**
`242710019732af7673d70bf67fa06fa9b4464c29`

**Scope:** unstructured Places Autocomplete ordering for exact-name landmarks
and other mapped destinations. Postal codes, raw-street relevance, business
context, forward geocoding, routing and MESSY STREETS were not changed.

## Investigation

The existing unstructured path orders an exact primary-name match ahead of a
prefix match, then by kind (`area`, `street`, `business`), BM25-style score and
stable entity sequence. Geographic areas additionally use the prominence and
settlement evidence introduced in the [previous historical qualification](0063-national-geographic-prominence.md).
Place records had no corresponding category evidence in the serving projection,
so an obscure exact business, street or minor area could lead a well-described
destination.

An opt-in artifact diagnostic inspected the postings order, every exact-name
entity, normalized locators and complete retained source rows for all 19
landmark checks. The immutable Overture place rows retain `basic_category`, the
taxonomy hierarchy and confidence even though normalized business entities and
the compact catalog do not. Overture documents taxonomy as a way to organize
places such as businesses, landmarks and attractions. It documents confidence
as the likelihood that a place exists and a relative filtering signal—not
popularity—and notes that duplicate records remain possible. The implementation
therefore uses taxonomy as type evidence and coarse confidence only among
comparable destination candidates; it does not reinterpret confidence as
national prominence. See the [Overture places guide](https://docs.overturemaps.org/guides/places/),
[place schema](https://docs.overturemaps.org/schema/reference/places/place/) and
[taxonomy reference](https://docs.overturemaps.org/schema/reference/places/types/taxonomy/).

The intended record is present for each newly fixed check. The retained source
evidence is sufficient to distinguish the Space Needle, Liberty Bell, Alcatraz
Island, Fenway Park, Wrigley Field and Walt Disney World from their prior top
competitors. It is not sufficient to resolve every query:

- The White House, Statue of Liberty, Yellowstone National Park, Mount Rushmore
  and Disneyland each have multiple exact-name place records. The retained
  category and existence-confidence values do not establish which same-type
  record is nationally prominent.
- The correct Golden Gate Bridge is retained as a `street` at the bridge. Exact
  `business` records are outside the golden 5 km radius, so the artifact cannot
  supply the required kind and location together.
- The New York Central Park is retained as a `business`; no exact `area` is
  present within the golden 10 km radius. The golden contract cannot be met by
  merely reordering this artifact without flattening distinct entity kinds.

Focused follow-up on an initial full-suite regression also found exact businesses
named Market Street (an equestrian facility), Pennsylvania Avenue and Beale St
(historic-site records), Sunset Boulevard (an amusement park), and Bourbon
Street (an adult-entertainment venue). This demonstrates why a broad destination
category must not override an explicitly street-shaped query. The final rule
preserves street precedence when the normalized exact name ends in a recognized
street suffix.

Retained investigation evidence:

- `/srv/openmaps/data/national-landmark-investigation-20260919-11703bb.test.log`,
  SHA-256 `ad43f02fdb45cf93bf5d24af477958afcd8c6e3f2d797f9e19b2a9576edcb87d`
- `/srv/openmaps/data/national-entity-source-investigation-20260919-2427100.test.log`,
  SHA-256 `33f45046168d6f3d9090e7ab1888817faee7ef565529db92a9afbf310ea9ef70`
- `/srv/openmaps/data/national-landmark-ranking-20260919-2427100.test.log`,
  SHA-256 `d3cb2a6f3eb1abe87cfd5ff2b5f398f38ee87b21b4d83ce4e4b6850f873cbbac`
- `/srv/openmaps/data/national-landmark-http-20260919-2427100.log`,
  SHA-256 `dc73f3aff350fc9a92b54efd725c5a655388645b3211121c216f8b95af794b8f`

## Implemented behavior

- The importer adapts current Overture taxonomy, basic category and confidence
  into provider-independent destination class, specificity, coarse reliability
  and narrowly scoped area-override evidence. Unknown providers receive no
  fabricated evidence, and invalid confidence fails visibly.
- Only complete exact-name queries are eligible. Exact city/region precedence
  and explicit street suffixes are preserved. Monument and stadium evidence can
  supersede a minor exact area; other entity kinds are not flattened.
- Comparable exact place candidates can improve on an ordinary business using
  category, coarse confidence and taxonomy specificity. The code does not impose
  a global ordering between unrelated taxonomy families. Stable catalog order
  and stable entity sequence remain deterministic tie-breakers.
- Source rows are read through manifest-bound locators, in source-shard order,
  and cached in the existing bounded primary-candidate cache. Evidence lookup is
  capped at 64 exact place candidates; a more duplicated name keeps its compact
  catalog order.
- Public IDs, normalized files, entity kinds, Google translation and serving
  schema are unchanged. No rebuild is required for this bounded improvement.
  No golden query name, landmark name or artifact-specific allowlist appears in
  production ranking.

Small deterministic fixtures cover monument, museum, stadium, amusement park,
bridge and generic-business competition; destination versus area and street;
explicit street-suffix preservation; exact city and state preservation;
multi-source identity; stable ties; a 65-candidate bound; and skipping source
evidence for a non-exact contextual top result.

## Qualification

Both the implementation host and a clean detached checkout on the Hetzner host
passed:

```text
gofmt on changed Go files
go test ./...
go vet ./...
git diff --check
```

The exact-revision cold contextual qualification cleared the candidate cache
before every query and passed **25/25** in 84.11 seconds: `city_state` was 16/16
and `street_context` was 9/9. There were zero failures and zero timeouts.

- Retained cold output:
  `/srv/openmaps/data/national-context-cold-20260919-2427100.test.log`
- Cold-output SHA-256:
  `6f99d538115cc0ebec25f1b3067644394a1d6663226e8c2118b3cc8e52717380`

The complete checked-in suite ran against an exact-revision HTTP server bound
only to loopback. The server was stopped afterward and ports 18084 and 18085
were confirmed closed. All 99 checks completed in 264.98 seconds with zero
timeouts. The process took 14 minutes 57 seconds to validate and open the
artifact from a cold start.

The slowest passing check was `In-N-Out Los Angeles` at 18.96 seconds, close to
the 20-second request deadline; the slowest failed check was `Main Street` at
15.96 seconds, and Providence took 14.19 seconds. The 19-query landmark HTTP
audit had no request errors and its slowest autocomplete was 1.73 seconds.
Exact-destination source reads are therefore bounded and modest in this slice,
but existing cold prefix/context latency and startup validation remain material
performance concerns.

- Golden set: `config/us-query-checks.json`
- Golden-set SHA-256:
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- Artifact: `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Retained full-suite output:
  `/srv/openmaps/data/national-relevance-20260919-2427100.test.log`
- Full-suite output SHA-256:
  `d376e11ee3bb38e2c0db401926eb3ae9eae2c815a7c9d837f2d4f1a050747316`
- Retained server output:
  `/srv/openmaps/data/national-landmark-qualification-20260919-2427100.server.log`
- Server-output SHA-256:
  `d438d706c6b760df4de551ba2396acec92b8306ed615303741dc06b3824ffd8a`

| Category | Before | After | Total |
| --- | ---: | ---: | ---: |
| Raw city names | 19 | 19 | 19 |
| State and federal-district names | 6 | 6 | 6 |
| `city, state` | 16 | 16 | 16 |
| Raw ZIP codes | 0 | 0 | 10 |
| ZIP with city/state context | 0 | 0 | 5 |
| Raw street names | 7 | 7 | 10 |
| `street, city, state` | 9 | 9 | 9 |
| National landmarks and major places | 6 | 12 | 19 |
| Business with city context | 1 | 1 | 3 |
| Negative queries | 1 | 1 | 2 |
| **Total** | **65** | **71** | **99** |

A mechanical diff of subtest results counted 65 baseline passes and 71 final
passes, with an empty baseline-missing set. The six additions were Alcatraz
Island, Fenway Park, Liberty Bell, Space Needle, Walt Disney World and Wrigley
Field. All 65 previously passing checks remain passing. The final landmark
results are:

| Query | Result | Observed top candidate |
| --- | --- | --- |
| White House | Fail | White House, 176 Robineau Rd, Syracuse, NY |
| Empire State Building | Pass | Empire State Building, 5th Ave, New York, NY |
| Statue of Liberty | Fail | Statue of Liberty, 3820 S Las Vegas Blvd, Las Vegas, NV |
| Golden Gate Bridge | Fail | Golden Gate Bridge, San Francisco, CA 94102, at the city label point outside 5 km |
| Space Needle | Pass | Space Needle, 400 Broad St, Seattle, WA |
| Grand Canyon National Park | Pass | Grand Canyon National Park, US, in northern Arizona |
| Yellowstone National Park | Fail | Yellowstone National Park, US, at 40.32490692, -104.98029163 |
| Mount Rushmore | Fail | Mount Rushmore, US, at 43.96582690, -103.33787050 |
| Gateway Arch | Pass | Gateway Arch, St Louis, MO 63103 |
| Liberty Bell | Pass | Liberty Bell, 526 Market St, Philadelphia, PA |
| Alcatraz Island | Pass | Alcatraz Island, 201 Fort Mason, San Francisco, CA |
| Pike Place Market | Pass | Pike Place Market, 85 Pike St, Seattle, WA |
| Fenway Park | Pass | Fenway Park, 4 Jersey St, Boston, MA |
| Wrigley Field | Pass | Wrigley Field, 1060 W Addison St, Chicago, IL |
| Hoover Dam | Pass | Hoover Dam, Henderson, NV |
| Central Park | Fail | Central Park `area` at 46.97273200, -123.70209900 |
| Times Square | Pass | Times Square `area` at 40.75726140, -73.98589983 |
| Disneyland | Fail | Disneyland, Irvine, CA |
| Walt Disney World | Pass | Walt Disney World, Orlando, FL |

The 28 remaining failures are the existing 15 ZIP checks, three raw streets,
the seven landmarks above, two business-context checks, and the `00000`
negative check. No previously passing check regressed.

## Decision and limitations

The existing artifact supports a principled improvement from 6/19 to 12/19
landmarks and from 65/99 to 71/99 overall. It cannot defensibly reach 19/19.
Golden Gate Bridge and Central Park require corrected/supplemental source typing
or a normalized/catalog rebuild. The five unresolved same-type duplicate cases
need a documented popularity/prominence signal, stronger cross-source identity
resolution, or both. Projecting that evidence into a future serving catalog
would also remove source-row read amplification. These are proposals, not
established architecture or activation decisions.

The national artifact remains **NOT APPROVED FOR ACTIVATION**. No national
generation was activated or published during this work.
