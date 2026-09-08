# Bellevue: information in the original sources

2026-09-07 · Follow-up to [0010](0010-bellevue-address-point-investigation.md),
geocoding milestone working tree based on `ee8a74c`.

Investigated whether the E-911 structure descriptions found in 0010 already exist
in the retained Overture or Geofabrik inputs. No product behavior changed.

## Overture

The eight address records are NAD-derived and retain coordinates that match the
historical RI E-911 sites essentially exactly. Thus the address-point geometry
appears to share that lineage, but no direct E-911 source IDs are preserved.
The archived Overture records do not contain the E-911 comments identifying
Coach House, Cottage, Weld, Derham or the A/B/C/D unit groups. Their NAD record IDs,
confidence and update dates are null. The exact source comparison and exporter
inspection are recorded in 0010. Those descriptions came from a separate query
of the RI E-911 service, not from the original Overture exports.

## Geofabrik

Scanned the original `data/rhode-island-260801.osm.pbf` with the existing Go OSM
decoder. Examined tagged nodes in longitude −71.3080 to −71.3060, latitude 41.4778
to 41.4789; ways referencing nodes in that rectangle; and relations referencing
those nodes or ways. Also checked explicit `addr:housenumber=364` plus a Bellevue
street tag throughout the file. This reads the source PBF, not just the subset
already imported into SQLite.

The local extract includes building outlines, driveways, pools and a tennis court.
The outlines have generic `building=yes` or `building=house` tags. It also has a
node named `De La Salle Academy` (OSM node 357259564), with school and GNIS tags,
and a road named `Weld Court` (way 1197820554). The academy label is a source fact,
not verification of the site's current use.

No examined local feature contains the E-911 site IDs, structure descriptions or
unit-group labels. None has an address/unit tag supplying those distinctions.
The existing importer only publishes named highway ways from this file; building
outlines and the academy node are not in the lookup database. The raw PBF therefore
has potentially useful additional geometry, but not the missing E-911 attributes.

## Conclusion and evidence

The original inputs supply the eight address locations and some surrounding
geometry. The richer E-911 structure/unit descriptions require a separate source
acquisition/enrichment. They are not hidden in our normalized SQLite records.
No source was newly imported, and no schema, identity or API change was made.

The read-only inspection script and extracted local tags are retained outside Git
as `data/investigations/364-bellevue/inspect-osm.go` and `osm-context.json`.
This report received a content/formatting review and `git diff --check`. No
unrelated Go tests were rerun and no commit was made.
