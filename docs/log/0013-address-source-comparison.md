# Newport address sources: OpenAddresses, NAD and direct RI E911

2026-09-07 (America/Los_Angeles; acquisition timestamps are UTC, September 8) ·
Investigation at revision `1c867e065d77d50c5dc990224891addb2b4da30b`.

Historical investigation, not an implemented import or new API contract. It
extends [0010](0010-bellevue-address-point-investigation.md) and
[0011](0011-bellevue-original-source-coverage.md), which inspected the historical
RI MapServer but did not obtain intermediate NAD records or newer E911 services.
Current behavior remains in [geocoding.md](../geocoding.md).

Follow-up: [0014](0014-openaddresses-northeast-bundle.md) inspects the user's
September 6 US Northeast collection ZIP, including every address record. That
bundle contains no RI source or Newport preview points. The older RI output
below was actually downloaded separately; its limitations must not be generalized
to OpenAddresses in other states or confused with the collection's build date.

## Recommendation

**Retain Overture as the lookup baseline. For meaningful Bellevue disambiguation,
prefer a targeted, statewide RI E911 enrichment adapter over an OpenAddresses
replacement. Use NAD as an inspection/identity bridge, and consider a reusable
NAD metadata adapter when expanding US coverage.** Do not import a second copy of
every NAD or OpenAddresses point as a new address entity.

NAD offers useful community, county, provider identifier, date and use-type data
that our Overture exports lack. It supplies essentially the **same 8,545 Newport
points**, however, and does not supply Bellevue structure names or units.
The separately downloaded October 2024 OpenAddresses RI output supplies community
labels but loses useful identifiers and comments, and changes benchmark coverage
and postcodes. It is the most recent successful RI artifact listed by the inspected
source catalog, not a statement about every OA collection or state's freshness.
Direct E911 is still necessary **among these inspected sources** to retrieve
Bellevue's meaningful structure descriptions. It is unnecessary merely to keep
our existing forward/reverse geocoding working, and it is not the only possible
route to community enrichment.

A first future adapter should acquire a pinned E911 export, preserve descriptors
as dated source observations, and enrich reviewed existing addresses without
moving their coordinates or renumbering them. The 2024 service provides the
strongest inspected GUID bridge to NAD. The preliminary 2026 service is a valuable
refresh candidate, but needs an explicit identifier transition and coordinate/
status review before adoption. Neither publication dates nor current-looking
comments justify verified unit geocodes or business-occupancy claims.

## Acquisitions and freshness

All comparisons use actual downloaded records. Coordinates below are WGS84
longitude, latitude; distances are spherical metres with radius 6,371,008.8 m.
The launch rectangle is `[-71.33, 41.47, -71.29, 41.51]`, including parts of
Middletown. It is not a Newport municipal polygon.

| Dataset actually inspected | Publication / accessible output | Source freshness evidence and access |
| --- | --- | --- |
| Retained Overture baseline / candidate | August 19 / July 22, 2026 | Same 8,545 address entities and raw address properties. These are release dates; NAD `record_id`, `update_time`, confidence are null. July is the historical candidate, not an upgrade. |
| OA current source configuration | `us/ri/statewide`, addresses/state; last file change March 11, 2025, HTTP→HTTPS | Still points to `E911_Sites/FeatureServer/0`. That endpoint returns ArcGIS `Invalid URL` now. A configuration is not evidence of a current successful dataset. |
| OA published successful output | Job **475152**, October 25, 2024; 440,571 statewide source features; 385,502 validated | Both gzipped GeoJSON outputs downloaded. No per-record source dates. Latest listed job **903253**, September 4, 2026, failed with no output. The catalog retains the older successful artifact. |
| OA older published output / cache | Legacy run **1194961**; 413,440 CSV rows | Results page says cached October 24, 2021; ZIP README says packaged around October 17. Downloaded both ZIP and cache CSV. Neither is a source survey date. Legacy page's problem flag does not negate the actual available ZIP. |
| NAD current public feature service | Describes compilation **June 30, 2026** | Downloaded all fields for the regional query, 8,545 points. Official bulk catalog exposes **NAD_r23.zip**, 9,733,944,292 bytes; ASCII ZIP 7,601,412,707 bytes. Bulk files were not downloaded. Regional `DateUpdate` values span 2020-09-17–2024-09-03, predominantly a September 2024 batch. |
| Earlier RI E911 MapServer | `risegis.ri.gov/.../E911/e911Sites/MapServer/0` | Still queryable. Bellevue `RelDate` is July 13, 2018; record updates 2008/2011, as in 0010. Useful historical comparison, not the OA-configured service. |
| RIGIS 2024 E911 successor | `FACILITY_Sites_E911_24r1/FeatureServer/0`; 417,719 statewide sites | Item created May 9, 2024, modified June 12, 2026; layer data edit February 4, 2025. Regional `Last_Updat` values are October 18, 2024; `DateUpdate` extends to September 26, 2024. Item modification does not mean all addresses were updated in 2026. |
| RIGIS preliminary 2026 E911 | `e911_AddressPoints_2026/FeatureServer/3`; 420,441 statewide sites | Item explicitly titled **preliminary release**, created September 2, 2026. Layer is **3**, not 0. Regional edit times extend to August 17, 2026, with large batches of older dates. Includes `Current` and `Pending` statuses. |

