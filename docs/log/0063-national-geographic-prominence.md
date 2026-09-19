# National geographic-area prominence qualification

**Date:** 2026-09-19

**Baseline revision:** `52643cfbe8d3ba03150385eda743b09f12ba35b8`

**Qualified implementation revision:**
`2277306fe708fc79c41788e362d8978409c5cf87`

**Scope:** unstructured Places Autocomplete ordering for exact-name localities,
regions and other geographic areas. Postal codes, landmarks, business context,
forward geocoding and MESSY STREETS behavior were not changed.

## Investigation

The unstructured path first normalizes the query, then intersects token-prefix
postings. Its prior ordering was exact primary-name match, name-prefix match,
kind (`area`, `street`, `business`, then other), BM25-style score and stable
entity sequence. Exact same-kind areas therefore reached the stable-ID order
without any prominence comparison. The structured `city, state` and
`street, city, state` paths are separate and already use exact primary names,
the division hierarchy and locality-relative street distance.

The normalized entity rows and schema-2 serving catalog retain kind, subtype,
location, hierarchy edges, stable source locators and source priority. They do
not project division population, class, administrative level or prominence.
The immutable source-record shards do retain the complete Overture division
record. For the pinned 2026-08-19.0 release those records include:

- `cartography.prominence` for localities;
- settlement `class`, including city, town, village and hamlet;
- `admin_level`, hierarchy and region code for administrative divisions.

