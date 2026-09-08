# Investigation: eight points at 364 Bellevue Avenue

2026-09-07 · Geocoding milestone working tree based on `ee8a74c`.

Historical investigation requested after the geocoding milestone. This report
adds evidence; it does not change the API, mappings, imports or deployed data.
Current implemented behavior remains in [geocoding.md](../geocoding.md).

## Finding

The eight retained address points correspond one-to-one to **eight separately
described structure sites within the De La Salle condominium complex**. They are
not eight identical-coordinate duplicates and are not eight separate street
addresses. Rhode Island's published E-911 site records distinguish the structures
and building/unit labels; our retained Overture/NAD address records do not contain
those distinctions.

This explains the repeated labels much more specifically than the uncertainty
recorded in [0008](0008-geocoding-contract.md) and [0009](0009-geocoding-verification.md).
The correspondence is strong evidence of shared source lineage, not a formally
preserved upstream-ID join: Overture's NAD `record_id` is null for these records.

## Source comparison

Inspected every matching entity, source record, winning attribute-provenance row
and relationship in the retained August and July SQLite files. Each entity has
one distinct Overture address source key, number `364`, street `BELLEVUE Avenue`,
postcode `02840`, country `US` and its own coordinate. Each raw record attributes
the point to NAD. Unit, building label, source confidence, source record ID and
source update time are unavailable; locality is null. There are no relationships
from or to these eight address entities in either snapshot.

All eight raw source features are identical across July and August. Both archived
address GeoJSON exports have SHA-256
`ecc98348f3558be2adb8fa0f6c9a067942e5be8f0abe1013d8faa248cc468f56`.
The eight SQLite raw records equal the corresponding archived export features.
The exporter reads all Parquet columns and retains all non-null properties except
geometry/bbox (geometry is represented separately). The geocoder does not strip
populated unit/building labels from these eight records: those fields are already
absent in the retained Overture exports. This investigation does not establish
whether the distinctions were omitted when producing NAD or when converting NAD
to Overture; the intermediate NAD input was not obtained.

