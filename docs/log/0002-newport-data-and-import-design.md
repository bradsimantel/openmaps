# Newport data inspection and import design

2026-09-07 · Milestone implementation: `1f24467`.

Records the regional sample findings, source choices and import rules used for
the first milestone. Counts, gaps and commands describe that snapshot.
See the [README](../../README.md) for current setup and supported behavior.

## Inspected samples

Inspected actual regional records on 2026-09-07 **before designing the SQLite
schema**. Launch bounds are WGS84 `[west, south, east, north]` =
`[-71.33, 41.47, -71.29, 41.51]`, approximately 3.3 × 4.4 km around downtown
Newport. This rectangle is a preview extent, not a municipal boundary. It offers
historic attractions, businesses and residential streets with a small regional
OSM extract. No claim of complete city coverage is made.

Overture snapshot: **2026-08-19.0**. Geofabrik snapshot:
**rhode-island-260801.osm.pbf**, 51,849,634 bytes. Inputs, URLs, SHA-256 digests,
versions and bounds are in [the lock file](../../imports/newport.lock.json).
The Overture hashes cover canonical regional GeoJSON exports, not whole global
Parquet files. Exports use Go `encoding/json` with one final newline, features
sorted by source ID and Parquet map entries represented as key-sorted pairs.
Nullable multilingual values are decoded as strings. Full source properties and
geometry survive. The catalog itself is also pinned by checksum.
The PBF checksum covers the original downloaded bytes.

| Observation | Actual sample result |
| --- | --- |
| Overture Places | 2,173; all have primary names and an address array |
| Place websites / phones | 1,887 / 1,944 records have these fields |
| Operating status | 1,581 open, 51 permanently closed, 541 unspecified |
| Same normalized place name + freeform address | 14 repeated groups, 14 excess records |
| Overture Addresses | 8,546 candidate points, all derived from NAD |
| Number / street / postcode | Present on all 8,546 candidates |
| Unit | Absent on all inspected address records |
| Same normalized address label | 88 repeated groups, 119 excess records |
| Geofabrik named highway ways | 880 ways, 541 distinct raw names |
| OSM address-tagged nodes in rectangle | 138; a limited comparison, not an address census |
| Overture Divisions in expanded sample | 299 points; four retained for launch |
| Business → standalone address links | 1,181 unique matches; 81 ambiguous cases left unlinked |

The spatial reader returns one address whose actual geometry falls outside the
rectangle despite its stored bounding box passing the filter. The adapter checks
the actual point as well: **8,545 addresses are imported**, for **11,602 source
records** total. Run `go run ./cmd/prepare`
to regenerate `data/audit.json` and the normalized bundle offline.

Examples inspected include White Horse Tavern (26 Marlborough St), Redwood
Library and Athenaeum (50 Bellevue Ave), standalone `50 BELLEVUE Avenue`, and
`Marlborough Street` ways. Address number strings include unusual values such as
`1 -550 AMERICA Street`; these are preserved without pretending to interpret a
range or unit. Street names mix uppercase and title case. Search normalizes
case, diacritics, punctuation and a small documented set of street abbreviations;
display retains source spelling.

NAD-derived `address_levels` contain `RI` and an empty locality slot in the
inspected sample. Upstream NAD source IDs and update dates are null here. The
Overture ID is retained as the qualified identity; absent upstream identifiers
are not invented. Snapshot dates do not establish individual attribute freshness.

Local divisions are Newport and Queen Anne Square (microhood); available parent
records add Newport County and Rhode Island, whose label points can lie outside
the launch rectangle. A wider `[-71.6,41.4,-71.1,41.9]` division sample supplies
these parents. The United States parent is outside this sample: its reference
remains in raw provenance but no dangling SQL relationship is created. Division
points are labels, not boundaries. No point-in-polygon containment is claimed,
and missing address locality is not filled from a nearest label point.

## Gaps and supplemental-source decision

The first four sources support the requested flow without a supplemental import.
Address field presence is encouraging, but it is **not a measured coverage or
accuracy percentage**. No independent authoritative address census was loaded.
108 of OSM's 138 address-node labels also occur in standalone Overture Addresses
after abbreviation normalization. The remaining 30 labels need investigation;
they are not a measured set of missing authoritative addresses. An earlier audit
incorrectly included business-only address strings in the overlap count; the Go
pipeline corrects that report without changing entities or relationships. These
138 nodes are too sparse to justify replacing the 8,545
Overture/NAD points; OSM building/way address tags were not evaluated as a complete
supplement. Municipal E911/address points are a future candidate for locality,
unit, entrance and freshness gaps, subject to checking their published terms,
source identifiers and overlap first. No government supplement is claimed as
implemented or selected.

Repeated labels do not prove duplicate identities. Separate address points may
represent units, structures or source errors; same-name businesses can coexist.
The importer preserves them. Businesses, addresses, streets and areas never
merge across kinds. Buildings are not imported in this milestone. A shared
address never establishes a shared business or building identity. Raw Overture
`addresses`, `sources`, hierarchy, names, categories, confidence, contact fields
and status remain available for subsequent investigation.

## Matching, identity and conflict resolution

1. Each initial record gets `om_` plus 128 bits of SHA-256 over
   `openmaps:entity:v1:` + its source-qualified anchor. Anchors are
   `overture:place:<id>`, `overture:address:<id>`, `overture:division:<id>` or
   `osm:way:<id>`. Releases, coordinates, names and SQLite row numbers do not enter
   the ID. OSM way versions are stored separately from the stable way ID.
