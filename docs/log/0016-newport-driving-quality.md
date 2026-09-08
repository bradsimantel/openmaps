# Newport passenger-car route quality

2026-09-08 · Working tree based on `f4708e1f4825604222c7f11f5865ced4e5b92a85`.

Historical implementation and verification record. Current behavior is maintained
in [routing](../routing.md), [refresh](../refresh.md) and the [README](../../README.md).
This supersedes the first milestone's dimension exclusion, destination exclusion
and single-nearest-snap policies in [0015](0015-newport-driving-routing.md).
Google Routes REST v2 request fields, masks and coordinate-only boundary remain
unchanged. No address enrichment, travel times or new endpoints were introduced.

## Source inspection and decisions

Read the actual retained `data/rhode-island-260801.osm.pbf` with the existing Go
PBF decoder, and inspected raw source records in the retained v1 graph. The input
SHA-256 remains `49a96292a8c6ee308c4d5c18f0462482170dfff12ff1dbaafd4fa171b6c4dfa9`.
The independent preview inspection retains highway ways and original coordinates,
including excluded ways whose internal nodes are absent from the old graph.
Generated inspection files are in `data/routing-quality/`.

Concrete findings guiding the work:

- Parking aisle **1312346391**, beneath the Newport Bridge approach, has
  `maxheight=14'9"`: 4.4958 m. V1 excluded it entirely even though a car fits.
  Its source coordinates cross drawings of elevated roads without sharing their
  nodes. Restoring the aisle must not create a motorway junction.
- Destination roads include Resolute **19359216/19352279**, Enterprise Court
  **19349648/1314758575/1314758576**, Cloyne Court **1131282109**, service access
  **19353128**, and the downtown destination parking/driveway ways
  **741262975–741262978** and **1192260087–1192260091**. They form source-connected
  zones, not inferred parcels. Access via West Marlborough and America's Cup
  Avenue must not create a through shortcut across the downtown parking area.
- Covered service way **1476568267** is `access=customers`, which does not grant
  the same rights as `destination`. The old nearest-eligible policy silently
  moved an endpoint on it about 13 m to a public street. Private driveway
  **19347923** remains unavailable without permission.
- Training Station Road **1499201952** includes gate **201248257**, with no
  explicit car permission. Its adjoining bridge **986758708** is internally
  traversable, but the component cannot reach downtown. Nearby geometry is not
  permission to cross the gate or jump components.
- Cotton Court **19354757** ends at node **4385085569**, tagged `noexit=yes`.
  It joins Thames only at **201166841**. Arrival and departure remain possible,
  with a fresh initial heading; there is no invented exit or mid-trip reversal.
- Edgar Court **19353499** and service way **19347880** share node **201065273**.
  The service approach has several very short source geometry segments. A safe
  local snap check must follow the same way to that junction, not require the
  nearest individual segment itself to touch the street.
- Region-wide values include metres, complete feet/inches, `st` and `lbs`, plus
  HGV-only limits and conditions. Davison Avenue **442656848** has malformed
  `maxwidth=8'0`; High Street **1212897240** has incomplete `maxlength=26'`.
  These remain closed, even though their height limits parse. No missing quote
  or zero inches is guessed. Relevant gates have only absent/yes/no lock values;
  future unknown lock values fail closed.

The ordinary loaded-car assumptions are 1.9 m high, 2.0 m wide including mirrors,
5.0 m long, 1.8 metric tonnes total, and at most 1.1 tonnes on an axle. No roof
load, trailer or configurable vehicle is supported. Complete unit conversions,
inclusive thresholds, physical/directional limits and conservative unknown/condition
handling are documented with current OSM references in [routing](../routing.md).
Numeric compatibility opens roads; it does not infer speed, travel time or legal
access. Unrelated HGV-only conditions no longer close a passenger-car road.

Destination qualification deliberately requires an endpoint on the source road
(with 0.1 m rounding tolerance), excluding nodes shared with unrestricted road
directions. This is a precise coordinate-only rule, not a claim about property
entrances. A lookup result or nearby point cannot grant private/customer/delivery/
permit access. Restricted travel is limited to an origin-zone prefix and a
destination-zone suffix. Public travel cannot enter and then exit a destination
zone to create a shortcut. Direction and prohibited-path history apply throughout.

Snapping considers at most eight nearest eligible segments inside 100 m. The
bounded service-to-street preference is independent of trip success: at most 30 m
from the request, at most 10 m farther, at most 20 m of source geometry to a shared
unrestricted surface junction, and at most 32 local node visits. One-way/elevated/
destination segments, prohibited-turn nodes, excluded approaches and crossings of
third roads prevent the preference. Disconnected roads retain explicit no-route
behavior. Excluded motor-road geometry now guards snapping too. Unknown entrances
and off-road obstacles remain limitations; dashed connectors are never road distance.

