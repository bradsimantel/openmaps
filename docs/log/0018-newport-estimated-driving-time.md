# Newport estimated driving-time routing

2026-09-08 · Working tree based on `8b06782c2606b2fe1b086822c74eeda8f4c9c8e9`.

Historical implementation and verification record. Current behavior is maintained
in [routing](../routing.md), [geocoding](../geocoding.md), [refresh](../refresh.md)
and the [README](../../README.md). This supersedes the distance-only objective and
absence of duration in [0015](0015-newport-driving-routing.md),
[0016](0016-newport-driving-quality.md) and [0017](0017-newport-address-routing.md)
for new v4 snapshots. Those retained graphs keep their documented distance behavior.
No active deployment, existing snapshot, source lock or public identity was changed.

## Contract and inspected evidence

Consulted current official Google Routes REST v2
[Compute Routes and Route fields](https://developers.google.com/maps/documentation/routes/reference/rest/v2/TopLevel/computeRoutes)
and [traffic options](https://developers.google.com/maps/documentation/routes/traffic-opt)
on 2026-09-08. `duration` is a protobuf-style seconds string; `staticDuration`
excludes traffic and must equal `duration` for `TRAFFIC_UNAWARE`. The local API now
supports both explicit mask paths, with whole-second rounding after accumulation.
It retains address/coordinate unions, automatic address resolution, GeoJSON,
road distance and existing error envelopes. It rejects traffic-aware modes,
departure/arrival times, traffic models, avoidances/modifiers, alternatives,
reference routes and unavailable fields. Broad masks are rejected even when mixed
with an otherwise supported path. There is no public distance-routing mode.

Read the README, agent instructions, maintained routing/geocoding/refresh documents
and relevant routing/address/refresh history before implementation. Ran the existing
**27-trip coordinate** and **22-case address** benchmarks on the retained v3 graph
before edits: both passed. Outputs are in `data/time-routing/before.log`.

Independently scanned `data/rhode-island-260801.osm.pbf` using the existing Go PBF
decoder and inspected retained graph-v3 raw source records. The source checksum
remains `49a96292a8c6ee308c4d5c18f0462482170dfff12ff1dbaafd4fa171b6c4dfa9`.
This scan did not acquire external data. Findings:

- **81,623 included regional ways**, of which **4,098** have `maxspeed`. Within
  Newport, **1,771 included ways** have a retained segment starting in the preview;
  only **115** have `maxspeed`: 104 at 25 mph, two at 15 mph, one at 35 mph and
  eight at 40 mph. This is a stated segment-based inspection window, not municipal
  coverage or a count of distinct streets. Missing speeds dominate the estimate.
- Newport classes include 1,009 service ways, 465 residential, 93 tertiary,
  50 secondary, 51 primary and motorway/trunk/link pieces. Local service tags
  include 336 driveways, 235 parking aisles, eight drive-throughs, four alleys and
  four slipways. Service classification matters to practical driving progress.
- Region-wide numeric maxima range from 5 to 65 mph, with **25 unitless values**
  on included ways. Unitless values remain km/h, not guessed mph. The retained
  included ways contain no directional maxspeed or unresolved symbolic/malformed
  base values. The full PBF has six `walk` highway ways outside the driving profile;
  source semantics still require deterministic parsing tests for future data.
- Seven included ways have conditional limits: school-calendar expressions with
  internal semicolons and holiday clauses, and `20 mph @ flashing`. The retained
  tagged-node sources also include three conditional speed nodes. Many speed-sign
  nodes in the full PBF do not share retained road-node identity; proximity is not
  used to infer their zone. Applicable shared-node evidence receives a conservative
  incident-way cap with the zone/direction uncertainty recorded.
- Regional advisory speeds occur on 407 included ways, at 15–45 mph. Advisory
  values are distinct from legal maxima. Relevant surfaces include asphalt,
  concrete, paving stones, gravel, cobblestones, ground, grass and sand; absent
  surface remains an assumption. Neither a basemap tile nor a road name supplies
  a speed or legal access right.
- Retained tagged-node sources include 1,636 traffic-signal nodes, 1,149 stop
  nodes and 207 give-way nodes. Signal tags mix junction and approach
  representations, ordinary/pedestrian/emergency signals, and forward/backward/
  both/missing directions. Stop tags mix all/minor/unknown approaches, including
  numeric headings. These are source feature counts, not unique controlled
  intersections. No timings, queues or complete approach associations are retained.
- **192 included ways are tagged roundabout** (not 192 distinct roundabouts).
  Ways **1239514920, 1159238010 and 1239514919** at JT Connell are `highway=trunk`
  **and** `junction=roundabout`. An early candidate incorrectly inherited the
  65 km/h trunk movement assumption. The final model caps circulatory roads at
  20 km/h; source classification prompted the correction, not a target output.

Primary semantics references inspected on the same date:
[OSM maxspeed](https://wiki.openstreetmap.org/wiki/Key:maxspeed),
[units](https://wiki.openstreetmap.org/wiki/Map_features/Units),
[conditions](https://wiki.openstreetmap.org/wiki/Conditional_restrictions),
[advisory speeds](https://wiki.openstreetmap.org/wiki/Key:maxspeed:advisory),
[surfaces](https://wiki.openstreetmap.org/wiki/Key:surface),
[service](https://wiki.openstreetmap.org/wiki/Key:service),
[signals](https://wiki.openstreetmap.org/wiki/Tag:highway%3Dtraffic_signals),
[stops](https://wiki.openstreetmap.org/wiki/Tag:highway%3Dstop), and
[RIDOT roundabout guidance](https://dot.ri.gov/safety/roundabout_safety.php).
RIDOT describes low circulatory speeds and distinguishes larger old rotaries;
it does not establish the model's effective speed or local calibration.
Full counts, examples and inspection programs remain in ignored
`data/time-routing/source-tags.json`, `pbf-tags.json` and `inspect.go`.

## Model and algorithm decisions

Graph **4**, profile **`driving-time-v4`**, cost model **`estimated-driving-v1`**
stores per-way directional effective km/h, interpreted numeric ceilings and
assumption notes in the existing checksummed SQLite graph payload. The maintained
[routing model](../routing.md#estimated-speed-and-elapsed-time-model) defines all
class/service/surface defaults, unit parsing, directional precedence, symbolic and
malformed handling, conditional policy and point-sign limitations.

Defaults are intentionally coarse and uncalibrated: for example, residential
25 km/h, ordinary service 10, parking aisles/driveways 7, and mapped roundabout/
circular road movement capped at 20. Numeric legal ceilings cap expected speed at
80% of the ceiling rather than assuming continuous travel at the limit. Unknown
legal speed stays unknown; unresolved speed data gets a noted 5 km/h estimate,
not a claim of legal compliance. `none` and `walk` have explicit policies.
Potentially applicable conditional minima apply all the time; no clock, calendar,
flashing-sign state, live traffic or historical traffic model is introduced.

Elapsed time is the sum of road length divided by directional effective speed.
No arbitrary preference score is reported as seconds. No discrete intersection,
turn or control-node delay is added: the source lacks enough association/timing
information to avoid double-counting junctions, and geometry vertices are not
stops. The roundabout cap changes movement speed without per-vertex delay. This
leaves individual turns and queues imperfectly priced; it is an explicit limit.

The existing Dijkstra state still contains incoming directed edge, prohibited-path
history and destination-access phase. V4 adjacency is canonically ordered by
stable segment reference; exact cost ties retain a deterministic predecessor.
Partial endpoint edges use their traversed road length, and rounding occurs only
at the API boundary. The internal distance comparator retains the same selected
endpoints and all restrictions and prices its result using the same time model.
API code still owns Google translation and address orchestration. No road-cost or
route-success signal selects an address, entrance, snap or destination permission.

## Source-backed route evaluation

The coordinate suite now has **31 cases**. Four additions use exact retained OSM
nodes on Eisenhower/Valley, JT Connell/Admiral Kalbfus, Narragansett/Bellevue and
the West Marlborough bridge approach/downtown. Existing cases retain one-ways,
bridges, parking access, residential streets, dead ends, barriers, disconnected
components, destination-only access and boundary detours.

Expectations use source tags, source-node adjacency, permitted directed paths,
prohibited-path/zone invariants, independently bounded road witnesses and endpoint
fidelity. Two added cases declare a practical-route criterion: use an ordinary-road
witness without service through-travel within a 30% distance allowance. This is an
explicit quality criterion, not a claim that the generated path is ground truth.
No exact path was copied into a new expected result. Tests also independently sum
geometry distance and directional duration, and check that the time optimum is
no slower under this model and the distance optimum is no longer.

Both durations below use the **final same model**. Distance-optimal segment paths
were separately checked against the retained v3 router: all 24 successful
coordinate paths match exactly. The 13 successful old address routes are obtained
through the actual retained v3 address API, with unchanged endpoint metadata, then
independently priced under v4 speeds. Whole numbers in these tables are rounded
observations, not accuracy claims; complete path IDs and geometry remain in the
ignored benchmark logs.

### Coordinate routes

| Trip | Distance-optimal m | Time-optimal m | Distance path, estimated s | Time path, estimated s | Path changed |
| --- | ---: | ---: | ---: | ---: | --- |
| White Horse to Bellevue | 1239 | 1251 | 146 | 138 | yes |
| Bellevue to White Horse | 966 | 966 | 122 | 122 | no |
| south Beacon Hill to Bellevue | 3541 | 3577 | 428 | 421 | yes |
| north Park Holm to east Valley | 2303 | 2487 | 448 | 288 | yes |
| Valley to Park Holm | 2303 | 2487 | 448 | 288 | yes |
| south to east | 7025 | 7158 | 834 | 809 | yes |
| Farewell one-way forward | 39 | 39 | 4 | 4 | no |
| Farewell one-way reverse detour | 439 | 439 | 53 | 53 | no |
| Goat Island bridge westbound | 181 | 181 | 26 | 26 | no |
| Goat Island bridge eastbound | 181 | 181 | 26 | 26 | no |
| Cotton Court dead end arrival | 765 | 765 | 103 | 103 | no |
| Cotton Court dead end departure | 865 | 865 | 113 | 113 | no |
| Training Station bridge within component | 28 | 28 | 3 | 3 | no |
| Newport Bridge parking aisle | 126 | 126 | 65 | 65 | no |
| Destination parking arrival | 552 | 552 | 85 | 85 | no |
| Destination parking departure | 324 | 324 | 55 | 55 | no |
| Resolute destination arrival | 2347 | 2446 | 308 | 277 | yes |
| Resolute destination departure | 2504 | 2539 | 328 | 279 | yes |
| No downtown destination through shortcut | 638 | 716 | 83 | 80 | yes |
| Edgar Court competing service snap | 771 | 771 | 91 | 91 | no |
| Eisenhower to Valley public-road alternative | 2184 | 2477 | 419 | 270 | yes |
| JT Connell to Admiral Kalbfus around service shortcut | 499 | 598 | 94 | 64 | yes |
| Narragansett to Bellevue without residential cut-through | 1477 | 1513 | 174 | 167 | yes |
| Goat Island bridge approach to downtown | 954 | 954 | 99 | 99 | no |

The seven remaining coordinate cases retain their expected failures: waterfront,
water, destination-proximity, customer-only and private-driveway points remain
unsnappable; outside-east remains outside coverage; Training Station cannot reach
downtown through its gate/disconnected component. No duration is fabricated.

### Address routes

| Trip | Distance-optimal m | Time-optimal m | Distance path, estimated s | Time path, estimated s | Path changed |
| --- | ---: | ---: | ---: | ---: | --- |
| ordinary residence | 7 | 7 | 1 | 1 | no |
| ordinary library | 1243 | 1254 | 146 | 138 | yes |
| downtown reverse | 963 | 963 | 121 | 121 | no |
| residential destination access | 2311 | 2410 | 303 | 272 | yes |
| destination departure | 2461 | 2496 | 322 | 273 | yes |
| destination complex | 2390 | 2490 | 314 | 284 | yes |
| destination Cloyne | 2145 | 2244 | 277 | 247 | yes |
| private public boundary | 3010 | 3021 | 349 | 341 | yes |
| private boundary departure | 2521 | 2521 | 292 | 292 | no |
| waterfront parking proximity | 1550 | 1550 | 198 | 198 | no |
| waterfront public approaches | 1139 | 1167 | 176 | 170 | yes |
| boundary south | 3545 | 3581 | 430 | 423 | yes |
| boundary north | 2302 | 2487 | 448 | 288 | yes |

The other nine address cases retain explicit failures: unnamed destination aisle,
private military association, competing waterfront entrances, barrier/disconnected
approaches, eight-way Bellevue ambiguity, twelve-way Connell ambiguity, unsupported
unit and absent house number. Source identities and coordinate uncertainty remain
unchanged. No supplemental address source was used.

### Meaningful differences and suspicious-route review

- **Park Holm/Valley and Eisenhower/Valley:** the old route crosses service ways
  **1195659812, 19349778, 897071105 and 897071104** after Adelaide Avenue.
  The new ordinary-road witness uses Hillside, Admiral Kalbfus, Miantonomi and
  Green End, then Valley. The retained whole-extract graph still supplies the
  necessary boundary detour. About 8% more distance on the original trip avoids
  a long low-speed service traverse. The added public-endpoint case adds about
  13%. Endpoint gaps and source nodes do not change.
- **Resolute/Cloyne and JT Connell:** service ways **1285454388/1285454389** provide
  a shorter corner connection. The new route uses the actual source roundabout/
  public junction and Admiral Kalbfus. The added case is 499 → 598 m and
  94 → 64 estimated seconds. Inspecting its trunk-tagged circular geometry found
  and corrected the early high-speed roundabout error. Destination access remains
  a permitted suffix/prefix, never a through shortcut or private permission.
- **South/Bellevue and Narragansett/Bellevue:** the new route stays on Narragansett
  and Bellevue instead of cutting through residential Dixon Street. It adds only
  about 36 m, with about seven modeled seconds difference. Both source witnesses
  are legal in the compiled profile; without observations, that small saving is
  not a validated real-world advantage.
- **White Horse/library and Leroy arrival:** the route continues along North
  Baptist/Thames rather than using residential Charles through Washington Square.
  It adds about 12 m with an eight-second modeled reduction. The Leroy public-road
  arrival and 49.8 m unverified private-driveway gap remain unchanged.
- **Downtown divided-road case:** the 638 → 716 m route uses America's Cup Avenue
  and mapped median connector **19350562**, with no prohibited maneuver or
  destination-parking through travel. This legal loop saves only about **three
  modeled seconds** versus the Thames route. It remains a sensitivity/comfort
  limitation: omitted maneuver/queue delays could reverse the ranking. The result
  is not described as a measured improvement, and expected results were not
  rewritten to bless an illegal shortcut. A route-specific penalty fitted to this
  output would not be independent validation.
- **Bridges, Cotton Court, parking aisle and gated Training Station:** source
  topology and access witnesses remain intact. Costs neither connect elevated
  crossings nor add a dead-end exit, motorway snap or barrier passage. The car
  still fits the explicitly retained Newport Bridge parking-aisle height limit.

No independent observed journey times were available or acquired. These checks
verify the algorithm, source fidelity and declared practical invariants; they do
**not** validate estimate accuracy against actual drives. Traffic, individual
signals, turn delays, school-zone extent, road-condition variation, source errors
and endpoint entrances remain uncertain. No arrival times, traffic conditions or
confidence percentages are produced.

## Snapshot, reproducibility and loading

Final candidate: `data/time-routing/candidate-v4-final.sqlite`.
Independent rebuild: `data/time-routing/rebuild-v4-final.sqlite`.
Earlier `candidate-v4.sqlite` and similarly named preliminary builds in that
ignored directory are superseded investigation artifacts without the final
circulatory-road cap; use the **final** files for this milestone.

| Artifact | SHA-256 |
| --- | --- |
| Final candidate SQLite | `e6447809236cd974c6b66de7b147dd67b6089e105ee70b5b6eacb12b74f6f525` |
| Independent rebuild SQLite | `3f2526b7805c8314f1c6f373a4432295771446b15d93ae48e5d87d7e886a93e8` |
| Identical final graph payload | `a3a2ee9ea7a48c67022c54504f47dcaf89594f64d7a9d81cb0d854df2a230a53` |
| v3 → final-v4 comparison | `73cd2c47b57858a6fd3eef1fc900db1df05c40bd94344ebdb97d3e05ca43abb8` |

Every logical table matches between independent final builds, including graph,
lookup, raw source records, provenance, relationships, metadata/history and FTS.
SQLite file bytes differ as permitted by the refresh contract. Comparing v3 with
v4 preserves all **11,602 public IDs**, with zero entity or relationship changes
and zero validation violations. The graph has unchanged **622,348 nodes, 657,190
segments, 1,173 banned paths, 856 destination segments and 125,127 guards**.
All node/segment/ban/guard/access arrays and original source records are identical
between retained v3 and final v4; new speed-related source decision notes explain
the cost interpretation. There are **81,623 per-way cost records**. Release,
source IDs/versions, attribution, public identities and original PBF checksum stay
intact. No new dependency or runtime infrastructure was added.

Unknown graph/profile/cost versions, invalid or missing speeds, duplicate/orphan
costs and corrupt checksums fail loading explicitly. Retained v1/v2 coordinate
observation suites pass; v3 preserves automatic addresses and distance routing.
Duration masks on these older versions return explicit 503 unavailability rather
than retroactively inventing estimates. Isolated lookup-only → v4 → lookup-only
and v3 → v4 → v3 cycles verify loaded profile, duration availability/equality,
address association, Places IDs and geocoding ambiguity using `t.TempDir()` state.

### Regional resource sample

Compiled the same isolated `performance` program against the final code and loaded
v3/v4 in separate processes. Five passes over the 31-case suite provide 120 routed
query samples per graph; other outcomes are also recorded. Queries include snap
scans, and address-geocoding time is not part of this coordinate sample. These are
single-host development measurements, not capacity claims or controlled production
benchmarks. Earlier samples varied; per-case timings are retained in ignored data.

| Measurement | Retained v3 | Final v4 |
| --- | ---: | ---: |
| SQLite size, MiB | 219.10 | 235.65 |
| Graph payload, MiB | 186.34 | 202.87 |
| Load, seconds | 2.09 | 2.20 |
| Heap after GC, MiB | 233.94 | 230.96 |
| Median routed query, ms | 35.61 | 32.54 |
| p95 routed query, ms | 48.79 | 40.64 |
| Maximum routed query, ms | 96.39 | 77.33 |
| Peak process RSS including load, MiB | 764.52 | 936.08 |

Persisted costs add about **16.55 MiB** (7.6% of the previous SQLite size).
Peak load RSS increases about **22%**, a material startup-memory cost of decoding
additional cost records and building canonical adjacency. Retained heap and this
sample’s query latency do not regress; loading is about 0.11 seconds slower. The
initial regional v4 build took about eight seconds with roughly 1.6 GiB peak RSS;
final builds complete within the same development-scale workflow. Startup memory,
serialized deployment requests and planet-scale graph loading remain limitations;
this milestone does not establish production concurrency capacity.

## Verification and browser behavior

Passed `gofmt`, `go test ./...`, `go vet ./...` and content/diff formatting review.
Small deterministic fixtures cover mph/km/h conversions, directions and car scope,
missing/symbolic/malformed/conditional speeds, surface/service/roundabout caps,
point-sign uncertainty, longer-but-faster paths, ties under reordered segments,
partial/reverse/zero trips, total-duration rounding, forbidden access/dimensions/
barriers, destination state and prohibited via-way turns. No regional dataset or
network download is required by routine tests. Existing corruption tests still
supply valid costs so they exercise their intended graph/access failures.

The final regional routing/address suite and both isolated snapshot cycles pass.
The 26-case geocoding suite passes against August and the final candidate. Live
Places/geocoding fingerprint checks pass in an isolated temporary deployment;
eight representative real HTTP route requests check addresses, mixed input, zero
travel, ambiguity/units and rejected traffic/departure/modifier options. A first
live fingerprint check against fixed-DB serving correctly lacked deployment-only
headers; the verification was rerun in temporary deployment mode, not the active
user deployment. This required no product change.

Browser verification uses a separate loopback candidate. It displays estimated
minutes beside road distance, explicitly excludes live traffic and unverified
off-road gaps, and retains requested A/B and road A′/B′ markers. Verified ordinary
address routing, source-qualified Resolute destination access through the capped
roundabout, Leroy's public-junction/49.8 m excluded approach, a mixed map-coordinate
and address route, ambiguous-address error, loading controls, immediate stale
result clearing on endpoint/map-action changes and Clear during calculation with
no late result repopulation. No arrival prediction or local confidence is shown.

Generated source scans, SQLite files, complete comparisons, HTTP responses,
resource measurements, server logs and verification output remain in ignored
`data/time-routing/`. The active `data/deployment.json` checksum remains
`d7070c4c86ff7221ea2691aae07fe37436b5d37fc878e12c7b2c5aa1c2e90aff`.
No commit, push or active deployment change was made.