Queried the public [RI E-911 Address Points layer](https://risegis.ri.gov/hosting/rest/services/E911/e911Sites/MapServer/0)
in a small rectangle around the points, requesting site/address descriptors and
coordinates. The query returned ten features: the eight numbered `364` sites, an
unnumbered tennis-court cabana and a separate nearby Sylvan Street address. The
cabana is not one of our eight geocoding candidates. The layer describes its
features as building/structure sites; [RI E-911's mapping explanation](https://ri911.ri.gov/mapping)
also describes collecting locations and addresses at structures.

The eight numbered sites match our points uniquely, each within **0.00015 m** of
its counterpart after the service returns WGS84 coordinates. That sub-millimetre
agreement is evidence of essentially identical stored geometry, **not physical
survey accuracy**. Even the closest other E-911 feature is at least 16.845 m away.
The eight imported points themselves have pairwise separations of 16.845–115.624 m.

Numbering below follows the existing forward-result order (public ID), not
importance, entrance preference or confidence. Descriptions paraphrase the E-911
comments. Labels are evidence as recorded, not newly verified unit ranges.

| Point | E-911 site ID | Recorded structure / labels | Latitude | Longitude |
| --- | ---: | --- | ---: | ---: |
| 1 | 158631 | Coach House; labels Ch201–Ch101 (source order) | 41.478570557 | -71.306385187 |
| 2 | 158634 | Cottage; label C101 | 41.478658017 | -71.306828417 |
| 3 | 158639 | Residential block; labels C1–C4 | 41.478363628 | -71.306976049 |
| 4 | 158638 | Residential block; labels D1–D10 | 41.478478645 | -71.307263570 |
| 5 | 158637 | Weld / De La Salle; labels W101–W302 | 41.478165805 | -71.307663636 |
| 6 | 158640 | Residential block; labels A1–A8 | 41.478085032 | -71.307229256 |
| 7 | 158635 | Derham; labels D101–D202 | 41.478679084 | -71.307458112 |
| 8 | 161637 | Residential block; labels B1–B2 | 41.478212147 | -71.306973600 |

Stable identifiers retained in Open Maps:

| Point | Public ID | Overture address ID |
| --- | --- | --- |
| 1 | `om_1756aadfad66fbf9a7f89d3f0da05cdd` | `79606b68-2e5d-4cf7-beeb-7e0255acd6be` |
| 2 | `om_3761b4bdcc9208d0bfb82775d5e1d75e` | `a0f73492-032c-459c-9258-21d1129e99b8` |
| 3 | `om_4098a705c5300791b2737d4f59bb8764` | `db79e7e6-372a-4200-9231-4d91d366f983` |
| 4 | `om_5d377aa5a5f02778be84dcf3100a7277` | `b50e1f58-7155-40eb-9e34-adbc528e956e` |
| 5 | `om_6acbc54681cd81a3e64b529aff599c80` | `aff5f066-55ec-49aa-b78f-a21f123f7c33` |
| 6 | `om_6f49f7a9f416862ad2fce0608cae3cd2` | `45e5f78d-0337-4e00-859d-3a0019281d40` |
| 7 | `om_b0476e6109c3097b8d3c9b0e2a7ad136` | `f1fd9222-7f32-4d72-9e8b-7023da345651` |
| 8 | `om_c46eae846a955c231972ace833b2ec36` | `027cf5d4-951e-4e9f-b4be-3f9ea63e3ae0` |

The published E-911 service is **historical evidence**, despite being accessible
today. All eight records report `RelDate` 2018-07-13; their `UpdateDate` values
are 2008-04-14, except C1–C4 and B1–B2, which report 2011-04-05. Neither the 2026
Overture release date nor exact coordinate agreement establishes present-day
building names, occupancy, unit validity or entrance positions.

As more recent independent corroboration, Newport's official
[December 10, 2024 Historic District Commission record, page 35 of the combined PDF](https://www.cityofnewport.com/getattachment/City-Hall/Boards-Commissions/Commissions/Historic-District-Commission/2024-HDC-Minutes-Combined.pdf.aspx?lang=en-US)
references this address with unit D102 and a unit-qualified plat/lot identifier.
This confirms that unit distinctions are meaningful at this address; it does not
verify every historical E-911 label or assign D102 to a point by itself.

The existing Overture business `Newport condo` lies close to the Weld point, but
that business location supplies no evidence that Weld should be the preferred
geocode for the whole complex. Its identity remains separate from all eight
address identities.

## Implications, not implemented decisions

- Keep the eight identities. Deduplicating them solely because their street
  labels agree would discard meaningful structure distinctions.
- A bare `364 Bellevue Avenue` identifies a shared complex address. The present
  list of eight identical labels loses useful context and asks the user to choose
  among distinctions that the UI cannot explain.
- A future enrichment could associate these existing entities with the verified
  E-911 site IDs and preserve the source descriptors, provenance and dates. A
  maintained source should be checked before promoting these old comments to
  current user-facing unit facts. This is a concrete possible supplement, not an
  implemented or approved general importer.
- A future grouped complex/address result could coexist with these component
  points and their stable IDs. It requires an explicit parent identity and a
  defensible representative location. Neither the first public ID, Weld's
  historic prominence nor the mean of the eight coordinates establishes a main
  entrance or canonical address point.
- A unit such as A6 could potentially narrow to the historically described A1–A8
  structure after validated enrichment. It would still establish a building/site
  point, not that unit's exact location or entrance. Do not fabricate a unit
  coordinate or expand comment ranges into verified unit entities.

No evidence here justifies choosing one of the eight as the uniquely correct
bare-address answer. Multiple results remain an honest current response, while
source enrichment and meaningful grouping offer a better product direction.

## Reproducibility and scope

The external read-only query is retained as:

[Exact E-911 query](https://risegis.ri.gov/hosting/rest/services/E911/e911Sites/MapServer/0/query?f=json&where=1%3D1&geometry=-71.3079%2C41.4778%2C-71.3061%2C41.4789&geometryType=esriGeometryEnvelope&inSR=4326&spatialRel=esriSpatialRelIntersects&outSR=4326&outFields=ESiteID%2CPSiteID%2CSiteType%2CHouseNumbe%2CPrimaryAdd%2CALIAddress%2CPrimaryNam%2CParcelNum%2CComments%2CUpdateDate%2CRelDate&returnGeometry=true).

Local evidence, excluded from Git, is in `data/investigations/364-bellevue/`:
`retained-points.json`, `e911-layer.json`, `e911-points.json`,
`e911-query-url.txt` and `point-correspondence.json`. The correspondence records
include full source IDs, nearest/next-nearest distances and source date fields.
The spatial query and nearest matches were computed independently of the
geocoding implementation. No new source was imported into the product.

This was a data and documentation investigation. No runtime code, public IDs,
source locks, database bytes, deployment selection or API behavior was changed.
The report received a content/formatting review and `git diff --check`; unrelated
Go tests were not rerun. No commit was made.