## Before and after evaluation

Expanded the deterministic suite from **9 to 27 trips**. New coordinates come from
source nodes/segments or explicitly constructed offsets at a source junction.
Expectations use tag evidence, required ways, prohibited destination through travel,
source geometry bounds, node adjacency and graph invariants. The original exact
router distances are retained as observations, not promoted to quality oracles.
An explicit old-graph observation mode still validates source tags, directed
adjacency, prohibited paths and road-distance sums, but does not claim v2 policy
compliance. The v2 run enforces all 27 outcome and quality expectations.

Metres below are observed road distances; gaps are separate. V1 is the retained
`data/routing/openmaps-routing-v2.sqlite` file (its filename predates graph format
2 and actually contains `driving-distance-v1`). V2 is this milestone's candidate.

| Trip | V1 → V2 road result | Meaning |
| --- | --- | --- |
| White Horse → Bellevue | 1,239 → 1,239 | Existing directed trip preserved |
| Bellevue → White Horse | 966 → 966 | Existing asymmetry preserved |
| South Beacon Hill → Bellevue | 3,541 → 3,541 | Still leaves the preview for a valid detour |
| Park Holm → Valley / reverse | 2,303 → 2,303 each | North boundary detours preserved |
| South → east boundary | 7,025 → 7,025 | Whole-extract routing preserved |
| Waterfront, water, outside-east cases | Same two unsnappable / one outside-coverage | Coverage behavior preserved |
| Farewell interior forward / reverse | 39 / 439 → same | Reverse requires a legal directed loop |
| Goat Island bridge west / east | 181 → 181 each | Same bidirectional source bridge |
| Cotton Court arrival / departure | 765 / 865 → same | Dead end permits endpoint use, not an invented exit |
| Training Station component → downtown | Unreachable → unreachable | Gate and disconnection preserved |
| Inside Training Station bridge | 28 → 28 | Internal component travel preserved |
| Newport Bridge parking aisle | Unreachable → **126** | Compatible height restores the actual aisle; gaps 0/0 |
| Destination parking arrival / departure | 449 / 221 → **552 / 324** | Now reaches requested aisle; old 16.9 m snap displacement removed |
| Resolute arrival / departure | 2,281 / 2,438 → **2,347 / 2,504** | Reaches restricted road point; old 65.5 m displacement removed |
| Point about 2 m off destination aisle | 449 → **unsnappable** | Proximity does not qualify as on-road destination access |
| Customer-only covered road point | 564 → **unsnappable** | No 13.4 m jump to a public road to hide access uncertainty |
| Private driveway point | Unsnappable → same | Private permission still required |
| Public endpoints around destination parking | 638 → 638 | Destination through shortcut forbidden |
| Edgar Court competing snap | 773 → **771** | Street gap 4.6 m vs nearest service gap 0.9 m; reason reported |

Longer destination routes are improvements in endpoint fidelity, not unexplained
regressions: their new road distance includes the source roads needed to reach the
requested endpoints. The two new rejections replace misleading public-road snaps.
No original trip regressed. The Edgar fixture initially failed because the local
check considered only immediately adjacent segments; inspecting its short source
vertices led to the bounded same-way traversal, without broadening permissions or
trying arbitrary roads until a route succeeded.

## Snapshot, provenance and reproducibility

Candidate: **`data/routing-quality/candidate-v2.sqlite`**. All artifacts remain in
ignored `data/`. Build used the unchanged lookup bundle, checksum and original
August baseline through `cmd/refresh build -routing-pbf`, followed by comparison.
The independently rebuilt `data/routing-quality/rebuild.sqlite` has byte-identical
graph payloads and identical logical rows in every lookup/provenance/metadata/FTS
table. Whole SQLite bytes differ as permitted by the existing rebuild contract. A further
final-code build, `final-rebuild.sqlite`, reproduces the same payload and all
logical rows after the conservative unknown-lock check was added.

| Artifact | SHA-256 |
| --- | --- |
| Candidate SQLite | `65fe730c026bbf8c0929d66d796bc2233f9dce215be6dfa6c50191c8a157468c` |
| Independent rebuild SQLite | `d849b02d7cddae72259a57a42626fb55b2a32d26f3800cce224c3d082de514a8` |
| Final-code rebuild SQLite | `cd53cf825cfd9efc933f3a9a39ee1324d9b978e0edcdb1d9bca9c779647a88c2` |
| All three graph payloads | `5716defa576c8ca0b71135dcc620e1ab0f3daab8d61ca198173598b7ed5a9c6d` |
| Retained-v1 → v2 comparison | `ac3ad32c9857ddf7d7ddf05d16bcfbd149d3ab9a9baefa3d86c9973b35b0f7f5` |
| Unchanged active deployment state | `d7070c4c86ff7221ea2691aae07fe37436b5d37fc878e12c7b2c5aa1c2e90aff` |