Overture defines prominence as a 1-100 significance measure derived from
factors including feature/subtype, population and capital status. See the
[Overture prominence reference](https://docs.overturemaps.org/schema/reference/core/prominence/).

An opt-in artifact diagnostic inspected the existing postings result and every
exact-name area candidate for all 25 raw city and state/federal-district checks.
It recorded 316 area candidates; per-query counts ranged from 1 to 59. The
intended locality was present for every city query and was the highest-prominence
exact locality in every case. Intended city prominence ranged from 67 for
Anchorage to 90 for Phoenix. Same-name hamlets, villages, neighborhoods and
other localities had lower values or no value. Each intended state/DC entity
was present as `subtype=region`, `admin_level=1`; regions did not carry numeric
prominence in this artifact.

The failures were therefore ordering failures, not missing intended records.
Denver and Anchorage each also have a same-name county at the locality's label
point. Those are legitimate distinct entity kinds, not duplicate identities.
New York similarly has distinct locality and region identities. No checked
entity needed merging or an allowlist.

Retained investigation evidence:

- `/srv/openmaps/data/national-prominence-investigation-20260919-52643cf.test.log`,
  SHA-256 `3559bf855fa27062bce9fb9ff38387781ce6cc9c9ad95f681052db42aab4c706`
- `/srv/openmaps/data/national-prominence-investigation-20260919-52643cf.summary.tsv`,
  SHA-256 `9e8311ce2ed9e0d7504d7a3eed91d9c1d44369a7d2261710dfbbe558e1ee0e62`

## Implemented behavior

- The importer adapts optional Overture division prominence and settlement
  class into provider-independent ranking evidence. Unknown providers receive
  no fabricated evidence, and invalid supplied prominence fails visibly.
- Unstructured exact-name city-class localities rank first and use supplied
  prominence to resolve same-class ambiguity. Exact regions then precede towns,
  villages, hamlets, counties, macrohoods and neighborhoods. Stable entity
  sequence remains the final tie-breaker.
- Exact areas retain their established precedence over same-name businesses and
  streets. Generic prefix and BM25 ordering is unchanged after exact areas.
- Source rows are read through existing manifest-bound locators. Readers reuse
  already-open Parquet metadata, process candidates in source-shard order and
  cache immutable evidence with the existing bounded primary-candidate cache.
- Public IDs, entity kinds, source priority, Google translation, normalized
  files and serving-catalog schema are unchanged. The exact existing national
  artifact works without a catalog or normalized-data rebuild.
- Structured city/state and street context keep their existing path. No golden
  query names or city-specific allowlist were added.

Deterministic fixtures cover same-name cities with different prominence,
city-locality versus region, town-locality versus county/neighborhood,
state-region preference over a same-name town, exact businesses and streets
competing with areas, and stable sequence ties. The existing structured fixture
continues to cover ambiguous city/state resolution, transitive hierarchy,
street locality, paging, distant-street rejection and cache-independent common
localities.

## Verification

Both the implementation host and a separate exact-revision checkout on the
Hetzner host passed:

```text
gofmt on changed Go files
go test ./...
go vet ./...
git diff --check
```

The exact-revision cold contextual qualification cleared the candidate cache
before every query and passed **25/25** in 83.97 seconds: `city_state` was 16/16
and `street_context` was 9/9. Pennsylvania Avenue ran first and took 13.73
seconds. There were zero failures and zero timeouts.

- Retained cold output:
  `/srv/openmaps/data/national-context-cold-20260919-2277306.test.log`
- Cold-output SHA-256:
  `ca219f38a728a7e8660c4e96aa851a024376b88d0dd52cc3172aed92828737d3`

The complete checked-in suite ran through an exact-revision HTTP server bound
only to `127.0.0.1:18083`. The server was stopped after testing. The 99 requests
completed in 252.66 seconds with the existing 20-second per-request deadline.
There were **zero timeouts**; the slowest request was the passing
`In-N-Out Los Angeles` case at 18.92 seconds.

- Golden set: `config/us-query-checks.json`
- Golden-set SHA-256:
  `a4cfa7b1ddd55614b327d9efc4e0a020b86bca03a805719c22f1e5595f7a62d8`
- Artifact: `/srv/openmaps/data/national-catalog-research-2cfd7fc`
- Manifest SHA-256:
  `7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`
- Retained full-suite output:
  `/srv/openmaps/data/national-relevance-20260919-2277306.test.log`
- Full-suite output SHA-256:
  `d5999b043f0d3b70f37527810f4440cbb2120af14b5bfb57e54d01f047eb92dc`
- Retained server output:
  `/srv/openmaps/data/national-relevance-20260919-2277306.server.log`
- Server-output SHA-256:
  `f9875dd26c2791c901f355bc8a07ee8e1da17f427ee34893ccc888872d408b83`

| Category | Before | After | Total |
| --- | ---: | ---: | ---: |
| Raw city names | 1 | 19 | 19 |
| State and federal-district names | 2 | 6 | 6 |
| `city, state` | 16 | 16 | 16 |
| Raw ZIP codes | 0 | 0 | 10 |
| ZIP with city/state context | 0 | 0 | 5 |
| Raw street names | 7 | 7 | 10 |
| `street, city, state` | 9 | 9 | 9 |
| National landmarks and major places | 6 | 6 | 19 |
| Business with city context | 1 | 1 | 3 |
| Negative queries | 1 | 1 | 2 |
| **Total** | **43** | **65** | **99** |

All 43 previously passing checks remain passing. The 34 remaining individual
failures are:

- **Raw ZIP codes (10):** 10001, 90210, 60601, 94105, 02108, 02840, 99501,
  96813, 83702 and 82001.
- **ZIP with context (5):** `10001, New York, NY`,
  `90210, Beverly Hills, CA`, `02840, Newport, RI`,
  `99501, Anchorage, AK` and `96813, Honolulu, HI`.
- **Raw street names (3):** Broadway, Main Street and Wall Street.
- **Landmarks (13):** White House, Statue of Liberty, Golden Gate Bridge,
  Space Needle, Yellowstone National Park, Mount Rushmore, Liberty Bell,
  Alcatraz Island, Fenway Park, Wrigley Field, Central Park, Disneyland and
  Walt Disney World.
- **Business with context (2):** Starbucks Seattle and Apple Park Cupertino.
- **Negative queries (1):** 00000.

## Decision and limitations

The requested milestone is met: raw cities are 19/19, states/DC are 6/6, and
all structured contextual checks remain 25/25. The implementation uses actual
retained source evidence and general type rules rather than golden names.

The national artifact remains **NOT APPROVED FOR ACTIVATION**. The complete gate
is 65/99, and the 34 failures above remain intentionally outside this change.
Runtime source-row access is compatible with the existing artifact but adds
read amplification for highly ambiguous exact area names; Providence took
14.33 seconds in the ordered suite. A future artifact may project normalized
prominence and settlement tier into the serving catalog to reduce that cost.
That is a proposal, not an activation requirement or an established rebuild
decision.