2. A concrete future importer emits the existing normalized bundle shape.
   [identities.json](../../imports/identities.json) explicitly maps verified new
   source keys to existing anchors. Preserve that mapping as part of the dataset
   when rebuilding, even if the original provider record is removed. There are
   no runtime provider plugins and no fuzzy automatic identity merges. New
   unrelated sources cannot renumber existing records.
3. For a mapped entity, highest numeric source priority wins each nonempty
   attribute; ties use lexicographically smallest source key. An entire location
   is one attribute, so latitude and longitude cannot come from different
   providers. Missing/empty attributes do not erase populated values. Explicit
   `false` is a value, not missing. The original records and losing values remain
   in `source_records`; `attribute_provenance` records the winning source and
   source property path. Upstream property attribution stays in raw `sources`.
   Aliases are selected as a whole attribute by the same rule, not unioned.
4. A business links to an address only when normalized number + street and the
   first five postcode characters agree and **exactly one** candidate lies within
   50 metres (haversine; Earth mean radius 6,371,008.8 m). Abbreviation expansion
   covers st/ave/rd/blvd/ln/dr/ct/pl. No nearest-address guesses, suite stripping
   or name-only matches. Links preserve both entity IDs and carry their evidence.
5. Parent-area links use explicit Overture `parent_division_id` and are inserted
   only when both ends were imported. No spatial relationship is inferred.
6. Street ways remain distinct source entities. Autocomplete collapses repeated
   normalized street names in this small launch region to the first ranked way;
   the selected ID resolves to that exact way's representative in-bounds source
   vertex. This is not a street center, entrance, complete street geometry or
   routing snap. This display-only collapse would need locality scoping before
   expanding the region.

Provider ID churn is not automatically solved by hashing. A reviewed identity
mapping is needed when upstream entities are replaced, split or merged. Existing
public IDs are preserved by retaining their anchors; arbitrary source removal
without a mapped replacement removes that entity from a rebuilt dataset. Refresh
and deletion reconciliation are later work, not silently implemented guarantees.

## Storage and reproducibility

`internal/importer` owns concrete Go source adapters, schema creation and
normalized import validation;
`internal/places` owns retrieval, FTS queries and ranking; `internal/api` alone
maps to Google wire fields. `cmd/prepare` owns acquisition and normalization;
`cmd/import` builds SQLite. Both the import pipeline and service are Go.

SQLite tables: `entities`, `source_records`, `attribute_provenance`,
`relationships`, `metadata`, plus `entity_fts` (FTS5). Source/entity and
relationship references have foreign keys. Invalid coordinates, unknown domain
attributes, conflicting kinds, duplicate source keys and dangling relationships
fail the import. `integrity_check` and `foreign_key_check` must succeed before the
command publishes a new file. Existing output databases are never overwritten.
Build a new database and restart the service against it for refreshes.

Reproducibility means identical normalized content, entity IDs, relationships
and query results for the same pins and identity mapping; SQLite file bytes are
not promised identical. The normalized bundle has its own committed SHA-256,
checked by both Go commands. Preparation verifies every source checksum before
parsing. Missing downloads go to temporary files and are published only after
verification. The pinned STAC catalog locates regional Parquet assets; accepted
asset URLs must match the source host and release. The reader uses row-group
bounding statistics, a bounded block cache and conditional HTTP ranges; it
rejects servers that ignore ranges or return inconsistent responses. Copies of
historical source exports should be archived: Overture's live catalog retains a limited number of releases, so an
expired pin can fail to download. It must never silently switch to latest.

`-write-lock` is a maintainer operation for intentionally changing export and
bundle checksums. To prepare an update, copy the source lock and identity mappings,
select explicit releases and bounds, and obtain the new catalog/PBF checksums
before running:

```sh
go run ./cmd/prepare -manifest imports/candidate.lock.json \
  -identities imports/identities.json -data data/candidate \
  -checksum imports/candidate.bundle.sha256 -fetch -write-lock
go run ./cmd/import -bundle data/candidate/newport.json \
  -checksum imports/candidate.bundle.sha256 -db data/candidate/openmaps.sqlite
```

Review the candidate audit, identity changes, provenance and representative
queries before promoting its locks and database. Do not use this mode to suppress
unexpected checksum errors. Retain identity mappings across updates.

The Go migration changed export serialization and therefore checksums; a full
regional comparison verified unchanged entity IDs, attributes, provenance and
relationships against the preceding database. Scheduling, automatic release
discovery, resumable partial downloads and source-ID churn reconciliation remain
future work. PBF decoding targets dense-node Geofabrik extracts; its in-memory
node index is appropriate for Rhode Island, not planet-scale imports.

## Attribution

The [demo attribution page](../../web/attribution.html) identifies the datasets and
links their upstream terms. Places in this sample include Meta, Microsoft,
BrightQuery, Foursquare, AllThePlaces and DAC; Divisions use OpenStreetMap;
Addresses use NAD. See [Overture's attribution inventory](https://docs.overturemaps.org/attribution/),
[Geofabrik's regional page](https://download.geofabrik.de/north-america/us/rhode-island.html),
and [OSM copyright/ODbL](https://www.openstreetmap.org/copyright).
Keep notices and source metadata with exported datasets. Large datasets are
excluded from Git; this repository does not distribute a combined database.