Graph format **2** pairs with **`driving-distance-v2`**. Format **1** retains its
original profile, compiled exclusions and nearest-snap behavior; unknown versions
or mismatched profiles fail explicitly. Source-qualified node/way/relation IDs,
versions, raw records, release, attribution, source checksum and deterministic
segment references remain retained. Snap guards refer to excluded source ways.
The API now reports the loaded profile rather than a process-wide constant.

The graph has **622,348 nodes, 657,190 segments, 81,623 included ways, 1,173 banned
paths, 856 destination segments and 125,127 excluded motor-road snap guards**.
There are **451 added ways**: 145 destination roads and 306 with vehicle limits or
unrelated HGV conditions; **zero removed ways**. Additional reachable restriction
paths are compiled rather than omitted. The 3,161 blocked-node count includes
newly scanned nodes on excluded motor roads for snap guards, so it is not directly
comparable to v1's smaller node scan and does not mean new arbitrary closures.

Comparison retains **11,602 public IDs**, with zero entity additions, removals,
changes, relationship changes or validation violations. Source lock, identity
mappings, lookup/geocoding records and original retained snapshots are unchanged.
The 26-case geocoding suite passes against August and the routing candidate.

Both snapshot cycles use temporary deployment state: August lookup-only → v2 →
lookup-only, and retained v1 → v2 → v1. Checks exercise comparison, review receipts,
validation, actual handler loading, route profile responses, Places IDs and eight
Bellevue candidates. The real deployment state was never switched. No commit or
push was made.

## Verification and limitations

Routine deterministic fixtures cover unit conversion and all five thresholds,
malformed values, dimensional nodes/height barriers, unrelated HGV conditions,
destination entry/exit versus through traffic, public boundary non-authorization,
private/customer/delivery/permit endpoints, prohibited turns, gated approaches,
competing nearby streets, distance bounds, grade separation, geometric crossings,
third-carriageway crossings and unsupported graph/profile pairs. Regional inputs
and network acquisition remain outside routine tests.

Passed `gofmt`, `go test ./...`, `go vet ./...`, `git diff --check`, the full
27-case candidate integration suite, both isolated snapshot cycles, the v1
observation run, and the 26-case geocoding suite on August and the candidate.
Final-code rebuild, logical-table comparison, source/checksum inspection and
maintained-document content/link checks passed. Generated commands, outputs,
comparisons and inspection JSON remain under `data/routing-quality/`.

The actual production browser UI was served from the isolated candidate on
127.0.0.1:8083. The in-app browser verified White Horse autocomplete → explicit
selection → origin, standalone 50 Bellevue → destination → 1.24 km driving route.
The map displayed A/B requested markers, distinct A′/B′ snapped labels, the road
line and separate unverified connector layer. Text showed both coordinates and
8.4/21.8 m gaps. Final visual review moved the road labels above their small road
markers so nearby A/B markers did not obscure the labels.

Forward geocoding at `364 Bellevue Avenue` still showed eight distinct IDs with
routing-selection buttons disabled until a candidate was chosen. Selecting the
second retained `om_3761b4bdcc9208d0bfb82775d5e1d75e` and its ambiguity notice.
Using it as an endpoint removed the previous route and snapped markers. A real
map click set a new origin at about 41.490632, −71.310778; Clear route removed both
requested markers, route and snaps, disabled calculation and restored reverse
address lookup. No JavaScript errors were observed. The existing missing
`townhall` basemap sprite warning remains unrelated to routing.

An ignored, clearly labeled browser fixture on 127.0.0.1:8084 exercised the **same
production routing module and real candidate API** with exact benchmark points
and a deliberate 1.5-second request delay. It verified visible loading/disabled
calculation; the 771 m Edgar result with 4.6 m chosen / 0.9 m nearest explanation;
unreachable Training Station with both snaps; customer-endpoint rejection without
stale success text; and Clear during loading with no late result repopulation.
This fixture supplies controls and no map; visual map verification was performed
separately in the actual app. No production test hooks, npm runner or frontend
dependency were added.

Remaining limits: shortest road distance rather than fastest/comfortable driving;
no travel times or navigation instructions; fixed car dimensions; no evaluation of
time-dependent conditions; strict destination coordinates rather than property
entrance modeling; conservative gate/node and malformed-tag exclusions; no
immediate segment reversals; bounded street preference rather than general
connectivity-based resnapping; unverified off-road connectors; finite extract and
endpoint coverage; and substantial immutable graph startup memory. Source fidelity
and these invariants do not establish surveyed road legality or completeness.