Official entry points and retained metadata:
[OA configuration](https://github.com/openaddresses/openaddresses/blob/master/sources/us/ri/statewide.json),
[successful job](https://batch.openaddresses.io/api/job/475152),
[publication history](https://batch.openaddresses.io/api/data/1932/history),
[legacy results](https://results.openaddresses.io/?runs=all),
[NAD download catalog](https://data.transportation.gov/api/views/yw36-suxr.json),
[NAD layer](https://services.arcgis.com/xOi1kZaI0eWDREZv/ArcGIS/rest/services/Address_Points_from_National_Address_Database_view/FeatureServer/0),
[2024 E911 item](https://www.arcgis.com/home/item.html?id=0a6ce9b22ac44ba6921f84172871ff11),
[preliminary 2026 item](https://www.arcgis.com/home/item.html?id=43e5b9e23c76476eb33a18c68cc590f6).

OA's pinned job configuration uses the same now-unavailable `E911_Sites` URL.
The 2024 RIGIS successor is **not asserted to be the exact bytes that OA read**.
OA's 2021 cache contains only `AddNumFull,MSAGComm,Post_Code,St_Full` and geometry
columns: even that cache is a selected-field extraction, not a full upstream
archive. No complete cache is listed for OA job 475152. We therefore cannot
reconstruct its entire upstream transformation chain.

NAD is available for Rhode Island through the official service and the national
bulk download. Direct unbounded and state-filtered count/statistics queries
returned ArcGIS query errors; no statewide NAD completeness count is claimed.
The successful regional spatial queries, field contents and checked ID sets are
the basis for this report. DOT's national coverage is voluntary and can be
incomplete; a national release date cannot establish Rhode Island freshness.

## Measured coverage and fields

ArcGIS returned 8,987 / 8,990 / 8,888 features for the 2024 / 2026 / historical
E911 spatial queries. Rechecking actual returned WGS84 coordinates against the
inclusive rectangle retains **8,977 / 8,975 / 8,877** respectively. Boundary
filtering is intentional, just as our Overture importer checks geometry after
bounding-box selection. NAD's explicit `Longitude`/`Latitude` values are used for
comparison; its map geometry differs slightly after projection round trips.

“Supported” below means eligible under the current simple-number/street grammar,
using explicit source fields, not physically validated or deliverable addresses.
2026 number components retain spaces between prefix, number and suffix. No
supplement has been fed to the production importer.

| Source, strict preview rectangle | Points/sites | Supported labels, counting distinct points | Blank number | Useful fields beyond retained Overture | Important missing information |
| --- | ---: | ---: | ---: | --- | --- |
| Overture August and July, each | 8,545 | 8,407 | 0 | Existing public IDs, provenance and five returned component types | Locality slot and units empty; upstream ID/date/confidence absent |
| OA October 2024 source output | 9,417 | 8,385 | 896 | `city` from MSAG community | `id`, `unit`, district and region empty; no names/comments/dates/accuracy |
| OA October 2024 validated output | 8,377 | 8,377 | 0 | Same narrow attributes after OA filtering | Removes additional records; validation is not postal/geospatial certification |
| OA legacy 2021 output | 8,885 | 8,295 | 471 | MSAG community | Same narrow attribute selection; historical labels/coordinates |
| NAD June 2026 service | 8,545 | 8,407 | 0 | County, neighborhood-community field, UUID/dataset identifier, date, address-use type; CDP on 562 records | Unit/building/floor/landmark/location description all null; placement always `Unknown` |
| Direct E911 2024 | 8,977 | 8,412 | 431 | MSAG community, GUID, aliases, site type, dates; comments on 8,803 | No dedicated unit/building-name/accuracy fields; comments contain mixed observations |
| Direct E911 preliminary 2026 | 8,975 | 8,405 | 432 | Municipality, site/global IDs, status, dates; comments on 8,801; capture method on 20 | Unit fields and `placename` empty throughout this extract; most capture methods absent |

All compared sources contain postcode information. This does not establish that
conflicting postcodes are correct. OA's larger raw count is largely additional
unnumbered sites, not demonstrated new geocodable-address coverage. Neither
NAD nor E911 supplies a measured positional error for the Bellevue points.
Historical E911 `Measure` is not interpreted as positional accuracy.
The current OA specification permits optional `accuracy` (default unknown),
`notes`, `addrtype` and `id` mappings. The RI configuration does not map them,
and the inspected 2024 output has none of those populated observations. This
is a limitation of the inspected RI publication, not proof that OA can never
carry richer information.

Two locality qualifications matter:

- NAD has `Nbrhd_Comm=NEWPORT` on 7,988 points and `MIDDLETOWN` on 557; county is
  Newport on all. But `Inc_Muni=Unincorporated` and `Post_City=Not stated` occur on
  **every** regional record. The [official NAD schema](https://www.transportation.gov/sites/dot.gov/files/2023-07/NAD_Schema_202304.pdf)
  distinguishes municipality, neighborhood and postal community. Do not promote
  these fields interchangeably or expose the placeholders as real locality names.
  The meaning of the Rhode Island neighborhood mapping warrants confirmation.
- Retained Overture's US locality slot is null, but **562** points have
  `postal_city=Newport East CDP`. Those values agree with NAD's `Census_Plc` on the
  corresponding records, while NAD's actual `Post_City` is unknown. This is
  evidence of a semantic mismatch worth investigating, not proof of the precise
  historical conversion rule. It would be wrong to present that CDP value as a
  verified postal city or simply infer Newport for the whole preview.

## What survives each step

| Observed boundary | Retained information | Missing, changed or unproven |
| --- | --- | --- |
| Historical E911 ↔ retained Overture | Bellevue label/point correspondence within 0.00015 m | No preserved ESiteID join. Geometry correspondence is lineage evidence, not a documented complete pipeline. |
| OA pinned configuration → published OA | Number, street, MSAG community, postcode and coordinates; seven decimal-place coordinates in the inspected output | Configuration does not select source ID, comments, aliases, dates, site type or units. Source attributes missing from the configuration/output are not proof they were absent upstream. |
| 2024 E911 ↔ current NAD | 8,538 unique nonzero GUID correspondences; community and postcode agree for these pairs; site-use classification has a NAD counterpart | NAD has no comments, building labels or units here. Street types/directions are expanded and some numbers differ. This is a verified shared-identifier correspondence, not proof which submission file/version or conversion code produced NAD. |
| Current NAD ↔ retained Overture | All 8,545 number/street/postcode/point combinations correspond; 8,544 coordinates equal numerically, one differs by about 0.0000000007 m | NAD identifiers, dates, county, community and use type are absent from our retained Overture records; CDP-like values survive in `postal_city`. We did not obtain Overture's exact NAD input file. |
| Retained Overture → our raw SQLite records → API | All source properties survive in raw SQLite; supported number, street, state, country and postcode are projected into components | `postal_city` is retained but not projected. Bellevue comments and units are already absent in Overture; this is not a geocoder field-dropping bug. |
| 2024 E911 ↔ preliminary 2026 E911 | Bellevue comments and distinct structures persist | `Site_GUID` disappears in favor of `siteaddid` and different `GlobalID` values; no old→new ID bridge obtained. Coordinates and some labels/statuses change. |

Overture's [current address guide](https://docs.overturemaps.org/guides/addresses/)
explains that identical features are matched using all attributes and geometry,
with one source promoted rather than attribute-level enrichment. Addresses lack
a stable matcher and are not in the GERS registry; attribute/location changes can
change their upstream IDs. Our own persistent identity anchors and reviewed
replacement mappings remain necessary.

## Eight Bellevue points

All eight persist in Overture, both OA outputs, NAD and both newer E911 services.
Number, postcode and community are shared within each dataset. NAD's use type
separates Cottage (`Single Family Home`) from the other seven (`Multi-Family Home`),
but that does not name or uniquely distinguish all eight. OA supplies eight
hashes/coordinates with otherwise identical address attributes. Direct E911
comments supply all eight meaningful distinctions.

The numbering/public IDs are those in [0010](0010-bellevue-address-point-investigation.md).
GUID prefixes below are display abbreviations only; full IDs and source fields
remain in the archived regional records and `comparison.json` under ignored
`data/investigations/address-supplements/`.

| Point | NAD UUID / 2024 E911 GUID prefix | OA 2024 hash | 2026 site ID | Recorded distinction, paraphrased from comments |
| --- | --- | --- | --- | --- |
| 1 | `c3f061ef` | `a0fd650d3b49de54` | `SID-207750` | Coach House; Ch201–Ch101, preserving source order |
| 2 | `ba8e9286` | `764bbd73ca924675` | `SID-207751` | Cottage; C101 |
| 3 | `e741fc37` | `44749a2496d351a2` | `SID-207756` | Residential structure; C1–C4 |
| 4 | `4ca0476a` | `d47e1fdf47c7322e` | `SID-207755` | Residential structure; D1–D10 |
| 5 | `f6c9c0c2` | `936d71c668fd90f2` | `SID-207754` | De LaSalle / Weld; W101–W302 |
| 6 | `050797b2` | `e35716656c175235` | `SID-207757` | Residential structure; A1–A8 |
| 7 | `337e036f` | `c64941143effd973` | `SID-207752` | Derham; D101–D202 |
| 8 | `edc1d0ee` | `8811cb2a938f6452` | `SID-209535` | Residential structure; B1–B2 |

Representative original fields for point 1:

```text
Open Maps ID: om_1756aadfad66fbf9a7f89d3f0da05cdd
Overture ID: 79606b68-2e5d-4cf7-beeb-7e0255acd6be
Overture/NAD point: [-71.30638518694232, 41.478570557118516]
NAD UUID: c3f061ef-0200-4790-9335-6b6294efd4ec
NAD DataSet_ID: {C3F061EF-0200-4790-9335-6B6294EFD4EC}
NAD Nbrhd_Comm: NEWPORT; Post_City: Not stated; Placement: Unknown
NAD Building / Unit / LocatnDesc: null / null / null
2024 E911 Site_GUID: {C3F061EF-0200-4790-9335-6B6294EFD4EC}
2024 E911 Comments: 2stry cream stucco blck trim, Coach House Ch201-Ch101
OA point: [-71.3063844, 41.478562]; city: NEWPORT; id: ""; unit: ""
2026 E911 siteaddid: SID-207750
2026 E911 GlobalID: 06fa0d0a-4bae-4986-951c-c06057dedfcd
2026 E911 point: [-71.30638441120507, 41.47856204023437]
2026 E911 unitid / placename / capturemeth: null / null / null
```

NAD Bellevue dates are September 3, 2024. The matching 2024 E911 records have
`Last_Updat=20241018095100`; their `DateUpdate` values correspond to NAD's dates.
2026 records report edits on February 19, 2025, except B1–B2 on August 5, 2025.
These are record timestamps; largely unchanged comments since 2008/2011 do not
prove field-level revalidation, current tenants or the validity of every unit.
Do not expand comment ranges into unit entities or select a preferred bare-address
point. Useful UI descriptions can be dated and qualified without claiming a unit
coordinate, entrance, rooftop, or canonical complex center.

The 2024 E911 WGS84 points are about **0.013 m** from retained Overture. OA 2024
and E911 2026 are about **0.95 m** away. Native 2024/2026 geometries both declare
EPSG:3438 (RI State Plane feet), and Bellevue's native Y coordinates differ by
about 3.095 feet, with a small nearly uniform X offset. Thus this is not merely
rounding by our WGS84 query. The cause could include upstream coordinate/datum
processing; no evidence here establishes an improvement in physical placement.

## Existing benchmark and representative checks beyond Bellevue

The real, read-only **26-case Go HTTP-handler benchmark passed on both retained
snapshots**, including source-backed components, ordered public IDs and reverse
distances. All 8,545 retained address records remain identical across snapshots.

A separate offline probe evaluates all 26 requests against supplemental source
labels/points. It reproduces the baseline's expected outcomes and ID sets, but
is **not** an API integration test for unimplemented adapters. It assumes RI/US
scope metadata is supplied for OA, keeps the same rectangle and 100 m reverse
limit, and reports source IDs rather than inventing Open Maps IDs. The optional
comparison normalization adds only `AV→avenue`; other missing label matches may
be spelling/direction/type differences, not coverage gaps.

| Benchmark group | Overture / NAD | OA October 2024, after AV normalization | Direct E911 2024 / preliminary 2026 |
| --- | --- | --- | --- |
| 50 Bellevue, normalized spelling, Newport context | Same point; retained Overture still treats Newport as partial context | Same corresponding point; MSAG community available | Same corresponding point; source community/municipality available |
| 364 Bellevue / 199 Connell | Eight / twelve points | Eight / twelve; no explanatory names | Eight / twelve, with comments |
| 26 Marlborough | Same standalone point | Corresponding point | Corresponding point; Tavern name remains an observation about the site, not a merged business |
| Forward 5 Beacon Hill | One point | No point in strict clip | 2024 one; 2026 none in clip |
| Forward 357 Valley | One point | No point in strict clip | One in both direct extracts |
| Reverse exact / nearby 50 Bellevue | 0 / 4.214521 m | 0.950473 / 4.748654 m | 2024: 0.012975 / 4.227060 m; 2026: 0.949428 / 4.747840 m |
| Reverse nearby 26 Marlborough | 0.916508 m | 1.662901 m | 2024: 0.928580 m; 2026: 1.660736 m |
| South-inside reverse | 5 Beacon Hill, 0.196356 m | 3 Beacon Hill, 30.685877 m | 2024: 5 Beacon Hill; 2026: 3 Beacon Hill, 30.685458 m |
| North-inside reverse | 79 Park Holm, 0.598977 m | 79 Park Holm, 1.547079 m | 2024: 79 Park Holm; 2026: 104 Truman, 19.979326 m |
| East-inside reverse | 357 Valley, 1.188921 m | 349 Valley, 17.979883 m | 357 Valley in both, 1.176149 / 1.470748 m |
| Wrong number, incomplete street, wrong city/postcode; water gap; four outside-coverage cases | Existing negative outcomes | Same outcomes | Same outcomes |
| Unit request, range, street alone, locality alone | Unsupported | Remain unsupported | Remain unsupported; comments do not implement unit parsing |

Without the AV adapter normalization, OA and E911 2024 also miss **four** forward
benchmark cases: Bellevue, normalization, Newport context and duplicate-eight.
Keeping the current matching contract while enriching only attributes avoids
these raw-label regressions. Supplemental enrichment would not by itself change
our existing partial-context behavior; that requires a reviewed matching/API
change with correctly typed locality evidence.

The OA statewide file contains `5 BEACON HILL RD` at latitude **41.4699943** and
`357 VALLEY RD` at longitude **−71.2899969**, just outside the clip. They are not
statewide deletions. E911 2026 also places 5 Beacon Hill just south of the clip.
A separate statewide label query found no 79 Park Holm in that preliminary
service. The current evidence does not establish its real-world removal.
These tests measure fidelity/regression against the retained benchmark, not
which source coordinate is more accurate.

Additional purposefully selected records test residential ZIP conflicts,
nonstandard numbers, source ID problems, direction spelling and commercial sites.
They are not a randomized accuracy sample:

| Record / issue | Actual observations | Consequence |
| --- | --- | --- |
| 22 Stockton Drive, Middletown | Overture/NAD and both direct E911 versions say `02840`; OA 2024 says `02842` at a point about 0.946 m from Overture | Community is useful enrichment; postcode conflict needs evidence beyond release recency. NAD's CDP value also survives in Overture `postal_city`. |
| 8 Cloyne Court, naval quarters | NAD/Overture number is `CD 8 <Null>`; 2024 E911 `AddNumFull=8` and comment identifies Quarters CD; 2026 prefix/number are `CD` / `8` | Direct data offers a possible number/alias repair. Do not strip the prefix or infer a new unit without review. |
| 14 Cloyne Court | Already in Overture/NAD; 2024 E911 GUID is blank although comment describes a supplied address number; 2026 has `SID-127298` | Missing source ID is not missing coverage. No GUID-based join is possible for this row. |
| 19 1/2 Freeborn Street | Fraction retained in Overture/NAD, OA and direct E911; 2024 comment describes number 19.5 | Remains outside the current grammar. A supplement alone does not fix fraction support. |
| 136 West Main Road | Two Overture/NAD points; OA/direct street label uses `W MAIN RD` | A failed literal label comparison is not an absent address; direction expansion belongs in an explicit adapter/matching decision. |
| 199 James T Connell Memorial Road | Twelve E911 comments distinguish recorded storefronts, including a video shop, shoe shop and supermarket; NAD use types are broader; OA has no descriptions | Comments add distinctions but can preserve historical business names. Do not overwrite current Places names or assert current tenants. |
| 13–21 Memorial Boulevard and another site | Five differently numbered Memorial sites plus one other point share the all-zero UUID/GUID in NAD/E911 | Reject placeholder IDs; otherwise an identifier-only join can merge or attach descriptors to unrelated addresses. |

Across the whole preview, 8,175 Overture points have some OA source point within
2 m; 11 have multiple candidates. Corresponding figures are 8,545 / 4 for NAD
and E911 2024, and 8,453 / 10 for E911 2026. **These are spatial review leads, not
identity matches or coverage percentages.** With a supported matching label
(including only the additional AV expansion), 71 Overture points have a nearby
OA postcode disagreement. More complete directional/type normalization would
change that sample; it would not settle which value is correct.

## Identity, enrichment and conflicts

The strongest new bridge is NAD `DataSet_ID` / UUID ↔ 2024 E911 `Site_GUID`, after
case/braces normalization, rejection of empty/all-zero values, and uniqueness
checks on both sides. There are **8,538** such pairs. All agree on postcode and
community and lie about 0.013 m apart. Eighteen have different number strings;
665 differ under the intentionally limited street/number normalization, largely
because of provider spelling/type expansion. A GUID does not authorize silently
replacing those labels. Six NAD and six E911 records use the zero GUID; one
numbered E911 record has a blank GUID.

NAD↔Overture correspondence is exact number/street/postcode plus essentially
identical coordinates, unique across the complete regional pair set. It is
strong candidate evidence, but Overture's NAD source ID is null. A future
mapping should record this as an explicitly reviewed geometric/attribute link,
not as a preserved upstream-ID relation. Verify competing neighbors, kinds,
source multiplicity, units and conflicts before accepting a mapping. Keep all
eight Bellevue public anchors and all distinct repeated addresses.

OA's `id` is blank; `hash` is unsuitable as our durable identity anchor. There
are **8,455** regionally retained OA records with identical selected address
attributes and coordinates in the 2021 and 2024 outputs, yet **none** retains
its hash. We have not established the hash-generation cause or stable-ID
contract; content hashes cannot substitute for provider entity identifiers.
The [OA source specification](https://github.com/openaddresses/openaddresses/blob/master/CONTRIBUTING.md)
distinguishes mapped IDs from other output fields. Do not interpret its
configuration repository's CC0 license as a data identity or reuse guarantee.

For a future enrichment implementation:

1. Keep original source keys, versions, raw observations and permanent Open Maps
   anchors. A provider rekey, disappearance or preliminary status needs review,
   not new public IDs or automatic deletion. Avoid ArcGIS OBJECTIDs as durable keys.
2. Accept a new source record onto an existing address only with recorded
   evidence and one-to-one checks. Model business/building/site relationships
   separately; a comment naming a business does not change the address's kind.
3. Add community, aliases and dated descriptors only with clear field semantics.
   Reject placeholder strings and invalid IDs. Keep comments as source text;
   no inferred unit expansion, occupancy, entrance, or physical accuracy.
4. Preserve losing values. Explicitly review postcode/number/location conflicts
   rather than assigning a global “newest provider wins” priority. Adding a
   high-priority E911 source wholesale would change our existing winner selection
   for labels and locations, so proposed attribute selection must be deliberate.
5. Exercise our existing comparison, stable-anchor, provenance, activation and
   rollback machinery on a separate candidate. Extend benchmark expectations
   only after investigating changes, including strict-boundary effects.

## Terms, access and maintenance

OA [does not relicense processed data](https://github.com/openaddresses/openaddresses/blob/master/README.md#license).
Its source JSON is CC0; the actual RI output points to RIGIS terms. The successful
job's `license:false` means no populated machine summary, not “no restrictions.”
The current Batch UI requires login for downloads and reserves some export
formats for financial backers. The public object explicitly named by job 475152
was downloadable over HTTPS in this investigation, as were the legacy ZIP and
cache. That observed access is not a promise of permanent anonymous distribution.
Archive the bytes and job/config revision; monitor latest **successful** output,
not merely scheduled attempts. OA's [development documentation](https://github.com/openaddresses/openaddresses/blob/master/DEVELOPMENT.md)
describes the batch workflow; a weekly run does not guarantee weekly RI freshness.

The official [NAD disclaimer](https://www.transportation.gov/mission/open/gis/national-address-database/national-address-database-nad-disclaimer)
states federal-government reuse without copyright restriction, with as-is/no-
accuracy warranty provisions and a caveat about state mailing-list restrictions.
Retain USDOT, NAD provider and release metadata. Public ArcGIS queries required no
credentials here; bulk download links are on DOT's portal. Maintain a schema-aware
national adapter and archive pins: the large mutable bulk resources and service
metadata are not immutable source releases. The portal describes quarterly updates;
participating jurisdictions have their own update schedules and coverage gaps.

The [RIGIS 2024 item terms](https://www.arcgis.com/sharing/rest/content/items/0a6ce9b22ac44ba6921f84172871ff11?f=json)
and [2026 item terms](https://www.arcgis.com/sharing/rest/content/items/43e5b9e23c76476eb33a18c68cc590f6?f=json)
provide the dataset as-is, disclaim warranties/liability, and ask derived products
to acknowledge RIGIS and primary producers. Both also say the data was designed
and maintained for E911 mapping and not intended for other purposes; preserve
that suitability limitation instead of describing the data as certified general
address validation. Attribution should identify **RIGIS, RI E 9-1-1 and AK
Associates**, along with the actual layer/version. The older MapServer's item
says `OPEN`, which is less informative than these terms; it should not replace
the newer item's full notice.

The RIGIS layers allow anonymous `Query,Extract` and advertise several export
formats. Tested acquisition used bounded queries and checked ID/count completeness,
not a paid geocoder or account-bound export. Maintenance must handle replaced
service names, layer IDs, schema changes, preliminary releases, changed identifier
names, source statuses, CRS declarations and stale comments. Availability of
an ArcGIS endpoint alone does not establish an ongoing publication SLA.

## Scaling and remaining questions

This recommendation does **not** require a custom adapter for every county.
Keep Overture as the broad baseline. A single NAD parser can expose standardized
metadata across participating US jurisdictions; a single OA parser can inspect
published global sources and fill demonstrated gaps. Their aggregation does not
make every field equally complete or semantically reliable, so use per-source
quality gates, retained provenance and explicit identity review. Many additional
point sources will overlap data already in Overture.

The exceptional RI work is one **statewide** adapter for a specific benefit
that both aggregators omit: richer site observations. Extend direct adapters only
where measured product benefit warrants maintenance, preferably at state or
regional scale. Nationwide, equally rich building/unit descriptions cannot be
promised without additional providers or local work. Do not start a county plugin
framework to hide that unresolved coverage constraint. A future upstream OA contribution
that repairs the RI endpoint and preserves IDs/notes could reduce the need for
a direct adapter; it would first need a successfully published output and the
same identity/field-quality checks. No upstream changes were submitted here.

Verified: actual published OA outputs; broken configured upstream endpoint;
newer RIGIS layers; same NAD/Overture regional point set; GUID bridge and invalid
IDs; field completeness; Bellevue descriptors; benchmark differences; and
source-specific access/terms above.

Dataset limitations: empty structured unit/building fields; NAD locality
placeholders and CDP/postal semantics; stale/mixed E911 comments; OA missing IDs
and dates; approximation/coordinate shifts; preliminary statuses; finite preview.

Unresolved before production enrichment: the exact NAD input/release consumed by
Overture; the full E911 submission/transformation history feeding NAD and OA;
field-level source freshness and postcode truth; why nearly uniform coordinate
shifts occurred; 2024 GUID→2026 SID/GlobalID transition; the appropriate mapping
of RI MSAG/neighborhood community to our domain; and which named storefronts or
unit ranges remain current. Current NAD does show that the missing Bellevue
comments are absent **by that aggregation stage**, but does not identify who
removed them in the historical pipeline used by Overture.

## Reproducibility and verification

Large downloads, original query pages, metadata, logs and generated comparison
are in ignored `data/investigations/address-supplements/`. Primary data acquisitions
have `.download.json` sidecars recording exact URL, UTC retrieval time, response
headers, bytes and SHA-256. Diagnostic browser-JavaScript downloads record the
retrieval date; their exact retrieval times/headers were not captured. `evidence-manifest.json` inventories source and
generated files, plus retained input fingerprints. Full ArcGIS pages remain
unaltered alongside assembled collections; source ID sets were checked for
omissions, duplicates and transfer-limit truncation before comparison. NAD and
the two newer E911 layers had unchanged `editingInfo` before/after acquisition;
this checks published edit metadata, not a transactional snapshot guarantee.

Temporary Python acquisition, comparison and fixture-check scripts and their
fixture are not retained in the repository. Archived evidence and generated
results remain in ignored `data/`; their manifests retain historical script
hashes, but the scripts are no longer available to rerun.
Acquisition used the source URLs and bounds documented above, with separate
network requests and offline comparisons. The live services may have changed;
compare against archived checksums instead of calling a new result the same
snapshot.

The existing Go benchmark can still be run independently:

```sh
OPENMAPS_BASELINE="$PWD/data/openmaps.sqlite" \
OPENMAPS_CANDIDATE="$PWD/data/newport-2026-07-22/openmaps-reviewed.sqlite" \
  go test -tags=integration ./internal/importer -run TestNewportGeocoding -count=1 -v
```

The archived comparison contains all 26 per-source probe outcomes, field counts,
raw regional records, label/spatial candidate lists, safe GUID correspondences
and OA hash comparison. Analysis ran offline and opened both SQLite snapshots
read-only. Six historical fixture checks covered eight distinct Bellevue points,
source descriptions versus units, placeholder IDs, postcode conflict, AV spelling
and the missing Cloyne GUID. These results are investigation evidence, not an
approved mapping file or a retained test suite.

Content/formatting review, fixture checks, retained Go benchmark and
`git diff --check` passed. No Go source changed, so no unrelated full Go test/vet
run was needed. Retained database hashes, active deployment selection, public
IDs, source locks and production lookup behavior were unchanged. No commit made.
